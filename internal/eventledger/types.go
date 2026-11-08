package eventledger

import (
	"encoding/json"
	"time"
)

const SchemaVersion = 1

type Visibility string

const (
	VisibilityConversation Visibility = "conversation"
	VisibilityInternal     Visibility = "internal"
	VisibilityAudit        Visibility = "audit"
)

type EventType string

const (
	TypeSessionStarted            EventType = "session.started"
	TypeUserMessageAccepted       EventType = "user.message.accepted"
	TypeUserMessageConsumed       EventType = "user.message.consumed"
	TypeUserToolCommandAccepted   EventType = "user.tool_command.accepted"
	TypeTurnStarted               EventType = "turn.started"
	TypeLLMRequested              EventType = "llm.requested"
	TypeLLMCompleted              EventType = "llm.completed"
	TypeLLMFailed                 EventType = "llm.failed"
	TypeToolRequested             EventType = "tool.requested"
	TypeToolCompleted             EventType = "tool.completed"
	TypeToolFailed                EventType = "tool.failed"
	TypeAssistantMessageCompleted EventType = "assistant.message.completed"
	TypeTurnCompleted             EventType = "turn.completed"
	TypeCompressionRequested      EventType = "compression.requested"
	TypeCompressionCompleted      EventType = "compression.completed"
	TypeCompressionFailed         EventType = "compression.failed"
	TypeSessionStopping           EventType = "session.stopping"
	TypeSessionStopped            EventType = "session.stopped"
)

// Draft is an event proposed by the Session Actor. Seq, EventID and CreatedAt
// are assigned only by the ledger writer.
type Draft struct {
	Type         EventType
	TraceID      string
	CausationSeq int64
	CallID       string
	Visibility   Visibility
	Status       string
	Payload      any
}

// Event is one committed line in messages.jsonl.
type Event struct {
	SchemaVersion int             `json:"schema_version"`
	SessionID     string          `json:"session_id"`
	Seq           int64           `json:"seq"`
	EventID       string          `json:"event_id"`
	Type          EventType       `json:"type"`
	CreatedAt     time.Time       `json:"created_at"`
	TraceID       string          `json:"trace_id,omitempty"`
	CausationSeq  int64           `json:"causation_seq,omitempty"`
	CallID        string          `json:"call_id,omitempty"`
	Visibility    Visibility      `json:"visibility"`
	Status        string          `json:"status,omitempty"`
	Payload       json.RawMessage `json:"payload"`
}

type Snapshot struct {
	SessionID string
	LastSeq   int64
	Events    []Event
}

type Options struct {
	MaxPayloadBytes int64
	Sync            bool
	Now             func() time.Time
}

func DefaultOptions() Options {
	return Options{
		MaxPayloadBytes: 1 << 20,
		Sync:            true,
		Now:             time.Now,
	}
}
