package operator

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"vibe-agent/internal/sessionruntime"
	accountv1 "vibe-agent/pkg/contracts/account/v1"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type fakeHub struct {
	mu            sync.Mutex
	lastSeq       int64
	events        []sessionv1.EventView
	nextTurn      int
	watchFailures int
}

func newFakeHub(t *testing.T) (*fakeHub, *httptest.Server) {
	t.Helper()
	hub := &fakeHub{lastSeq: 10}
	hub.events = append(hub.events, sessionv1.EventView{
		SchemaVersion: 1, SessionID: "b", Seq: 10, EventID: "b-seq-10",
		Type: "turn.completed", Visibility: "conversation", Payload: json.RawMessage(`{"turn_id":"old"}`),
	})
	server := httptest.NewServer(http.HandlerFunc(hub.serveHTTP))
	t.Cleanup(server.Close)
	return hub, server
}

func (h *fakeHub) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	var envelope sessionv1.HTTPRequest
	if err := json.NewDecoder(request.Body).Decode(&envelope); err != nil {
		writeFakeError(writer, "invalid_request")
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	var payload any
	switch envelope.Method {
	case sessionv1.MethodHubListSessions:
		payload = map[string]any{"sessions": []sessionv1.SessionView{
			{SessionID: "a", Name: "A", State: sessionv1.StateIdle, LastSeq: 5},
			{SessionID: "b", Name: "B", State: sessionv1.StateIdle, LastSeq: h.lastSeq},
		}}
	case sessionv1.MethodHubInspect:
		var ref sessionv1.SessionRef
		_ = json.Unmarshal(envelope.Payload, &ref)
		payload = sessionv1.CreateSessionResponse{Session: sessionv1.SessionView{
			SessionID: ref.SessionID, Name: ref.SessionID, State: sessionv1.StateIdle, LastSeq: h.lastSeq,
		}}
	case sessionv1.MethodSessionSubmit:
		var submitted sessionv1.SubmitRequest
		_ = json.Unmarshal(envelope.Payload, &submitted)
		messageID := "msg-" + submitted.Text
		h.nextTurn++
		turnID := "turn-" + submitted.Text
		h.appendEvent("user.message.consumed", map[string]string{
			"message_id": messageID, "turn_id": turnID,
		})
		h.appendEvent("assistant.message.completed", map[string]string{
			"turn_id": turnID, "text": "answer-" + submitted.Text,
		})
		h.appendEvent("turn.completed", map[string]string{"turn_id": turnID})
		payload = sessionv1.SubmitResponse{MessageID: messageID, Seq: h.lastSeq - 2}
	case sessionv1.MethodSessionEvents:
		var query sessionv1.EventsRequest
		_ = json.Unmarshal(envelope.Payload, &query)
		payload = h.eventPage(query.AfterSeq, query.Limit)
	case sessionv1.MethodSessionWatch:
		var query sessionv1.WatchRequest
		_ = json.Unmarshal(envelope.Payload, &query)
		if h.watchFailures > 0 {
			h.watchFailures--
			writer.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(writer).Encode(sessionv1.HTTPResponse{
				Error: &sessionv1.Error{
					Code: "dependency_unavailable", Message: "temporary disconnect", Retryable: true,
				},
			})
			return
		}
		response := h.eventPage(query.AfterSeq, query.Limit)
		response.TimedOut = len(response.Events) == 0
		payload = response
	case sessionv1.MethodSessionSnapshot:
		var query sessionv1.SessionSnapshotRequest
		_ = json.Unmarshal(envelope.Payload, &query)
		events := h.events
		limit := query.TailLimit
		if limit <= 0 || limit > len(events) {
			limit = len(events)
		}
		events = events[len(events)-limit:]
		payload = sessionv1.SessionSnapshotResponse{
			SessionID: query.SessionID, State: sessionv1.StateIdle, Watermark: h.lastSeq,
			Events: events, HasMoreBefore: len(events) > 0 && events[0].Seq > 1,
		}
	case sessionv1.MethodSessionHistory:
		var query sessionv1.SessionHistoryRequest
		_ = json.Unmarshal(envelope.Payload, &query)
		before := query.BeforeSeq
		if before <= 0 {
			before = h.lastSeq + 1
		}
		var events []sessionv1.EventView
		for _, event := range h.events {
			if event.Seq < before {
				events = append(events, event)
			}
		}
		if len(events) > query.Limit && query.Limit > 0 {
			events = events[len(events)-query.Limit:]
		}
		payload = sessionv1.SessionHistoryResponse{
			SessionID: query.SessionID, Watermark: h.lastSeq, Events: events,
			HasMoreBefore: len(events) > 0 && events[0].Seq > 1,
		}
	case sessionv1.MethodSessionDirect:
		h.appendEvent("turn.completed", map[string]string{"turn_id": "direct"})
		payload = sessionv1.DirectToolResponse{
			TurnID: "direct", Result: json.RawMessage(`{"value":2}`),
		}
	default:
		writeFakeError(writer, "not_found")
		return
	}
	encoded, _ := json.Marshal(payload)
	_ = json.NewEncoder(writer).Encode(sessionv1.HTTPResponse{OK: true, Payload: encoded})
}

func (h *fakeHub) eventPage(afterSeq int64, limit int) sessionv1.EventsResponse {
	response := sessionv1.EventsResponse{Watermark: h.lastSeq, Cursor: afterSeq}
	for _, event := range h.events {
		if event.Seq > afterSeq {
			response.Events = append(response.Events, event)
			if limit > 0 && len(response.Events) == limit {
				break
			}
		}
	}
	if len(response.Events) > 0 {
		response.Cursor = response.Events[len(response.Events)-1].Seq
		response.HasMore = response.Cursor < h.lastSeq
		if response.Events[0].Seq != afterSeq+1 {
			response.ResyncRequired = true
		}
	}
	return response
}

func (h *fakeHub) appendEvent(eventType string, value any) {
	h.lastSeq++
	encoded, _ := json.Marshal(value)
	h.events = append(h.events, sessionv1.EventView{
		SchemaVersion: 1, SessionID: "b", Seq: h.lastSeq,
		EventID: "b-seq-" + jsonNumber(h.lastSeq), Type: eventType,
		Visibility: "conversation", Payload: encoded,
	})
}

func writeFakeError(writer http.ResponseWriter, code string) {
	_ = json.NewEncoder(writer).Encode(sessionv1.HTTPResponse{
		Error: &sessionv1.Error{Code: code, Message: code},
	})
}

func jsonNumber(value int64) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func TestTUISynchronizesSessionAndSlashCursors(t *testing.T) {
	_, server := newFakeHub(t)
	client := NewClient(sessionruntime.NewHubClient(server.URL, "token", server.Client()))
	var output bytes.Buffer
	input := bytes.NewBufferString("/use b\nhello\n/calculator 1+1\nagain\n/exit\n")
	if err := RunTUI(context.Background(), client, input, &output); err != nil {
		t.Fatal(err)
	}
	if got := bytes.Count(output.Bytes(), []byte("answer-hello")); got != 1 {
		t.Fatalf("first answer count=%d\n%s", got, output.String())
	}
	if got := bytes.Count(output.Bytes(), []byte("answer-again")); got != 1 {
		t.Fatalf("second answer count=%d\n%s", got, output.String())
	}
}

func TestWaitForMessageTurnReconnectsWithCursor(t *testing.T) {
	hub, server := newFakeHub(t)
	hub.watchFailures = 1
	client := NewClient(sessionruntime.NewHubClient(server.URL, "token", server.Client()))
	submitted, err := client.Submit(context.Background(), "b", "reconnect", "queue")
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	cursor, err := waitForMessageTurn(context.Background(), client, "b", submitted.MessageID, 10, &output)
	if err != nil {
		t.Fatal(err)
	}
	if cursor != hub.lastSeq {
		t.Fatalf("cursor=%d watermark=%d", cursor, hub.lastSeq)
	}
	if got := bytes.Count(output.Bytes(), []byte("answer-reconnect")); got != 1 {
		t.Fatalf("answer count=%d output=%q", got, output.String())
	}
}

func TestConsumeTurnEventsPrintsToolLifecycleOnce(t *testing.T) {
	events := []sessionv1.EventView{
		{
			Seq: 11, Type: "tool.requested", CallID: "call-1",
			Payload: json.RawMessage(`{"tool_name":"todo_board","invocation_mode":"model"}`),
		},
		{
			Seq: 12, Type: "tool.completed", CallID: "call-1",
			Payload: json.RawMessage(`{"tool_name":"todo_board","timing":{"total_ms":3},"result":{"action":"list"}}`),
		},
		{
			Seq: 13, Type: "turn.completed",
			Payload: json.RawMessage(`{"turn_id":"turn-13"}`),
		},
	}
	var output bytes.Buffer
	done, cursor, _ := consumeTurnEvents(events, 10, "", "", &output)
	if !done || cursor != 13 {
		t.Fatalf("done=%v cursor=%d", done, cursor)
	}
	text := output.String()
	if bytes.Count([]byte(text), []byte("[tool] requested")) != 1 ||
		bytes.Count([]byte(text), []byte("[tool] completed")) != 1 {
		t.Fatalf("output=%q", text)
	}
}

func TestLoadExampleAccountAndSmokeFiles(t *testing.T) {
	root := filepath.Join("..", "..", "config")
	accounts, err := LoadAccountFile(filepath.Join(root, "llm-accounts.example.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 3 {
		t.Fatalf("accounts=%d", len(accounts))
	}
	deepSeek := accounts[0]
	if deepSeek.AccountID != "deepseek-main" {
		t.Fatalf("first account=%q", deepSeek.AccountID)
	}
	for _, capability := range []accountv1.Capability{
		accountv1.CapabilityChat, accountv1.CapabilityToolCall,
	} {
		binding := deepSeek.Services[capability]
		if binding.Model != "deepseek-flash" ||
			binding.APIKeyEnv != "DEEPSEEK_API_KEY" ||
			binding.Thinking == nil || binding.Thinking.Type != "enabled" ||
			binding.ReasoningEffort != "high" {
			t.Fatalf("%s binding=%#v", capability, binding)
		}
	}
	for _, account := range accounts {
		for capability, binding := range account.Services {
			if binding.TimeoutMS <= 0 {
				t.Fatalf("%s/%s timeout not parsed", account.AccountID, capability)
			}
		}
	}
	reports, err := ValidateAccountFile(filepath.Join(root, "llm-accounts.example.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	for index, report := range reports {
		if !report.Valid {
			t.Fatalf("example account %d invalid: %#v", index, report.Issues)
		}
	}
	cases, err := LoadSmokeCases(filepath.Join(root, "llm-smoke.example.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 5 || !cases[0].Enabled || !containsTag(cases[0].Tags, "real-api") {
		t.Fatalf("cases=%#v", cases)
	}
}

func TestSanitizeTerminal(t *testing.T) {
	if got := sanitizeTerminal("ok\x1b[31mred\nnext"); got != "ok[31mred\nnext" {
		t.Fatalf("sanitized=%q", got)
	}
}

func TestLoadMinimalDeepSeekAccountNormalizesEffortAlias(t *testing.T) {
	path := filepath.Join(t.TempDir(), "accounts.jsonl")
	record := `{"schema_version":1,"account_id":"deepseek","revision":1,"enabled":true,"display_name":"DeepSeek","default_for":["chat"],"services":{"chat":{"reasoning_effort":"medium"}}}`
	if err := os.WriteFile(path, []byte(record+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	accounts, err := LoadAccountFile(path)
	if err != nil {
		t.Fatal(err)
	}
	binding := accounts[0].Services[accountv1.CapabilityChat]
	if binding.Model != "deepseek-flash" ||
		binding.APIKeyEnv != "DEEPSEEK_API_KEY" ||
		binding.Thinking == nil || binding.Thinking.Type != "enabled" ||
		binding.ReasoningEffort != "high" {
		t.Fatalf("binding=%#v", binding)
	}
	if _, ok := accounts[0].Services[accountv1.CapabilityToolCall]; !ok {
		t.Fatal("DeepSeek chat binding was not reused for tool_call")
	}
}

func TestDeepSeekAndClaudeCodeExamplesAreUsable(t *testing.T) {
	root := filepath.Join("..", "..", "config")
	deepSeekPath := filepath.Join(root, "deepseek-account.example.jsonl")
	accounts, err := LoadAccountFile(deepSeekPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts=%d", len(accounts))
	}
	binding := accounts[0].Services[accountv1.CapabilityChat]
	if binding.Model != "deepseek-flash" ||
		binding.APIKeyEnv != "DEEPSEEK_API_KEY" ||
		binding.Thinking == nil || binding.Thinking.Type != "enabled" ||
		binding.ReasoningEffort != "high" {
		t.Fatalf("binding=%#v", binding)
	}
	reports, err := ValidateAccountFile(deepSeekPath)
	if err != nil || len(reports) != 1 || !reports[0].Valid {
		t.Fatalf("reports=%#v err=%v", reports, err)
	}

	data, err := os.ReadFile(filepath.Join(root, "claude-code-deepseek.settings.example.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("DEEPSEEK_API_KEY=")) ||
		bytes.Contains(data, []byte("ANTHROPIC_AUTH_TOKEN")) {
		t.Fatal("Claude Code example must not contain a credential")
	}
	var settings struct {
		Model       string            `json:"model"`
		EffortLevel string            `json:"effortLevel"`
		Env         map[string]string `json:"env"`
	}
	if err := json.Unmarshal(data, &settings); err != nil {
		t.Fatal(err)
	}
	if settings.Model != "deepseek-flash[1m]" || settings.EffortLevel != "max" ||
		settings.Env["ANTHROPIC_BASE_URL"] != "https://api.deepseek.com/anthropic" ||
		settings.Env["ANTHROPIC_MODEL"] != "deepseek-flash[1m]" {
		t.Fatalf("settings=%#v", settings)
	}

	embeddingRerankPath := filepath.Join(root, "embedding-rerank.example.jsonl")
	accounts, err = LoadAccountFile(embeddingRerankPath)
	if err != nil || len(accounts) != 2 {
		t.Fatalf("accounts=%#v err=%v", accounts, err)
	}
	reports, err = ValidateAccountFile(embeddingRerankPath)
	if err != nil || len(reports) != 2 || !reports[0].Valid || !reports[1].Valid {
		t.Fatalf("reports=%#v err=%v", reports, err)
	}
}
