package search

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type ConfigResolver interface {
	Resolve(context.Context) (tooling.RemoteConfig, error)
}

type ResolverFunc func(context.Context) (tooling.RemoteConfig, error)

func (f ResolverFunc) Resolve(ctx context.Context) (tooling.RemoteConfig, error) {
	return f(ctx)
}

type Tool struct {
	resolver ConfigResolver
	client   *http.Client
}

func New(resolver ConfigResolver, client *http.Client) *Tool {
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	return &Tool{resolver: resolver, client: client}
}

func Spec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:               "search",
		Version:            "v1",
		Kind:               "remote",
		Description:        "Search an account-backed web search endpoint",
		Parameters:         json.RawMessage(`{"type":"object","required":["query"],"properties":{"query":{"type":"string"},"limit":{"type":"integer"}}}`),
		DirectRequestField: "query",
		ReadOnly:           true,
		TimeoutMS:          20000,
		Capabilities:       []string{"search"},
	}
}

func (t *Tool) Invoke(ctx context.Context, call tooling.Call) (any, error) {
	var args struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Query) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "query is required", false)
	}
	if args.Limit <= 0 || args.Limit > 20 {
		args.Limit = 5
	}
	config, err := t.resolver.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	body, _ := json.Marshal(map[string]any{"query": args.Query, "limit": args.Limit})
	requestCtx := ctx
	cancel := func() {}
	if config.TimeoutMS > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(config.TimeoutMS)*time.Millisecond)
	}
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, strings.TrimRight(config.BaseURL, "/")+"/"+strings.TrimLeft(config.Path, "/"), bytes.NewReader(body))
	if err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid search endpoint", false)
	}
	request.Header.Set("Content-Type", "application/json")
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	response, err := t.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, tooling.NewError(tooling.CodeDependency, "search provider is unavailable", true)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, tooling.HTTPError(response, config.Provider)
	}
	defer response.Body.Close()
	decoded, err := tooling.DecodeJSONObject(response.Body)
	if err != nil {
		return nil, err
	}
	results, ok := decoded["results"]
	if !ok {
		results = []any{}
	}
	return map[string]any{"results": results, "query": args.Query}, nil
}

type Adapter struct{}

func (Adapter) Adapt(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "search query is required", false)
	}
	return json.Marshal(map[string]string{"query": raw})
}
