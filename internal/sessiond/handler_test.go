package sessiond

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"vibe-agent/internal/eventledger"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func TestWatchWakesAfterCommittedEvent(t *testing.T) {
	writer, err := eventledger.OpenWriter(
		filepath.Join(t.TempDir(), "messages.jsonl"), "session-watch", eventledger.DefaultOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	handler := &commandHandler{sessionID: "session-watch", writer: writer}
	payload, _ := json.Marshal(sessionv1.WatchRequest{
		SessionID: "session-watch", AfterSeq: 0, Limit: 10, WaitMS: 2000,
	})
	type result struct {
		value any
		err   error
	}
	done := make(chan result, 1)
	go func() {
		value, watchErr := handler.Handle(context.Background(), sessionv1.MethodSessionWatch, payload)
		done <- result{value: value, err: watchErr}
	}()
	if _, err := writer.Append(context.Background(), eventledger.Draft{
		Type: eventledger.TypeSessionStarted, Payload: map[string]bool{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		response := result.value.(sessionv1.EventsResponse)
		if len(response.Events) != 1 || response.Cursor != 1 ||
			response.Watermark != 1 || response.TimedOut {
			t.Fatalf("response=%#v", response)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watch did not wake")
	}
}

func TestEventPagingAndHistoryExposePaginationState(t *testing.T) {
	writer, err := eventledger.OpenWriter(
		filepath.Join(t.TempDir(), "messages.jsonl"), "session-page", eventledger.DefaultOptions(),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	for index := 0; index < 3; index++ {
		if _, err := writer.Append(context.Background(), eventledger.Draft{
			Type: eventledger.TypeUserMessageAccepted, Payload: map[string]int{"index": index},
		}); err != nil {
			t.Fatal(err)
		}
	}
	handler := &commandHandler{sessionID: "session-page", writer: writer}
	payload, _ := json.Marshal(sessionv1.EventsRequest{
		SessionID: "session-page", AfterSeq: 0, Limit: 2,
	})
	value, err := handler.Handle(context.Background(), sessionv1.MethodSessionEvents, payload)
	if err != nil {
		t.Fatal(err)
	}
	page := value.(sessionv1.EventsResponse)
	if len(page.Events) != 2 || !page.HasMore || page.Cursor != 2 || page.ResyncRequired {
		t.Fatalf("page=%#v", page)
	}

	payload, _ = json.Marshal(sessionv1.SessionHistoryRequest{
		SessionID: "session-page", BeforeSeq: 3, Limit: 1,
	})
	value, err = handler.Handle(context.Background(), sessionv1.MethodSessionHistory, payload)
	if err != nil {
		t.Fatal(err)
	}
	history := value.(sessionv1.SessionHistoryResponse)
	if len(history.Events) != 1 || history.Events[0].Seq != 2 || !history.HasMoreBefore {
		t.Fatalf("history=%#v", history)
	}
}
