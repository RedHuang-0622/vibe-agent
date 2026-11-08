package contextmgr

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"vibe-agent/internal/eventledger"
)

func testEvent(seq int64, eventType eventledger.EventType, payload any, callID string) eventledger.Event {
	encoded, _ := json.Marshal(payload)
	return eventledger.Event{
		SchemaVersion: 1, SessionID: "s", Seq: seq,
		EventID: eventledger.EventID("s", seq), Type: eventType,
		Visibility: eventledger.VisibilityConversation, Payload: encoded, CallID: callID,
	}
}

func TestCutoffCannotSplitPendingTool(t *testing.T) {
	events := []eventledger.Event{
		testEvent(1, eventledger.TypeTurnStarted, map[string]any{}, ""),
		testEvent(2, eventledger.TypeToolRequested, map[string]any{}, "c1"),
		testEvent(3, eventledger.TypeToolCompleted, map[string]any{}, "c1"),
		testEvent(4, eventledger.TypeTurnCompleted, map[string]any{}, ""),
		testEvent(5, eventledger.TypeToolRequested, map[string]any{}, "c2"),
	}
	cutoff, err := SafeCutoff(CutoffInput{Events: events, TargetSeq: 5})
	if err != nil {
		t.Fatal(err)
	}
	if cutoff != 4 {
		t.Fatalf("cutoff=%d want 4", cutoff)
	}
}

func TestCutoffCannotSwallowUnconsumedMessage(t *testing.T) {
	events := []eventledger.Event{
		testEvent(1, eventledger.TypeUserMessageAccepted, map[string]string{"message_id": "m1", "text": "first"}, ""),
		testEvent(2, eventledger.TypeUserMessageConsumed, map[string]string{"message_id": "m1"}, ""),
		testEvent(3, eventledger.TypeTurnCompleted, map[string]any{}, ""),
		testEvent(4, eventledger.TypeUserMessageAccepted, map[string]string{"message_id": "m2", "text": "queued"}, ""),
	}
	cutoff, err := SafeCutoff(CutoffInput{Events: events})
	if err != nil {
		t.Fatal(err)
	}
	if cutoff != 3 {
		t.Fatalf("cutoff=%d want 3", cutoff)
	}
}

func TestBuildUsesLatestCheckpointAndTail(t *testing.T) {
	events := []eventledger.Event{
		testEvent(1, eventledger.TypeUserMessageAccepted, map[string]string{"message_id": "m1", "text": "old"}, ""),
	}
	events = append(events, testEvent(2, eventledger.TypeCompressionCompleted, map[string]any{
		"source_to_seq": 1, "red_line_seq": 1, "summary": "old summary",
		"source_digest": SourceDigest(events, 1, 1),
	}, ""))
	events = append(events, testEvent(3, eventledger.TypeUserMessageAccepted, map[string]string{"message_id": "m2", "text": "new"}, ""))
	view := Build(events, 3)
	if view.Checkpoint == nil || view.Checkpoint.Summary != "old summary" {
		t.Fatalf("checkpoint=%#v", view.Checkpoint)
	}
	if want := "Summary of earlier conversation:\nold summary\nuser: new"; view.Text != want {
		t.Fatalf("text=%q want %q", view.Text, want)
	}
}

func TestBuildRejectsCheckpointWithMismatchedDigest(t *testing.T) {
	events := []eventledger.Event{
		testEvent(1, eventledger.TypeUserMessageAccepted, map[string]string{"message_id": "m1", "text": "old"}, ""),
		testEvent(2, eventledger.TypeCompressionCompleted, map[string]any{
			"source_to_seq": 1, "red_line_seq": 1, "summary": "stale", "source_digest": "sha256:wrong",
		}, ""),
	}
	view := Build(events, 2)
	if view.Checkpoint != nil || view.Text != "user: old" {
		t.Fatalf("view=%#v", view)
	}
}

func TestCutoffDoesNotSplitBeforeTurnCompleted(t *testing.T) {
	events := []eventledger.Event{
		testEvent(1, eventledger.TypeAssistantMessageCompleted, map[string]string{"text": "answer"}, ""),
		testEvent(2, eventledger.TypeTurnCompleted, map[string]string{"turn_id": "t1"}, ""),
	}
	cutoff, err := SafeCutoff(CutoffInput{Events: events, TargetSeq: 1})
	if !errors.Is(err, ErrNoSafeCutoff) || cutoff != 0 {
		t.Fatalf("cutoff=%d err=%v", cutoff, err)
	}
}

func TestBasicSummarizer(t *testing.T) {
	summary, err := (BasicSummarizer{MaxRunes: 3}).Summarize(context.Background(), []eventledger.Event{
		testEvent(1, eventledger.TypeUserMessageAccepted, map[string]string{"message_id": "m", "text": "abcdef"}, ""),
	})
	if err != nil || summary != "def" {
		t.Fatalf("summary=%q err=%v", summary, err)
	}
}
