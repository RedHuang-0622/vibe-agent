package sessiond

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"vibe-agent/internal/conversation"
	"vibe-agent/internal/eventledger"
	"vibe-agent/internal/observability"
	"vibe-agent/internal/sessionruntime"
	"vibe-agent/internal/tooling"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

const (
	defaultSnapshotLimit = 100
	maxEventPageLimit    = 1000
	maxWatchWait         = 30 * time.Second
)

type commandHandler struct {
	sessionID     string
	generation    uint64
	actor         *conversation.Actor
	writer        *eventledger.Writer
	tools         *tooling.Registry
	shutdown      context.CancelFunc
	projectionDir string
}

func (h *commandHandler) Handle(ctx context.Context, method string, payload json.RawMessage) (any, error) {
	switch method {
	case sessionv1.MethodSessionHealth:
		return h.health(ctx)
	case sessionv1.MethodSessionSubmit:
		var request sessionv1.SubmitRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
			return nil, invalidRequest("invalid submit request")
		}
		policy := conversation.QueuePolicy(request.QueuePolicy)
		response, err := h.actor.Submit(ctx, conversation.SubmitRequest{
			Text: request.Text, QueuePolicy: policy,
		})
		if err != nil {
			return nil, err
		}
		return sessionv1.SubmitResponse{MessageID: response.MessageID, Seq: response.Seq}, nil
	case sessionv1.MethodSessionDirect:
		var request sessionv1.DirectToolRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
			return nil, invalidRequest("invalid direct tool request")
		}
		result, err := h.actor.DirectTool(ctx, request.Command)
		if err != nil {
			return nil, err
		}
		response := sessionv1.DirectToolResponse{TurnID: result.TurnID, Result: result.Result}
		if result.Error != nil {
			response.Error = &sessionv1.Error{
				Code: result.Error.Code, Message: result.Error.Message, Retryable: result.Error.Retryable,
				Scope: result.Error.Scope, Provider: result.Error.Provider,
				RequestID: result.Error.RequestID, ResetAt: result.Error.ResetAt,
			}
		}
		return response, nil
	case sessionv1.MethodSessionCancel:
		var request sessionv1.CancelRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
			return nil, invalidRequest("invalid cancel request")
		}
		canceled, err := h.actor.Cancel(ctx)
		return map[string]bool{"canceled": canceled}, err
	case sessionv1.MethodSessionCompress:
		var request sessionv1.CompressionRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
			return nil, invalidRequest("invalid compression request")
		}
		result, err := h.actor.Compress(ctx, request.Trigger, request.ThroughSeq, request.KeepTailEvents)
		return sessionv1.CompressionResponse{
			Committed: result.Committed, RedLineSeq: result.RedLineSeq, ErrorCode: result.ErrorCode,
		}, err
	case sessionv1.MethodSessionEvents:
		return h.events(ctx, payload)
	case sessionv1.MethodSessionWatch:
		return h.watch(ctx, payload)
	case sessionv1.MethodSessionSnapshot:
		return h.snapshot(ctx, payload)
	case sessionv1.MethodSessionHistory:
		return h.history(ctx, payload)
	case sessionv1.MethodSessionTrace:
		var request sessionv1.TraceRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
			return nil, invalidRequest("invalid trace request")
		}
		events, err := h.writer.ReadRange(ctx, 0, 0)
		if err != nil {
			return nil, err
		}
		result := observability.Trace(events, request)
		h.writeProjections(ctx, events, nil)
		return result, nil
	case sessionv1.MethodSessionStats:
		var request sessionv1.StatsRequest
		if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
			return nil, invalidRequest("invalid stats request")
		}
		events, err := h.writer.ReadRange(ctx, 0, 0)
		if err != nil {
			return nil, err
		}
		status, _ := h.actor.Status(ctx)
		result := observability.BuildStats(events, status.ActiveCalls, request)
		h.writeProjections(ctx, events, status.ActiveCalls)
		return result, nil
	case sessionv1.MethodHubToolList:
		return toolv1.ListResponse{Tools: h.tools.List()}, nil
	case sessionv1.MethodSessionShutdown:
		if h.shutdown != nil {
			go h.shutdown()
		}
		return map[string]bool{"stopping": true}, nil
	default:
		return nil, &sessionruntime.CodedError{Code: "not_found", Message: "unknown session method"}
	}
}

func (h *commandHandler) writeProjections(ctx context.Context, events []eventledger.Event, active map[string]bool) {
	if h.projectionDir == "" {
		return
	}
	_ = observability.WriteProjections(
		ctx, observability.DefaultProjectionPaths(h.projectionDir), events, active,
	)
}

func (h *commandHandler) health(ctx context.Context) (sessionv1.HealthResponse, error) {
	status, err := h.actor.Status(ctx)
	if err != nil {
		return sessionv1.HealthResponse{}, err
	}
	state := sessionv1.StateIdle
	if status.Active {
		state = sessionv1.StateRunning
	}
	return sessionv1.HealthResponse{
		SessionID: h.sessionID, PID: os.Getpid(), State: state,
		Generation: h.generation, LastSeq: status.LastSeq, Protocol: sessionv1.ProtocolVersion,
	}, nil
}

func (h *commandHandler) events(ctx context.Context, payload json.RawMessage) (any, error) {
	var request sessionv1.EventsRequest
	if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
		return nil, invalidRequest("invalid events request")
	}
	return h.readEvents(ctx, request.AfterSeq, request.Limit)
}

func (h *commandHandler) watch(ctx context.Context, payload json.RawMessage) (any, error) {
	var request sessionv1.WatchRequest
	if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID ||
		request.AfterSeq < 0 {
		return nil, invalidRequest("invalid watch request")
	}
	wait := time.Duration(request.WaitMS) * time.Millisecond
	if wait < 0 {
		return nil, invalidRequest("invalid watch wait")
	}
	if wait > maxWatchWait {
		wait = maxWatchWait
	}
	deadline := time.NewTimer(wait)
	defer deadline.Stop()
	for {
		signal := h.writer.ChangeSignal()
		response, err := h.readEvents(ctx, request.AfterSeq, request.Limit)
		if err != nil || len(response.Events) > 0 || response.ResyncRequired || wait == 0 {
			return response, err
		}
		select {
		case <-signal:
			continue
		case <-deadline.C:
			response.TimedOut = true
			return response, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (h *commandHandler) snapshot(ctx context.Context, payload json.RawMessage) (any, error) {
	var request sessionv1.SessionSnapshotRequest
	if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
		return nil, invalidRequest("invalid snapshot request")
	}
	limit := normalizeLimit(request.TailLimit, defaultSnapshotLimit)
	events, err := h.writer.ReadRange(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	watermark, err := h.writer.LastCommitted(ctx)
	if err != nil {
		return nil, err
	}
	if len(events) > limit {
		events = events[len(events)-limit:]
	}
	status, err := h.actor.Status(ctx)
	if err != nil {
		return nil, err
	}
	state := sessionv1.StateIdle
	if status.Active || status.Compressing {
		state = sessionv1.StateRunning
	}
	return sessionv1.SessionSnapshotResponse{
		SessionID: h.sessionID, State: state, Watermark: watermark,
		Events: eventViews(events), HasMoreBefore: len(events) > 0 && events[0].Seq > 1,
	}, nil
}

func (h *commandHandler) history(ctx context.Context, payload json.RawMessage) (any, error) {
	var request sessionv1.SessionHistoryRequest
	if err := json.Unmarshal(payload, &request); err != nil || request.SessionID != h.sessionID {
		return nil, invalidRequest("invalid history request")
	}
	limit := normalizeLimit(request.Limit, defaultSnapshotLimit)
	events, err := h.writer.ReadRange(ctx, 0, 0)
	if err != nil {
		return nil, err
	}
	watermark, err := h.writer.LastCommitted(ctx)
	if err != nil {
		return nil, err
	}
	before := request.BeforeSeq
	resync := before > watermark+1
	if before <= 0 {
		before = watermark + 1
	}
	page := make([]eventledger.Event, 0, limit)
	for _, event := range events {
		if event.Seq < before {
			page = append(page, event)
		}
	}
	if len(page) > limit {
		page = page[len(page)-limit:]
	}
	return sessionv1.SessionHistoryResponse{
		SessionID: h.sessionID, Watermark: watermark, Events: eventViews(page),
		HasMoreBefore: len(page) > 0 && page[0].Seq > 1, ResyncRequired: resync,
	}, nil
}

func (h *commandHandler) readEvents(ctx context.Context, afterSeq int64, limit int) (sessionv1.EventsResponse, error) {
	limit = normalizeLimit(limit, defaultSnapshotLimit)
	events, err := h.writer.ReadRange(ctx, afterSeq, limit)
	if err != nil {
		return sessionv1.EventsResponse{}, err
	}
	watermark, err := h.writer.LastCommitted(ctx)
	if err != nil {
		return sessionv1.EventsResponse{}, err
	}
	response := sessionv1.EventsResponse{
		Events: eventViews(events), Watermark: watermark, Cursor: afterSeq,
		ResyncRequired: afterSeq > watermark,
	}
	if len(events) > 0 {
		response.Cursor = events[len(events)-1].Seq
		response.HasMore = response.Cursor < watermark
		if events[0].Seq != afterSeq+1 {
			response.ResyncRequired = true
		}
	}
	return response, nil
}

func eventViews(events []eventledger.Event) []sessionv1.EventView {
	response := make([]sessionv1.EventView, 0, len(events))
	for _, event := range events {
		response = append(response, sessionv1.EventView{
			SchemaVersion: event.SchemaVersion, SessionID: event.SessionID,
			Seq: event.Seq, EventID: event.EventID, Type: string(event.Type),
			CreatedAt: event.CreatedAt, TraceID: event.TraceID,
			CausationSeq: event.CausationSeq, CallID: event.CallID,
			Visibility: string(event.Visibility), Status: event.Status, Payload: event.Payload,
		})
	}
	return response
}

func normalizeLimit(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	if value > maxEventPageLimit {
		return maxEventPageLimit
	}
	return value
}

func invalidRequest(message string) error {
	return &sessionruntime.CodedError{Code: "invalid_request", Message: message}
}
