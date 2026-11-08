package conversation

import (
	"encoding/json"
	"fmt"
	"strings"

	"vibe-agent/internal/llm"
)

const agentSystemPolicy = `You are a local operations agent. Use tools when they can establish facts or perform requested work.
Do not claim a mutation succeeded until its tool result confirms success.
Each session has one session-scoped todo_board. For multi-step operational work:
1. call todo_board action=list or action=get before changing existing items;
2. create requires title and idempotency_key;
3. update/delete require the current id, expected_version from the latest read, and a new idempotency_key;
4. keep statuses current: pending, in_progress, blocked, completed;
5. reuse the original idempotency_key when retrying the same mutation.
Never use a todo item title as its identity. Treat tool errors as authoritative and follow their stable code.`

func systemPrompt(contextText string, tools []llm.ToolDefinition) string {
	var builder strings.Builder
	builder.WriteString(agentSystemPolicy)
	builder.WriteString("\n\nTool summary:\n")
	for _, tool := range tools {
		builder.WriteString(toolPromptSummary(tool))
		builder.WriteByte('\n')
	}
	builder.WriteString("\nConversation context:\n")
	builder.WriteString(contextText)
	return builder.String()
}

func toolPromptSummary(tool llm.ToolDefinition) string {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Enum []string `json:"enum"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(tool.Parameters, &schema)
	summary := fmt.Sprintf("- %s: %s", tool.Name, strings.TrimSpace(tool.Description))
	if len(schema.Required) > 0 {
		summary += "; required=" + strings.Join(schema.Required, ",")
	}
	if action, ok := schema.Properties["action"]; ok && len(action.Enum) > 0 {
		summary += "; actions=" + strings.Join(action.Enum, "|")
	}
	return summary
}
