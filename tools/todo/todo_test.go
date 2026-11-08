package todo

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"vibe-agent/internal/tooling"
)

func TestTodoIdempotencyAndRecovery(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todos.jsonl")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tool := New(store)
	raw, _ := json.Marshal(map[string]any{
		"action":          "create",
		"text":            "ship runtime",
		"idempotency_key": "k1",
	})
	first, err := tool.Invoke(context.Background(), tooling.Call{Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	second, err := tool.Invoke(context.Background(), tooling.Call{Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	firstID := first.(map[string]any)["item"].(Item).ID
	secondID := second.(map[string]any)["item"].(Item).ID
	if firstID != secondID {
		t.Fatalf("idempotent IDs differ: %s %s", firstID, secondID)
	}
	reopened, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.items[firstID]; !ok {
		t.Fatal("todo did not persist")
	}
	replayed, err := New(reopened).Invoke(context.Background(), tooling.Call{Arguments: raw})
	if err != nil {
		t.Fatal(err)
	}
	if replayed.(map[string]any)["idempotent_replay"] != true {
		t.Fatal("idempotency state did not survive restart")
	}
}

func TestTodoWriteFailureDoesNotMutateMemory(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "todos.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tool := New(store)
	create, _ := json.Marshal(map[string]any{
		"action": "create", "text": "durable", "idempotency_key": "create",
	})
	created, err := tool.Invoke(context.Background(), tooling.Call{Arguments: create})
	if err != nil {
		t.Fatal(err)
	}
	id := created.(map[string]any)["item"].(Item).ID
	store.path = t.TempDir()
	update, _ := json.Marshal(map[string]any{
		"action": "complete", "id": id, "idempotency_key": "complete",
	})
	if _, err := tool.Invoke(context.Background(), tooling.Call{Arguments: update}); err == nil {
		t.Fatal("update should fail when persistence fails")
	}
	if got := store.items[id]; got.Status != "open" || got.Version != 1 {
		t.Fatalf("item mutated after failed write: %#v", got)
	}
	if _, ok := store.idempotency["complete"]; ok {
		t.Fatal("failed update retained idempotency key")
	}
}
