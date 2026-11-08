package observability

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"

	"vibe-agent/internal/eventledger"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func Trace(events []eventledger.Event, request sessionv1.TraceRequest) sessionv1.TraceResponse {
	response := sessionv1.TraceResponse{SessionID: request.SessionID}
	if len(events) > 0 {
		response.Watermark = events[len(events)-1].Seq
	}
	for _, event := range events {
		if request.TraceID != "" && event.TraceID != request.TraceID {
			continue
		}
		if request.CallID != "" && event.CallID != request.CallID {
			continue
		}
		response.Nodes = append(response.Nodes, sessionv1.TraceNode{
			Seq: event.Seq, EventID: event.EventID, Type: string(event.Type),
			TraceID: event.TraceID, CausationSeq: event.CausationSeq,
			CallID: event.CallID, Status: event.Status, Payload: event.Payload,
		})
	}
	sort.Slice(response.Nodes, func(i, j int) bool { return response.Nodes[i].Seq < response.Nodes[j].Seq })
	return response
}

type ProjectionPaths struct {
	Trace string
	Stats string
}

func DefaultProjectionPaths(root string) ProjectionPaths {
	return ProjectionPaths{
		Trace: filepath.Join(root, "trace.jsonl"),
		Stats: filepath.Join(root, "stats.json"),
	}
}

func WriteProjections(_ context.Context, paths ProjectionPaths, events []eventledger.Event, active map[string]bool) error {
	if err := os.MkdirAll(filepath.Dir(paths.Trace), 0o700); err != nil {
		return err
	}
	traceTemp, err := os.CreateTemp(filepath.Dir(paths.Trace), ".trace-*.tmp")
	if err != nil {
		return err
	}
	tracePath := traceTemp.Name()
	defer os.Remove(tracePath)
	encoder := json.NewEncoder(traceTemp)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			_ = traceTemp.Close()
			return err
		}
	}
	if err := traceTemp.Sync(); err != nil {
		_ = traceTemp.Close()
		return err
	}
	if err := traceTemp.Close(); err != nil {
		return err
	}
	stats := BuildStats(events, active, sessionv1.StatsRequest{})
	stats.SessionID = ""
	statsBytes, err := json.Marshal(stats)
	if err != nil {
		return err
	}
	statsTemp, err := os.CreateTemp(filepath.Dir(paths.Stats), ".stats-*.tmp")
	if err != nil {
		return err
	}
	statsPath := statsTemp.Name()
	defer os.Remove(statsPath)
	if _, err := statsTemp.Write(statsBytes); err != nil {
		_ = statsTemp.Close()
		return err
	}
	if err := statsTemp.Sync(); err != nil {
		_ = statsTemp.Close()
		return err
	}
	if err := statsTemp.Close(); err != nil {
		return err
	}
	if err := os.Rename(statsPath, paths.Stats); err != nil {
		return err
	}
	return os.Rename(tracePath, paths.Trace)
}

func LoadStats(path string) (sessionv1.StatsResponse, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionv1.StatsResponse{}, nil
		}
		return sessionv1.StatsResponse{}, err
	}
	var response sessionv1.StatsResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return sessionv1.StatsResponse{}, err
	}
	return response, nil
}
