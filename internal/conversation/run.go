package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"vibe-agent/internal/contextmgr"
	"vibe-agent/internal/eventledger"
	"vibe-agent/internal/llm"
	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

func (a *Actor) runReAct(ctx context.Context, turn activeTurn, message pendingMessage, turnStartSeq int64) {
	history := []llm.Message{{Role: "user", Content: message.Text}}
	tools := modelTools(a.tools.ListTools())
	steps := 0
	lastSeq := turnStartSeq
	for {
		if err := ctx.Err(); err != nil {
			a.finishCanceled(ctx, turn, steps, lastSeq)
			return
		}
		if a.config.AutoCompress {
			_ = a.autoCompress(ctx)
		}
		contextText, err := a.contextText(ctx, message.AcceptedSeq-1)
		if err != nil {
			a.finishFailed(ctx, turn, steps, lastSeq, "ledger_io_error", err)
			return
		}
		requestMessages := append([]llm.Message{{
			Role: "system", Content: systemPrompt(contextText, tools),
		}}, history...)
		requested, err := a.append(ctx, eventledger.Draft{
			Type: eventledger.TypeLLMRequested, TraceID: turn.TraceID, CausationSeq: lastSeq,
			Payload: map[string]any{
				"purpose": "react", "input_bytes": len(contextText), "tools": len(tools),
			},
		})
		if err != nil {
			a.finishFailed(ctx, turn, steps, lastSeq, "ledger_io_error", err)
			return
		}
		if a.model == nil {
			a.finishFailed(ctx, turn, steps, requested.Seq, "dependency_unavailable", errors.New("model client is unavailable"))
			return
		}
		response, err := a.model.Complete(ctx, llm.Request{Messages: requestMessages, Tools: tools, Purpose: "react"})
		if err != nil {
			if ctx.Err() != nil {
				a.finishCanceled(ctx, turn, steps, requested.Seq)
				return
			}
			a.finishModelError(ctx, turn, steps, requested.Seq, err)
			return
		}
		completed, err := a.appendLLMCompleted(ctx, turn, requested.Seq, response)
		if err != nil {
			a.finishFailed(ctx, turn, steps, requested.Seq, "ledger_io_error", err)
			return
		}
		lastSeq = completed.Seq
		steps++
		if len(response.ToolCalls) == 0 {
			a.finishAnswer(ctx, turn, steps, response.Content, lastSeq)
			return
		}
		if steps >= a.config.MaxSteps {
			a.finishFailed(ctx, turn, steps, lastSeq, "max_steps_exceeded", errors.New("maximum ReAct steps reached"))
			return
		}
		history = append(history, llm.Message{Role: "assistant", Content: response.Content, ToolCalls: response.ToolCalls})
		for _, proposed := range response.ToolCalls {
			toolMessage, seq, err := a.runModelTool(ctx, turn, lastSeq, proposed)
			if err != nil {
				a.finishFailed(ctx, turn, steps, lastSeq, "tool_runtime_failed", err)
				return
			}
			lastSeq = seq
			history = append(history, toolMessage)
		}
	}
}

func (a *Actor) contextText(ctx context.Context, upTo int64) (string, error) {
	events, err := a.reader.ReadRange(ctx, 0, 0)
	if err != nil {
		return "", err
	}
	return contextmgr.Build(events, upTo).Text, nil
}

func (a *Actor) appendLLMCompleted(ctx context.Context, turn activeTurn, cause int64, response llm.Response) (eventledger.Event, error) {
	return a.append(ctx, eventledger.Draft{
		Type: eventledger.TypeLLMCompleted, TraceID: turn.TraceID, CausationSeq: cause,
		Status: "succeeded", Payload: map[string]any{
			"content": response.Content, "tool_calls": response.ToolCalls,
			"finish_reason": response.FinishReason, "usage": response.Usage,
			"account_id": response.AccountID, "account_revision": response.Revision,
			"model": response.Model,
		},
	})
}

func (a *Actor) runModelTool(ctx context.Context, turn activeTurn, cause int64, proposed llm.ToolCall) (llm.Message, int64, error) {
	callID := proposed.ID
	if callID == "" {
		callID = randomID("call")
	}
	requested, err := a.append(ctx, eventledger.Draft{
		Type: eventledger.TypeToolRequested, TraceID: turn.TraceID, CallID: callID,
		CausationSeq: cause, Payload: map[string]any{
			"tool_name": proposed.Name, "tool_kind": a.toolKind(proposed.Name), "invocation_mode": "model",
			"arguments_digest": digest(proposed.Arguments), "idempotency_key": callID,
		},
	})
	if err != nil {
		return llm.Message{}, cause, err
	}
	outcome := a.tools.Execute(ctx, tooling.Call{
		CallID: callID, ToolName: proposed.Name, InvocationMode: toolv1.InvocationModeModel,
		Arguments: json.RawMessage(proposed.Arguments), IdempotencyKey: callID, QueuedAt: time.Now(),
	})
	payload := toolEventPayload(outcome)
	eventType := eventledger.TypeToolCompleted
	status := "succeeded"
	if outcome.Error != nil {
		eventType = eventledger.TypeToolFailed
		status = "failed"
		if outcome.Error.Code == tooling.CodeCanceled {
			status = "canceled"
		}
	}
	if ctx.Err() != nil {
		eventType = eventledger.TypeToolFailed
		status = "canceled"
	}
	appendCtx := ctx
	cancel := func() {}
	if ctx.Err() != nil {
		appendCtx, cancel = cleanupContext()
	}
	defer cancel()
	finished, err := a.append(appendCtx, eventledger.Draft{
		Type: eventType, TraceID: turn.TraceID, CallID: callID,
		CausationSeq: requested.Seq, Status: status, Payload: payload,
	})
	if err != nil {
		return llm.Message{}, cause, err
	}
	content := string(outcome.Payload)
	if outcome.Error != nil {
		encoded, _ := json.Marshal(outcome.Error.Public())
		content = string(encoded)
	}
	return llm.Message{Role: "tool", ToolCallID: callID, Content: content}, finished.Seq, nil
}

func toolEventPayload(outcome tooling.Result) map[string]any {
	payload := map[string]any{
		"tool_name": outcome.Name, "tool_kind": outcome.Kind,
		"invocation_mode": outcome.Mode, "timing": outcome.Timing,
	}
	if outcome.Error != nil {
		payload["error"] = outcome.Error.Public()
	} else {
		payload["result"] = json.RawMessage(outcome.Payload)
	}
	return payload
}

func modelTools(specs []toolv1.ToolSpec) []llm.ToolDefinition {
	result := make([]llm.ToolDefinition, 0, len(specs))
	for _, spec := range specs {
		result = append(result, llm.ToolDefinition{
			Name: spec.Name, Description: spec.Description, Parameters: spec.Parameters,
		})
	}
	return result
}

func (a *Actor) runDirect(ctx context.Context, turn activeTurn, job *directJob, requested eventledger.Event) {
	outcome := a.tools.Execute(ctx, tooling.Call{
		CallID: requested.CallID, ToolName: job.toolName, InvocationMode: toolv1.InvocationModeDirect,
		RawRequest: job.raw, IdempotencyKey: digest(job.raw), QueuedAt: time.Now(),
	})
	eventType := eventledger.TypeToolCompleted
	status := "succeeded"
	if outcome.Error != nil {
		eventType = eventledger.TypeToolFailed
		status = "failed"
		if outcome.Error.Code == tooling.CodeCanceled {
			status = "canceled"
		}
	}
	terminalCtx, cancel := cleanupContext()
	defer cancel()
	finished, err := a.append(terminalCtx, eventledger.Draft{
		Type: eventType, TraceID: turn.TraceID, CallID: requested.CallID,
		CausationSeq: requested.Seq, Status: status, Payload: toolEventPayload(outcome),
	})
	if err == nil {
		_, _ = a.append(terminalCtx, eventledger.Draft{
			Type: eventledger.TypeTurnCompleted, TraceID: turn.TraceID,
			CausationSeq: finished.Seq, Status: status, Payload: map[string]any{
				"turn_id": turn.ID, "status": status, "step_count": 0,
			},
		})
	}
	result := DirectToolResult{TurnID: turn.ID, Result: outcome.Payload}
	if outcome.Error != nil {
		result.Error = outcome.Error
	}
	if err != nil {
		result.Error = errTool(err)
	}
	job.reply <- directReply{result: result}
	a.postCommand(finishedRequest{turnID: turn.ID})
}

func (a *Actor) finishAnswer(ctx context.Context, turn activeTurn, steps int, answer string, cause int64) {
	terminal, cancel := cleanupContext()
	defer cancel()
	assistant, err := a.append(terminal, eventledger.Draft{
		Type: eventledger.TypeAssistantMessageCompleted, TraceID: turn.TraceID, CausationSeq: cause,
		Status: "succeeded", Payload: map[string]any{
			"message_id": randomID("assistant"), "turn_id": turn.ID, "text": answer,
		},
	})
	if err == nil {
		_, _ = a.append(terminal, eventledger.Draft{
			Type: eventledger.TypeTurnCompleted, TraceID: turn.TraceID, CausationSeq: assistant.Seq,
			Status: "succeeded", Payload: map[string]any{
				"turn_id": turn.ID, "status": "succeeded", "step_count": steps,
			},
		})
	}
	a.postCommand(finishedRequest{turnID: turn.ID})
}

func (a *Actor) finishCanceled(_ context.Context, turn activeTurn, steps int, cause int64) {
	a.finishTerminal(turn, steps, cause, "canceled", "canceled", nil)
}

func (a *Actor) finishFailed(_ context.Context, turn activeTurn, steps int, cause int64, code string, err error) {
	a.finishTerminal(turn, steps, cause, "failed", code, err)
}

func (a *Actor) finishModelError(ctx context.Context, turn activeTurn, steps int, cause int64, err error) {
	code, retryable := modelErrorCode(err)
	payload := map[string]any{
		"reason_code": code, "retryable": retryable,
		"message": "model invocation failed",
	}
	var modelErr *llm.Error
	if errors.As(err, &modelErr) {
		if modelErr.ResetAt != nil {
			payload["reset_at"] = modelErr.ResetAt
		}
		if modelErr.RequestID != "" {
			payload["provider_request_id"] = modelErr.RequestID
		}
	}
	terminal, cancel := cleanupContext()
	defer cancel()
	_, _ = a.append(terminal, eventledger.Draft{
		Type: eventledger.TypeLLMFailed, TraceID: turn.TraceID, CausationSeq: cause,
		Status: "failed", Payload: payload,
	})
	_ = ctx
	a.finishFailed(terminal, turn, steps, cause, code, errors.New("model invocation failed"))
}

func (a *Actor) finishTerminal(turn activeTurn, steps int, cause int64, status, code string, err error) {
	terminal, cancel := cleanupContext()
	defer cancel()
	payload := map[string]any{"turn_id": turn.ID, "status": status, "step_count": steps}
	if code != "" {
		payload["reason_code"] = code
	}
	if err != nil {
		payload["message"] = err.Error()
	}
	_, _ = a.append(terminal, eventledger.Draft{
		Type: eventledger.TypeTurnCompleted, TraceID: turn.TraceID,
		CausationSeq: cause, Status: status, Payload: payload,
	})
	a.postCommand(finishedRequest{turnID: turn.ID})
}

func modelErrorCode(err error) (string, bool) {
	var modelErr *llm.Error
	if errors.As(err, &modelErr) {
		return modelErr.Code, modelErr.Retryable
	}
	return "dependency_unavailable", true
}

func cleanupContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 5*time.Second)
}

func (a *Actor) autoCompress(ctx context.Context) error {
	events, err := a.reader.ReadRange(ctx, 0, 0)
	if err != nil {
		return err
	}
	if len(contextmgr.Build(events, 0).Text) <= a.config.MaxContextBytes {
		return nil
	}
	reply := make(chan compressionReply, 1)
	if err := a.send(ctx, compressionCommand{
		trigger: "autonomous", keepTail: a.config.KeepTailEvents,
		allowActive: true, reply: reply,
	}); err != nil {
		return err
	}
	select {
	case result := <-reply:
		return result.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
