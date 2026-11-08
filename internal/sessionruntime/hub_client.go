package sessionruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type HubClient struct {
	baseURL string
	token   string
	client  *http.Client
}

func NewHubClient(baseURL, token string, client *http.Client) *HubClient {
	if client == nil {
		client = &http.Client{Timeout: 3 * time.Minute}
	}
	return &HubClient{baseURL: strings.TrimRight(baseURL, "/"), token: token, client: client}
}

func (c *HubClient) Invoke(ctx context.Context, method string, payload, target any) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	requestBody, err := json.Marshal(sessionv1.HTTPRequest{Method: method, Payload: encoded})
	if err != nil {
		return err
	}
	requestID, err := randomToken(12)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/invoke", bytes.NewReader(requestBody))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Vibe-Token", c.token)
	request.Header.Set("X-Request-ID", requestID)
	response, err := c.client.Do(request)
	if err != nil {
		return fmt.Errorf("connect hub: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
		return &CodedError{Code: "dependency_unavailable", Message: "hub returned an HTTP error", Retryable: true}
	}
	var envelope sessionv1.HTTPResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, MaxFrameBytes)).Decode(&envelope); err != nil {
		return errors.New("hub returned an invalid response")
	}
	if !envelope.OK {
		if envelope.Error == nil {
			return errors.New("hub returned an empty error")
		}
		return &CodedError{
			Code: envelope.Error.Code, Message: envelope.Error.Message,
			Retryable: envelope.Error.Retryable, Scope: envelope.Error.Scope,
			Provider: envelope.Error.Provider, RequestID: envelope.Error.RequestID,
			ResetAt: envelope.Error.ResetAt,
		}
	}
	if target == nil || len(envelope.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(envelope.Payload, target)
}
