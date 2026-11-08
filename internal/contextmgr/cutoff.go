package contextmgr

import (
	"encoding/json"
	"errors"

	"vibe-agent/internal/eventledger"
)

var ErrNoSafeCutoff = errors.New("no safe compression cutoff")

type CutoffInput struct {
	Events         []eventledger.Event
	TargetSeq      int64
	KeepTailEvents int
}

func SafeCutoff(input CutoffInput) (int64, error) {
	if len(input.Events) == 0 {
		return 0, ErrNoSafeCutoff
	}
	latest := input.Events[len(input.Events)-1].Seq
	upper := latest
	if input.TargetSeq > 0 && input.TargetSeq < upper {
		upper = input.TargetSeq
	}
	if input.KeepTailEvents > 0 {
		keepBoundary := latest - int64(input.KeepTailEvents)
		if keepBoundary < upper {
			upper = keepBoundary
		}
	}
	earliestUnconsumed := earliestUnconsumedAccepted(input.Events)
	if earliestUnconsumed > 0 && earliestUnconsumed-1 < upper {
		upper = earliestUnconsumed - 1
	}
	earliestPendingTool := earliestPendingToolRequest(input.Events)
	if earliestPendingTool > 0 && earliestPendingTool-1 < upper {
		upper = earliestPendingTool - 1
	}
	for index := len(input.Events) - 1; index >= 0; index-- {
		event := input.Events[index]
		if event.Seq > upper {
			continue
		}
		if safeBoundary(event.Type) {
			return event.Seq, nil
		}
	}
	return 0, ErrNoSafeCutoff
}

func earliestUnconsumedAccepted(events []eventledger.Event) int64 {
	accepted := make(map[string]int64)
	consumed := make(map[string]bool)
	for _, event := range events {
		switch event.Type {
		case eventledger.TypeUserMessageAccepted:
			accepted[payloadString(event.Payload, "message_id")] = event.Seq
		case eventledger.TypeUserMessageConsumed:
			consumed[payloadString(event.Payload, "message_id")] = true
		}
	}
	var earliest int64
	for messageID, seq := range accepted {
		if !consumed[messageID] && (earliest == 0 || seq < earliest) {
			earliest = seq
		}
	}
	return earliest
}

func earliestPendingToolRequest(events []eventledger.Event) int64 {
	type callState struct {
		seq       int64
		completed bool
	}
	calls := make(map[string]callState)
	for _, event := range events {
		switch event.Type {
		case eventledger.TypeToolRequested:
			calls[event.CallID] = callState{seq: event.Seq}
		case eventledger.TypeToolCompleted, eventledger.TypeToolFailed:
			state := calls[event.CallID]
			state.completed = true
			calls[event.CallID] = state
		}
	}
	var earliest int64
	for _, state := range calls {
		if !state.completed && (earliest == 0 || state.seq < earliest) {
			earliest = state.seq
		}
	}
	return earliest
}

func safeBoundary(eventType eventledger.EventType) bool {
	switch eventType {
	case eventledger.TypeTurnCompleted, eventledger.TypeToolCompleted,
		eventledger.TypeToolFailed:
		return true
	default:
		return false
	}
}

func payloadString(payload json.RawMessage, key string) string {
	var value map[string]any
	if err := json.Unmarshal(payload, &value); err != nil {
		return ""
	}
	text, _ := value[key].(string)
	return text
}
