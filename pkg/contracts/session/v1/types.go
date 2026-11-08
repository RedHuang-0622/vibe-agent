package sessionv1

import (
	"encoding/json"
	"time"
)

const (
	ContractName    = "vibe.session"
	Version         = 1
	ProtocolVersion = "v1"
)

type SessionRef struct {
	SessionID string `json:"session_id"`
}

// SessionState is the externally visible lifecycle state of one session.
type SessionState string

const (
	StateCreated  SessionState = "created"
	StateStarting SessionState = "starting"
	StateIdle     SessionState = "idle"
	StateRunning  SessionState = "running"
	StateStopped  SessionState = "stopped"
	StateFailed   SessionState = "failed"
)

// SessionView is the Hub control-plane projection of a session process.
type SessionView struct {
	SessionID  string       `json:"session_id"`
	Name       string       `json:"name"`
	State      SessionState `json:"state"`
	PID        int          `json:"pid,omitempty"`
	Generation uint64       `json:"generation"`
	LastSeq    int64        `json:"last_seq"`
	Error      string       `json:"error,omitempty"`
}

type CreateSessionRequest struct {
	Name string `json:"name"`
}

type CreateSessionResponse struct {
	Session SessionView `json:"session"`
}

type SubmitRequest struct {
	SessionID   string `json:"session_id"`
	Text        string `json:"text"`
	QueuePolicy string `json:"queue_policy,omitempty"`
}

type SubmitResponse struct {
	MessageID string `json:"message_id"`
	Seq       int64  `json:"seq"`
}

type DirectToolRequest struct {
	SessionID string `json:"session_id"`
	Command   string `json:"command"`
}

type DirectToolResponse struct {
	TurnID string          `json:"turn_id"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type CancelRequest struct {
	SessionID string `json:"session_id"`
}

type CompressionRequest struct {
	SessionID      string `json:"session_id"`
	Trigger        string `json:"trigger"`
	ThroughSeq     int64  `json:"through_seq,omitempty"`
	KeepTailEvents int    `json:"keep_tail_events,omitempty"`
}

type CompressionResponse struct {
	Committed  bool   `json:"committed"`
	RedLineSeq int64  `json:"red_line_seq,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
}

// EventView is a stable, read-only representation of a committed session
// event. Payload remains opaque to clients.
type EventView struct {
	SchemaVersion int             `json:"schema_version"`
	SessionID     string          `json:"session_id"`
	Seq           int64           `json:"seq"`
	EventID       string          `json:"event_id"`
	Type          string          `json:"type"`
	CreatedAt     time.Time       `json:"created_at"`
	TraceID       string          `json:"trace_id,omitempty"`
	CausationSeq  int64           `json:"causation_seq,omitempty"`
	CallID        string          `json:"call_id,omitempty"`
	Visibility    string          `json:"visibility"`
	Status        string          `json:"status,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

type EventsRequest struct {
	SessionID string `json:"session_id"`
	AfterSeq  int64  `json:"after_seq"`
	Limit     int    `json:"limit,omitempty"`
}

type EventsResponse struct {
	Events         []EventView `json:"events"`
	Watermark      int64       `json:"watermark"`
	Cursor         int64       `json:"cursor"`
	HasMore        bool        `json:"has_more"`
	ResyncRequired bool        `json:"resync_required"`
	TimedOut       bool        `json:"timed_out"`
}

// WatchRequest is a resumable, long-poll observation request. The next request
// must use the returned cursor as after_seq only after the caller applies the
// current page.
type WatchRequest struct {
	SessionID string `json:"session_id"`
	AfterSeq  int64  `json:"after_seq"`
	Limit     int    `json:"limit,omitempty"`
	WaitMS    int    `json:"wait_ms,omitempty"`
}

type SessionSnapshotRequest struct {
	SessionID string `json:"session_id"`
	TailLimit int    `json:"tail_limit,omitempty"`
}

// SessionSnapshotResponse is the latest committed event window. HasMoreBefore
// indicates that older events exist before Events[0].
type SessionSnapshotResponse struct {
	SessionID     string       `json:"session_id"`
	State         SessionState `json:"state"`
	Watermark     int64        `json:"watermark"`
	Events        []EventView  `json:"events"`
	HasMoreBefore bool         `json:"has_more_before"`
}

type SessionHistoryRequest struct {
	SessionID string `json:"session_id"`
	BeforeSeq int64  `json:"before_seq,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// SessionHistoryResponse contains one ascending page strictly before
// BeforeSeq. HasMoreBefore indicates another older page exists.
type SessionHistoryResponse struct {
	SessionID      string      `json:"session_id"`
	Watermark      int64       `json:"watermark"`
	Events         []EventView `json:"events"`
	HasMoreBefore  bool        `json:"has_more_before"`
	ResyncRequired bool        `json:"resync_required"`
}

type TraceRequest struct {
	SessionID string `json:"session_id"`
	TraceID   string `json:"trace_id,omitempty"`
	CallID    string `json:"call_id,omitempty"`
}

type TraceNode struct {
	Seq          int64           `json:"seq"`
	EventID      string          `json:"event_id"`
	Type         string          `json:"type"`
	TraceID      string          `json:"trace_id,omitempty"`
	CausationSeq int64           `json:"causation_seq,omitempty"`
	CallID       string          `json:"call_id,omitempty"`
	Status       string          `json:"status,omitempty"`
	Payload      json.RawMessage `json:"payload"`
}

type TraceResponse struct {
	SessionID string      `json:"session_id"`
	Watermark int64       `json:"watermark"`
	Nodes     []TraceNode `json:"nodes"`
}

type StatsRequest struct {
	SessionID string `json:"session_id"`
	ToolName  string `json:"tool_name,omitempty"`
	Mode      string `json:"invocation_mode,omitempty"`
}

type LatencySummary struct {
	Samples int64   `json:"samples"`
	AvgMS   float64 `json:"avg_ms"`
	P50MS   float64 `json:"p50_ms"`
	P95MS   float64 `json:"p95_ms"`
}

type ToolStat struct {
	ToolName       string           `json:"tool_name"`
	ToolKind       string           `json:"tool_kind"`
	InvocationMode string           `json:"invocation_mode"`
	Calls          int64            `json:"calls"`
	Succeeded      int64            `json:"succeeded"`
	Failed         int64            `json:"failed"`
	InFlight       int64            `json:"in_flight"`
	Orphaned       int64            `json:"orphaned"`
	FailureReasons map[string]int64 `json:"failure_reasons,omitempty"`
	Total          LatencySummary   `json:"total"`
	Execute        LatencySummary   `json:"execute"`
}

type StatsResponse struct {
	SessionID string     `json:"session_id"`
	Watermark int64      `json:"watermark"`
	ByTool    []ToolStat `json:"by_tool"`
}

type HealthResponse struct {
	SessionID  string       `json:"session_id"`
	PID        int          `json:"pid"`
	Generation uint64       `json:"generation"`
	State      SessionState `json:"state"`
	LastSeq    int64        `json:"last_seq"`
	Protocol   string       `json:"protocol"`
}

// Error is a stable transport error and never carries credential material.
type Error struct {
	Code      string     `json:"code"`
	Message   string     `json:"message"`
	Retryable bool       `json:"retryable"`
	Scope     string     `json:"scope,omitempty"`
	Provider  string     `json:"provider,omitempty"`
	RequestID string     `json:"request_id,omitempty"`
	ResetAt   *time.Time `json:"reset_at,omitempty"`
}

// Frame is the Hub/Sessiond control-frame envelope.
type Frame struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	Method    string          `json:"method"`
	Token     string          `json:"token,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

type FrameResponse struct {
	Version   int             `json:"version"`
	RequestID string          `json:"request_id"`
	OK        bool            `json:"ok"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     *Error          `json:"error,omitempty"`
}

type HTTPRequest struct {
	Method  string          `json:"method"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type HTTPResponse struct {
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

const (
	MethodHubStatus        = "hub.status"
	MethodHubShutdown      = "hub.shutdown"
	MethodHubCreateSession = "hub.session.create"
	MethodHubListSessions  = "hub.session.list"
	MethodHubInspect       = "hub.session.inspect"
	MethodHubStopSession   = "hub.session.stop"
	MethodHubToolList      = "hub.tool.list"
	MethodKnowledgeWrite   = "knowledge.memory.write"
	MethodKnowledgeSearch  = "knowledge.memory.search"
	MethodKnowledgeUpdate  = "knowledge.memory.update"
	MethodCredentialRedeem = "credential.lease.redeem"
	MethodSessionHealth    = "session.health"
	MethodSessionSubmit    = "session.submit"
	MethodSessionDirect    = "session.direct_tool"
	MethodSessionCancel    = "session.cancel"
	MethodSessionCompress  = "session.compress"
	MethodSessionEvents    = "session.events"
	MethodSessionWatch     = "session.watch"
	MethodSessionSnapshot  = "session.snapshot"
	MethodSessionHistory   = "session.history"
	MethodSessionTrace     = "session.trace"
	MethodSessionStats     = "session.stats"
	MethodSessionShutdown  = "session.shutdown"
)
