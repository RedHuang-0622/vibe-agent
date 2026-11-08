package tooling

import (
	"context"
	"encoding/json"
	"time"

	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

const (
	CodeUnknownTool          = "unknown_tool"
	CodeInvalidRequest       = "invalid_request"
	CodePermissionDenied     = "permission_denied"
	CodeNotFound             = "not_found"
	CodeConflict             = "conflict"
	CodeMissingCredential    = "missing_credential"
	CodeAuthenticationFailed = "authentication_failed"
	CodeQuotaExhausted       = "quota_exhausted"
	CodeRateLimited          = "rate_limited"
	CodeTimeout              = "timeout"
	CodeCanceled             = "canceled"
	CodeDependency           = "dependency_unavailable"
	CodeUnsupported          = "unsupported_capability"
	CodeUpstreamSchema       = "upstream_schema_error"
	CodePartialResult        = "partial_result"
	CodeInternal             = "internal_error"
)

// Error is the single error shape returned by every Tool Runtime path.
type Error struct {
	Code      string
	Message   string
	Retryable bool
	Scope     string
	Provider  string
	RequestID string
	ResetAt   *time.Time
	Details   map[string]any
}

func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

func (e *Error) Public() *toolv1.Error {
	return &toolv1.Error{
		Code:      e.Code,
		Message:   e.Message,
		Retryable: e.Retryable,
		Scope:     e.Scope,
		Provider:  e.Provider,
		RequestID: e.RequestID,
		ResetAt:   e.ResetAt,
		Details:   e.Details,
	}
}

func NewError(code, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

// Call is one normalized direct or model-proposed tool invocation.
type Call struct {
	CallID         string
	ToolName       string
	Version        string
	InvocationMode toolv1.InvocationMode
	Arguments      json.RawMessage
	RawRequest     string
	IdempotencyKey string
	QueuedAt       time.Time
}

// Result is the normalized output of the Tool Runtime. It is converted to
// domain events by the Session Actor, never by a concrete tool.
type Result struct {
	CallID  string
	Name    string
	Kind    string
	Mode    toolv1.InvocationMode
	Payload json.RawMessage
	Error   *Error
	Timing  toolv1.Timing
}

type Handler interface {
	Invoke(ctx context.Context, call Call) (any, error)
}

type DirectAdapter interface {
	Adapt(raw string) (json.RawMessage, error)
}

type Authorizer interface {
	Authorize(ctx context.Context, call Call, spec toolv1.ToolSpec) error
}

type AllowAllAuthorizer struct{}

func (AllowAllAuthorizer) Authorize(context.Context, Call, toolv1.ToolSpec) error {
	return nil
}

type RegisteredTool struct {
	Spec    toolv1.ToolSpec
	Handler Handler
	Direct  DirectAdapter
}
