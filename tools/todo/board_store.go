package todo

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"encoding/json"
)

const (
	BoardStatusPending    = "pending"
	BoardStatusInProgress = "in_progress"
	BoardStatusBlocked    = "blocked"
	BoardStatusCompleted  = "completed"

	BoardPriorityLow    = "low"
	BoardPriorityMedium = "medium"
	BoardPriorityHigh   = "high"
	BoardPriorityUrgent = "urgent"
)

// BoardItem is one durable item in a session-scoped operational todo board.
type BoardItem struct {
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Details            string            `json:"details,omitempty"`
	Status             string            `json:"status"`
	Priority           string            `json:"priority"`
	Version            int               `json:"version"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
	Deleted            bool              `json:"deleted,omitempty"`
	DeletedAt          *time.Time        `json:"deleted_at,omitempty"`
	IdempotencyActions map[string]string `json:"idempotency_actions,omitempty"`
}

type BoardSummary struct {
	Total      int `json:"total"`
	Pending    int `json:"pending"`
	InProgress int `json:"in_progress"`
	Blocked    int `json:"blocked"`
	Completed  int `json:"completed"`
}

type boardReplay struct {
	itemID string
	action string
}

// BoardStore persists one board. Sessiond constructs it from the current
// session directory, so a store instance can never address another session.
type BoardStore struct {
	mu          sync.Mutex
	path        string
	items       map[string]BoardItem
	idempotency map[string]boardReplay
}

func OpenBoardStore(path string) (*BoardStore, error) {
	store := &BoardStore{
		path: path, items: make(map[string]BoardItem),
		idempotency: make(map[string]boardReplay),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *BoardStore) load() error {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open todo board: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var item BoardItem
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return fmt.Errorf("decode todo board: %w", err)
		}
		if item.ID == "" {
			return errors.New("todo board item id is required")
		}
		s.items[item.ID] = cloneBoardItem(item)
		for key, action := range item.IdempotencyActions {
			s.idempotency[key] = boardReplay{itemID: item.ID, action: action}
		}
	}
	return scanner.Err()
}

func (s *BoardStore) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".todo-board-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	items := make([]BoardItem, 0, len(s.items))
	for _, item := range s.items {
		items = append(items, item)
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	encoder := json.NewEncoder(temp)
	for _, item := range items {
		if err := encoder.Encode(item); err != nil {
			_ = temp.Close()
			return err
		}
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, s.path)
}
