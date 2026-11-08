package weather

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
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
		client = &http.Client{Timeout: 15 * time.Second}
	}
	return &Tool{resolver: resolver, client: client}
}

func Spec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:               "weatherapi",
		Version:            "v1",
		Kind:               "remote",
		Description:        "Get weather for a location and optional date",
		Parameters:         json.RawMessage(`{"type":"object","required":["location"],"properties":{"location":{"type":"string"},"date":{"type":"string"}}}`),
		DirectRequestField: "location",
		ReadOnly:           true,
		TimeoutMS:          15000,
		Capabilities:       []string{"weather"},
	}
}

func (t *Tool) Invoke(ctx context.Context, call tooling.Call) (any, error) {
	var args struct {
		Location string `json:"location"`
		Date     string `json:"date"`
	}
	if err := json.Unmarshal(call.Arguments, &args); err != nil || strings.TrimSpace(args.Location) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "location is required", false)
	}
	config, err := t.resolver.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(strings.TrimRight(config.BaseURL, "/") + "/" + strings.TrimLeft(config.Path, "/"))
	if err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid weather endpoint", false)
	}
	query := endpoint.Query()
	query.Set("location", args.Location)
	if args.Date != "" {
		query.Set("date", args.Date)
	}
	endpoint.RawQuery = query.Encode()
	requestCtx := ctx
	cancel := func() {}
	if config.TimeoutMS > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(config.TimeoutMS)*time.Millisecond)
	}
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid weather request", false)
	}
	if config.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+config.APIKey)
	}
	response, err := t.client.Do(request)
	if err != nil {
		if requestCtx.Err() != nil {
			return nil, requestCtx.Err()
		}
		return nil, tooling.NewError(tooling.CodeDependency, "weather provider is unavailable", true)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, tooling.HTTPError(response, config.Provider)
	}
	defer response.Body.Close()
	decoded, err := tooling.DecodeJSONObject(response.Body)
	if err != nil {
		return nil, err
	}
	if len(decoded) == 0 {
		return nil, tooling.NewError(tooling.CodeNotFound, "weather result is empty", false)
	}
	return map[string]any{"location": args.Location, "date": args.Date, "weather": decoded}, nil
}

type Adapter struct{}

func (Adapter) Adapt(raw string) (json.RawMessage, error) {
	fields := strings.Fields(raw)
	if len(fields) == 0 {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "weather location is required", false)
	}
	value := map[string]string{"location": fields[0]}
	if len(fields) > 1 {
		value["date"] = fields[1]
	}
	return json.Marshal(value)
}
