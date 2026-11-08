package adapters

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
	"os"
	"strings"
	"time"

	"vibe-agent/internal/accounts/domain"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

type HTTPDemoSmokeRunner struct {
	client *http.Client
}

func NewHTTPDemoSmokeRunner(client *http.Client) *HTTPDemoSmokeRunner {
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	return &HTTPDemoSmokeRunner{client: client}
}

func (r *HTTPDemoSmokeRunner) Run(ctx context.Context, account domain.Account, capability domain.Capability, _ string, input map[string]any, timeoutMS int64) error {
	binding, ok := account.Services[capability]
	if !ok {
		return contractSmokeError(accountv1.ErrorNotFound, "capability binding is missing")
	}
	secret, err := credentialValue(binding.CredentialRef)
	if err != nil {
		return contractSmokeError(accountv1.ErrorMissingCredential, "credential is missing")
	}
	requestBody := smokeBody(capability, binding, input)
	endpoint, err := smokeEndpoint(binding.BaseURL, binding.Path)
	if err != nil {
		return contractSmokeError(accountv1.ErrorInvalidRequest, "provider endpoint is invalid")
	}
	timeout := time.Duration(binding.TimeoutMS) * time.Millisecond
	if timeoutMS > 0 && (timeout <= 0 || timeoutMS < binding.TimeoutMS) {
		timeout = time.Duration(timeoutMS) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	encoded, _ := json.Marshal(requestBody)
	request, err := http.NewRequestWithContext(runCtx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return contractSmokeError(accountv1.ErrorInvalidRequest, "provider request is invalid")
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+string(secret))
	response, err := r.client.Do(request)
	if err != nil {
		if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
			return contractSmokeError(accountv1.ErrorDependency, "provider smoke timed out")
		}
		return contractSmokeError(accountv1.ErrorDependency, "provider is unavailable")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if response.StatusCode == http.StatusPaymentRequired || response.StatusCode == http.StatusTooManyRequests {
			code := accountv1.ErrorQuotaExhausted
			if response.StatusCode == http.StatusTooManyRequests {
				code = accountv1.ErrorRateLimited
			}
			return contractSmokeError(code, "provider quota or rate limit is unavailable")
		}
		if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden {
			return contractSmokeError(accountv1.ErrorAuthenticationFailed, "provider authentication failed")
		}
		return contractSmokeError(accountv1.ErrorDependency, "provider smoke failed")
	}
	var decoded map[string]any
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&decoded); err != nil || len(decoded) == 0 {
		return contractSmokeError(accountv1.ErrorInvalidRequest, "provider response schema is invalid")
	}
	if err := validateSmokeResponse(capability, binding, decoded); err != nil {
		return contractSmokeError(accountv1.ErrorInvalidRequest, err.Error())
	}
	return nil
}

func validateSmokeResponse(capability domain.Capability, binding domain.Binding, decoded map[string]any) error {
	switch capability {
	case domain.CapabilityEmbedding:
		data, ok := decoded["data"].([]any)
		if !ok || len(data) == 0 {
			return errors.New("embedding smoke returned no vectors")
		}
		for _, item := range data {
			entry, _ := item.(map[string]any)
			vector, _ := entry["embedding"].([]any)
			if binding.Dimensions > 0 && len(vector) != binding.Dimensions {
				return errors.New("embedding smoke dimension mismatch")
			}
			for _, value := range vector {
				number, ok := value.(float64)
				if !ok || math.IsNaN(number) || math.IsInf(number, 0) {
					return errors.New("embedding smoke returned a non-finite value")
				}
			}
		}
	case domain.CapabilityRerank:
		results, ok := decoded["results"].([]any)
		if !ok || len(results) == 0 {
			return errors.New("rerank smoke returned no results")
		}
		for _, item := range results {
			entry, _ := item.(map[string]any)
			index, ok := entry["index"].(float64)
			score, scoreOK := entry["relevance_score"].(float64)
			if !ok || index < 0 || !scoreOK || math.IsNaN(score) || math.IsInf(score, 0) {
				return errors.New("rerank smoke response is invalid")
			}
		}
	case domain.CapabilitySearch:
		if _, ok := decoded["results"]; !ok {
			return errors.New("search smoke response has no results field")
		}
	default:
		choices, ok := decoded["choices"].([]any)
		if !ok || len(choices) == 0 {
			return errors.New("chat smoke returned no choices")
		}
		choice, _ := choices[0].(map[string]any)
		message, _ := choice["message"].(map[string]any)
		content, _ := message["content"].(string)
		toolCalls, _ := message["tool_calls"].([]any)
		if strings.TrimSpace(content) == "" && len(toolCalls) == 0 {
			return errors.New("chat smoke returned neither text nor tool calls")
		}
	}
	return nil
}

func credentialValue(ref string) ([]byte, error) {
	scheme, locator, ok := strings.Cut(ref, ":")
	if !ok || scheme != "env" || locator == "" {
		return nil, errors.New("unsupported credential reference")
	}
	value := os.Getenv(locator)
	if value == "" {
		return nil, errors.New("missing credential")
	}
	return []byte(value), nil
}

func smokeEndpoint(base, path string) (string, error) {
	parsed, err := url.Parse(strings.TrimRight(base, "/") + "/" + strings.TrimLeft(path, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid endpoint")
	}
	return parsed.String(), nil
}

func smokeBody(capability domain.Capability, binding domain.Binding, input map[string]any) map[string]any {
	if len(input) > 0 {
		body := make(map[string]any, len(input)+2)
		for key, value := range input {
			body[key] = value
		}
		if capability != domain.CapabilitySearch {
			if _, ok := body["model"]; !ok {
				body["model"] = binding.Model
			}
		}
		if capability == domain.CapabilityEmbedding {
			if texts, ok := body["texts"]; ok {
				delete(body, "texts")
				body["input"] = texts
			}
			if _, ok := body["dimensions"]; !ok && binding.Dimensions > 0 {
				body["dimensions"] = binding.Dimensions
			}
		}
		if capability == domain.CapabilityRerank {
			if _, ok := body["top_n"]; !ok && binding.TopN > 0 {
				body["top_n"] = binding.TopN
			}
		}
		if capability == domain.CapabilityChat || capability == domain.CapabilityToolCall {
			if _, ok := body["stream"]; !ok {
				body["stream"] = false
			}
		}
		applyThinkingOptions(body, capability, binding)
		return body
	}
	switch capability {
	case domain.CapabilityEmbedding:
		return map[string]any{
			"model": binding.Model, "input": []string{"smoke"}, "dimensions": binding.Dimensions,
		}
	case domain.CapabilityRerank:
		return map[string]any{"model": binding.Model, "query": "smoke", "documents": []string{"a", "b"}, "top_n": 1}
	case domain.CapabilitySearch:
		return map[string]any{"query": "smoke", "limit": 1}
	default:
		body := map[string]any{
			"model": binding.Model, "stream": false,
			"messages": []map[string]string{{"role": "user", "content": "Reply OK"}},
		}
		applyThinkingOptions(body, capability, binding)
		return body
	}
}

func applyThinkingOptions(body map[string]any, capability domain.Capability, binding domain.Binding) {
	if capability != domain.CapabilityChat && capability != domain.CapabilityToolCall {
		return
	}
	if binding.ThinkingType != "" {
		body["thinking"] = map[string]string{"type": binding.ThinkingType}
	}
	if binding.ReasoningEffort != "" {
		body["reasoning_effort"] = binding.ReasoningEffort
	}
}

func contractSmokeError(code accountv1.ErrorCode, message string) error {
	return &accountv1.ContractError{Code: code, Message: message, Retryable: code == accountv1.ErrorDependency}
}
