package conversation

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"vibe-agent/internal/contextmgr"
	"vibe-agent/internal/eventledger"
)

type Actor struct {
	config     Config
	appender   EventAppender
	reader     EventReader
	model      ModelClient
	tools      ToolExecutor
	summarizer contextmgr.Summarizer
	commands   chan any
	done       chan struct{}
	cancel     context.CancelFunc

	jobs        []sessionJob
	active      *activeTurn
	activeCalls map[string]bool
	compressing bool
	lastSeq     int64
	closed      bool
}

type sessionJob struct {
	seq     int64
	message *pendingMessage
	direct  *directJob
}

type directJob struct {
	requestID   string
	turnID      string
	traceID     string
	toolName    string
	raw         string
	acceptedSeq int64
	reply       chan directReply
}

type submitCommand struct {
	request SubmitRequest
	reply   chan submitReply
}

type submitReply struct {
	response SubmitResponse
	err      error
}

type directCommand struct {
	command string
	reply   chan directReply
}

type cancelCommand struct {
	reply chan bool
}

type compressionCommand struct {
	trigger     string
	target      int64
	keepTail    int
	allowActive bool
	reply       chan compressionReply
}

type compressionReply struct {
	result CompressionResult
	err    error
}

type compressionFinished struct {
	result CompressionResult
	reply  chan compressionReply
}

type wakeCommand struct{}
type stopCommand struct{}
type statusCommand struct {
	reply chan Status
}

func New(config Config, appender EventAppender, reader EventReader, model ModelClient, tools ToolExecutor, summarizer contextmgr.Summarizer) (*Actor, error) {
	if strings.TrimSpace(config.SessionID) == "" {
		return nil, errors.New("session id is required")
	}
	if appender == nil || reader == nil {
		return nil, errors.New("event ledger is required")
	}
	if tools == nil {
		return nil, errors.New("tool executor is required")
	}
	if config.QueueSize <= 0 {
		config.QueueSize = DefaultConfig(config.SessionID).QueueSize
	}
	if config.MaxSteps <= 0 {
		config.MaxSteps = DefaultConfig(config.SessionID).MaxSteps
	}
	return &Actor{
		config: config, appender: appender, reader: reader, model: model, tools: tools,
		summarizer: summarizer, commands: make(chan any, config.QueueSize),
		done: make(chan struct{}), activeCalls: make(map[string]bool),
	}, nil
}

func (a *Actor) Start(ctx context.Context) error {
	events, err := a.reader.ReadRange(ctx, 0, 0)
	if err != nil {
		return err
	}
	recoverQueue(&a.jobs, events)
	a.lastSeq, err = a.reader.LastCommitted(ctx)
	if err != nil {
		return err
	}
	if _, err := a.appender.Append(ctx, eventledger.Draft{
		Type: eventledger.TypeSessionStarted, Visibility: eventledger.VisibilityInternal,
		Payload: map[string]any{"schema_version": eventledger.SchemaVersion, "recovered_seq": a.lastSeq},
	}); err != nil {
		return err
	}
	a.lastSeq++
	runCtx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	go a.loop(runCtx)
	a.commands <- wakeCommand{}
	return nil
}

func (a *Actor) Submit(ctx context.Context, request SubmitRequest) (SubmitResponse, error) {
	reply := make(chan submitReply, 1)
	if err := a.send(ctx, submitCommand{request: request, reply: reply}); err != nil {
		return SubmitResponse{}, err
	}
	select {
	case result := <-reply:
		return result.response, result.err
	case <-ctx.Done():
		return SubmitResponse{}, ctx.Err()
	case <-a.done:
		return SubmitResponse{}, errors.New("session actor is closed")
	}
}

func (a *Actor) DirectTool(ctx context.Context, command string) (DirectToolResult, error) {
	reply := make(chan directReply, 1)
	if err := a.send(ctx, directCommand{command: command, reply: reply}); err != nil {
		return DirectToolResult{}, err
	}
	select {
	case result := <-reply:
		return result.result, nil
	case <-ctx.Done():
		return DirectToolResult{}, ctx.Err()
	case <-a.done:
		return DirectToolResult{}, errors.New("session actor is closed")
	}
}

func (a *Actor) Cancel(ctx context.Context) (bool, error) {
	reply := make(chan bool, 1)
	if err := a.send(ctx, cancelCommand{reply: reply}); err != nil {
		return false, err
	}
	select {
	case canceled := <-reply:
		return canceled, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

func (a *Actor) Compress(ctx context.Context, trigger string, target int64, keepTail int) (CompressionResult, error) {
	reply := make(chan compressionReply, 1)
	if err := a.send(ctx, compressionCommand{
		trigger: trigger, target: target, keepTail: keepTail, reply: reply,
	}); err != nil {
		return CompressionResult{}, err
	}
	select {
	case result := <-reply:
		return result.result, result.err
	case <-ctx.Done():
		return CompressionResult{}, ctx.Err()
	}
}

func (a *Actor) Status(ctx context.Context) (Status, error) {
	reply := make(chan Status, 1)
	if err := a.send(ctx, statusCommand{reply: reply}); err != nil {
		return Status{}, err
	}
	select {
	case status := <-reply:
		return status, nil
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
}

func (a *Actor) Close(ctx context.Context) error {
	select {
	case a.commands <- stopCommand{}:
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return nil
	}
	select {
	case <-a.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (a *Actor) send(ctx context.Context, command any) error {
	select {
	case a.commands <- command:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-a.done:
		return errors.New("session actor is closed")
	}
}

func (a *Actor) loop(ctx context.Context) {
	defer close(a.done)
	for {
		select {
		case <-ctx.Done():
			a.stopActive()
			return
		case command := <-a.commands:
			if !a.handle(ctx, command) {
				a.stopActive()
				return
			}
		}
	}
}

func (a *Actor) handle(ctx context.Context, command any) bool {
	switch value := command.(type) {
	case wakeCommand:
		a.startNext(ctx)
	case submitCommand:
		a.handleSubmit(ctx, value)
	case directCommand:
		a.handleDirect(ctx, value)
	case appendRequest:
		event, err := a.appendNow(value.ctx, value.draft)
		if err == nil && value.draft.Type == eventledger.TypeTurnCompleted {
			turnID := draftTurnID(value.draft.Payload)
			if a.active != nil && (turnID == "" || a.active.ID == turnID) {
				a.active = nil
				a.startNext(ctx)
			}
		}
		value.reply <- appendResult{event: event, err: err}
	case snapshotRequest:
		events, _ := a.reader.ReadRange(ctx, 0, 0)
		value.reply <- events
	case cutoffRequest:
		events, err := a.reader.ReadRange(ctx, 0, 0)
		if err != nil {
			value.reply <- cutoffResult{err: err}
			break
		}
		seq, err := contextmgr.SafeCutoff(contextmgr.CutoffInput{
			Events: events, TargetSeq: value.target, KeepTailEvents: a.config.KeepTailEvents,
		})
		value.reply <- cutoffResult{seq: seq, err: err}
	case finishedRequest:
		if a.active != nil && a.active.ID == value.turnID {
			a.active = nil
			a.startNext(ctx)
		}
	case cancelCommand:
		value.reply <- a.cancelActive()
	case compressionCommand:
		a.handleCompression(ctx, value)
	case compressionFinished:
		a.compressing = false
		value.reply <- compressionReply{result: value.result}
	case stopCommand:
		a.closed = true
		return false
	case statusCommand:
		activeCalls := make(map[string]bool, len(a.activeCalls))
		for callID, active := range a.activeCalls {
			activeCalls[callID] = active
		}
		value.reply <- Status{
			LastSeq: a.lastSeq, Active: a.active != nil,
			Compressing: a.compressing, ActiveCalls: activeCalls,
		}
	default:
		return false
	}
	return true
}

func (a *Actor) appendNow(ctx context.Context, draft eventledger.Draft) (eventledger.Event, error) {
	event, err := a.appender.Append(ctx, draft)
	if err == nil {
		a.lastSeq = event.Seq
		if draft.CallID != "" {
			if draft.Type == eventledger.TypeToolRequested {
				a.activeCalls[draft.CallID] = true
			}
			if draft.Type == eventledger.TypeToolCompleted || draft.Type == eventledger.TypeToolFailed {
				delete(a.activeCalls, draft.CallID)
			}
		}
	}
	return event, err
}

func (a *Actor) append(ctx context.Context, draft eventledger.Draft) (eventledger.Event, error) {
	reply := make(chan appendResult, 1)
	select {
	case a.commands <- appendRequest{ctx: ctx, draft: draft, reply: reply}:
	case <-ctx.Done():
		return eventledger.Event{}, ctx.Err()
	case <-a.done:
		return eventledger.Event{}, errors.New("session actor is closed")
	}
	select {
	case result := <-reply:
		return result.event, result.err
	case <-ctx.Done():
		return eventledger.Event{}, ctx.Err()
	case <-a.done:
		return eventledger.Event{}, errors.New("session actor is closed")
	}
}

func (a *Actor) postCommand(command any) {
	select {
	case <-a.done:
		return
	default:
	}
	select {
	case a.commands <- command:
	case <-a.done:
	}
}

func recoverQueue(jobs *[]sessionJob, events []eventledger.Event) {
	type accepted struct {
		text string
		seq  int64
	}
	values := make(map[string]accepted)
	consumed := make(map[string]bool)
	for _, event := range events {
		switch event.Type {
		case eventledger.TypeUserMessageAccepted:
			var payload struct {
				MessageID string `json:"message_id"`
				Text      string `json:"text"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err == nil {
				values[payload.MessageID] = accepted{text: payload.Text, seq: event.Seq}
			}
		case eventledger.TypeUserMessageConsumed:
			var payload struct {
				MessageID string `json:"message_id"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err == nil {
				consumed[payload.MessageID] = true
			}
		}
	}
	for id, value := range values {
		if consumed[id] {
			continue
		}
		message := pendingMessage{ID: id, Text: value.text, AcceptedSeq: value.seq, Policy: QueuePolicyQueue}
		*jobs = append(*jobs, sessionJob{seq: value.seq, message: &message})
	}
	sort.Slice(*jobs, func(i, j int) bool { return (*jobs)[i].seq < (*jobs)[j].seq })
}

func randomID(prefix string) string {
	value := make([]byte, 12)
	if _, err := rand.Read(value); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + base64.RawURLEncoding.EncodeToString(value)
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func draftTurnID(payload any) string {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	var value struct {
		TurnID string `json:"turn_id"`
	}
	if json.Unmarshal(encoded, &value) != nil {
		return ""
	}
	return value.TurnID
}

func (a *Actor) stopActive() {
	if a.active != nil && a.active.Cancel != nil {
		a.active.Cancel()
	}
}

func (a *Actor) cancelActive() bool {
	if a.active == nil || a.active.Cancel == nil {
		return false
	}
	a.active.Cancel()
	return true
}
