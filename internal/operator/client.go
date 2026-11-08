package operator

import (
	"context"
	"encoding/json"

	"vibe-agent/internal/sessionruntime"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type Client struct {
	hub *sessionruntime.HubClient
}

func NewClient(hub *sessionruntime.HubClient) *Client {
	return &Client{hub: hub}
}

func (c *Client) Invoke(ctx context.Context, method string, payload, target any) error {
	return c.hub.Invoke(ctx, method, payload, target)
}

func (c *Client) Status(ctx context.Context) (hubStatus, error) {
	var result hubStatus
	err := c.hub.Invoke(ctx, sessionv1.MethodHubStatus, struct{}{}, &result)
	return result, err
}

type hubStatus struct {
	Endpoint string `json:"endpoint"`
	Version  string `json:"version"`
}

func (c *Client) CreateSession(ctx context.Context, name string) (sessionv1.SessionView, error) {
	var result sessionv1.CreateSessionResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodHubCreateSession, sessionv1.CreateSessionRequest{Name: name}, &result)
	return result.Session, err
}

func (c *Client) ListSessions(ctx context.Context) ([]sessionv1.SessionView, error) {
	var result struct {
		Sessions []sessionv1.SessionView `json:"sessions"`
	}
	err := c.hub.Invoke(ctx, sessionv1.MethodHubListSessions, struct{}{}, &result)
	return result.Sessions, err
}

func (c *Client) InspectSession(ctx context.Context, sessionID string) (sessionv1.SessionView, error) {
	var result sessionv1.CreateSessionResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodHubInspect, sessionv1.SessionRef{SessionID: sessionID}, &result)
	return result.Session, err
}

func (c *Client) StopSession(ctx context.Context, sessionID string) error {
	return c.hub.Invoke(ctx, sessionv1.MethodHubStopSession, sessionv1.SessionRef{SessionID: sessionID}, nil)
}

func (c *Client) Submit(ctx context.Context, sessionID, text, queuePolicy string) (sessionv1.SubmitResponse, error) {
	var result sessionv1.SubmitResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionSubmit, sessionv1.SubmitRequest{
		SessionID: sessionID, Text: text, QueuePolicy: queuePolicy,
	}, &result)
	return result, err
}

func (c *Client) DirectTool(ctx context.Context, sessionID, command string) (sessionv1.DirectToolResponse, error) {
	var result sessionv1.DirectToolResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionDirect, sessionv1.DirectToolRequest{
		SessionID: sessionID, Command: command,
	}, &result)
	return result, err
}

func (c *Client) Cancel(ctx context.Context, sessionID string) (bool, error) {
	var result map[string]bool
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionCancel, sessionv1.CancelRequest{SessionID: sessionID}, &result)
	return result["canceled"], err
}

func (c *Client) Compress(ctx context.Context, request sessionv1.CompressionRequest) (sessionv1.CompressionResponse, error) {
	var result sessionv1.CompressionResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionCompress, request, &result)
	return result, err
}

func (c *Client) Events(ctx context.Context, sessionID string, afterSeq int64, limit int) (sessionv1.EventsResponse, error) {
	var result sessionv1.EventsResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionEvents, sessionv1.EventsRequest{
		SessionID: sessionID, AfterSeq: afterSeq, Limit: limit,
	}, &result)
	return result, err
}

func (c *Client) Watch(ctx context.Context, request sessionv1.WatchRequest) (sessionv1.EventsResponse, error) {
	var result sessionv1.EventsResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionWatch, request, &result)
	return result, err
}

func (c *Client) Snapshot(ctx context.Context, sessionID string, tailLimit int) (sessionv1.SessionSnapshotResponse, error) {
	var result sessionv1.SessionSnapshotResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionSnapshot, sessionv1.SessionSnapshotRequest{
		SessionID: sessionID, TailLimit: tailLimit,
	}, &result)
	return result, err
}

func (c *Client) History(ctx context.Context, request sessionv1.SessionHistoryRequest) (sessionv1.SessionHistoryResponse, error) {
	var result sessionv1.SessionHistoryResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionHistory, request, &result)
	return result, err
}

func (c *Client) Trace(ctx context.Context, request sessionv1.TraceRequest) (sessionv1.TraceResponse, error) {
	var result sessionv1.TraceResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionTrace, request, &result)
	return result, err
}

func (c *Client) Stats(ctx context.Context, request sessionv1.StatsRequest) (sessionv1.StatsResponse, error) {
	var result sessionv1.StatsResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodSessionStats, request, &result)
	return result, err
}

func (c *Client) Tools(ctx context.Context, sessionID string) (toolv1.ListResponse, error) {
	var result toolv1.ListResponse
	err := c.hub.Invoke(ctx, sessionv1.MethodHubToolList, map[string]string{"session_id": sessionID}, &result)
	return result, err
}

func (c *Client) ListAccounts(ctx context.Context, filter accountv1.ListFilter) ([]accountv1.AccountView, error) {
	var result []accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodAdminList, filter, &result)
	return result, err
}

func (c *Client) Account(ctx context.Context, ref accountv1.AccountRef) (accountv1.AccountView, error) {
	var result accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodReadGet, ref, &result)
	return result, err
}

func (c *Client) CreateAccount(ctx context.Context, request accountv1.CreateAccount) (accountv1.AccountView, error) {
	var result accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodAdminCreate, request, &result)
	return result, err
}

func (c *Client) UpdateAccount(ctx context.Context, request accountv1.UpdateAccount) (accountv1.AccountView, error) {
	var result accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodAdminUpdate, request, &result)
	return result, err
}

func (c *Client) SetDefault(ctx context.Context, request accountv1.SetDefault) (accountv1.AccountView, error) {
	var result accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodAdminSetDefault, request, &result)
	return result, err
}

func (c *Client) SetEnabled(ctx context.Context, request accountv1.SetEnabled) (accountv1.AccountView, error) {
	var result accountv1.AccountView
	err := c.hub.Invoke(ctx, accountv1.MethodAdminSetEnabled, request, &result)
	return result, err
}

func (c *Client) ArchiveAccount(ctx context.Context, ref accountv1.AccountRef) error {
	return c.hub.Invoke(ctx, accountv1.MethodAdminArchive, ref, nil)
}

func (c *Client) ValidateAccount(ctx context.Context, request accountv1.ValidateAccount) (accountv1.ValidationReport, error) {
	var result accountv1.ValidationReport
	err := c.hub.Invoke(ctx, accountv1.MethodAdminValidate, request, &result)
	return result, err
}

func (c *Client) SmokeAccount(ctx context.Context, request accountv1.SmokeRequest) (accountv1.SmokeReport, error) {
	var result accountv1.SmokeReport
	err := c.hub.Invoke(ctx, accountv1.MethodAdminSmoke, request, &result)
	return result, err
}

func (c *Client) MemorySearch(ctx context.Context, request knowledgev1.Query) (knowledgev1.Result, error) {
	var result knowledgev1.Result
	err := c.hub.Invoke(ctx, sessionv1.MethodKnowledgeSearch, request, &result)
	return result, err
}

func decodeMap[T any](value any) (T, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		var zero T
		return zero, err
	}
	var result T
	err = json.Unmarshal(encoded, &result)
	return result, err
}
