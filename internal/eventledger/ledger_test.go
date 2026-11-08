package eventledger

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestSessionsStartAtOneIndependently(t *testing.T) {
	root := t.TempDir()
	for _, sessionID := range []string{"sessiona", "sessionb"} {
		writer, err := OpenWriter(filepath.Join(root, sessionID+".jsonl"), sessionID, DefaultOptions())
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			event, err := writer.Append(context.Background(), Draft{
				Type:    TypeUserMessageAccepted,
				Payload: map[string]any{"index": i},
			})
			if err != nil {
				t.Fatal(err)
			}
			if event.Seq != int64(i+1) || event.EventID != EventID(sessionID, int64(i+1)) {
				t.Fatalf("unexpected event: %#v", event)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentAppendHasContinuousSeq(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	options := DefaultOptions()
	options.Sync = false
	writer, err := OpenWriter(path, "sess", options)
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := writer.Append(context.Background(), Draft{
				Type:    TypeUserMessageAccepted,
				Payload: map[string]int{"index": i},
			}); err != nil {
				t.Errorf("append %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	snapshot, err := ReadFile(context.Background(), path, "sess")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.LastSeq != 100 || len(snapshot.Events) != 100 {
		t.Fatalf("last=%d events=%d", snapshot.LastSeq, len(snapshot.Events))
	}
	for i, event := range snapshot.Events {
		if event.Seq != int64(i+1) {
			t.Fatalf("event %d seq=%d", i, event.Seq)
		}
	}
}

func TestChangeSignalClosesAfterDurableAppend(t *testing.T) {
	writer, err := OpenWriter(filepath.Join(t.TempDir(), "messages.jsonl"), "sess", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	signal := writer.ChangeSignal()
	if _, err := writer.Append(context.Background(), Draft{
		Type: TypeSessionStarted, Payload: map[string]bool{"ok": true},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("append did not notify watchers")
	}
	next := writer.ChangeSignal()
	select {
	case <-next:
		t.Fatal("fresh signal closed without another append")
	default:
	}
}

func TestTrailingPartialLineIsIsolated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	writer, err := OpenWriter(path, "sess", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(context.Background(), Draft{Type: TypeSessionStarted, Payload: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"partial":`); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()

	reopened, err := OpenWriter(path, "sess", DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	event, err := reopened.Append(context.Background(), Draft{Type: TypeSessionStopped, Payload: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if event.Seq != 2 {
		t.Fatalf("seq=%d, want 2", event.Seq)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	backups, err := filepath.Glob(path + ".corrupt-*.bak")
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup files=%v err=%v", backups, err)
	}
}

func TestMalformedCompleteLineFailsRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	if err := os.WriteFile(path, []byte("{not-json}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWriter(path, "sess", DefaultOptions()); err == nil {
		t.Fatal("malformed complete line must fail recovery")
	}
}
