package contextmgr

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"vibe-agent/internal/eventledger"
)

type Checkpoint struct {
	Seq      int64
	SourceTo int64
	Summary  string
	Digest   string
}

type ContextView struct {
	Text       string
	Checkpoint *Checkpoint
	UpToSeq    int64
}

type Summarizer interface {
	Summarize(ctx context.Context, events []eventledger.Event) (string, error)
}

func Build(events []eventledger.Event, upTo int64) ContextView {
	if upTo <= 0 || upTo > int64(len(events)) {
		upTo = int64(len(events))
	}
	view := ContextView{UpToSeq: upTo}
	checkpoint := latestCheckpoint(events)
	start := int64(1)
	if checkpoint != nil {
		view.Checkpoint = checkpoint
		view.Text = "Summary of earlier conversation:\n" + checkpoint.Summary + "\n"
		start = checkpoint.SourceTo + 1
	}
	var lines []string
	for _, event := range events {
		if event.Seq < start || event.Seq > upTo || event.Visibility != eventledger.VisibilityConversation {
			continue
		}
		if text := conversationText(event); text != "" {
			lines = append(lines, text)
		}
	}
	view.Text += strings.Join(lines, "\n")
	return view
}

func SourceDigest(events []eventledger.Event, from, to int64) string {
	hash := sha256.New()
	for _, event := range events {
		if event.Seq < from || event.Seq > to {
			continue
		}
		hash.Write([]byte(event.EventID))
		hash.Write([]byte{0})
		hash.Write(event.Payload)
		hash.Write([]byte{'\n'})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

type BasicSummarizer struct {
	MaxRunes int
}

func (s BasicSummarizer) Summarize(_ context.Context, events []eventledger.Event) (string, error) {
	limit := s.MaxRunes
	if limit <= 0 {
		limit = 2000
	}
	var parts []string
	for _, event := range events {
		if event.Visibility != eventledger.VisibilityConversation {
			continue
		}
		if text := conversationText(event); text != "" {
			parts = append(parts, text)
		}
	}
	summary := strings.Join(parts, "\n")
	runes := []rune(summary)
	if len(runes) > limit {
		summary = string(runes[len(runes)-limit:])
	}
	if strings.TrimSpace(summary) == "" {
		return "", errors.New("compression source is empty")
	}
	return summary, nil
}

func latestCheckpoint(events []eventledger.Event) *Checkpoint {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != eventledger.TypeCompressionCompleted {
			continue
		}
		var payload struct {
			SourceToSeq  int64  `json:"source_to_seq"`
			RedLineSeq   int64  `json:"red_line_seq"`
			Summary      string `json:"summary"`
			SourceDigest string `json:"source_digest"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			continue
		}
		if payload.SourceToSeq <= 0 || payload.SourceToSeq >= event.Seq ||
			payload.RedLineSeq != payload.SourceToSeq || payload.SourceDigest == "" {
			continue
		}
		if expected := SourceDigest(events, 1, payload.SourceToSeq); expected != payload.SourceDigest {
			continue
		}
		return &Checkpoint{
			Seq: event.Seq, SourceTo: payload.SourceToSeq,
			Summary: payload.Summary, Digest: payload.SourceDigest,
		}
	}
	return nil
}

func conversationText(event eventledger.Event) string {
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return ""
	}
	switch event.Type {
	case eventledger.TypeUserMessageAccepted:
		return "user: " + stringValue(payload["text"])
	case eventledger.TypeAssistantMessageCompleted:
		return "assistant: " + stringValue(payload["text"])
	case eventledger.TypeToolCompleted, eventledger.TypeToolFailed:
		return "tool: " + event.CallID + " " + jsonValue(payload["result"])
	default:
		return ""
	}
}

func jsonValue(value any) string {
	if value == nil {
		return ""
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return stringValue(value)
	}
	return string(encoded)
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.RawMessage:
		return string(typed)
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}
