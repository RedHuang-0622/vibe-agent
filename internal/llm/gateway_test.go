package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

type fakeReader struct {
	binding accountv1.BindingView
}

func (f fakeReader) ResolveForInvocation(_ context.Context, request accountv1.ResolveRequest) (accountv1.ResolvedInvocation, error) {
	binding := f.binding
	binding.Capability = request.Capability
	return accountv1.ResolvedInvocation{
		Account:   accountv1.AccountRef{AccountID: "acct", Revision: 3},
		Binding:   binding,
		LeaseID:   "lease-1",
		ExpiresAt: time.Now().Add(time.Minute),
	}, nil
}

type fakeLease struct{}

func (fakeLease) Redeem(context.Context, string, string) ([]byte, error) {
	return []byte("secret"), nil
}

func TestGatewayChatToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "deepseek-flash" || body["stream"] != false {
			t.Errorf("body=%#v", body)
		}
		thinking, _ := body["thinking"].(map[string]any)
		if thinking["type"] != "enabled" || body["reasoning_effort"] != "high" {
			t.Errorf("thinking=%#v effort=%#v", thinking, body["reasoning_effort"])
		}
		if _, present := body["temperature"]; present {
			t.Errorf("zero temperature should be omitted: %#v", body)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"tool_calls":[{"id":"c1","function":{"name":"calculator","arguments":"{\"expression\":\"1+2\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"total_tokens":3}}`))
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/chat", Model: "deepseek-flash", TimeoutMS: 1000,
		Thinking: &accountv1.ThinkingConfig{Type: "enabled"}, ReasoningEffort: "high",
	}}, fakeLease{}, server.Client())
	response, err := gateway.Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "calc"}},
		Tools:    []ToolDefinition{{Name: "calculator", Parameters: json.RawMessage(`{"type":"object"}`)}},
		Purpose:  "chat",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "calculator" {
		t.Fatalf("response=%#v", response)
	}
}

func TestGatewayOpenAIFormatsToolContinuationMessages(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Messages) != 4 {
			t.Fatalf("messages=%#v", body.Messages)
		}
		calls, ok := body.Messages[2]["tool_calls"].([]any)
		if !ok || len(calls) != 1 {
			t.Fatalf("assistant tool_calls=%#v", body.Messages[2])
		}
		function := calls[0].(map[string]any)["function"].(map[string]any)
		if function["name"] != "calculator" || function["arguments"] != `{"expression":"1+2"}` {
			t.Fatalf("function=%#v", function)
		}
		if body.Messages[3]["role"] != "tool" || body.Messages[3]["tool_call_id"] != "c1" {
			t.Fatalf("tool message=%#v", body.Messages[3])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/chat", Model: "model",
		Protocol: "openai_compatible", TimeoutMS: 1000,
	}}, fakeLease{}, server.Client())
	_, err := gateway.Complete(context.Background(), Request{Messages: []Message{
		{Role: "system", Content: "policy"},
		{Role: "user", Content: "calc"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "c1", Name: "calculator", Arguments: `{"expression":"1+2"}`,
		}}},
		{Role: "tool", ToolCallID: "c1", Content: `{"value":3}`},
	}})
	if err != nil {
		t.Fatal(err)
	}
}

func TestGatewayAnthropicToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" ||
			r.Header.Get("anthropic-version") != "2023-06-01" {
			t.Errorf("headers=%v", r.Header)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["system"] != "policy" || body["max_tokens"] != float64(anthropicMaxOutputTokens) {
			t.Fatalf("body=%#v", body)
		}
		tools := body["tools"].([]any)
		tool := tools[0].(map[string]any)
		if tool["name"] != "todo_board" || tool["input_schema"] == nil {
			t.Fatalf("tools=%#v", tools)
		}
		_, _ = w.Write([]byte(`{
			"content":[
				{"type":"text","text":"working"},
				{"type":"tool_use","id":"toolu-1","name":"todo_board","input":{"action":"list"}}
			],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":12,"output_tokens":4}
		}`))
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/v1/messages", Model: "model",
		Protocol: "anthropic_messages_v1", TimeoutMS: 1000,
	}}, fakeLease{}, server.Client())
	response, err := gateway.Complete(context.Background(), Request{
		Messages: []Message{{Role: "system", Content: "policy"}, {Role: "user", Content: "list"}},
		Tools: []ToolDefinition{{
			Name: "todo_board", Description: "board",
			Parameters: json.RawMessage(`{"type":"object"}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.ToolCalls) != 1 || response.ToolCalls[0].Name != "todo_board" ||
		response.ToolCalls[0].Arguments != `{"action":"list"}` || response.Usage.TotalTokens != 16 {
		t.Fatalf("response=%#v", response)
	}
}

func TestAnthropicMessagesFormatsToolResult(t *testing.T) {
	messages, system := anthropicMessages([]Message{
		{Role: "system", Content: "policy"},
		{Role: "user", Content: "update"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "c1", Name: "todo_board", Arguments: `{"action":"update"}`,
		}}},
		{Role: "tool", ToolCallID: "c1", Content: `{"ok":true}`},
	})
	if system != "policy" || len(messages) != 3 {
		t.Fatalf("system=%q messages=%#v", system, messages)
	}
	resultBlocks := messages[2]["content"].([]any)
	result := resultBlocks[0].(map[string]any)
	if result["type"] != "tool_result" || result["tool_use_id"] != "c1" {
		t.Fatalf("tool result=%#v", result)
	}
}

func TestGatewayEmbeddingAndDimensionMismatch(t *testing.T) {
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dimensions := 2
		if mode == "bad" {
			dimensions = 1
		}
		if dimensions == 1 {
			_, _ = w.Write([]byte(`{"data":[{"embedding":[1]}]}`))
			return
		}
		_, _ = fmt.Fprint(w, `{"data":[{"embedding":[3,4]}]}`)
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/embeddings", Model: "embed", Dimensions: 2, Normalize: true,
	}}, fakeLease{}, server.Client())
	batch, err := gateway.Embed(context.Background(), []string{"x"})
	if err != nil {
		t.Fatal(err)
	}
	if batch.Dimensions != 2 || batch.Fingerprint != "embed:2:norm=true" {
		t.Fatalf("batch=%#v", batch)
	}
	mode = "bad"
	if _, err := gateway.Embed(context.Background(), []string{"x"}); err == nil {
		t.Fatal("dimension mismatch must fail")
	}
}

func TestGatewayEmbeddingBatchesAndDimensions(t *testing.T) {
	var calls int
	var batchSizes []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input      []string `json:"input"`
			Dimensions int      `json:"dimensions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Dimensions != 2 {
			t.Errorf("dimensions=%d", body.Dimensions)
		}
		calls++
		batchSizes = append(batchSizes, len(body.Input))
		data := make([]map[string]any, 0, len(body.Input))
		for range body.Input {
			data = append(data, map[string]any{"embedding": []float32{1, 0}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": "embed-model"})
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/embeddings", Model: "embed-model",
		Dimensions: 2, BatchSize: 2, TimeoutMS: 1000,
	}}, fakeLease{}, server.Client())
	batch, err := gateway.Embed(context.Background(), []string{"a", "b", "c", "d", "e"})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 || len(batchSizes) != 3 || batchSizes[0] != 2 || batchSizes[1] != 2 || batchSizes[2] != 1 {
		t.Fatalf("calls=%d sizes=%v", calls, batchSizes)
	}
	if len(batch.Vectors) != 5 || batch.Fingerprint != "embed-model:2:norm=false" {
		t.Fatalf("batch=%#v", batch)
	}
}

func TestGatewayRerankTopN(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body["model"] != "rerank" || body["query"] != "q" || body["top_n"] != float64(2) {
			t.Errorf("body=%#v", body)
		}
		if documents, ok := body["documents"].([]any); !ok || len(documents) != 2 {
			t.Errorf("documents=%#v", body["documents"])
		}
		_, _ = w.Write([]byte(`{"results":[{"index":1,"relevance_score":0.9},{"index":0,"relevance_score":0.4}]}`))
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/rerank", Model: "rerank", TopN: 2, MaxCandidates: 3,
	}}, fakeLease{}, server.Client())
	result, err := gateway.Rerank(context.Background(), RerankRequest{Query: "q", Documents: []string{"a", "b"}, TopN: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Results) != 2 || result.Results[0].Index != 1 {
		t.Fatalf("result=%#v", result)
	}
}

func TestGatewayUsesBindingTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"late"}}]}`))
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/chat", Model: "model", TimeoutMS: 20,
	}}, fakeLease{}, server.Client())
	_, err := gateway.Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != CodeTimeout {
		t.Fatalf("err=%#v", err)
	}
}

func TestGatewayQuotaPreservesResetInformation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusPaymentRequired)
	}))
	defer server.Close()
	gateway := NewGateway(fakeReader{binding: accountv1.BindingView{
		BaseURL: server.URL, Path: "/chat", Model: "model", TimeoutMS: 1000,
	}}, fakeLease{}, server.Client())
	_, err := gateway.Complete(context.Background(), Request{
		Messages: []Message{{Role: "user", Content: "hello"}},
	})
	var providerErr *Error
	if !errors.As(err, &providerErr) || providerErr.Code != CodeQuotaExhausted ||
		providerErr.Retryable || providerErr.ResetAt == nil {
		t.Fatalf("err=%#v", err)
	}
}
