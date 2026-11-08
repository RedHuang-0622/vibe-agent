package sessiond

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"testing"

	"vibe-agent/internal/tooling"
	knowledgev1 "vibe-agent/pkg/contracts/knowledge/v1"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type emptyKnowledge struct{}

func (emptyKnowledge) Write(ctx context.Context, request knowledgev1.WriteRequest) (knowledgev1.RecordRef, error) {
	return knowledgev1.RecordRef{}, nil
}

func (emptyKnowledge) Search(ctx context.Context, request knowledgev1.Query) (knowledgev1.Result, error) {
	return knowledgev1.Result{}, nil
}

func (emptyKnowledge) Update(ctx context.Context, request knowledgev1.UpdateRequest) (knowledgev1.RecordRef, error) {
	return knowledgev1.RecordRef{}, nil
}

func TestBuildRegistryHasDefaultTools(t *testing.T) {
	registry, err := buildRegistry(Config{SessionDir: t.TempDir()}, nil, emptyKnowledge{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, spec := range registry.List() {
		names = append(names, spec.Name)
	}
	sort.Strings(names)
	want := []string{
		"calculator", "memory_search", "memory_update", "memory_write",
		"read_docs", "search", "todo", "todo_board", "weatherapi",
	}
	if len(names) != len(want) {
		t.Fatalf("tools=%v", names)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tools=%v want=%v", names, want)
		}
	}
}

func TestTodoBoardIsIsolatedPerSessionDirectory(t *testing.T) {
	root := t.TempDir()
	registryA, err := buildRegistry(Config{SessionDir: filepath.Join(root, "a")}, nil, emptyKnowledge{})
	if err != nil {
		t.Fatal(err)
	}
	registryB, err := buildRegistry(Config{SessionDir: filepath.Join(root, "b")}, nil, emptyKnowledge{})
	if err != nil {
		t.Fatal(err)
	}
	create := tooling.NewRuntime(registryA, nil).Execute(context.Background(), tooling.Call{
		CallID: "call-create", ToolName: "todo_board", InvocationMode: toolv1.InvocationModeModel,
		Arguments: json.RawMessage(`{"action":"create","title":"session A only","idempotency_key":"a-create"}`),
	})
	if create.Error != nil {
		t.Fatalf("create error=%v", create.Error)
	}
	list := tooling.NewRuntime(registryB, nil).Execute(context.Background(), tooling.Call{
		CallID: "call-list", ToolName: "todo_board", InvocationMode: toolv1.InvocationModeModel,
		Arguments: json.RawMessage(`{"action":"list"}`),
	})
	if list.Error != nil {
		t.Fatalf("list error=%v", list.Error)
	}
	var result struct {
		Items []any `json:"items"`
	}
	if err := json.Unmarshal(list.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("session B leaked session A board items: %s", list.Payload)
	}
}
