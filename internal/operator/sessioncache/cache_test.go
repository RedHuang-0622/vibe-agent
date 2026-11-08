package sessioncache

import (
	"encoding/json"
	"testing"

	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func TestSnapshotPreservesOlderAndDeduplicatesSeam(t *testing.T) {
	cache := New()
	if !cache.ApplySnapshot(snapshot("s", 6, 7, 8)) {
		t.Fatal("snapshot rejected")
	}
	if !cache.ApplyHistory(history("s", 5, 4, 5, 6)) {
		t.Fatal("history rejected")
	}
	if got := sequence(cache.Window()); !equalSeq(got, []int64{4, 5, 6, 7, 8}) {
		t.Fatalf("window=%v", got)
	}

	if !cache.ApplySnapshot(snapshot("s", 9, 10)) {
		t.Fatal("refresh rejected")
	}
	if got := sequence(cache.Window()); !equalSeq(got, []int64{4, 5, 6, 7, 8, 9, 10}) {
		t.Fatalf("refresh lost older page: %v", got)
	}
}

func TestIncrementalRejectsHoleAndKeepsCursorStable(t *testing.T) {
	cache := New()
	cache.ApplySnapshot(snapshot("s", 1, 2))
	response := sessionv1.EventsResponse{
		Events:    []sessionv1.EventView{event("s", 4), event("s", 5)},
		Watermark: 5, Cursor: 5,
	}
	if cache.ApplyIncremental(response) {
		t.Fatal("cache applied a gapped page")
	}
	if !cache.NeedsResync() {
		t.Fatal("expected resync request")
	}
	if got := sequence(cache.Window()); !equalSeq(got, []int64{1, 2}) {
		t.Fatalf("gap changed window: %v", got)
	}
}

func TestLiveTurnStateComesOnlyFromTurnEvents(t *testing.T) {
	cache := New()
	cache.ApplySnapshot(snapshot("s", 1))
	if _, known, _ := cache.TurnState(); known {
		t.Fatal("snapshot must not infer turn state")
	}
	response := sessionv1.EventsResponse{
		Events: []sessionv1.EventView{
			eventWithPayload("s", 2, "turn.started", `{"turn_id":"turn-2"}`),
			eventWithPayload("s", 3, "assistant.message.completed", `{"turn_id":"turn-2"}`),
		},
		Watermark: 3, Cursor: 3,
	}
	cache.ApplyIncremental(response)
	running, known, turnID := cache.TurnState()
	if !running || !known || turnID != "turn-2" {
		t.Fatalf("turn state=%v/%v/%q", running, known, turnID)
	}
	done := sessionv1.EventsResponse{
		Events: []sessionv1.EventView{
			eventWithPayload("s", 4, "turn.completed", `{"turn_id":"turn-2"}`),
		},
		Watermark: 4, Cursor: 4,
	}
	cache.ApplyIncremental(done)
	running, known, turnID = cache.TurnState()
	if running || !known || turnID != "" {
		t.Fatalf("terminal turn state=%v/%v/%q", running, known, turnID)
	}
}

func TestSessionSwitchDropsPreviousContent(t *testing.T) {
	cache := New()
	cache.ApplySnapshot(snapshot("a", 1, 2, 3))
	cache.ApplySnapshot(snapshot("b", 1))
	if cache.SessionID() != "b" {
		t.Fatalf("session=%q", cache.SessionID())
	}
	if got := sequence(cache.Window()); !equalSeq(got, []int64{1}) {
		t.Fatalf("session switch leaked events: %v", got)
	}
}

func snapshot(sessionID string, seqs ...int64) sessionv1.SessionSnapshotResponse {
	events := make([]sessionv1.EventView, 0, len(seqs))
	for _, seq := range seqs {
		events = append(events, event(sessionID, seq))
	}
	watermark := int64(0)
	if len(seqs) > 0 {
		watermark = seqs[len(seqs)-1]
	}
	return sessionv1.SessionSnapshotResponse{
		SessionID: sessionID, State: sessionv1.StateIdle, Watermark: watermark,
		Events: events, HasMoreBefore: len(seqs) > 0 && seqs[0] > 1,
	}
}

func history(sessionID string, before int64, seqs ...int64) sessionv1.SessionHistoryResponse {
	events := make([]sessionv1.EventView, 0, len(seqs))
	for _, seq := range seqs {
		events = append(events, event(sessionID, seq))
	}
	return sessionv1.SessionHistoryResponse{
		SessionID: sessionID, Watermark: before + 2, Events: events,
		HasMoreBefore: len(seqs) > 0 && seqs[0] > 1,
	}
}

func event(sessionID string, seq int64) sessionv1.EventView {
	return sessionv1.EventView{
		SchemaVersion: 1, SessionID: sessionID, Seq: seq,
		EventID: sessionID + "-seq-" + jsonNumber(seq), Type: "test.event",
		Visibility: "conversation", Payload: json.RawMessage(`{}`),
	}
}

func eventWithPayload(sessionID string, seq int64, eventType, payload string) sessionv1.EventView {
	event := event(sessionID, seq)
	event.Type = eventType
	event.Payload = json.RawMessage(payload)
	return event
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func sequence(events []sessionv1.EventView) []int64 {
	result := make([]int64, 0, len(events))
	for _, event := range events {
		result = append(result, event.Seq)
	}
	return result
}

func equalSeq(left, right []int64) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
