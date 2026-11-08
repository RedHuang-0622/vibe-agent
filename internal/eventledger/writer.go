package eventledger

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"
)

// Writer is the only component allowed to assign and commit seq for a session.
type Writer struct {
	path      string
	sessionID string
	options   Options
	file      *os.File
	offset    int64
	lastSeq   int64
	mu        sync.Mutex
	closed    bool
	changes   chan struct{}
}

func OpenWriter(path, sessionID string, options Options) (*Writer, error) {
	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}
	if options.MaxPayloadBytes <= 0 {
		options.MaxPayloadBytes = DefaultOptions().MaxPayloadBytes
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open event ledger: %w", err)
	}
	w := &Writer{
		path: path, sessionID: sessionID, options: options, file: file,
		changes: make(chan struct{}),
	}
	if err := w.recover(); err != nil {
		_ = file.Close()
		return nil, err
	}
	return w, nil
}

func (w *Writer) recover() error {
	info, err := w.file.Stat()
	if err != nil {
		return err
	}
	if _, err := w.file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	reader := bufio.NewReader(w.file)
	var completeOffset int64
	expected := int64(1)
	for {
		line, readErr := reader.ReadBytes('\n')
		if len(line) == 0 && errors.Is(readErr, io.EOF) {
			break
		}
		if errors.Is(readErr, io.EOF) {
			if err := w.isolateTail(line); err != nil {
				return err
			}
			break
		}
		if readErr != nil {
			return fmt.Errorf("read event ledger: %w", readErr)
		}
		line = bytes.TrimSuffix(line, []byte{'\n'})
		if len(bytes.TrimSpace(line)) == 0 {
			return fmt.Errorf("line %d: blank lines are not valid events", expected)
		}
		var event Event
		if err := json.Unmarshal(line, &event); err != nil {
			return fmt.Errorf("line %d: invalid JSON: %w", expected, err)
		}
		if err := validateEvent(w.sessionID, expected, event); err != nil {
			return err
		}
		completeOffset += int64(len(line) + 1)
		expected++
	}
	if err := w.file.Truncate(completeOffset); err != nil {
		return fmt.Errorf("truncate recovered ledger: %w", err)
	}
	if info.Size() != completeOffset {
		if err := w.file.Sync(); err != nil {
			return fmt.Errorf("sync recovered ledger: %w", err)
		}
	}
	w.offset = completeOffset
	w.lastSeq = expected - 1
	return nil
}

func (w *Writer) isolateTail(tail []byte) error {
	if len(tail) == 0 {
		return nil
	}
	data, err := os.ReadFile(w.path)
	if err != nil {
		return fmt.Errorf("read corrupt ledger for backup: %w", err)
	}
	backup := backupName(w.path, w.options.Now)
	if err := os.WriteFile(backup, data, 0o600); err != nil {
		return fmt.Errorf("write corrupt ledger backup: %w", err)
	}
	return nil
}

// Append serializes and durably commits one event before acknowledging it.
func (w *Writer) Append(ctx context.Context, draft Draft) (Event, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Event{}, err
	}
	if w.closed {
		return Event{}, errors.New("event ledger is closed")
	}
	if draft.Visibility == "" {
		draft.Visibility = VisibilityConversation
	}
	payload, err := json.Marshal(draft.Payload)
	if err != nil {
		return Event{}, fmt.Errorf("encode event payload: %w", err)
	}
	if int64(len(payload)) > w.options.MaxPayloadBytes {
		return Event{}, fmt.Errorf("event payload exceeds %d bytes", w.options.MaxPayloadBytes)
	}
	seq := w.lastSeq + 1
	event := Event{
		SchemaVersion: SchemaVersion,
		SessionID:     w.sessionID,
		Seq:           seq,
		EventID:       EventID(w.sessionID, seq),
		Type:          draft.Type,
		CreatedAt:     w.options.Now().UTC(),
		TraceID:       draft.TraceID,
		CausationSeq:  draft.CausationSeq,
		CallID:        draft.CallID,
		Visibility:    draft.Visibility,
		Status:        draft.Status,
		Payload:       payload,
	}
	line, err := json.Marshal(event)
	if err != nil {
		return Event{}, fmt.Errorf("encode event: %w", err)
	}
	line = append(line, '\n')
	if _, err := w.file.WriteAt(line, w.offset); err != nil {
		return Event{}, w.rollbackAppend(err)
	}
	if w.options.Sync {
		if err := w.file.Sync(); err != nil {
			return Event{}, w.rollbackAppend(err)
		}
	}
	w.offset += int64(len(line))
	w.lastSeq = seq
	w.notifyLocked()
	return event, nil
}

func (w *Writer) rollbackAppend(cause error) error {
	truncateErr := w.file.Truncate(w.offset)
	if truncateErr == nil {
		truncateErr = w.file.Sync()
	}
	if truncateErr != nil {
		return fmt.Errorf("append event failed (%v) and rollback failed: %w", cause, truncateErr)
	}
	return fmt.Errorf("append event: %w", cause)
}

func (w *Writer) LastCommitted(context.Context) (int64, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastSeq, nil
}

// ChangeSignal returns a channel that is closed after the next successful
// append. Callers should re-read the ledger and request a fresh signal after
// observing a close; the method deliberately does not expose a sequence value
// that could be mistaken for a committed watermark.
func (w *Writer) ChangeSignal() <-chan struct{} {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.changes
}

func (w *Writer) notifyLocked() {
	close(w.changes)
	w.changes = make(chan struct{})
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if err := w.file.Sync(); err != nil {
		_ = w.file.Close()
		return err
	}
	return w.file.Close()
}
