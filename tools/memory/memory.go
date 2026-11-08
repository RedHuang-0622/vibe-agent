package memory

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"

	"vibe-agent/internal/tooling"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type Client interface {
	Write(ctx context.Context, request knowledgev1.WriteRequest) (knowledgev1.RecordRef, error)
	Search(ctx context.Context, request knowledgev1.Query) (knowledgev1.Result, error)
	Update(ctx context.Context, request knowledgev1.UpdateRequest) (knowledgev1.RecordRef, error)
}

type Tool struct {
	operation string
	client    Client
}

func NewWrite(client Client) *Tool {
	return &Tool{operation: "write", client: client}
}

func NewSearch(client Client) *Tool {
	return &Tool{operation: "search", client: client}
}

func NewUpdate(client Client) *Tool {
	return &Tool{operation: "update", client: client}
}

func (t *Tool) Name() string {
	return "memory_" + t.operation
}

func Spec(operation string) toolv1.ToolSpec {
	var parameters string
	var kind string
	switch operation {
	case "search":
		kind = "memory"
		parameters = `{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"scope":{"type":"string"},"limit":{"type":"integer"},"rerank":{"type":"boolean"},"allow_degraded":{"type":"boolean"}}}`
	case "write":
		kind = "mutating"
		parameters = `{"type":"object","required":["text","idempotency_key"],"properties":{"scope":{"type":"string"},"text":{"type":"string"},"idempotency_key":{"type":"string"},"metadata":{"type":"object"}}}`
	case "update":
		kind = "mutating"
		parameters = `{"type":"object","required":["record_id","text","idempotency_key"],"properties":{"record_id":{"type":"string"},"expected_version":{"type":"integer"},"text":{"type":"string"},"idempotency_key":{"type":"string"},"patch_metadata":{"type":"object"}}}`
	}
	return toolv1.ToolSpec{
		Name:               "memory_" + operation,
		Version:            "v1",
		Kind:               kind,
		Description:        "Read or write the shared agent memory knowledge base",
		Parameters:         json.RawMessage(parameters),
		DirectRequestField: "query",
		ReadOnly:           operation == "search",
		TimeoutMS:          30000,
		Capabilities:       []string{"memory"},
		Idempotent:         operation != "search",
	}
}

func (t *Tool) Invoke(ctx context.Context, call tooling.Call) (any, error) {
	switch t.operation {
	case "search":
		return t.search(ctx, call)
	case "write":
		return t.write(ctx, call)
	case "update":
		return t.update(ctx, call)
	default:
		return nil, tooling.NewError(tooling.CodeInternal, "unknown memory operation", false)
	}
}

func (t *Tool) search(ctx context.Context, call tooling.Call) (any, error) {
	var input struct {
		Query         string `json:"query"`
		Text          string `json:"text"`
		Scope         string `json:"scope"`
		Limit         int    `json:"limit"`
		Rerank        bool   `json:"rerank"`
		AllowDegraded bool   `json:"allow_degraded"`
	}
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid memory query", false)
	}
	text := input.Query
	if text == "" {
		text = input.Text
	}
	if strings.TrimSpace(text) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "memory query is required", false)
	}
	request := knowledgev1.Query{
		Text:          text,
		Scope:         input.Scope,
		Limit:         input.Limit,
		Rerank:        input.Rerank,
		AllowDegraded: input.AllowDegraded,
	}
	if request.Scope == "" {
		request.Scope = "workspace"
	}
	if request.Limit <= 0 || request.Limit > 50 {
		request.Limit = 10
	}
	result, err := t.client.Search(ctx, request)
	if err != nil {
		return nil, mapKnowledgeError(err)
	}
	return result, nil
}

func (t *Tool) write(ctx context.Context, call tooling.Call) (any, error) {
	var request knowledgev1.WriteRequest
	if err := json.Unmarshal(call.Arguments, &request); err != nil || strings.TrimSpace(request.Text) == "" || request.IdempotencyKey == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "text and idempotency_key are required", false)
	}
	if request.Scope == "" {
		request.Scope = "workspace"
	}
	record, err := t.client.Write(ctx, request)
	if err != nil {
		return nil, mapKnowledgeError(err)
	}
	return record, nil
}

func (t *Tool) update(ctx context.Context, call tooling.Call) (any, error) {
	var request knowledgev1.UpdateRequest
	if err := json.Unmarshal(call.Arguments, &request); err != nil || request.RecordID == "" || request.IdempotencyKey == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "record_id and idempotency_key are required", false)
	}
	record, err := t.client.Update(ctx, request)
	if err != nil {
		return nil, mapKnowledgeError(err)
	}
	return record, nil
}

type SearchAdapter struct{}

func (SearchAdapter) Adapt(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "memory query is required", false)
	}
	return json.Marshal(map[string]any{"query": raw})
}

type WriteAdapter struct{}

func (WriteAdapter) Adapt(raw string) (json.RawMessage, error) {
	values := parseKeyValues(raw)
	text, ok := values["text"]
	if !ok {
		text = raw
	}
	return json.Marshal(map[string]any{
		"scope":           valueOr(values["scope"], "workspace"),
		"text":            text,
		"idempotency_key": valueOr(values["idempotency_key"], "direct:"+raw),
	})
}

type UpdateAdapter struct{}

func (UpdateAdapter) Adapt(raw string) (json.RawMessage, error) {
	values := parseKeyValues(raw)
	if values["record_id"] == "" || values["text"] == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "record_id and text are required", false)
	}
	version, _ := strconv.Atoi(values["expected_version"])
	return json.Marshal(map[string]any{
		"record_id":        values["record_id"],
		"expected_version": version,
		"text":             values["text"],
		"idempotency_key":  valueOr(values["idempotency_key"], "direct:"+raw),
	})
}

func parseKeyValues(raw string) map[string]string {
	result := make(map[string]string)
	for _, part := range splitQuoted(raw) {
		key, value, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"`)
		result[strings.TrimSpace(key)] = value
	}
	return result
}

func splitQuoted(raw string) []string {
	var parts []string
	start := 0
	quoted := false
	for i, r := range raw {
		switch r {
		case '"':
			quoted = !quoted
		case ' ':
			if !quoted {
				parts = append(parts, raw[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, raw[start:])
	return parts
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func mapKnowledgeError(err error) error {
	var toolErr *tooling.Error
	if errors.As(err, &toolErr) {
		return toolErr
	}
	return tooling.NewError(tooling.CodeDependency, "memory service is unavailable", true)
}
