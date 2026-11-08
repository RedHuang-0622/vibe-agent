package llm

import (
	"context"
	"encoding/json"
	"time"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

const (
	CodeMissingCredential    = "missing_credential"
	CodeAuthenticationFailed = "authentication_failed"
	CodeQuotaExhausted       = "quota_exhausted"
	CodeRateLimited          = "rate_limited"
	CodeTimeout              = "timeout"
	CodeDependency           = "dependency_unavailable"
	CodeUpstreamSchema       = "upstream_schema_error"
	CodeUnsupported          = "unsupported_capability"
)

type Error struct {
	Code      string
	Message   string
	Retryable bool
	Scope     string
	Provider  string
	RequestID string
	ResetAt   *time.Time
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

type Request struct {
	Messages    []Message
	Tools       []ToolDefinition
	Temperature float64
	Purpose     string
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type Response struct {
	Content      string
	ToolCalls    []ToolCall
	FinishReason string
	Usage        Usage
	AccountID    string
	Revision     uint64
	Model        string
}

type EmbeddingBatch struct {
	Vectors     [][]float32
	Model       string
	Dimensions  int
	Fingerprint string
	Usage       Usage
}

type RerankRequest struct {
	Query     string
	Documents []string
	TopN      int
}

type RerankResult struct {
	Index int
	Score float64
}

type RerankResponse struct {
	Results []RerankResult
	Model   string
}

type Reader interface {
	ResolveForInvocation(context.Context, accountv1.ResolveRequest) (accountv1.ResolvedInvocation, error)
}

type LeaseRedeemer interface {
	Redeem(context.Context, string, string) ([]byte, error)
}

// gatewayAPI is kept narrow so tests can replace transport while retaining
// capability resolution and lease behavior in the concrete gateway.
type gatewayAPI interface {
	Complete(context.Context, Request) (Response, error)
	Embed(context.Context, []string) (EmbeddingBatch, error)
	Rerank(context.Context, RerankRequest) (RerankResponse, error)
}
