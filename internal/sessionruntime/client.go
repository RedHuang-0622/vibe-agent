package sessionruntime

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type Client struct {
	endpoint string
	token    string
}

func NewClient(endpoint, token string) *Client {
	return &Client{endpoint: endpoint, token: token}
}

func (c *Client) Do(ctx context.Context, method string, payload, target any) error {
	var dialer net.Dialer
	connection, err := dialer.DialContext(ctx, "tcp", c.endpoint)
	if err != nil {
		return fmt.Errorf("connect sessiond: %w", err)
	}
	defer connection.Close()
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	requestID, err := randomToken(12)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if err := WriteFrame(connection, sessionv1.Frame{
		Version: sessionv1.Version, RequestID: requestID,
		Method: method, Token: c.token, Payload: encoded,
	}); err != nil {
		return err
	}
	var response sessionv1.FrameResponse
	if err := ReadFrame(bufio.NewReader(connection), &response); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, net.ErrClosed) || errors.Is(err, context.DeadlineExceeded) {
			return errors.New("sessiond connection closed")
		}
		return fmt.Errorf("read session response: %w", err)
	}
	if response.Version != sessionv1.Version || response.RequestID != requestID {
		return errors.New("sessiond protocol response mismatch")
	}
	if !response.OK {
		if response.Error == nil {
			return errors.New("sessiond returned an empty error")
		}
		return &CodedError{
			Code: response.Error.Code, Message: response.Error.Message,
			Retryable: response.Error.Retryable, Scope: response.Error.Scope,
			Provider: response.Error.Provider, RequestID: response.Error.RequestID,
			ResetAt: response.Error.ResetAt,
		}
	}
	if target == nil || len(response.Payload) == 0 {
		return nil
	}
	return json.Unmarshal(response.Payload, target)
}

func (c *Client) Health(ctx context.Context) (sessionv1.HealthResponse, error) {
	var health sessionv1.HealthResponse
	err := c.Do(ctx, sessionv1.MethodSessionHealth, struct{}{}, &health)
	return health, err
}

func RandomToken(bytes int) (string, error) {
	return randomToken(bytes)
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

type Metadata struct {
	SessionID  string    `json:"session_id"`
	PID        int       `json:"pid"`
	Generation uint64    `json:"generation"`
	Endpoint   string    `json:"endpoint"`
	Token      string    `json:"token"`
	Protocol   string    `json:"protocol"`
	StartedAt  time.Time `json:"started_at"`
}
