package llm

import (
	"context"
	"encoding/json"
	"strings"

	accountv1 "vibe-agent/pkg/contracts/account/v1"
)

const anthropicMaxOutputTokens = 4096

func openAIMessages(messages []Message) []map[string]any {
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "assistant":
			encoded := map[string]any{"role": "assistant"}
			if message.Content != "" {
				encoded["content"] = message.Content
			}
			if len(message.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(message.ToolCalls))
				for _, call := range message.ToolCalls {
					calls = append(calls, map[string]any{
						"id": call.ID, "type": "function",
						"function": map[string]any{"name": call.Name, "arguments": call.Arguments},
					})
				}
				encoded["tool_calls"] = calls
			}
			result = append(result, encoded)
		case "tool":
			result = append(result, map[string]any{
				"role": "tool", "tool_call_id": message.ToolCallID, "content": message.Content,
			})
		default:
			result = append(result, map[string]any{
				"role": message.Role, "content": message.Content,
			})
		}
	}
	return result
}

func (g *Gateway) completeAnthropic(
	ctx context.Context,
	invocation accountv1.ResolvedInvocation,
	secret []byte,
	request Request,
) (Response, error) {
	messages, system := anthropicMessages(request.Messages)
	body := map[string]any{
		"model": invocation.Binding.Model, "max_tokens": anthropicMaxOutputTokens,
		"messages": messages,
	}
	if system != "" {
		body["system"] = system
	}
	if len(request.Tools) > 0 {
		body["tools"] = anthropicTools(request.Tools)
	}
	response, cancel, err := g.postJSON(ctx, invocation.Binding, secret, body)
	if err != nil {
		return Response{}, err
	}
	defer cancel()
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Response{}, mapHTTPError(response)
	}
	var decoded struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := decodeLimited(response.Body, &decoded); err != nil {
		return Response{}, NewError(CodeUpstreamSchema, "anthropic response schema is invalid", false)
	}
	result := Response{
		FinishReason: decoded.StopReason,
		AccountID:    invocation.Account.AccountID,
		Revision:     invocation.Account.Revision,
		Model:        invocation.Binding.Model,
		Usage: Usage{
			PromptTokens: decoded.Usage.InputTokens, CompletionTokens: decoded.Usage.OutputTokens,
			TotalTokens: decoded.Usage.InputTokens + decoded.Usage.OutputTokens,
		},
	}
	var content strings.Builder
	for _, block := range decoded.Content {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "tool_use":
			arguments := string(block.Input)
			if arguments == "" {
				arguments = "{}"
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID: block.ID, Name: block.Name, Arguments: arguments,
			})
		}
	}
	result.Content = content.String()
	if len(result.ToolCalls) > 0 && result.FinishReason == "" {
		result.FinishReason = "tool_use"
	}
	return result, nil
}

func anthropicMessages(messages []Message) ([]map[string]any, string) {
	var system []string
	result := make([]map[string]any, 0, len(messages))
	for _, message := range messages {
		switch message.Role {
		case "system":
			if strings.TrimSpace(message.Content) != "" {
				system = append(system, message.Content)
			}
		case "user":
			result = appendAnthropicMessage(result, "user", textBlock(message.Content))
		case "assistant":
			blocks := make([]any, 0, len(message.ToolCalls)+1)
			if message.Content != "" {
				blocks = append(blocks, textBlock(message.Content))
			}
			for _, call := range message.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": call.ID, "name": call.Name,
					"input": anthropicInput(call.Arguments),
				})
			}
			result = appendAnthropicMessage(result, "assistant", blocks...)
		case "tool":
			result = appendAnthropicMessage(result, "user", map[string]any{
				"type": "tool_result", "tool_use_id": message.ToolCallID,
				"content": message.Content,
			})
		}
	}
	return result, strings.Join(system, "\n\n")
}

func appendAnthropicMessage(messages []map[string]any, role string, blocks ...any) []map[string]any {
	if len(blocks) == 0 {
		return messages
	}
	if len(messages) > 0 && messages[len(messages)-1]["role"] == role {
		existing, _ := messages[len(messages)-1]["content"].([]any)
		messages[len(messages)-1]["content"] = append(existing, blocks...)
		return messages
	}
	return append(messages, map[string]any{"role": role, "content": blocks})
}

func textBlock(text string) map[string]any {
	return map[string]any{"type": "text", "text": text}
}

func anthropicInput(arguments string) any {
	var input any
	if json.Unmarshal([]byte(arguments), &input) == nil {
		return input
	}
	return map[string]any{"raw_arguments": arguments}
}

func anthropicTools(definitions []ToolDefinition) []map[string]any {
	result := make([]map[string]any, 0, len(definitions))
	for _, definition := range definitions {
		result = append(result, map[string]any{
			"name": definition.Name, "description": definition.Description,
			"input_schema": definition.Parameters,
		})
	}
	return result
}
