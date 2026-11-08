package calculator

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

func TestCalculatorSuccessAndErrors(t *testing.T) {
	tests := []struct {
		expression string
		want       float64
		wantCode   string
	}{
		{expression: "1+2*3", want: 7},
		{expression: "(12 + 4) * 3 / 2", want: 24},
		{expression: "1/0", wantCode: tooling.CodeInvalidRequest},
		{expression: "2 + nope", wantCode: tooling.CodeInvalidRequest},
	}
	for _, tt := range tests {
		got, err := Evaluate(tt.expression)
		if tt.wantCode != "" {
			if err == nil {
				t.Fatalf("%q: expected error", tt.expression)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Fatalf("%q: got=%v err=%v", tt.expression, got, err)
		}
	}
}

func TestRuntimeDirectAdapter(t *testing.T) {
	registry := tooling.NewRegistry()
	if err := registry.Register(Spec(), New(), Adapter{}); err != nil {
		t.Fatal(err)
	}
	runtime := tooling.NewRuntime(registry, nil)
	result := runtime.Execute(context.Background(), tooling.Call{
		CallID:         "c1",
		ToolName:       "calculator",
		InvocationMode: toolv1.InvocationModeDirect,
		RawRequest:     "1+2*3",
		QueuedAt:       time.Now(),
	})
	if result.Error != nil {
		t.Fatal(result.Error)
	}
	var payload map[string]any
	if err := json.Unmarshal(result.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["value"].(float64) != 7 {
		t.Fatalf("payload=%v", payload)
	}
}

func FuzzEvaluate(f *testing.F) {
	for _, seed := range []string{"1+2*3", "(1+2)/3", "", "1/0", "中文"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, expression string) {
		_, _ = Evaluate(expression)
	})
}
