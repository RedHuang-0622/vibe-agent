package conversation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"vibe-agent/internal/contextmgr"
	"vibe-agent/internal/eventledger"
	"vibe-agent/internal/llm"
	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
	"vibe-agent/tools/calculator"
)

type scriptedModel struct {
	mu        sync.Mutex
	responses []llm.Response
	errors    []error
	calls     int
	active    int32
	maxActive int32
	started   chan struct{}
	release   chan struct{}
}

func (m *scriptedModel) Complete(ctx context.Context, _ llm.Request) (llm.Response, error) {
	current := atomic.AddInt32(&m.active, 1)
	defer atomic.AddInt32(&m.active, -1)
	for {
		maximum := atomic.LoadInt32(&m.maxActive)
		if current <= maximum || atomic.CompareAndSwapInt32(&m.maxActive, maximum, current) {
			break
		}
	}
	if m.started != nil {
		select {
		case m.started <- struct{}{}:
		default:
		}
	}
	if m.release != nil {
		select {
		case <-m.release:
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	index := m.calls
	m.calls++
	if index < len(m.errors) && m.errors[index] != nil {
		return llm.Response{}, m.errors[index]
	}
	if index >= len(m.responses) {
		return llm.Response{Content: "done"}, nil
	}
	return m.responses[index], nil
}

func TestActorCancel(t *testing.T) {
	writer, err := eventledger.OpenWriter(filepath.Join(t.TempDir(), "messages.jsonl"), "sess", eventledger.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{
		responses: []llm.Response{{Content: "late"}},
		started:   make(chan struct{}, 1), release: make(chan struct{}, 1),
	}
	actor := newTestActor(t, writer, model)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := actor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Submit(ctx, SubmitRequest{Text: "cancel me"}); err != nil {
		t.Fatal(err)
	}
	<-model.started
	canceled, err := actor.Cancel(ctx)
	if err != nil || !canceled {
		t.Fatalf("canceled=%v err=%v", canceled, err)
	}
	waitForEvent(t, writer, eventledger.TypeTurnCompleted)
	events, _ := writer.ReadRange(context.Background(), 0, 0)
	last := events[len(events)-1]
	if last.Status != "canceled" {
		t.Fatalf("last=%#v", last)
	}
	_ = actor.Close(context.Background())
	_ = writer.Close()
}

type runtimeTools struct {
	registry *tooling.Registry
	runtime  *tooling.Runtime
}

func newTestTools(t *testing.T) *runtimeTools {
	t.Helper()
	registry := tooling.NewRegistry()
	if err := registry.Register(calculator.Spec(), calculator.New(), calculator.Adapter{}); err != nil {
		t.Fatal(err)
	}
	return &runtimeTools{registry: registry, runtime: tooling.NewRuntime(registry, nil)}
}

func (r *runtimeTools) Execute(ctx context.Context, call tooling.Call) tooling.Result {
	return r.runtime.Execute(ctx, call)
}

func (r *runtimeTools) ListTools() []toolv1.ToolSpec {
	return r.registry.List()
}

func TestActorChatAndRecoveredQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "messages.jsonl")
	writer, err := eventledger.OpenWriter(path, "sess", eventledger.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Append(context.Background(), eventledger.Draft{
		Type:    eventledger.TypeUserMessageAccepted,
		Payload: map[string]any{"message_id": "pending", "text": "recovered", "queue_policy": "queue"},
	}); err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{responses: []llm.Response{{Content: "answer"}}}
	actor := newTestActor(t, writer, model)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := actor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, writer, eventledger.TypeTurnCompleted, 1)
	snapshot, err := writer.ReadRange(context.Background(), 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot[0].Seq != 1 || snapshot[0].EventID != "sess-seq-1" {
		t.Fatalf("unexpected first event: %#v", snapshot[0])
	}
	types := eventTypes(snapshot)
	for _, want := range []eventledger.EventType{
		eventledger.TypeSessionStarted, eventledger.TypeUserMessageConsumed,
		eventledger.TypeTurnStarted, eventledger.TypeLLMRequested,
		eventledger.TypeLLMCompleted, eventledger.TypeAssistantMessageCompleted,
		eventledger.TypeTurnCompleted,
	} {
		if !types[want] {
			t.Fatalf("missing %s in %v", want, types)
		}
	}
	if err := actor.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestActorQueueWhileModelInFlight(t *testing.T) {
	writer, err := eventledger.OpenWriter(filepath.Join(t.TempDir(), "messages.jsonl"), "sess", eventledger.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{
		responses: []llm.Response{{Content: "one"}, {Content: "two"}},
		started:   make(chan struct{}, 1), release: make(chan struct{}, 1),
	}
	actor := newTestActor(t, writer, model)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := actor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Submit(ctx, SubmitRequest{Text: "first"}); err != nil {
		t.Fatal(err)
	}
	<-model.started
	second, err := actor.Submit(ctx, SubmitRequest{Text: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if second.Seq <= 1 {
		t.Fatalf("second seq=%d", second.Seq)
	}
	model.release <- struct{}{}
	model.release <- struct{}{}
	waitForCount(t, writer, eventledger.TypeTurnCompleted, 2)
	if atomic.LoadInt32(&model.maxActive) != 1 {
		t.Fatalf("concurrent model calls=%d", model.maxActive)
	}
	_ = actor.Close(context.Background())
	_ = writer.Close()
}

func TestActorReActToolCallAndDirectTool(t *testing.T) {
	writer, err := eventledger.OpenWriter(filepath.Join(t.TempDir(), "messages.jsonl"), "sess", eventledger.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "calculator", Arguments: `{"expression":"1+2*3"}`}}},
		{Content: "seven"},
	}}
	tools := newTestTools(t)
	actor, err := New(DefaultConfig("sess"), writer, writer, model, tools, contextmgr.BasicSummarizer{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := actor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Submit(ctx, SubmitRequest{Text: "calculate"}); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, writer, eventledger.TypeTurnCompleted, 1)
	result, err := actor.DirectTool(ctx, "/calculator 1+2*3")
	if err != nil || result.Error != nil {
		t.Fatalf("direct result=%#v err=%v", result, err)
	}
	snapshot, _ := writer.ReadRange(context.Background(), 0, 0)
	modelCalls, directCalls := 0, 0
	for _, event := range snapshot {
		if event.Type != eventledger.TypeToolRequested {
			continue
		}
		var payload map[string]any
		_ = json.Unmarshal(event.Payload, &payload)
		switch payload["invocation_mode"] {
		case "model":
			modelCalls++
		case "direct":
			directCalls++
		}
	}
	if modelCalls != 1 || directCalls != 1 {
		t.Fatalf("model=%d direct=%d", modelCalls, directCalls)
	}
	_ = actor.Close(context.Background())
	_ = writer.Close()
}

func TestActorMaxSteps(t *testing.T) {
	writer, err := eventledger.OpenWriter(filepath.Join(t.TempDir(), "messages.jsonl"), "sess", eventledger.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	responses := make([]llm.Response, 2)
	for index := range responses {
		responses[index] = llm.Response{ToolCalls: []llm.ToolCall{{ID: "c" + string(rune('1'+index)), Name: "calculator", Arguments: `{"expression":"1"}`}}}
	}
	model := &scriptedModel{responses: responses}
	config := DefaultConfig("sess")
	config.MaxSteps = 2
	actor, _ := New(config, writer, writer, model, newTestTools(t), contextmgr.BasicSummarizer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := actor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Submit(ctx, SubmitRequest{Text: "loop"}); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, writer, eventledger.TypeTurnCompleted, 1)
	snapshot, _ := writer.ReadRange(context.Background(), 0, 0)
	last := snapshot[len(snapshot)-1]
	if last.Status != "failed" {
		t.Fatalf("last=%#v", last)
	}
	_ = actor.Close(context.Background())
	_ = writer.Close()
}

func TestActorCompression(t *testing.T) {
	writer, err := eventledger.OpenWriter(filepath.Join(t.TempDir(), "messages.jsonl"), "sess", eventledger.DefaultOptions())
	if err != nil {
		t.Fatal(err)
	}
	model := &scriptedModel{responses: []llm.Response{{Content: "answer"}}}
	config := DefaultConfig("sess")
	config.KeepTailEvents = 0
	config.AutoCompress = false
	actor, _ := New(config, writer, writer, model, newTestTools(t), contextmgr.BasicSummarizer{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := actor.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := actor.Submit(ctx, SubmitRequest{Text: "summarize me"}); err != nil {
		t.Fatal(err)
	}
	waitForCount(t, writer, eventledger.TypeTurnCompleted, 1)
	result, err := actor.Compress(ctx, "external", 0, 0)
	if err != nil || !result.Committed {
		t.Fatalf("compression=%#v err=%v", result, err)
	}
	waitForEvent(t, writer, eventledger.TypeCompressionCompleted)
	_ = actor.Close(context.Background())
	_ = writer.Close()
}

func TestParseSlashAcceptsAnyWhitespace(t *testing.T) {
	name, raw := parseSlash("/calculator\t1 + 2")
	if name != "calculator" || raw != "1 + 2" {
		t.Fatalf("name=%q raw=%q", name, raw)
	}
}

func newTestActor(t *testing.T, writer *eventledger.Writer, model ModelClient) *Actor {
	t.Helper()
	config := DefaultConfig("sess")
	config.AutoCompress = false
	actor, err := New(config, writer, writer, model, newTestTools(t), contextmgr.BasicSummarizer{})
	if err != nil {
		t.Fatal(err)
	}
	return actor
}

func waitForEvent(t *testing.T, writer *eventledger.Writer, eventType eventledger.EventType) {
	t.Helper()
	waitForCount(t, writer, eventType, 1)
}

func waitForCount(t *testing.T, writer *eventledger.Writer, eventType eventledger.EventType, count int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		events, err := writer.ReadRange(context.Background(), 0, 0)
		if err == nil {
			found := 0
			for _, event := range events {
				if event.Type == eventType {
					found++
				}
			}
			if found >= count {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d %s events", count, eventType)
}

func eventTypes(events []eventledger.Event) map[eventledger.EventType]bool {
	result := make(map[eventledger.EventType]bool)
	for _, event := range events {
		result[event.Type] = true
	}
	return result
}
