package todo

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"vibe-agent/internal/tooling"
)

func TestBoardCRUDLifecycleAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "todo-board.jsonl")
	store, err := OpenBoardStore(path)
	if err != nil {
		t.Fatal(err)
	}
	tool := NewBoard(store)

	created := invokeBoard(t, tool, map[string]any{
		"action": "create", "title": "verify release",
		"details": "run smoke", "priority": "high", "idempotency_key": "create-1",
	})
	item := created["item"].(BoardItem)
	if item.Status != BoardStatusPending || item.Version != 1 {
		t.Fatalf("created=%#v", item)
	}

	got := invokeBoard(t, tool, map[string]any{"action": "get", "id": item.ID})
	if got["item"].(BoardItem).Title != "verify release" {
		t.Fatalf("get=%#v", got)
	}
	updated := invokeBoard(t, tool, map[string]any{
		"action": "update", "id": item.ID, "status": BoardStatusInProgress,
		"expected_version": 1, "idempotency_key": "update-1",
	})
	if updated["item"].(BoardItem).Status != BoardStatusInProgress ||
		updated["item"].(BoardItem).Version != 2 {
		t.Fatalf("updated=%#v", updated)
	}
	listed := invokeBoard(t, tool, map[string]any{"action": "list"})
	if len(listed["items"].([]BoardItem)) != 1 ||
		listed["summary"].(BoardSummary).InProgress != 1 {
		t.Fatalf("listed=%#v", listed)
	}

	reopened, err := OpenBoardStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewBoard(reopened).Invoke(context.Background(), boardCall(map[string]any{
		"action": "get", "id": item.ID,
	})); err != nil {
		t.Fatalf("reopened get failed: %v", err)
	}

	deleted := invokeBoard(t, NewBoard(reopened), map[string]any{
		"action": "delete", "id": item.ID, "expected_version": 2,
		"idempotency_key": "delete-1",
	})
	if deleted["deleted"] != true {
		t.Fatalf("deleted=%#v", deleted)
	}
	after, err := OpenBoardStore(path)
	if err != nil {
		t.Fatal(err)
	}
	listed = invokeBoard(t, NewBoard(after), map[string]any{"action": "list"})
	if len(listed["items"].([]BoardItem)) != 0 ||
		listed["summary"].(BoardSummary).Total != 0 {
		t.Fatalf("deleted item remained on board: %#v", listed)
	}
}

func TestBoardIdempotencyAndVersionConflict(t *testing.T) {
	store, err := OpenBoardStore(filepath.Join(t.TempDir(), "todo-board.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tool := NewBoard(store)
	first := invokeBoard(t, tool, map[string]any{
		"action": "create", "title": "durable", "idempotency_key": "same",
	})
	second := invokeBoard(t, tool, map[string]any{
		"action": "create", "title": "durable", "idempotency_key": "same",
	})
	if first["item"].(BoardItem).ID != second["item"].(BoardItem).ID ||
		second["idempotent_replay"] != true {
		t.Fatalf("replay mismatch: %#v %#v", first, second)
	}
	_, err = tool.Invoke(context.Background(), boardCall(map[string]any{
		"action": "update", "id": first["item"].(BoardItem).ID,
		"status": BoardStatusCompleted, "expected_version": 99,
		"idempotency_key": "update-conflict",
	}))
	var toolErr *tooling.Error
	if err == nil || !asToolError(err, &toolErr) || toolErr.Code != tooling.CodeConflict {
		t.Fatalf("err=%T %v", err, err)
	}
}

func TestBoardStatusAndDeletePolicy(t *testing.T) {
	store, err := OpenBoardStore(filepath.Join(t.TempDir(), "todo-board.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	tool := NewBoard(store)
	created := invokeBoard(t, tool, map[string]any{
		"action": "create", "title": "blocked work", "idempotency_key": "create",
	})
	item := created["item"].(BoardItem)
	blocked := invokeBoard(t, tool, map[string]any{
		"action": "update", "id": item.ID, "status": BoardStatusBlocked,
		"expected_version": 1, "idempotency_key": "block",
	})
	blockedItem := blocked["item"].(BoardItem)
	if blockedItem.Status != BoardStatusBlocked {
		t.Fatalf("blocked=%#v", blockedItem)
	}
	_, err = tool.Invoke(context.Background(), boardCall(map[string]any{
		"action": "delete", "id": item.ID, "expected_version": 1,
		"idempotency_key": "delete-stale",
	}))
	if err == nil {
		t.Fatal("stale delete must fail")
	}
	deleted := invokeBoard(t, tool, map[string]any{
		"action": "delete", "id": item.ID, "expected_version": blockedItem.Version,
		"idempotency_key": "delete",
	})
	if deleted["deleted"] != true {
		t.Fatalf("delete=%#v", deleted)
	}
}

func invokeBoard(t *testing.T, tool *BoardTool, args map[string]any) map[string]any {
	t.Helper()
	value, err := tool.Invoke(context.Background(), boardCall(args))
	if err != nil {
		t.Fatal(err)
	}
	return value.(map[string]any)
}

func boardCall(args map[string]any) tooling.Call {
	encoded, _ := json.Marshal(args)
	return tooling.Call{Arguments: encoded}
}

func asToolError(err error, target **tooling.Error) bool {
	value, ok := err.(*tooling.Error)
	if ok {
		*target = value
	}
	return ok
}
