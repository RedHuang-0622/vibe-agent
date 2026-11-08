package conversation

import (
	"context"
	"encoding/json"
	"errors"

	"vibe-agent/internal/contextmgr"
	"vibe-agent/internal/eventledger"
)

func (a *Actor) handleCompression(ctx context.Context, command compressionCommand) {
	if a.compressing {
		command.reply <- compressionReply{err: errors.New("compression already in progress")}
		return
	}
	if a.active != nil && !command.allowActive {
		command.reply <- compressionReply{err: errors.New("cannot compress while a turn is active")}
		return
	}
	events, err := a.reader.ReadRange(ctx, 0, 0)
	if err != nil {
		command.reply <- compressionReply{err: err}
		return
	}
	keepTail := command.keepTail
	if keepTail <= 0 {
		keepTail = a.config.KeepTailEvents
	}
	redLine, err := contextmgr.SafeCutoff(contextmgr.CutoffInput{
		Events: events, TargetSeq: command.target, KeepTailEvents: keepTail,
	})
	if err != nil {
		command.reply <- compressionReply{result: CompressionResult{
			Committed: false, ErrorCode: "no_safe_cutoff",
		}, err: err}
		return
	}
	trigger := command.trigger
	if trigger == "" {
		trigger = "external"
	}
	previousCheckpoint := latestCheckpointSeq(events)
	requested, err := a.appendNow(ctx, eventledger.Draft{
		Type: eventledger.TypeCompressionRequested, Visibility: eventledger.VisibilityInternal,
		Payload: map[string]any{
			"trigger": trigger, "red_line_seq": redLine,
			"source_from_seq": 1, "source_to_seq": redLine,
			"previous_checkpoint_seq": previousCheckpoint,
			"policy_version":          "v1",
		},
	})
	if err != nil {
		command.reply <- compressionReply{err: err}
		return
	}
	a.compressing = true
	source := selectSource(events, redLine)
	digestValue := contextmgr.SourceDigest(events, 1, redLine)
	go a.runCompression(ctx, command.reply, requested, redLine, previousCheckpoint, source, digestValue)
}

func (a *Actor) runCompression(ctx context.Context, reply chan compressionReply, requested eventledger.Event, redLine, previousCheckpoint int64, source []eventledger.Event, sourceDigest string) {
	summary := ""
	var err error
	if a.summarizer != nil {
		summary, err = a.summarizer.Summarize(ctx, source)
	} else {
		err = errors.New("summarizer is unavailable")
	}
	result := CompressionResult{Committed: err == nil, RedLineSeq: redLine}
	terminal, cancel := cleanupContext()
	defer cancel()
	sourceChanged := false
	if err == nil {
		current, readErr := a.reader.ReadRange(terminal, 0, 0)
		if readErr != nil {
			err = readErr
		} else if contextmgr.SourceDigest(current, 1, redLine) != sourceDigest {
			err = errors.New("compression source changed")
			sourceChanged = true
		}
	}
	if err != nil {
		result.Committed = false
		result.ErrorCode = "compression_failed"
		if sourceChanged {
			result.ErrorCode = "source_digest_mismatch"
		}
		_, appendErr := a.append(terminal, eventledger.Draft{
			Type: eventledger.TypeCompressionFailed, Visibility: eventledger.VisibilityInternal,
			CausationSeq: requested.Seq, Status: "failed",
			Payload: map[string]any{"red_line_seq": redLine, "reason_code": result.ErrorCode},
		})
		if appendErr != nil {
			result.ErrorCode = "ledger_io_error"
		}
	} else {
		_, appendErr := a.append(terminal, eventledger.Draft{
			Type: eventledger.TypeCompressionCompleted, Visibility: eventledger.VisibilityInternal,
			CausationSeq: requested.Seq, Status: "succeeded",
			Payload: map[string]any{
				"source_from_seq": 1, "source_to_seq": redLine, "red_line_seq": redLine,
				"summary": summary, "source_digest": sourceDigest,
				"tokens_before": len(source), "tokens_after": len([]rune(summary)),
				"previous_checkpoint_seq": previousCheckpoint,
			},
		})
		if appendErr != nil {
			result.Committed = false
			result.ErrorCode = "ledger_io_error"
		}
	}
	a.postCommand(compressionFinished{result: result, reply: reply})
}

func latestCheckpointSeq(events []eventledger.Event) int64 {
	for index := len(events) - 1; index >= 0; index-- {
		event := events[index]
		if event.Type != eventledger.TypeCompressionCompleted {
			continue
		}
		var payload struct {
			SourceToSeq int64  `json:"source_to_seq"`
			RedLineSeq  int64  `json:"red_line_seq"`
			Digest      string `json:"source_digest"`
		}
		if json.Unmarshal(event.Payload, &payload) == nil &&
			payload.SourceToSeq > 0 && payload.SourceToSeq < event.Seq &&
			payload.RedLineSeq == payload.SourceToSeq && payload.Digest != "" {
			return event.Seq
		}
	}
	return 0
}

func selectSource(events []eventledger.Event, redLine int64) []eventledger.Event {
	source := make([]eventledger.Event, 0, len(events))
	for _, event := range events {
		if event.Seq <= redLine {
			source = append(source, event)
		}
	}
	return source
}
