package knowledgev1

import "time"

type Channel string

const (
	ChannelBM25   Channel = "bm25"
	ChannelDense  Channel = "dense"
	ChannelRerank Channel = "rerank"
)

type Query struct {
	Text          string `json:"text"`
	Scope         string `json:"scope"`
	Limit         int    `json:"limit"`
	Rerank        bool   `json:"rerank"`
	AllowDegraded bool   `json:"allow_degraded"`
}

type Hit struct {
	RecordID    string   `json:"record_id"`
	ChunkID     string   `json:"chunk_id"`
	Text        string   `json:"text"`
	LexicalRank *int     `json:"lexical_rank,omitempty"`
	DenseRank   *int     `json:"dense_rank,omitempty"`
	FusionScore float64  `json:"fusion_score"`
	RerankScore *float64 `json:"rerank_score,omitempty"`
}

type Result struct {
	Hits      []Hit     `json:"hits"`
	Channels  []Channel `json:"channels"`
	Degraded  bool      `json:"degraded"`
	Warnings  []Warning `json:"warnings,omitempty"`
	Retrieval Retrieval `json:"retrieval"`
}

type Warning struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Cause   string `json:"cause,omitempty"`
}

type Retrieval struct {
	IndexVersion         string `json:"index_version"`
	EmbeddingFingerprint string `json:"embedding_fingerprint,omitempty"`
	RerankModel          string `json:"rerank_model,omitempty"`
	BM25Candidates       int    `json:"bm25_candidates"`
	DenseCandidates      int    `json:"dense_candidates"`
	Returned             int    `json:"returned"`
	BM25MS               int64  `json:"bm25_ms"`
	EmbeddingMS          int64  `json:"embedding_ms"`
	FusionMS             int64  `json:"fusion_ms"`
	RerankMS             int64  `json:"rerank_ms"`
	TotalMS              int64  `json:"total_ms"`
}

type WriteRequest struct {
	ID             string            `json:"id,omitempty"`
	Scope          string            `json:"scope"`
	Text           string            `json:"text"`
	Metadata       map[string]string `json:"metadata,omitempty"`
	IdempotencyKey string            `json:"idempotency_key"`
}

type UpdateRequest struct {
	RecordID        string            `json:"record_id"`
	ExpectedVersion int               `json:"expected_version"`
	Text            string            `json:"text"`
	PatchMetadata   map[string]string `json:"patch_metadata,omitempty"`
	IdempotencyKey  string            `json:"idempotency_key"`
}

type RecordRef struct {
	RecordID  string    `json:"record_id"`
	Version   int       `json:"version"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}
