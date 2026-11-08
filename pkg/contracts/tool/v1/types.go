package toolv1

import (
	"encoding/json"
	"time"
)

type InvocationMode string

const (
	InvocationModeModel  InvocationMode = "model"
	InvocationModeDirect InvocationMode = "direct"
)

type Timing struct {
	QueueMS      int64 `json:"queue_ms"`
	ValidationMS int64 `json:"validation_ms"`
	ExecuteMS    int64 `json:"execute_ms"`
	TotalMS      int64 `json:"total_ms"`
}

type ToolSpec struct {
	Name               string          `json:"name"`
	Version            string          `json:"version"`
	Kind               string          `json:"kind"`
	Description        string          `json:"description"`
	Parameters         json.RawMessage `json:"parameters"`
	DirectRequestField string          `json:"direct_request_field,omitempty"`
	ReadOnly           bool            `json:"read_only"`
	TimeoutMS          int64           `json:"timeout_ms"`
	Capabilities       []string        `json:"capabilities,omitempty"`
	Idempotent         bool            `json:"idempotent"`
}

type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Scope     string         `json:"scope,omitempty"`
	Provider  string         `json:"provider,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
	ResetAt   *time.Time     `json:"reset_at,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
}

type Result struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *Error          `json:"error,omitempty"`
	Timing  Timing          `json:"timing"`
}

type ListResponse struct {
	Tools []ToolSpec `json:"tools"`
}
