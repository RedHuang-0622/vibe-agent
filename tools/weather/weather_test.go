package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"vibe-agent/internal/tooling"
)

func TestWeatherSuccessAndUpstreamFailure(t *testing.T) {
	mode := "ok"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mode == "failure" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		if r.URL.Query().Get("location") != "Shanghai" {
			t.Errorf("location=%q", r.URL.Query().Get("location"))
		}
		_, _ = w.Write([]byte(`{"temperature_c":18}`))
	}))
	defer server.Close()
	tool := New(ResolverFunc(func(context.Context) (tooling.RemoteConfig, error) {
		return tooling.RemoteConfig{BaseURL: server.URL, Path: "/weather", Provider: "fake"}, nil
	}), server.Client())
	args, _ := json.Marshal(map[string]string{"location": "Shanghai"})
	if _, err := tool.Invoke(context.Background(), tooling.Call{Arguments: args}); err != nil {
		t.Fatal(err)
	}
	mode = "failure"
	if _, err := tool.Invoke(context.Background(), tooling.Call{Arguments: args}); err == nil {
		t.Fatal("upstream failure must be mapped")
	}
}
