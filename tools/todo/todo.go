package todo

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type Item struct {
	ID              string    `json:"id"`
	Text            string    `json:"text"`
	Status          string    `json:"status"`
	Version         int       `json:"version"`
	UpdatedAt       time.Time `json:"updated_at"`
	IdempotencyKeys []string  `json:"idempotency_keys,omitempty"`
}

type Store struct {
	mu          sync.Mutex
	path        string
	items       map[string]Item
	idempotency map[string]string
}

func OpenStore(path string) (*Store, error) {
	store := &Store{
		path:        path,
		items:       make(map[string]Item),
		idempotency: make(map[string]string),
	}
	if err := store.load(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) load() error {
	file, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open todo store: %w", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var item Item
		if err := json.Unmarshal(scanner.Bytes(), &item); err != nil {
			return fmt.Errorf("decode todo store: %w", err)
		}
		s.items[item.ID] = item
		for _, key := range item.IdempotencyKeys {
			s.idempotency[key] = item.ID
		}
	}
	return scanner.Err()
}

func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(s.path), ".todo-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	items := make([]Item, 0, len(s.items))
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

type Tool struct {
	store *Store
	now   func() time.Time
}

func New(store *Store) *Tool {
	return &Tool{store: store, now: time.Now}
}

func Spec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:        "todo",
		Version:     "v1",
		Kind:        "mutating",
		Description: "Create, list, update or complete durable todo items",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["action"],
			"properties":{
				"action":{"type":"string"},
				"text":{"type":"string"},
				"id":{"type":"string"},
				"idempotency_key":{"type":"string"},
				"expected_version":{"type":"integer"}
			}
		}`),
		DirectRequestField: "text",
		ReadOnly:           false,
		TimeoutMS:          3000,
		Idempotent:         true,
	}
}

type args struct {
	Action          string `json:"action"`
	Text            string `json:"text"`
	ID              string `json:"id"`
	IdempotencyKey  string `json:"idempotency_key"`
	ExpectedVersion int    `json:"expected_version"`
}

func (t *Tool) Invoke(_ context.Context, call tooling.Call) (any, error) {
	var input args
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid todo arguments", false)
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	switch input.Action {
	case "list":
		items := make([]Item, 0, len(t.store.items))
		for _, item := range t.store.items {
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		return map[string]any{"items": items}, nil
	case "create":
		return t.create(input)
	case "update":
		return t.update(input, false)
	case "complete":
		return t.update(input, true)
	default:
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "unsupported todo action", false)
	}
}

func (t *Tool) create(input args) (any, error) {
	if strings.TrimSpace(input.Text) == "" || input.IdempotencyKey == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "text and idempotency_key are required", false)
	}
	if id, ok := t.store.idempotency[input.IdempotencyKey]; ok {
		return map[string]any{"item": t.store.items[id], "idempotent_replay": true}, nil
	}
	sum := sha256.Sum256([]byte(input.IdempotencyKey))
	id := "todo_" + hex.EncodeToString(sum[:8])
	if _, exists := t.store.items[id]; exists {
		return nil, tooling.NewError(tooling.CodeConflict, "todo id collision", false)
	}
	item := Item{
		ID: id, Text: input.Text, Status: "open", Version: 1,
		UpdatedAt: t.now().UTC(), IdempotencyKeys: []string{input.IdempotencyKey},
	}
	t.store.items[id] = item
	t.store.idempotency[input.IdempotencyKey] = id
	if err := t.store.saveLocked(); err != nil {
		delete(t.store.items, id)
		delete(t.store.idempotency, input.IdempotencyKey)
		return nil, tooling.NewError(tooling.CodeDependency, "todo store write failed", true)
	}
	return map[string]any{"item": item}, nil
}

func (t *Tool) update(input args, complete bool) (any, error) {
	if input.IdempotencyKey != "" {
		if id, ok := t.store.idempotency[input.IdempotencyKey]; ok {
			return map[string]any{"item": t.store.items[id], "idempotent_replay": true}, nil
		}
	}
	item, ok := t.store.items[input.ID]
	if !ok {
		return nil, tooling.NewError(tooling.CodeNotFound, "todo item not found", false)
	}
	previous := cloneItem(item)
	if input.ExpectedVersion > 0 && input.ExpectedVersion != item.Version {
		return nil, tooling.NewError(tooling.CodeConflict, "todo item version conflict", false)
	}
	if strings.TrimSpace(input.Text) != "" {
		item.Text = input.Text
	}
	if complete {
		item.Status = "completed"
	}
	item.Version++
	item.UpdatedAt = t.now().UTC()
	if input.IdempotencyKey != "" {
		item.IdempotencyKeys = append(item.IdempotencyKeys, input.IdempotencyKey)
		t.store.idempotency[input.IdempotencyKey] = item.ID
	}
	t.store.items[item.ID] = item
	if err := t.store.saveLocked(); err != nil {
		t.store.items[item.ID] = previous
		if input.IdempotencyKey != "" {
			delete(t.store.idempotency, input.IdempotencyKey)
		}
		return nil, tooling.NewError(tooling.CodeDependency, "todo store write failed", true)
	}
	return map[string]any{"item": item}, nil
}

func cloneItem(item Item) Item {
	item.IdempotencyKeys = append([]string(nil), item.IdempotencyKeys...)
	return item
}

type Adapter struct{}

func (Adapter) Adapt(raw string) (json.RawMessage, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "todo text is required", false)
	}
	key := fmt.Sprintf("direct-%x", sha256.Sum256([]byte(raw)))
	return json.Marshal(map[string]any{
		"action":          "create",
		"text":            raw,
		"idempotency_key": key,
	})
}
