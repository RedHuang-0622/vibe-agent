package observability

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vibe-agent/internal/eventledger"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func observableEvent(seq int64, eventType eventledger.EventType, callID, traceID string, payload any) eventledger.Event {
	encoded, _ := json.Marshal(payload)
	return eventledger.Event{
		SchemaVersion: 1, SessionID: "s", Seq: seq,
		EventID: eventledger.EventID("s", seq), Type: eventType,
		Visibility: eventledger.VisibilityConversation, CallID: callID,
		TraceID: traceID, Payload: encoded,
	}
}

func statsEvents() []eventledger.Event {
	var events []eventledger.Event
	seq := int64(1)
	for index := 0; index < 5; index++ {
		callID := "c" + string(rune('1'+index))
		events = append(events, observableEvent(seq, eventledger.TypeToolRequested, callID, "t1", map[string]any{
			"tool_name": "search", "tool_kind": "remote", "invocation_mode": "model",
		}))
		seq++
		eventType := eventledger.TypeToolCompleted
		payload := map[string]any{
			"timing": map[string]int64{"total_ms": int64((index + 1) * 10), "execute_ms": int64((index + 1) * 5)},
		}
		if index >= 3 {
			eventType = eventledger.TypeToolFailed
			payload["error"] = map[string]string{"code": "timeout"}
		}
		events = append(events, observableEvent(seq, eventType, callID, "t1", payload))
		seq++
	}
	events = append(events, observableEvent(seq, eventledger.TypeToolRequested, "orphan", "t2", map[string]any{
		"tool_name": "read_docs", "tool_kind": "local", "invocation_mode": "direct",
	}))
	return events
}

func TestToolStatsAndPercentiles(t *testing.T) {
	response := BuildStats(statsEvents(), map[string]bool{}, sessionv1.StatsRequest{})
	if len(response.ByTool) != 2 {
		t.Fatalf("stats=%#v", response)
	}
	var search sessionv1.ToolStat
	for _, stat := range response.ByTool {
		if stat.ToolName == "search" {
			search = stat
		}
	}
	if search.Calls != 5 || search.Succeeded != 3 || search.Failed != 2 ||
		search.FailureReasons["timeout"] != 2 {
		t.Fatalf("search=%#v", search)
	}
	if search.Total.AvgMS != 30 || search.Total.P50MS != 30 || search.Total.P95MS != 50 {
		t.Fatalf("latency=%#v", search.Total)
	}
}

func TestZeroDurationTimingRemainsASample(t *testing.T) {
	events := []eventledger.Event{
		observableEvent(1, eventledger.TypeToolRequested, "c0", "t0", map[string]any{
			"tool_name": "calculator", "tool_kind": "local", "invocation_mode": "direct",
		}),
		observableEvent(2, eventledger.TypeToolCompleted, "c0", "t0", map[string]any{
			"timing": map[string]int64{
				"queue_ms": 0, "validation_ms": 0, "execute_ms": 0, "total_ms": 0,
			},
		}),
	}
	response := BuildStats(events, nil, sessionv1.StatsRequest{})
	if len(response.ByTool) != 1 || response.ByTool[0].Total.Samples != 1 ||
		response.ByTool[0].Execute.Samples != 1 {
		t.Fatalf("stats=%#v", response)
	}
}

func TestInFlightVersusOrphaned(t *testing.T) {
	events := statsEvents()
	response := BuildStats(events, map[string]bool{"orphan": true}, sessionv1.StatsRequest{})
	for _, stat := range response.ByTool {
		if stat.ToolName == "read_docs" && stat.InFlight != 1 {
			t.Fatalf("stat=%#v", stat)
		}
	}
	response = BuildStats(events, map[string]bool{}, sessionv1.StatsRequest{})
	for _, stat := range response.ByTool {
		if stat.ToolName == "read_docs" && stat.Orphaned != 1 {
			t.Fatalf("stat=%#v", stat)
		}
	}
}

func TestTraceByCall(t *testing.T) {
	response := Trace(statsEvents(), sessionv1.TraceRequest{SessionID: "s", CallID: "c1"})
	if len(response.Nodes) != 2 || response.Nodes[0].Seq != 1 {
		t.Fatalf("trace=%#v", response)
	}
}

func TestProjectionCanBeDeletedAndRebuilt(t *testing.T) {
	root := t.TempDir()
	paths := DefaultProjectionPaths(root)
	events := statsEvents()
	if err := WriteProjections(context.Background(), paths, events, nil); err != nil {
		t.Fatal(err)
	}
	first, err := LoadStats(paths.Stats)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Stats); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(paths.Trace); err != nil {
		t.Fatal(err)
	}
	if err := WriteProjections(context.Background(), paths, events, nil); err != nil {
		t.Fatal(err)
	}
	second, err := LoadStats(paths.Stats)
	if err != nil {
		t.Fatal(err)
	}
	firstRaw, _ := json.Marshal(first)
	secondRaw, _ := json.Marshal(second)
	if string(firstRaw) != string(secondRaw) {
		t.Fatalf("projection mismatch\n%s\n%s", firstRaw, secondRaw)
	}
	if _, err := os.Stat(filepath.Join(root, "trace.jsonl")); err != nil {
		t.Fatal(err)
	}
}
