package knowledge

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"vibe-agent/internal/llm"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
)

type fakeEmbedder struct {
	vectors map[string][]float32
	fail    bool
}

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) (llm.EmbeddingBatch, error) {
	if f.fail {
		return llm.EmbeddingBatch{}, &llm.Error{Code: "quota_exhausted", Message: "quota"}
	}
	batch := llm.EmbeddingBatch{Fingerprint: "fake:2:norm=false", Dimensions: 2}
	for _, text := range texts {
		vector, ok := f.vectors[text]
		if !ok {
			vector = []float32{0, 0}
		}
		batch.Vectors = append(batch.Vectors, vector)
	}
	return batch, nil
}

type fakeReranker struct {
	fail bool
}

func (f fakeReranker) Rerank(_ context.Context, request llm.RerankRequest) (llm.RerankResponse, error) {
	if f.fail {
		return llm.RerankResponse{}, &llm.Error{Code: "timeout", Message: "timeout"}
	}
	response := llm.RerankResponse{Model: "rerank-fake"}
	for index := len(request.Documents) - 1; index >= 0; index-- {
		response.Results = append(response.Results, llm.RerankResult{Index: index, Score: float64(len(request.Documents) - index)})
	}
	return response, nil
}

type partialReranker struct{}

func (partialReranker) Rerank(_ context.Context, request llm.RerankRequest) (llm.RerankResponse, error) {
	if len(request.Documents) == 0 {
		return llm.RerankResponse{}, nil
	}
	return llm.RerankResponse{
		Model: "partial", Results: []llm.RerankResult{{Index: 0, Score: 1}},
	}, nil
}

func TestMemoryWriteSearchUpdateAndRecovery(t *testing.T) {
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "memory")
	embedder := &fakeEmbedder{vectors: map[string][]float32{
		"lexical lexical": {1, 0},
		"dense":           {0, 1},
		"lexical":         {0, 1},
	}}
	service, err := Open(root, embedder, fakeReranker{}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Write(ctx, knowledgev1.WriteRequest{
		Scope: "workspace", Text: "lexical lexical", IdempotencyKey: "w1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Write(ctx, knowledgev1.WriteRequest{
		Scope: "workspace", Text: "dense", IdempotencyKey: "w2",
	}); err != nil {
		t.Fatal(err)
	}
	replayed, err := service.Write(ctx, knowledgev1.WriteRequest{
		Scope: "workspace", Text: "different", IdempotencyKey: "w1",
	})
	if err != nil || replayed.RecordID != first.RecordID {
		t.Fatalf("idempotent replay=%#v err=%v", replayed, err)
	}
	result, err := service.Search(ctx, knowledgev1.Query{
		Text: "lexical", Scope: "workspace", Limit: 5, Rerank: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Hits) != 2 || !result.Degraded && len(result.Channels) != 3 {
		t.Fatalf("result=%#v", result)
	}
	explainable := false
	for _, hit := range result.Hits {
		explainable = explainable || hit.LexicalRank != nil || hit.DenseRank != nil
	}
	if !explainable {
		t.Fatalf("explainability missing: %#v", result.Hits)
	}
	updated, err := service.Update(ctx, knowledgev1.UpdateRequest{
		RecordID: first.RecordID, ExpectedVersion: 1, Text: "dense",
		IdempotencyKey: "u1",
	})
	if err != nil || updated.Version != 2 {
		t.Fatalf("updated=%#v err=%v", updated, err)
	}
	if _, err := service.Update(ctx, knowledgev1.UpdateRequest{
		RecordID: first.RecordID, ExpectedVersion: 1, Text: "stale",
		IdempotencyKey: "u2",
	}); err == nil {
		t.Fatal("stale version must conflict")
	}
	reopened, err := Open(root, embedder, fakeReranker{}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	result, err = reopened.Search(ctx, knowledgev1.Query{Text: "dense", Scope: "workspace", Limit: 5})
	if err != nil || len(result.Hits) == 0 {
		t.Fatalf("reopened result=%#v err=%v", result, err)
	}
}

func TestHybridStrictAndDegraded(t *testing.T) {
	ctx := context.Background()
	embedder := &fakeEmbedder{fail: true}
	service, err := Open(filepath.Join(t.TempDir(), "memory"), embedder, nil, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(ctx, knowledgev1.Query{Text: "x", Scope: "workspace", Limit: 5}); err == nil {
		t.Fatal("strict mode must fail when embedding is unavailable")
	}
	result, err := service.Search(ctx, knowledgev1.Query{
		Text: "x", Scope: "workspace", Limit: 5, AllowDegraded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Degraded || len(result.Warnings) == 0 {
		t.Fatalf("degraded result=%#v", result)
	}
}

func TestRRFKnownOrdering(t *testing.T) {
	lexical := []LexicalHit{
		{RecordID: "r1", ChunkID: "a", Text: "a"},
		{RecordID: "r2", ChunkID: "b", Text: "b"},
	}
	dense := []denseHit{
		{chunk: chunk{ID: "b", RecordID: "r2", Text: "b"}},
		{chunk: chunk{ID: "c", RecordID: "r3", Text: "c"}},
	}
	result := fuseRRF(lexical, dense, 60)
	if len(result) != 3 || result[0].Hit.ChunkID != "b" {
		t.Fatalf("result=%#v", result)
	}
}

func TestRerankDegraded(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float32{"x": {1, 0}}}
	service, err := Open(filepath.Join(t.TempDir(), "memory"), embedder, fakeReranker{fail: true}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Write(context.Background(), knowledgev1.WriteRequest{
		Scope: "workspace", Text: "x", IdempotencyKey: "w",
	}); err != nil {
		t.Fatal(err)
	}
	service.embedder = nil
	result, err := service.Search(context.Background(), knowledgev1.Query{
		Text: "x", Scope: "workspace", Rerank: true, AllowDegraded: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, warning := range result.Warnings {
		found = found || warning.Cause == "timeout"
	}
	if !found {
		t.Fatalf("warnings=%#v", result.Warnings)
	}
}

func TestServiceErrorPreserved(t *testing.T) {
	original := &ServiceError{Code: "quota_exhausted", Message: "quota"}
	mapped := mapLLMError(original)
	var serviceErr *ServiceError
	if !errors.As(mapped, &serviceErr) || serviceErr.Code != original.Code {
		t.Fatalf("mapped=%#v", mapped)
	}
}

func TestFailedUpdateDoesNotMutateMetadata(t *testing.T) {
	ctx := context.Background()
	embedder := &fakeEmbedder{vectors: map[string][]float32{"text": {1, 0}}}
	service, err := Open(filepath.Join(t.TempDir(), "memory"), embedder, nil, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	ref, err := service.Write(ctx, knowledgev1.WriteRequest{
		Scope: "workspace", Text: "text", Metadata: map[string]string{"a": "1"}, IdempotencyKey: "w",
	})
	if err != nil {
		t.Fatal(err)
	}
	embedder.fail = true
	_, err = service.Update(ctx, knowledgev1.UpdateRequest{
		RecordID: ref.RecordID, ExpectedVersion: 1, Text: "text",
		PatchMetadata: map[string]string{"a": "2"}, IdempotencyKey: "u",
	})
	if err == nil {
		t.Fatal("update should fail when embedding is unavailable")
	}
	if got := service.records[ref.RecordID].Metadata["a"]; got != "1" {
		t.Fatalf("metadata mutated to %q", got)
	}
}

func TestRerankRequiresEveryCandidate(t *testing.T) {
	embedder := &fakeEmbedder{vectors: map[string][]float32{"x": {1, 0}, "x y": {1, 0}}}
	service, err := Open(filepath.Join(t.TempDir(), "memory"), embedder, partialReranker{}, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Write(context.Background(), knowledgev1.WriteRequest{
		Scope: "workspace", Text: "x y", IdempotencyKey: "w",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Write(context.Background(), knowledgev1.WriteRequest{
		Scope: "workspace", Text: "x z", IdempotencyKey: "w2",
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Search(context.Background(), knowledgev1.Query{
		Text: "x", Scope: "workspace", Rerank: true, Limit: 1,
	})
	if err == nil {
		t.Fatal("partial rerank response must be rejected in strict mode")
	}
}

func TestSearchRejectsEmptyQueryAndUnknownScope(t *testing.T) {
	service, err := Open(filepath.Join(t.TempDir(), "memory"), &fakeEmbedder{}, nil, DefaultConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(context.Background(), knowledgev1.Query{Scope: "workspace"}); err == nil {
		t.Fatal("empty query must fail")
	}
	if _, err := service.Search(context.Background(), knowledgev1.Query{Text: "x", Scope: "invalid"}); err == nil {
		t.Fatal("unknown scope must fail")
	}
}
