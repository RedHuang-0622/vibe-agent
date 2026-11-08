package search

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vibe-agent/internal/tooling"
)

func TestSearchSuccessAndQuota(t *testing.T) {
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "quota" {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusPaymentRequired)
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"title":"Go"}]}`))
	}))
	defer server.Close()
	tool := New(ResolverFunc(func(context.Context) (tooling.RemoteConfig, error) {
		return tooling.RemoteConfig{BaseURL: server.URL, Path: "/search", Provider: "fake"}, nil
	}), server.Client())
	args, _ := json.Marshal(map[string]any{"query": "Go", "limit": 3})
	payload, err := tool.Invoke(context.Background(), tooling.Call{Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	if len(payload.(map[string]any)["results"].([]any)) != 1 {
		t.Fatalf("payload=%v", payload)
	}
	mode = "quota"
	if _, err := tool.Invoke(context.Background(), tooling.Call{Arguments: args}); err == nil {
		t.Fatal("quota must fail")
	}
}
