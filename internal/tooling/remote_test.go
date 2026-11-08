package tooling

import (
	"bytes"
	"net/http"
	"testing"
	"time"
)

func TestHTTPErrorMappings(t *testing.T) {
	tests := []struct {
		status int
		code   string
	}{
		{status: http.StatusUnauthorized, code: CodeAuthenticationFailed},
		{status: http.StatusPaymentRequired, code: CodeQuotaExhausted},
		{status: http.StatusTooManyRequests, code: CodeRateLimited},
		{status: http.StatusBadGateway, code: CodeDependency},
	}
	for _, tt := range tests {
		response := &http.Response{
			StatusCode: tt.status,
			Header:     http.Header{"Retry-After": []string{"3"}},
			Body:       http.NoBody,
		}
		err := HTTPError(response, "fake")
		if err.Code != tt.code {
			t.Errorf("status %d code=%s, want %s", tt.status, err.Code, tt.code)
		}
	}
}

func TestDecodeJSONObject(t *testing.T) {
	value, err := DecodeJSONObject(bytes.NewBufferString(`{"ok":true}`))
	if err != nil || value["ok"] != true {
		t.Fatalf("value=%v err=%v", value, err)
	}
	at := retryAt("2", time.Unix(100, 0))
	if at == nil || at.Unix() != 102 {
		t.Fatalf("retryAt=%v", at)
	}
}
