package knowledge

import (
	"context"
	"errors"
	"math"
	"sort"
	"strings"
	"time"

	"vibe-agent/internal/llm"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
)

func (s *Service) Search(ctx context.Context, query knowledgev1.Query) (knowledgev1.Result, error) {
	if strings.TrimSpace(query.Text) == "" {
		return knowledgev1.Result{}, &ServiceError{Code: "invalid_request", Message: "query text is required"}
	}
	if query.Scope == "" {
		query.Scope = "workspace"
	}
	if err := validateScope(query.Scope); err != nil {
		return knowledgev1.Result{}, err
	}
	started := time.Now()
	s.mu.RLock()
	defer s.mu.RUnlock()
	config := s.config
	embedder := s.embedder
	reranker := s.reranker
	if query.Limit <= 0 {
		query.Limit = 10
	}
	bm25Started := time.Now()
	lexical := s.lexical.Search(ctx, query.Text, query.Scope, config.LexicalCandidates)
	bm25Duration := time.Since(bm25Started)
	result := knowledgev1.Result{
		Channels: []knowledgev1.Channel{knowledgev1.ChannelBM25},
		Retrieval: knowledgev1.Retrieval{
			IndexVersion:   s.lexical.Version(),
			BM25Candidates: len(lexical),
		},
	}
	var dense []denseHit
	embedDuration := time.Duration(0)
	if embedder != nil {
		embedStarted := time.Now()
		batch, err := embedder.Embed(ctx, []string{query.Text})
		embedDuration = time.Since(embedStarted)
		if err != nil {
			if !query.AllowDegraded {
				return knowledgev1.Result{}, mapLLMError(err)
			}
			result.Degraded = true
			result.Warnings = append(result.Warnings, knowledgev1.Warning{
				Code: "partial_result", Message: "embedding unavailable; lexical channel only",
				Cause: errorCode(err),
			})
		} else if len(batch.Vectors) != 1 || batch.Dimensions <= 0 || len(batch.Vectors[0]) != batch.Dimensions {
			return knowledgev1.Result{}, &ServiceError{Code: "upstream_schema_error", Message: "query embedding count mismatch"}
		} else if !finiteVector(batch.Vectors[0]) || batch.Fingerprint == "" {
			return knowledgev1.Result{}, &ServiceError{Code: "upstream_schema_error", Message: "query embedding is invalid"}
		} else {
			dense = s.denseSearchLocked(query.Scope, batch, config.DenseCandidates)
			result.Channels = append(result.Channels, knowledgev1.ChannelDense)
			result.Retrieval.EmbeddingFingerprint = batch.Fingerprint
			result.Retrieval.DenseCandidates = len(dense)
		}
	} else if !query.AllowDegraded {
		return knowledgev1.Result{}, &ServiceError{Code: "dependency_unavailable", Message: "embedding client is unavailable", Retryable: true}
	} else {
		result.Degraded = true
		result.Warnings = append(result.Warnings, knowledgev1.Warning{
			Code: "partial_result", Message: "embedding unavailable; lexical channel only",
			Cause: "dependency_unavailable",
		})
	}
	fusionStarted := time.Now()
	fused := fuseRRF(lexical, dense, config.RRFK)
	fusionDuration := time.Since(fusionStarted)
	if query.Rerank && reranker == nil {
		if !query.AllowDegraded {
			return knowledgev1.Result{}, &ServiceError{Code: "dependency_unavailable", Message: "reranker is unavailable", Retryable: true}
		}
		result.Degraded = true
		result.Warnings = append(result.Warnings, knowledgev1.Warning{
			Code: "partial_result", Message: "reranker unavailable; using RRF order", Cause: "dependency_unavailable",
		})
	}
	if query.Rerank && reranker != nil && len(fused) > 0 {
		reranked, rerankDuration, rerankModel, err := s.rerank(ctx, query, fused, config.RerankCandidates)
		result.Retrieval.RerankMS = rerankDuration.Milliseconds()
		if err != nil {
			if !query.AllowDegraded {
				return knowledgev1.Result{}, err
			}
			result.Degraded = true
			result.Warnings = append(result.Warnings, knowledgev1.Warning{
				Code: "partial_result", Message: "reranker unavailable; using RRF order", Cause: errorCode(err),
			})
		} else {
			fused = reranked
			result.Retrieval.RerankModel = rerankModel
			result.Channels = append(result.Channels, knowledgev1.ChannelRerank)
		}
	}
	if len(fused) > query.Limit {
		fused = fused[:query.Limit]
	}
	for _, hit := range fused {
		result.Hits = append(result.Hits, hit.Hit)
	}
	result.Retrieval.Returned = len(result.Hits)
	result.Retrieval.BM25MS = bm25Duration.Milliseconds()
	result.Retrieval.EmbeddingMS = embedDuration.Milliseconds()
	result.Retrieval.FusionMS = fusionDuration.Milliseconds()
	result.Retrieval.TotalMS = time.Since(started).Milliseconds()
	return result, nil
}

type denseHit struct {
	chunk
	score float64
}

func (s *Service) denseSearchLocked(scope string, batch llm.EmbeddingBatch, limit int) []denseHit {
	var hits []denseHit
	for _, record := range s.records {
		if record.Status != "ready" || record.Scope != scope {
			continue
		}
		for _, chunk := range record.Chunks {
			if chunk.Fingerprint == "" || chunk.Fingerprint != batch.Fingerprint {
				continue
			}
			if len(chunk.Vector) != len(batch.Vectors[0]) || !finiteVector(chunk.Vector) {
				continue
			}
			hits = append(hits, denseHit{chunk: chunk, score: cosine(batch.Vectors[0], chunk.Vector)})
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score == hits[j].score {
			return hits[i].ID < hits[j].ID
		}
		return hits[i].score > hits[j].score
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	return hits
}

type fusedHit struct {
	Hit    knowledgev1.Hit
	Fusion float64
}

func fuseRRF(lexical []LexicalHit, dense []denseHit, k float64) []fusedHit {
	if k <= 0 {
		k = 60
	}
	values := make(map[string]*fusedHit)
	for index, hit := range lexical {
		rank := index + 1
		entry := values[hit.ChunkID]
		if entry == nil {
			entry = &fusedHit{Hit: knowledgev1.Hit{RecordID: hit.RecordID, ChunkID: hit.ChunkID, Text: hit.Text}}
			values[hit.ChunkID] = entry
		}
		entry.Hit.LexicalRank = &rank
		entry.Fusion += 1 / (k + float64(rank))
	}
	for index, hit := range dense {
		rank := index + 1
		entry := values[hit.ID]
		if entry == nil {
			entry = &fusedHit{Hit: knowledgev1.Hit{RecordID: hit.RecordID, ChunkID: hit.ID, Text: hit.Text}}
			values[hit.ID] = entry
		}
		entry.Hit.DenseRank = &rank
		entry.Fusion += 1 / (k + float64(rank))
	}
	result := make([]fusedHit, 0, len(values))
	for _, entry := range values {
		entry.Hit.FusionScore = entry.Fusion
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool {
		if math.Abs(result[i].Fusion-result[j].Fusion) < 1e-12 {
			return result[i].Hit.ChunkID < result[j].Hit.ChunkID
		}
		return result[i].Fusion > result[j].Fusion
	})
	return result
}

func (s *Service) rerank(ctx context.Context, query knowledgev1.Query, candidates []fusedHit, limit int) ([]fusedHit, time.Duration, string, error) {
	if limit <= 0 || limit > len(candidates) {
		limit = len(candidates)
	}
	documents := make([]string, limit)
	for i := 0; i < limit; i++ {
		documents[i] = candidates[i].Hit.Text
	}
	started := time.Now()
	response, err := s.reranker.Rerank(ctx, llm.RerankRequest{Query: query.Text, Documents: documents, TopN: limit})
	duration := time.Since(started)
	if err != nil {
		return nil, duration, "", mapLLMError(err)
	}
	if len(response.Results) != limit {
		return nil, duration, "", &ServiceError{Code: "upstream_schema_error", Message: "rerank result count does not match candidates"}
	}
	result := make([]fusedHit, 0, len(response.Results))
	seen := make(map[int]bool)
	for _, item := range response.Results {
		if item.Index < 0 || item.Index >= limit || seen[item.Index] ||
			math.IsNaN(item.Score) || math.IsInf(item.Score, 0) {
			return nil, duration, "", &ServiceError{Code: "upstream_schema_error", Message: "rerank index out of range"}
		}
		seen[item.Index] = true
		entry := candidates[item.Index]
		score := item.Score
		entry.Hit.RerankScore = &score
		result = append(result, entry)
	}
	if len(result) < len(candidates) {
		result = append(result, candidates[limit:]...)
	}
	return result, duration, response.Model, nil
}

func finiteVector(values []float32) bool {
	for _, value := range values {
		converted := float64(value)
		if math.IsNaN(converted) || math.IsInf(converted, 0) {
			return false
		}
	}
	return true
}

func errorCode(err error) string {
	var serviceErr *ServiceError
	if errors.As(err, &serviceErr) {
		return serviceErr.Code
	}
	return "dependency_unavailable"
}
