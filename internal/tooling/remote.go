package tooling

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const MaxRemoteResponseBytes = 4 << 20

type RemoteConfig struct {
	BaseURL   string
	Path      string
	Model     string
	APIKey    string
	Provider  string
	TimeoutMS int64
}

func HTTPError(response *http.Response, provider string) *Error {
	defer response.Body.Close()
	err := &Error{
		Provider: provider,
		Scope:    "provider",
	}
	switch {
	case response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden:
		err.Code = CodeAuthenticationFailed
		err.Message = "provider authentication failed"
	case response.StatusCode == http.StatusPaymentRequired || response.StatusCode == http.StatusTooManyRequests && strings.Contains(strings.ToLower(response.Header.Get("X-Rate-Limit-Reason")), "quota"):
		err.Code = CodeQuotaExhausted
		err.Message = "provider quota is exhausted"
		err.Details = map[string]any{"status": response.StatusCode}
	case response.StatusCode == http.StatusTooManyRequests:
		err.Code = CodeRateLimited
		err.Message = "provider rate limit reached"
		err.Retryable = true
	case response.StatusCode >= 500:
		err.Code = CodeDependency
		err.Message = "provider is unavailable"
		err.Retryable = true
	default:
		err.Code = CodeDependency
		err.Message = "provider rejected the request"
		err.Details = map[string]any{"status": response.StatusCode}
	}
	err.ResetAt = retryAt(response.Header.Get("Retry-After"), time.Now())
	return err
}

func retryAt(value string, now time.Time) *time.Time {
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

func DecodeJSONObject(reader io.Reader) (map[string]any, error) {
	limited := io.LimitReader(reader, MaxRemoteResponseBytes)
	decoder := json.NewDecoder(limited)
	decoder.UseNumber()
	var value map[string]any
	if err := decoder.Decode(&value); err != nil {
		return nil, NewError(CodeUpstreamSchema, "provider returned invalid JSON", false)
	}
	if value == nil {
		return nil, NewError(CodeUpstreamSchema, "provider returned an empty object", false)
	}
	return value, nil
}
