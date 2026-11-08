package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

const maxResponseBytes = 8 << 20

type Gateway struct {
	reader Reader
	leases LeaseRedeemer
	client *http.Client
}

type RemoteConfig struct {
	BaseURL   string
	Path      string
	Model     string
	APIKey    string
	Provider  string
	TimeoutMS int64
}

func NewGateway(reader Reader, leases LeaseRedeemer, client *http.Client) *Gateway {
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	return &Gateway{reader: reader, leases: leases, client: client}
}

func (g *Gateway) Complete(ctx context.Context, request Request) (Response, error) {
	capability := accountv1.CapabilityChat
	if len(request.Tools) > 0 {
		capability = accountv1.CapabilityToolCall
	}
	invocation, binding, secret, err := g.resolve(ctx, capability, request.Purpose)
	if err != nil {
		return Response{}, err
	}
	if invocation.Binding.Protocol == "anthropic_messages_v1" {
		return g.completeAnthropic(ctx, invocation, secret, request)
	}
	if invocation.Binding.Protocol != "" && invocation.Binding.Protocol != "openai_compatible" {
		return Response{}, NewError(CodeUnsupported, "chat protocol is not supported", false)
	}
	_ = binding
	body := map[string]any{
		"model":    invocation.Binding.Model,
		"messages": openAIMessages(request.Messages),
		"stream":   false,
	}
	if request.Temperature != 0 {
		body["temperature"] = request.Temperature
	}
	if invocation.Binding.Thinking != nil && invocation.Binding.Thinking.Type != "" {
		body["thinking"] = map[string]string{"type": invocation.Binding.Thinking.Type}
	}
	if invocation.Binding.ReasoningEffort != "" {
		body["reasoning_effort"] = invocation.Binding.ReasoningEffort
	}
	if len(request.Tools) > 0 {
		body["tools"] = toolPayload(request.Tools)
	}
	response, cancel, err := g.postJSON(ctx, invocation.Binding, secret, body)
	if err != nil {
		return Response{}, err
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, mapHTTPError(response)
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := decodeLimited(response.Body, &decoded); err != nil {
		return Response{}, NewError(CodeUpstreamSchema, "chat response schema is invalid", false)
	}
	if len(decoded.Choices) == 0 {
		return Response{}, NewError(CodeUpstreamSchema, "chat response has no choices", false)
	}
	choice := decoded.Choices[0]
	result := Response{
		Content:      choice.Message.Content,
		FinishReason: choice.FinishReason,
		Usage:        decoded.Usage,
		AccountID:    invocation.Account.AccountID,
		Revision:     invocation.Account.Revision,
		Model:        invocation.Binding.Model,
	}
	for _, call := range choice.Message.ToolCalls {
		result.ToolCalls = append(result.ToolCalls, ToolCall{
			ID:        call.ID,
			Name:      call.Function.Name,
			Arguments: call.Function.Arguments,
		})
	}
	return result, nil
}

func (g *Gateway) Embed(ctx context.Context, texts []string) (EmbeddingBatch, error) {
	if len(texts) == 0 {
		return EmbeddingBatch{}, NewError(CodeUnsupported, "embedding input is empty", false)
	}
	invocation, _, secret, err := g.resolve(ctx, accountv1.CapabilityEmbedding, "embedding")
	if err != nil {
		return EmbeddingBatch{}, err
	}
	batchSize := invocation.Binding.BatchSize
	if batchSize <= 0 || batchSize > len(texts) {
		batchSize = len(texts)
	}
	result := EmbeddingBatch{Dimensions: invocation.Binding.Dimensions}
	model := ""
	for start := 0; start < len(texts); start += batchSize {
		end := min(start+batchSize, len(texts))
		vectors, responseModel, usage, err := g.embedBatch(ctx, invocation.Binding, secret, texts[start:end])
		if err != nil {
			return EmbeddingBatch{}, err
		}
		if model == "" {
			model = responseModel
		} else if responseModel != model {
			return EmbeddingBatch{}, NewError(CodeUpstreamSchema, "embedding model changed between batches", false)
		}
		result.Vectors = append(result.Vectors, vectors...)
		result.Usage.PromptTokens += usage.PromptTokens
		result.Usage.CompletionTokens += usage.CompletionTokens
		result.Usage.TotalTokens += usage.TotalTokens
	}
	result.Model = model
	result.Fingerprint = fingerprint(model, invocation.Binding.Dimensions, invocation.Binding.Normalize)
	return result, nil
}

func (g *Gateway) embedBatch(ctx context.Context, binding accountv1.BindingView, secret []byte, texts []string) ([][]float32, string, Usage, error) {
	body := map[string]any{"model": binding.Model, "input": texts}
	if binding.Dimensions > 0 {
		body["dimensions"] = binding.Dimensions
	}
	response, cancel, err := g.postJSON(ctx, binding, secret, body)
	if err != nil {
		return nil, "", Usage{}, err
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", Usage{}, mapHTTPError(response)
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Model string `json:"model"`
		Usage Usage  `json:"usage"`
	}
	if err := decodeLimited(response.Body, &decoded); err != nil {
		return nil, "", Usage{}, NewError(CodeUpstreamSchema, "embedding response schema is invalid", false)
	}
	if len(decoded.Data) != len(texts) {
		return nil, "", Usage{}, NewError(CodeUpstreamSchema, "embedding vector count mismatch", false)
	}
	vectors := make([][]float32, len(decoded.Data))
	for i, item := range decoded.Data {
		if len(item.Embedding) != binding.Dimensions {
			return nil, "", Usage{}, NewError("embedding_dimension_mismatch", "embedding dimension does not match binding", false)
		}
		if !finite(item.Embedding) {
			return nil, "", Usage{}, NewError(CodeUpstreamSchema, "embedding contains non-finite values", false)
		}
		vectors[i] = append([]float32(nil), item.Embedding...)
		if binding.Normalize {
			normalize(vectors[i])
		}
	}
	model := decoded.Model
	if model == "" {
		model = binding.Model
	}
	return vectors, model, decoded.Usage, nil
}

func (g *Gateway) Rerank(ctx context.Context, request RerankRequest) (RerankResponse, error) {
	invocation, _, secret, err := g.resolve(ctx, accountv1.CapabilityRerank, "rerank")
	if err != nil {
		return RerankResponse{}, err
	}
	topN := request.TopN
	if topN <= 0 {
		topN = invocation.Binding.TopN
	}
	if topN > invocation.Binding.MaxCandidates || topN > len(request.Documents) {
		return RerankResponse{}, NewError(CodeUnsupported, "rerank top_n exceeds candidate limit", false)
	}
	body := map[string]any{
		"model":     invocation.Binding.Model,
		"query":     request.Query,
		"documents": request.Documents,
		"top_n":     topN,
	}
	response, cancel, err := g.postJSON(ctx, invocation.Binding, secret, body)
	if err != nil {
		return RerankResponse{}, err
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return RerankResponse{}, mapHTTPError(response)
	}
	var decoded struct {
		Results []struct {
			Index int     `json:"index"`
			Score float64 `json:"relevance_score"`
		} `json:"results"`
		Model string `json:"model"`
	}
	if err := decodeLimited(response.Body, &decoded); err != nil {
		return RerankResponse{}, NewError(CodeUpstreamSchema, "rerank response schema is invalid", false)
	}
	seen := make(map[int]bool)
	results := make([]RerankResult, 0, len(decoded.Results))
	for _, item := range decoded.Results {
		if item.Index < 0 || item.Index >= len(request.Documents) || seen[item.Index] || math.IsNaN(item.Score) || math.IsInf(item.Score, 0) {
			return RerankResponse{}, NewError(CodeUpstreamSchema, "rerank result is invalid", false)
		}
		seen[item.Index] = true
		results = append(results, RerankResult{Index: item.Index, Score: item.Score})
	}
	if len(results) == 0 {
		return RerankResponse{}, NewError(CodeUpstreamSchema, "rerank response is empty", false)
	}
	model := decoded.Model
	if model == "" {
		model = invocation.Binding.Model
	}
	return RerankResponse{Results: results, Model: model}, nil
}

func (g *Gateway) ResolveRemote(ctx context.Context, capability accountv1.Capability, purpose string) (RemoteConfig, error) {
	invocation, _, secret, err := g.resolve(ctx, capability, purpose)
	if err != nil {
		return RemoteConfig{}, err
	}
	return RemoteConfig{
		BaseURL:   invocation.Binding.BaseURL,
		Path:      invocation.Binding.Path,
		Model:     invocation.Binding.Model,
		APIKey:    string(secret),
		Provider:  invocation.Binding.Provider,
		TimeoutMS: invocation.Binding.TimeoutMS,
	}, nil
}

func (g *Gateway) resolve(ctx context.Context, capability accountv1.Capability, purpose string) (accountv1.ResolvedInvocation, accountv1.BindingView, []byte, error) {
	if g.reader == nil || g.leases == nil {
		return accountv1.ResolvedInvocation{}, accountv1.BindingView{}, nil, NewError(CodeDependency, "account services are unavailable", true)
	}
	invocation, err := g.reader.ResolveForInvocation(ctx, accountv1.ResolveRequest{
		Capability: capability,
		Purpose:    purpose,
	})
	if err != nil {
		return accountv1.ResolvedInvocation{}, accountv1.BindingView{}, nil, NewError(CodeDependency, "account capability is unavailable", true)
	}
	secret, err := g.leases.Redeem(ctx, invocation.LeaseID, purpose)
	if err != nil {
		return accountv1.ResolvedInvocation{}, accountv1.BindingView{}, nil, NewError(CodeMissingCredential, "credential lease is unavailable", false)
	}
	return invocation, invocation.Binding, secret, nil
}

func (g *Gateway) postJSON(ctx context.Context, binding accountv1.BindingView, secret []byte, body any) (*http.Response, context.CancelFunc, error) {
	endpoint, err := joinURL(binding.BaseURL, binding.Path)
	if err != nil {
		return nil, func() {}, NewError(CodeDependency, "provider endpoint is invalid", false)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, func() {}, NewError(CodeUpstreamSchema, "request cannot be encoded", false)
	}
	requestCtx := ctx
	cancel := func() {}
	if binding.TimeoutMS > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, time.Duration(binding.TimeoutMS)*time.Millisecond)
	}
	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		cancel()
		return nil, func() {}, NewError(CodeDependency, "provider request cannot be created", false)
	}
	request.Header.Set("Content-Type", "application/json")
	if binding.Protocol == "anthropic_messages_v1" {
		request.Header.Set("anthropic-version", "2023-06-01")
	}
	if len(secret) > 0 {
		request.Header.Set("Authorization", "Bearer "+string(secret))
	}
	response, err := g.client.Do(request)
	if err != nil {
		cancel()
		if errors.Is(requestCtx.Err(), context.DeadlineExceeded) {
			return nil, func() {}, NewError(CodeTimeout, "provider request timed out", true)
		}
		return nil, func() {}, NewError(CodeDependency, "provider is unavailable", true)
	}
	return response, cancel, nil
}

func mapHTTPError(response *http.Response) error {
	defer response.Body.Close()
	resetAt := providerRetryAt(response.Header.Get("Retry-After"), time.Now())
	provider := ""
	if response.Request != nil && response.Request.URL != nil {
		provider = response.Request.URL.Host
	}
	switch response.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &Error{Code: CodeAuthenticationFailed, Message: "provider authentication failed", Scope: "provider", Provider: provider, ResetAt: resetAt}
	case http.StatusPaymentRequired:
		return &Error{Code: CodeQuotaExhausted, Message: "provider quota is exhausted", Scope: "provider", Provider: provider, ResetAt: resetAt}
	case http.StatusTooManyRequests:
		return &Error{Code: CodeRateLimited, Message: "provider rate limit reached", Retryable: true, Scope: "provider", Provider: provider, ResetAt: resetAt}
	default:
		if response.StatusCode >= 500 {
			return &Error{Code: CodeDependency, Message: "provider is unavailable", Retryable: true, Scope: "provider", Provider: provider}
		}
		return &Error{Code: CodeDependency, Message: "provider rejected the request", Scope: "provider", Provider: provider}
	}
}

func providerRetryAt(value string, now time.Time) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		at := now.Add(time.Duration(seconds) * time.Second).UTC()
		return &at
	}
	if parsed, err := http.ParseTime(value); err == nil {
		parsed = parsed.UTC()
		return &parsed
	}
	return nil
}

func NewError(code, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

func joinURL(base, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid URL")
	}
	return parsed.String(), nil
}

func toolPayload(definitions []ToolDefinition) []map[string]any {
	result := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        definition.Name,
				"description": definition.Description,
				"parameters":  definition.Parameters,
			},
		})
	}
	return result
}

func decodeLimited(reader io.Reader, target any) error {
	decoder := json.NewDecoder(io.LimitReader(reader, maxResponseBytes))
	decoder.UseNumber()
	return decoder.Decode(target)
}

func finite(values []float32) bool {
	for _, value := range values {
		converted := float64(value)
		if math.IsNaN(converted) || math.IsInf(converted, 0) {
			return false
		}
	}
	return true
}

func normalize(values []float32) {
	var sum float64
	for _, value := range values {
		sum += float64(value) * float64(value)
	}
	if sum == 0 {
		return
	}
	magnitude := math.Sqrt(sum)
	for i := range values {
		values[i] = float32(float64(values[i]) / magnitude)
	}
}

func fingerprint(model string, dimensions int, normalized bool) string {
	return model + ":" + strconv.Itoa(dimensions) + ":norm=" + strconv.FormatBool(normalized)
}
