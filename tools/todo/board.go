package todo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

// BoardTool implements the model/direct CRUD surface for one session board.
type BoardTool struct {
	store *BoardStore
	now   func() time.Time
}

func NewBoard(store *BoardStore) *BoardTool {
	return &BoardTool{store: store, now: time.Now}
}

func BoardSpec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:    "todo_board",
		Version: "v1",
		Kind:    "mutating",
		Description: "Manage the current session's operational todo board. " +
			"Use list/get before mutations. create requires title and idempotency_key; " +
			"update/delete require id, expected_version and idempotency_key. " +
			"Statuses: pending, in_progress, blocked, completed. " +
			"Use this board for multi-step operations and keep item status current.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["action"],
			"additionalProperties":false,
			"properties":{
				"action":{"type":"string","enum":["list","get","create","update","delete"]},
				"id":{"type":"string"},
				"title":{"type":"string"},
				"details":{"type":"string"},
				"status":{"type":"string","enum":["pending","in_progress","blocked","completed"]},
				"priority":{"type":"string","enum":["low","medium","high","urgent"]},
				"expected_version":{"type":"integer","minimum":1},
				"idempotency_key":{"type":"string"},
				"filter_status":{"type":"string","enum":["pending","in_progress","blocked","completed"]},
				"limit":{"type":"integer","minimum":1,"maximum":500}
			}
		}`),
		DirectRequestField: "title",
		ReadOnly:           false,
		TimeoutMS:          3000,
		Capabilities:       []string{"todo_board.crud", "session_scoped"},
		Idempotent:         true,
	}
}

type boardArgs struct {
	Action          string  `json:"action"`
	ID              string  `json:"id"`
	Title           *string `json:"title"`
	Details         *string `json:"details"`
	Status          *string `json:"status"`
	Priority        *string `json:"priority"`
	ExpectedVersion int     `json:"expected_version"`
	IdempotencyKey  string  `json:"idempotency_key"`
	FilterStatus    string  `json:"filter_status"`
	Limit           int     `json:"limit"`
}

func (t *BoardTool) Invoke(_ context.Context, call tooling.Call) (any, error) {
	var input boardArgs
	if err := json.Unmarshal(call.Arguments, &input); err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid todo board arguments", false)
	}
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	switch input.Action {
	case "list":
		return t.list(input)
	case "get":
		return t.get(input)
	case "create":
		return t.create(input)
	case "update":
		return t.update(input)
	case "delete":
		return t.delete(input)
	default:
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "unsupported todo board action", false)
	}
}

func (t *BoardTool) list(input boardArgs) (any, error) {
	if input.FilterStatus != "" && !validBoardStatus(input.FilterStatus) {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid todo board status filter", false)
	}
	limit := input.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	items := make([]BoardItem, 0)
	for _, item := range t.store.items {
		if item.Deleted || (input.FilterStatus != "" && item.Status != input.FilterStatus) {
			continue
		}
		items = append(items, cloneBoardItem(item))
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].Priority == items[j].Priority {
			return items[i].ID < items[j].ID
		}
		return boardPriorityRank(items[i].Priority) > boardPriorityRank(items[j].Priority)
	})
	if len(items) > limit {
		items = items[:limit]
	}
	return map[string]any{
		"action": "list", "items": items, "summary": summarizeBoard(t.store.items),
	}, nil
}

func (t *BoardTool) get(input boardArgs) (any, error) {
	item, err := t.activeItem(input.ID)
	if err != nil {
		return nil, err
	}
	return map[string]any{"action": "get", "item": item}, nil
}

func (t *BoardTool) create(input boardArgs) (any, error) {
	if replay, ok, err := t.replay(input, "create"); ok || err != nil {
		return replay, err
	}
	title := strings.TrimSpace(valueOrEmpty(input.Title))
	if title == "" || strings.TrimSpace(input.IdempotencyKey) == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "title and idempotency_key are required", false)
	}
	status := valueOr(input.Status, BoardStatusPending)
	priority := valueOr(input.Priority, BoardPriorityMedium)
	if err := validateBoardFields(status, priority); err != nil {
		return nil, err
	}
	id := boardItemID(input.IdempotencyKey)
	if _, exists := t.store.items[id]; exists {
		return nil, tooling.NewError(tooling.CodeConflict, "todo board item id collision", false)
	}
	now := t.now().UTC()
	item := BoardItem{
		ID: id, Title: title, Details: valueOrEmpty(input.Details), Status: status,
		Priority: priority, Version: 1, CreatedAt: now, UpdatedAt: now,
		IdempotencyActions: map[string]string{input.IdempotencyKey: "create"},
	}
	t.store.items[id] = item
	t.store.idempotency[input.IdempotencyKey] = boardReplay{itemID: id, action: "create"}
	if err := t.store.saveLocked(); err != nil {
		delete(t.store.items, id)
		delete(t.store.idempotency, input.IdempotencyKey)
		return nil, tooling.NewError(tooling.CodeDependency, "todo board write failed", true)
	}
	return map[string]any{"action": "create", "item": item}, nil
}

func (t *BoardTool) update(input boardArgs) (any, error) {
	if replay, ok, err := t.replay(input, "update"); ok || err != nil {
		return replay, err
	}
	item, err := t.activeItem(input.ID)
	if err != nil {
		return nil, err
	}
	if err := requireBoardMutationIdentity(input, item); err != nil {
		return nil, err
	}
	previous := cloneBoardItem(item)
	if input.Title != nil {
		item.Title = strings.TrimSpace(*input.Title)
	}
	if input.Details != nil {
		item.Details = *input.Details
	}
	if input.Status != nil {
		item.Status = *input.Status
	}
	if input.Priority != nil {
		item.Priority = *input.Priority
	}
	if item.Title == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "todo board title cannot be empty", false)
	}
	if err := validateBoardFields(item.Status, item.Priority); err != nil {
		return nil, err
	}
	if !validBoardTransition(previous.Status, item.Status) {
		return nil, tooling.NewError(tooling.CodeInvalidRequest,
			fmt.Sprintf("invalid todo board status transition %s -> %s", previous.Status, item.Status), false)
	}
	item.Version++
	item.UpdatedAt = t.now().UTC()
	item.IdempotencyActions = cloneActions(item.IdempotencyActions)
	item.IdempotencyActions[input.IdempotencyKey] = "update"
	t.store.items[item.ID] = item
	t.store.idempotency[input.IdempotencyKey] = boardReplay{itemID: item.ID, action: "update"}
	if err := t.store.saveLocked(); err != nil {
		t.store.items[item.ID] = previous
		delete(t.store.idempotency, input.IdempotencyKey)
		return nil, tooling.NewError(tooling.CodeDependency, "todo board write failed", true)
	}
	return map[string]any{"action": "update", "item": item}, nil
}

func (t *BoardTool) delete(input boardArgs) (any, error) {
	if replay, ok, err := t.replay(input, "delete"); ok || err != nil {
		return replay, err
	}
	item, err := t.activeItem(input.ID)
	if err != nil {
		return nil, err
	}
	if err := requireBoardMutationIdentity(input, item); err != nil {
		return nil, err
	}
	previous := cloneBoardItem(item)
	now := t.now().UTC()
	item.Deleted = true
	item.DeletedAt = &now
	item.Version++
	item.UpdatedAt = now
	item.IdempotencyActions = cloneActions(item.IdempotencyActions)
	item.IdempotencyActions[input.IdempotencyKey] = "delete"
	t.store.items[item.ID] = item
	t.store.idempotency[input.IdempotencyKey] = boardReplay{itemID: item.ID, action: "delete"}
	if err := t.store.saveLocked(); err != nil {
		t.store.items[item.ID] = previous
		delete(t.store.idempotency, input.IdempotencyKey)
		return nil, tooling.NewError(tooling.CodeDependency, "todo board write failed", true)
	}
	return map[string]any{"action": "delete", "item": item, "deleted": true}, nil
}

func (t *BoardTool) replay(input boardArgs, action string) (any, bool, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return nil, false, nil
	}
	replay, ok := t.store.idempotency[key]
	if !ok {
		return nil, false, nil
	}
	if replay.action != action {
		return nil, false, tooling.NewError(tooling.CodeConflict,
			"idempotency_key was already used for "+replay.action, false)
	}
	item, ok := t.store.items[replay.itemID]
	if !ok {
		return nil, false, tooling.NewError(tooling.CodeNotFound, "todo board item not found", false)
	}
	return map[string]any{
		"action": action, "item": item, "idempotent_replay": true,
		"deleted": action == "delete",
	}, true, nil
}

func (t *BoardTool) activeItem(id string) (BoardItem, error) {
	item, ok := t.store.items[strings.TrimSpace(id)]
	if !ok || item.Deleted {
		return BoardItem{}, tooling.NewError(tooling.CodeNotFound, "todo board item not found", false)
	}
	return cloneBoardItem(item), nil
}

func requireBoardMutationIdentity(input boardArgs, item BoardItem) error {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return tooling.NewError(tooling.CodeInvalidRequest, "idempotency_key is required", false)
	}
	if input.ExpectedVersion <= 0 {
		return tooling.NewError(tooling.CodeInvalidRequest, "expected_version is required", false)
	}
	if input.ExpectedVersion != item.Version {
		return tooling.NewError(tooling.CodeConflict, "todo board item version conflict", false)
	}
	return nil
}

func validateBoardFields(status, priority string) error {
	if !validBoardStatus(status) {
		return tooling.NewError(tooling.CodeInvalidRequest, "invalid todo board status", false)
	}
	if !validBoardPriority(priority) {
		return tooling.NewError(tooling.CodeInvalidRequest, "invalid todo board priority", false)
	}
	return nil
}

func validBoardStatus(status string) bool {
	switch status {
	case BoardStatusPending, BoardStatusInProgress, BoardStatusBlocked, BoardStatusCompleted:
		return true
	default:
		return false
	}
}

func validBoardPriority(priority string) bool {
	switch priority {
	case BoardPriorityLow, BoardPriorityMedium, BoardPriorityHigh, BoardPriorityUrgent:
		return true
	default:
		return false
	}
}

func validBoardTransition(from, to string) bool {
	if from == to {
		return true
	}
	switch from {
	case BoardStatusPending:
		return to == BoardStatusInProgress || to == BoardStatusBlocked || to == BoardStatusCompleted
	case BoardStatusInProgress:
		return to == BoardStatusPending || to == BoardStatusBlocked || to == BoardStatusCompleted
	case BoardStatusBlocked:
		return to == BoardStatusPending || to == BoardStatusInProgress
	case BoardStatusCompleted:
		return to == BoardStatusPending || to == BoardStatusInProgress
	default:
		return false
	}
}

func summarizeBoard(items map[string]BoardItem) BoardSummary {
	var summary BoardSummary
	for _, item := range items {
		if item.Deleted {
			continue
		}
		summary.Total++
		switch item.Status {
		case BoardStatusPending:
			summary.Pending++
		case BoardStatusInProgress:
			summary.InProgress++
		case BoardStatusBlocked:
			summary.Blocked++
		case BoardStatusCompleted:
			summary.Completed++
		}
	}
	return summary
}

func boardPriorityRank(priority string) int {
	switch priority {
	case BoardPriorityUrgent:
		return 4
	case BoardPriorityHigh:
		return 3
	case BoardPriorityMedium:
		return 2
	case BoardPriorityLow:
		return 1
	default:
		return 0
	}
}

func boardItemID(key string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(key)))
	return "task_" + hex.EncodeToString(sum[:8])
}

func cloneBoardItem(item BoardItem) BoardItem {
	item.IdempotencyActions = cloneActions(item.IdempotencyActions)
	if item.DeletedAt != nil {
		value := *item.DeletedAt
		item.DeletedAt = &value
	}
	return item
}

func cloneActions(input map[string]string) map[string]string {
	result := make(map[string]string, len(input)+1)
	for key, value := range input {
		result[key] = value
	}
	return result
}

func valueOr(value *string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	return strings.TrimSpace(*value)
}

func valueOrEmpty(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

type BoardAdapter struct{}

func (BoardAdapter) Adapt(raw string) (json.RawMessage, error) {
	title := strings.TrimSpace(raw)
	if title == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "todo board title is required", false)
	}
	key := fmt.Sprintf("direct-%x", sha256.Sum256([]byte(title)))
	return json.Marshal(map[string]any{
		"action": "create", "title": title, "idempotency_key": key,
	})
}
