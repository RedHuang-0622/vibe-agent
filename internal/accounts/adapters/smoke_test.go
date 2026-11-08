package adapters

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vibe-agent/internal/accounts/domain"
)

func TestHTTPDemoSmokeRunnerUsesFakeServer(t *testing.T) {
	t.Setenv("SMOKE_KEY", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("authorization=%q", r.Header.Get("Authorization"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if values, ok := body["input"].([]any); !ok || len(values) != 1 || values[0] != "alpha" {
			t.Errorf("body=%#v", body)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]}]}`))
	}))
	defer server.Close()
	account := domain.Account{
		ID: "embed", Revision: 1, Enabled: true, DisplayName: "Embed",
		Services: map[domain.Capability]domain.Binding{
			domain.CapabilityEmbedding: {
				Capability: domain.CapabilityEmbedding,
				BaseURL:    server.URL, Path: "/embed", Model: "embed-model",
				CredentialRef: "env:SMOKE_KEY", Dimensions: 2, BatchSize: 1, TimeoutMS: 2000,
			},
		},
	}
	runner := NewHTTPDemoSmokeRunner(server.Client())
	if err := runner.Run(context.Background(), account, domain.CapabilityEmbedding, "case", map[string]any{
		"texts": []string{"alpha"},
	}, 1000); err != nil {
		t.Fatal(err)
	}
}

func TestHTTPDemoSmokeRunnerSendsDeepSeekThinking(t *testing.T) {
	t.Setenv("SMOKE_KEY", "secret")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		thinking, _ := body["thinking"].(map[string]any)
		if body["model"] != "deepseek-flash" ||
			thinking["type"] != "enabled" ||
			body["reasoning_effort"] != "high" {
			t.Errorf("body=%#v", body)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()
	account := domain.Account{
		ID: "deepseek", Revision: 1, Enabled: true, DisplayName: "DeepSeek",
		Services: map[domain.Capability]domain.Binding{
			domain.CapabilityChat: {
				Capability: domain.CapabilityChat, Provider: "deepseek",
				Protocol: "openai_compatible", BaseURL: server.URL, Path: "/chat",
				Model: "deepseek-flash", CredentialRef: "env:SMOKE_KEY",
				ThinkingType: "enabled", ReasoningEffort: "high", TimeoutMS: 2000,
			},
		},
	}
	runner := NewHTTPDemoSmokeRunner(server.Client())
	if err := runner.Run(context.Background(), account, domain.CapabilityChat, "chat", nil, 1000); err != nil {
		t.Fatal(err)
	}
}
