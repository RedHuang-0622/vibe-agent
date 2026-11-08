package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
)

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/chat", chat)
	mux.HandleFunc("/v1/messages", anthropicChat)
	mux.HandleFunc("/embedding", embedding)
	mux.HandleFunc("/rerank", rerank)
	mux.HandleFunc("/search", search)
	mux.HandleFunc("/weather", weather)
	mux.HandleFunc("/quota", quota)
	address := os.Getenv("FAKE_PROVIDER_ADDR")
	if address == "" {
		address = "127.0.0.1:47832"
	}
	log.Printf("fake provider listening on %s", address)
	log.Fatal(http.ListenAndServe(address, mux))
}

func anthropicChat(w http.ResponseWriter, r *http.Request) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	results := anthropicToolResults(body["messages"])
	if len(results) >= 5 {
		writeJSON(w, map[string]any{
			"content": []any{map[string]any{
				"type": "text", "text": "todo board CRUD complete",
			}},
			"stop_reason": "end_turn",
			"usage":       map[string]int{"input_tokens": 20, "output_tokens": 5},
		})
		return
	}
	_, arguments := anthropicBoardStep(results)
	writeJSON(w, map[string]any{
		"content": []any{map[string]any{
			"type": "tool_use", "id": fmt.Sprintf("toolu-fake-%d", len(results)+1),
			"name": "todo_board", "input": arguments,
		}},
		"stop_reason": "tool_use",
		"usage":       map[string]int{"input_tokens": 10, "output_tokens": 2},
	})
}

func anthropicToolResults(value any) []map[string]any {
	messages, _ := value.([]any)
	var results []map[string]any
	for _, rawMessage := range messages {
		message, _ := rawMessage.(map[string]any)
		content, _ := message["content"].([]any)
		for _, rawBlock := range content {
			block, _ := rawBlock.(map[string]any)
			if block["type"] != "tool_result" {
				continue
			}
			text, _ := block["content"].(string)
			var result map[string]any
			if json.Unmarshal([]byte(text), &result) == nil {
				results = append(results, result)
			}
		}
	}
	return results
}

func anthropicBoardStep(results []map[string]any) (string, map[string]any) {
	switch len(results) {
	case 0:
		return "create", map[string]any{
			"action": "create", "title": "release smoke", "details": "PTY fake provider",
			"priority": "high", "idempotency_key": "fake-create",
		}
	case 1:
		return "list", map[string]any{"action": "list"}
	case 2:
		id, _ := boardItemField(results[0], "id")
		return "update", map[string]any{
			"action": "update", "id": id, "status": "in_progress",
			"expected_version": 1, "idempotency_key": "fake-update",
		}
	case 3:
		id, version := boardItemIdentity(results[2])
		return "get", map[string]any{"action": "get", "id": id, "expected_version": version}
	default:
		id, version := boardItemIdentity(results[2])
		return "delete", map[string]any{
			"action": "delete", "id": id, "expected_version": version,
			"idempotency_key": "fake-delete",
		}
	}
}

func boardItemField(result map[string]any, field string) (any, bool) {
	item, _ := result["item"].(map[string]any)
	value, ok := item[field]
	return value, ok
}

func boardItemIdentity(result map[string]any) (any, int) {
	item, _ := result["item"].(map[string]any)
	version, _ := item["version"].(float64)
	return item["id"], int(version)
}

func chat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
		Tools           []any  `json:"tools"`
		Model           string `json:"model"`
		ReasoningEffort string `json:"reasoning_effort"`
		Thinking        struct {
			Type string `json:"type"`
		} `json:"thinking"`
		Stream bool `json:"stream"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Model == "deepseek-flash" &&
		(body.Thinking.Type != "enabled" || body.ReasoningEffort != "high" || body.Stream) {
		http.Error(w, `{"error":"deepseek request options missing"}`, http.StatusBadRequest)
		return
	}
	for _, message := range body.Messages {
		if message.Role == "tool" {
			writeJSON(w, map[string]any{
				"choices": []any{map[string]any{
					"message":       map[string]any{"content": "The calculator result is 7."},
					"finish_reason": "stop",
				}},
				"usage": map[string]int{"total_tokens": 8},
			})
			return
		}
	}
	last := ""
	if len(body.Messages) > 0 {
		last = body.Messages[len(body.Messages)-1].Content
	}
	if len(body.Tools) > 0 && strings.Contains(strings.ToLower(last), "calc") {
		writeJSON(w, map[string]any{
			"choices": []any{map[string]any{
				"message": map[string]any{"tool_calls": []any{map[string]any{
					"id": "fake-call-1",
					"function": map[string]any{
						"name": "calculator", "arguments": `{"expression":"1+2*3"}`,
					},
				}}},
				"finish_reason": "tool_calls",
			}},
			"usage": map[string]int{"total_tokens": 6},
		})
		return
	}
	writeJSON(w, map[string]any{
		"choices": []any{map[string]any{
			"message":       map[string]any{"content": "fake answer"},
			"finish_reason": "stop",
		}},
		"usage": map[string]int{"total_tokens": 4},
	})
}

func embedding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Input any `json:"input"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	var values []string
	switch input := body.Input.(type) {
	case []any:
		for _, value := range input {
			values = append(values, fmt.Sprint(value))
		}
	case string:
		values = []string{input}
	}
	data := make([]any, 0, len(values))
	for _, value := range values {
		vector := []float32{0.1, 0.2}
		if strings.Contains(strings.ToLower(value), "dense") {
			vector = []float32{0.9, 0.1}
		}
		data = append(data, map[string]any{"embedding": vector})
	}
	writeJSON(w, map[string]any{"data": data, "model": "fake-embedding"})
}

func rerank(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Documents []string `json:"documents"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	results := make([]any, 0, len(body.Documents))
	for index := range body.Documents {
		results = append(results, map[string]any{
			"index": index, "relevance_score": float64(len(body.Documents)-index) / 10,
		})
	}
	writeJSON(w, map[string]any{"results": results, "model": "fake-rerank"})
}

func search(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("location") != "" {
		writeJSON(w, map[string]any{"temperature_c": 20, "condition": "clear"})
		return
	}
	writeJSON(w, map[string]any{"results": []any{map[string]any{
		"title": "fake result", "url": "http://127.0.0.1/fake",
	}}})
}

func weather(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"temperature_c": 20, "condition": "clear"})
}

func quota(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Retry-After", "60")
	w.WriteHeader(http.StatusPaymentRequired)
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
