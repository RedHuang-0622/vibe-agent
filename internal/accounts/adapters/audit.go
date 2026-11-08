package adapters

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"vibe-agent/internal/accounts/app"
)

type FileAudit struct {
	mu   sync.Mutex
	path string
}

func NewFileAudit(path string) *FileAudit {
	return &FileAudit{path: path}
}

func (a *FileAudit) Write(_ context.Context, entry app.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(a.path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(a.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	line, err := json.Marshal(entry)
	if err == nil {
		_, err = file.Write(append(line, '\n'))
	}
	if syncErr := file.Sync(); err == nil {
		err = syncErr
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}
