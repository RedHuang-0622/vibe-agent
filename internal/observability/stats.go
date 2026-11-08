package observability

import (
	"encoding/json"
	"math"
	"sort"

	"vibe-agent/internal/eventledger"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

type toolKey struct {
	name string
	kind string
	mode string
}

type callInfo struct {
	key         toolKey
	completed   bool
	failed      bool
	failureCode string
	totalMS     int64
	executeMS   int64
	hasTiming   bool
}

type accumulator struct {
	stat          sessionv1.ToolStat
	totalValues   []int64
	executeValues []int64
}

func BuildStats(events []eventledger.Event, active map[string]bool, filter sessionv1.StatsRequest) sessionv1.StatsResponse {
	response := sessionv1.StatsResponse{SessionID: filter.SessionID}
	if len(events) > 0 {
		response.Watermark = events[len(events)-1].Seq
	}
	calls := make(map[string]callInfo)
	for _, event := range events {
		switch event.Type {
		case eventledger.TypeToolRequested:
			var payload struct {
				ToolName       string `json:"tool_name"`
				ToolKind       string `json:"tool_kind"`
				InvocationMode string `json:"invocation_mode"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			calls[event.CallID] = callInfo{key: toolKey{
				name: payload.ToolName, kind: payload.ToolKind, mode: payload.InvocationMode,
			}}
		case eventledger.TypeToolCompleted, eventledger.TypeToolFailed:
			info, ok := calls[event.CallID]
			if !ok {
				info.key = callKeyFromPayload(event.Payload)
			}
			var payload struct {
				Timing struct {
					ExecuteMS *int64 `json:"execute_ms"`
					TotalMS   *int64 `json:"total_ms"`
				} `json:"timing"`
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(event.Payload, &payload)
			info.completed = event.Type == eventledger.TypeToolCompleted
			info.failed = event.Type == eventledger.TypeToolFailed
			info.failureCode = payload.Error.Code
			if payload.Timing.TotalMS != nil {
				info.totalMS = *payload.Timing.TotalMS
			}
			if payload.Timing.ExecuteMS != nil {
				info.executeMS = *payload.Timing.ExecuteMS
			}
			info.hasTiming = payload.Timing.TotalMS != nil || payload.Timing.ExecuteMS != nil
			calls[event.CallID] = info
		}
	}
	accumulators := make(map[toolKey]*accumulator)
	for callID, info := range calls {
		if filter.ToolName != "" && info.key.name != filter.ToolName {
			continue
		}
		if filter.Mode != "" && info.key.mode != filter.Mode {
			continue
		}
		acc := accumulators[info.key]
		if acc == nil {
			acc = &accumulator{stat: sessionv1.ToolStat{
				ToolName: info.key.name, ToolKind: info.key.kind,
				InvocationMode: info.key.mode, FailureReasons: make(map[string]int64),
			}}
			accumulators[info.key] = acc
		}
		acc.stat.Calls++
		if info.completed {
			acc.stat.Succeeded++
		}
		if info.failed {
			acc.stat.Failed++
			code := info.failureCode
			if code == "" {
				code = "unknown"
			}
			acc.stat.FailureReasons[code]++
		}
		if !info.completed && !info.failed {
			if active[callID] {
				acc.stat.InFlight++
			} else {
				acc.stat.Orphaned++
			}
		}
		if info.hasTiming {
			acc.totalValues = append(acc.totalValues, info.totalMS)
			acc.executeValues = append(acc.executeValues, info.executeMS)
		}
	}
	for _, acc := range accumulators {
		acc.stat.Total = summarize(acc.totalValues)
		acc.stat.Execute = summarize(acc.executeValues)
		if len(acc.stat.FailureReasons) == 0 {
			acc.stat.FailureReasons = nil
		}
		response.ByTool = append(response.ByTool, acc.stat)
	}
	sort.Slice(response.ByTool, func(i, j int) bool {
		left, right := response.ByTool[i], response.ByTool[j]
		if left.ToolName != right.ToolName {
			return left.ToolName < right.ToolName
		}
		if left.InvocationMode != right.InvocationMode {
			return left.InvocationMode < right.InvocationMode
		}
		return left.ToolKind < right.ToolKind
	})
	return response
}

func callKeyFromPayload(payload json.RawMessage) toolKey {
	var value struct {
		ToolName       string `json:"tool_name"`
		ToolKind       string `json:"tool_kind"`
		InvocationMode string `json:"invocation_mode"`
	}
	_ = json.Unmarshal(payload, &value)
	return toolKey{name: value.ToolName, kind: value.ToolKind, mode: value.InvocationMode}
}

func summarize(values []int64) sessionv1.LatencySummary {
	if len(values) == 0 {
		return sessionv1.LatencySummary{}
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	var total int64
	for _, value := range sorted {
		total += value
	}
	return sessionv1.LatencySummary{
		Samples: int64(len(sorted)),
		AvgMS:   float64(total) / float64(len(sorted)),
		P50MS:   percentile(sorted, 0.50),
		P95MS:   percentile(sorted, 0.95),
	}
}

func percentile(sorted []int64, ratio float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	index := int(math.Ceil(float64(len(sorted))*ratio)) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sorted) {
		index = len(sorted) - 1
	}
	return float64(sorted[index])
}
