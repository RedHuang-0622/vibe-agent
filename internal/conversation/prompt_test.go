package conversation

import (
	"encoding/json"
	"strings"
	"testing"

	"vibe-agent/internal/llm"
)

func TestSystemPromptSummarizesBoardSchema(t *testing.T) {
	prompt := systemPrompt("user: organize release", []llm.ToolDefinition{{
		Name:        "todo_board",
		Description: "Manage the current session's operational todo board.",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["action"],
			"properties":{"action":{"type":"string","enum":["list","get","create","update","delete"]}}
		}`),
	}})
	for _, expected := range []string{
		"Each session has one session-scoped todo_board",
		"required=action",
		"actions=list|get|create|update|delete",
		"expected_version",
		"organize release",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt missing %q:\n%s", expected, prompt)
		}
	}
}
