package tooling

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Runtime struct {
	registry   *Registry
	authorizer Authorizer
}

func NewRuntime(registry *Registry, authorizer Authorizer) *Runtime {
	if authorizer == nil {
		authorizer = AllowAllAuthorizer{}
	}
	return &Runtime{registry: registry, authorizer: authorizer}
}

func (r *Runtime) Registry() *Registry {
	return r.registry
}

func (r *Runtime) Execute(ctx context.Context, call Call) Result {
	started := time.Now()
	result := Result{
		CallID: call.CallID,
		Name:   call.ToolName,
		Mode:   call.InvocationMode,
	}
	tool, err := r.registry.Resolve(call.ToolName, call.Version)
	if err != nil {
		result.Error = asToolError(err)
		result.Timing = timing(call.QueuedAt, started, started, started, started)
		return result
	}
	result.Kind = tool.Spec.Kind
	args, err := r.arguments(call, tool)
	if err != nil {
		result.Error = asToolError(err)
		finished := time.Now()
		result.Timing = timing(call.QueuedAt, started, finished, finished, finished)
		return result
	}
	validated := time.Now()
	if err := validateRequired(tool.Spec, args); err != nil {
		result.Error = asToolError(err)
		result.Timing = timing(call.QueuedAt, started, validated, validated, validated)
		return result
	}
	if err := r.authorizer.Authorize(ctx, call, tool.Spec); err != nil {
		result.Error = asToolError(err)
		finished := time.Now()
		result.Timing = timing(call.QueuedAt, started, finished, finished, finished)
		return result
	}
	call.Arguments, _ = json.Marshal(args)
	executeStarted := time.Now()
	execCtx, cancel := context.WithTimeout(ctx, timeoutDuration(tool.Spec.TimeoutMS))
	defer cancel()
	payload, invokeErr := invoke(execCtx, tool.Handler, call)
	finished := time.Now()
	result.Timing = timing(call.QueuedAt, started, validated, executeStarted, finished)
	if invokeErr != nil {
		result.Error = mapExecutionError(execCtx, invokeErr)
		return result
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		result.Error = NewError(CodeInternal, "tool result cannot be encoded", false)
		return result
	}
	result.Payload = encoded
	return result
}

func (r *Runtime) arguments(call Call, tool RegisteredTool) (map[string]any, error) {
	raw := call.Arguments
	if len(raw) == 0 && call.RawRequest != "" {
		if json.Valid([]byte(call.RawRequest)) {
			raw = json.RawMessage(call.RawRequest)
		} else if tool.Direct != nil {
			adapted, err := tool.Direct.Adapt(call.RawRequest)
			if err != nil {
				return nil, asToolError(err)
			}
			raw = adapted
		}
	}
	if len(raw) == 0 {
		return nil, NewError(CodeInvalidRequest, "tool arguments are required", false)
	}
	return decodeArguments(raw)
}

func invoke(ctx context.Context, handler Handler, call Call) (payload any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			payload = nil
			err = NewError(CodeInternal, "tool handler panicked", false)
		}
	}()
	return handler.Invoke(ctx, call)
}

func timing(queuedAt, validationStart, validationEnd, executeStart, end time.Time) (out toolTiming) {
	if queuedAt.IsZero() {
		queuedAt = validationStart
	}
	out.QueueMS = max(0, validationStart.Sub(queuedAt).Milliseconds())
	out.ValidationMS = max(0, validationEnd.Sub(validationStart).Milliseconds())
	out.ExecuteMS = max(0, end.Sub(executeStart).Milliseconds())
	out.TotalMS = max(0, end.Sub(queuedAt).Milliseconds())
	return out
}

// toolTiming is structurally identical to toolv1.Timing but avoids importing
// contract code into the timing helper.
type toolTiming = struct {
	QueueMS      int64 `json:"queue_ms"`
	ValidationMS int64 `json:"validation_ms"`
	ExecuteMS    int64 `json:"execute_ms"`
	TotalMS      int64 `json:"total_ms"`
}

func mapExecutionError(ctx context.Context, err error) *Error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return NewError(CodeTimeout, "tool execution timed out", true)
	}
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return NewError(CodeCanceled, "tool execution canceled", false)
	}
	return asToolError(err)
}

func asToolError(err error) *Error {
	var toolErr *Error
	if errors.As(err, &toolErr) {
		return toolErr
	}
	return &Error{
		Code:    CodeInternal,
		Message: fmt.Sprintf("tool execution failed: %v", err),
	}
}
