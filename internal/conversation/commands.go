package conversation

import (
	"context"
	"errors"
	"strings"
	"unicode"

	"vibe-agent/internal/eventledger"
	"vibe-agent/internal/tooling"
)

func (a *Actor) handleSubmit(ctx context.Context, command submitCommand) {
	request := command.request
	if strings.TrimSpace(request.Text) == "" {
		command.reply <- submitReply{err: errors.New("message text is required")}
		return
	}
	if request.MessageID == "" {
		request.MessageID = randomID("msg")
	}
	if request.QueuePolicy == "" {
		request.QueuePolicy = QueuePolicyQueue
	}
	event, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeUserMessageAccepted,
		Payload: map[string]any{
			"message_id": request.MessageID, "text": request.Text, "queue_policy": request.QueuePolicy,
		},
	})
	if err != nil {
		command.reply <- submitReply{err: err}
		return
	}
	message := pendingMessage{
		ID: request.MessageID, Text: request.Text,
		AcceptedSeq: event.Seq, Policy: request.QueuePolicy,
	}
	a.jobs = append(a.jobs, sessionJob{seq: event.Seq, message: &message})
	if request.QueuePolicy == QueuePolicyCancelAndQueue && a.active != nil && a.active.Cancel != nil {
		a.active.Cancel()
	}
	command.reply <- submitReply{response: SubmitResponse{MessageID: request.MessageID, Seq: event.Seq}}
	a.startNext(ctx)
}

func (a *Actor) handleDirect(ctx context.Context, command directCommand) {
	name, raw := parseSlash(command.command)
	requestID := randomID("req")
	event, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeUserToolCommandAccepted,
		Payload: map[string]any{
			"request_id": requestID, "tool_name": name,
			"raw_request": raw, "idempotency_key": digest(raw),
		},
	})
	if err != nil {
		command.reply <- directReply{result: DirectToolResult{
			Error: errTool(err),
		}}
		return
	}
	job := &directJob{
		requestID: requestID, turnID: randomID("turn"), traceID: randomID(a.config.SessionID + "-trace"),
		toolName: name, raw: raw, acceptedSeq: event.Seq, reply: command.reply,
	}
	a.jobs = append(a.jobs, sessionJob{seq: event.Seq, direct: job})
	a.startNext(ctx)
}

func (a *Actor) startNext(ctx context.Context) {
	if a.active != nil || len(a.jobs) == 0 || a.closed {
		return
	}
	job := a.jobs[0]
	a.jobs = a.jobs[1:]
	turnCtx, cancel := context.WithCancel(ctx)
	if job.message != nil {
		a.startMessageTurn(turnCtx, cancel, *job.message)
		return
	}
	if job.direct != nil {
		a.startDirectTurn(turnCtx, cancel, job.direct)
		return
	}
	cancel()
}

func (a *Actor) startMessageTurn(ctx context.Context, cancel context.CancelFunc, message pendingMessage) {
	turn := &activeTurn{
		ID: randomID("turn"), TraceID: randomID(a.config.SessionID + "-trace"),
		MessageID: message.ID, Cancel: cancel,
	}
	consumed, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeUserMessageConsumed, TraceID: turn.TraceID,
		CausationSeq: message.AcceptedSeq,
		Payload:      map[string]any{"message_id": message.ID, "turn_id": turn.ID},
	})
	if err != nil {
		cancel()
		a.finishWithoutTurn(err)
		return
	}
	started, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeTurnStarted, TraceID: turn.TraceID, CausationSeq: consumed.Seq,
		Payload: map[string]any{"turn_id": turn.ID, "kind": "chat"},
	})
	if err != nil {
		cancel()
		a.finishWithoutTurn(err)
		return
	}
	a.active = turn
	go a.runReAct(ctx, *turn, message, started.Seq)
}

func (a *Actor) startDirectTurn(ctx context.Context, cancel context.CancelFunc, job *directJob) {
	turn := &activeTurn{ID: job.turnID, TraceID: job.traceID, Cancel: cancel}
	started, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeTurnStarted, TraceID: turn.TraceID, CausationSeq: job.acceptedSeq,
		Payload: map[string]any{"turn_id": turn.ID, "kind": "direct_tool"},
	})
	if err != nil {
		cancel()
		job.reply <- directReply{result: DirectToolResult{TurnID: turn.ID, Error: errTool(err)}}
		a.finishWithoutTurn(err)
		return
	}
	callID := randomID("call")
	requested, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeToolRequested, TraceID: turn.TraceID,
		CausationSeq: started.Seq, CallID: callID,
		Payload: map[string]any{
			"tool_name": job.toolName, "tool_kind": a.toolKind(job.toolName), "invocation_mode": "direct",
			"arguments_digest": digest(job.raw), "idempotency_key": digest(job.raw),
		},
	})
	if err != nil {
		cancel()
		job.reply <- directReply{result: DirectToolResult{TurnID: turn.ID, Error: errTool(err)}}
		a.finishWithoutTurn(err)
		return
	}
	a.active = turn
	go a.runDirect(ctx, *turn, job, requested)
}

func (a *Actor) toolKind(name string) string {
	for _, spec := range a.tools.ListTools() {
		if spec.Name == name {
			return spec.Kind
		}
	}
	return ""
}

func (a *Actor) finishWithoutTurn(err error) {
	_ = err
	a.startNext(context.Background())
}

func parseSlash(command string) (string, string) {
	trimmed := strings.TrimSpace(command)
	if !strings.HasPrefix(trimmed, "/") || strings.HasPrefix(trimmed, "//") {
		return "", trimmed
	}
	trimmed = strings.TrimPrefix(trimmed, "/")
	separator := strings.IndexFunc(trimmed, unicode.IsSpace)
	if separator < 0 {
		return trimmed, ""
	}
	return trimmed[:separator], strings.TrimSpace(trimmed[separator:])
}

func errTool(err error) *tooling.Error {
	if err == nil {
		return nil
	}
	return &tooling.Error{Code: tooling.CodeInternal, Message: err.Error()}
}
