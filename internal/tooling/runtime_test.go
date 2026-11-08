package tooling

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

type testHandler struct {
	payload any
	err     error
}

type panicHandler struct{}

func (panicHandler) Invoke(context.Context, Call) (any, error) {
	panic("boom")
}

func (h testHandler) Invoke(context.Context, Call) (any, error) {
	return h.payload, h.err
}

func testSpec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:        "echo",
		Version:     "v1",
		Kind:        "local",
		Description: "test",
		Parameters:  json.RawMessage(`{"type":"object","required":["value"],"properties":{"value":{"type":"string"}}}`),
		ReadOnly:    true,
		TimeoutMS:   100,
	}
}

func TestRegisterDuplicateFailsAndList(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testSpec(), testHandler{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(testSpec(), testHandler{}, nil); err == nil {
		t.Fatal("duplicate registration must fail")
	}
	if len(registry.List()) != 1 {
		t.Fatal("tool missing from catalog")
	}
}

func TestRuntimeValidationAndSuccess(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testSpec(), testHandler{payload: map[string]bool{"ok": true}}, nil); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(registry, nil)
	bad := runtime.Execute(context.Background(), Call{
		CallID:    "bad",
		ToolName:  "echo",
		Arguments: json.RawMessage(`{}`),
		QueuedAt:  time.Now(),
	})
	if bad.Error == nil || bad.Error.Code != CodeInvalidRequest {
		t.Fatalf("bad=%#v", bad)
	}
	good := runtime.Execute(context.Background(), Call{
		CallID:    "good",
		ToolName:  "echo",
		Arguments: json.RawMessage(`{"value":"x"}`),
		QueuedAt:  time.Now(),
	})
	if good.Error != nil || !json.Valid(good.Payload) {
		t.Fatalf("good=%#v", good)
	}
}

func TestRuntimeUnknownTool(t *testing.T) {
	result := NewRuntime(NewRegistry(), nil).Execute(context.Background(), Call{CallID: "c", ToolName: "missing"})
	if result.Error == nil || result.Error.Code != CodeUnknownTool {
		t.Fatalf("result=%#v", result)
	}
}

func TestRuntimeRejectsTrailingAndFractionalInteger(t *testing.T) {
	registry := NewRegistry()
	spec := testSpec()
	spec.Parameters = json.RawMessage(`{"type":"object","properties":{"count":{"type":"integer"}}}`)
	if err := registry.Register(spec, testHandler{}, nil); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(registry, nil)
	for _, args := range []string{`{} {}`, `{"count":1.5}`} {
		result := runtime.Execute(context.Background(), Call{
			CallID: "c", ToolName: "echo", Arguments: json.RawMessage(args),
		})
		if result.Error == nil || result.Error.Code != CodeInvalidRequest {
			t.Fatalf("args=%s result=%#v", args, result)
		}
	}
}

func TestRuntimeConvertsHandlerPanic(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(testSpec(), panicHandler{}, nil); err != nil {
		t.Fatal(err)
	}
	result := NewRuntime(registry, nil).Execute(context.Background(), Call{
		CallID: "c", ToolName: "echo", Arguments: json.RawMessage(`{"value":"x"}`),
	})
	if result.Error == nil || result.Error.Code != CodeInternal {
		t.Fatalf("result=%#v", result)
	}
}
