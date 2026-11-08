package eventledger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

func ReadFile(ctx context.Context, path, sessionID string) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("open ledger for read: %w", err)
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	snapshot := Snapshot{SessionID: sessionID}
	expected := int64(1)
	for {
		line, readErr := reader.ReadBytes('\n')
		if errorsEOF(readErr) && len(line) == 0 {
			break
		}
		if errorsEOF(readErr) {
			// Readers do not mutate the truth source. A trailing partial frame is
			// reported as not committed by OpenWriter on the next startup.
			break
		}
		if readErr != nil {
			return Snapshot{}, fmt.Errorf("read ledger: %w", readErr)
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return Snapshot{}, fmt.Errorf("line %d: invalid JSON: %w", expected, err)
		}
		if err := validateEvent(sessionID, expected, event); err != nil {
			return Snapshot{}, err
		}
		snapshot.Events = append(snapshot.Events, event)
		snapshot.LastSeq = event.Seq
		expected++
	}
	return snapshot, nil
}

func (w *Writer) ReadRange(ctx context.Context, afterSeq int64, limit int) ([]Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	snapshot, err := ReadFile(ctx, w.path, w.sessionID)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = len(snapshot.Events)
	}
	events := make([]Event, 0, min(limit, len(snapshot.Events)))
	for _, event := range snapshot.Events {
		if event.Seq <= afterSeq {
			continue
		}
		events = append(events, event)
		if len(events) == limit {
			break
		}
	}
	return events, nil
}

func errorsEOF(err error) bool {
	return err == io.EOF
}
