package eventledger

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"regexp"
	"time"
)

var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,95}$`)

func validateSessionID(sessionID string) error {
	if !sessionIDPattern.MatchString(sessionID) {
		return fmt.Errorf("invalid session id %q", sessionID)
	}
	return nil
}

func validateEvent(sessionID string, expectedSeq int64, event Event) error {
	if event.SchemaVersion != SchemaVersion {
		return fmt.Errorf("seq %d: unsupported schema version %d", expectedSeq, event.SchemaVersion)
	}
	if event.SessionID != sessionID {
		return fmt.Errorf("seq %d: session id %q does not match %q", expectedSeq, event.SessionID, sessionID)
	}
	if event.Seq != expectedSeq {
		return fmt.Errorf("seq discontinuity: got %d, want %d", event.Seq, expectedSeq)
	}
	wantID := EventID(sessionID, expectedSeq)
	if event.EventID != wantID {
		return fmt.Errorf("seq %d: event id %q does not match %q", expectedSeq, event.EventID, wantID)
	}
	if event.Type == "" {
		return fmt.Errorf("seq %d: event type is required", expectedSeq)
	}
	if event.CreatedAt.IsZero() {
		return fmt.Errorf("seq %d: created_at is required", expectedSeq)
	}
	if len(event.Payload) == 0 {
		return fmt.Errorf("seq %d: payload is required", expectedSeq)
	}
	return nil
}

// EventID returns the canonical session-scoped event identity.
func EventID(sessionID string, seq int64) string {
	return fmt.Sprintf("%s-seq-%d", sessionID, seq)
}

func backupName(path string, now func() time.Time) string {
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return filepath.Clean(path) + ".corrupt.bak"
	}
	return filepath.Clean(path) + ".corrupt-" + now().UTC().Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(suffix) + ".bak"
}
