package conversation

import (
	"context"
	"encoding/json"

	"vibe-agent/internal/eventledger"
	"vibe-agent/internal/llm"
	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type ModelClient interface {
	Complete(context.Context, llm.Request) (llm.Response, error)
}

type ToolExecutor interface {
	Execute(context.Context, tooling.Call) tooling.Result
	ListTools() []toolv1.ToolSpec
}

type EventAppender interface {
	Append(context.Context, eventledger.Draft) (eventledger.Event, error)
}

type EventReader interface {
	ReadRange(context.Context, int64, int) ([]eventledger.Event, error)
	LastCommitted(context.Context) (int64, error)
}

type Config struct {
	SessionID       string
	QueueSize       int
	MaxSteps        int
	MaxContextBytes int
	KeepTailEvents  int
	AutoCompress    bool
}

func DefaultConfig(sessionID string) Config {
	return Config{
		SessionID:       sessionID,
		QueueSize:       64,
		MaxSteps:        12,
		MaxContextBytes: 64 << 10,
		KeepTailEvents:  20,
		AutoCompress:    true,
	}
}

type QueuePolicy string

const (
	QueuePolicyQueue          QueuePolicy = "queue"
	QueuePolicyCancelAndQueue QueuePolicy = "cancel-and-queue"
)

type SubmitRequest struct {
	Text        string
	MessageID   string
	QueuePolicy QueuePolicy
}

type SubmitResponse struct {
	MessageID string
	Seq       int64
}

type DirectToolResult struct {
	TurnID string
	Result json.RawMessage
	Error  *tooling.Error
}

type CompressionResult struct {
	Committed  bool
	RedLineSeq int64
	ErrorCode  string
}

type Status struct {
	LastSeq     int64
	Active      bool
	Compressing bool
	ActiveCalls map[string]bool
}

type pendingMessage struct {
	ID          string
	Text        string
	AcceptedSeq int64
	Policy      QueuePolicy
}

type activeTurn struct {
	ID        string
	TraceID   string
	MessageID string
	Cancel    context.CancelFunc
}

type appendRequest struct {
	ctx   context.Context
	draft eventledger.Draft
	reply chan appendResult
}

type appendResult struct {
	event eventledger.Event
	err   error
}

type snapshotRequest struct {
	reply chan []eventledger.Event
}

type cutoffRequest struct {
	target int64
	reply  chan cutoffResult
}

type cutoffResult struct {
	seq int64
	err error
}

type finishedRequest struct {
	turnID string
}

type startedRequest struct {
	message pendingMessage
}

type toolOutcome struct {
	result tooling.Result
	err    *tooling.Error
}

type directReply struct {
	result DirectToolResult
}
