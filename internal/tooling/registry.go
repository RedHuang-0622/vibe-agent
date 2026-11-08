package tooling

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

var toolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,63}$`)
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

type Registry struct {
	mu    sync.RWMutex
	tools map[string]RegisteredTool
}

func NewRegistry() *Registry {
	return &Registry{tools: make(map[string]RegisteredTool)}
}

func (r *Registry) Register(spec toolv1.ToolSpec, handler Handler, direct DirectAdapter) error {
	if !toolNamePattern.MatchString(spec.Name) {
		return NewError(CodeInvalidRequest, "tool name is invalid", false)
	}
	if !versionPattern.MatchString(spec.Version) {
		return NewError(CodeInvalidRequest, "tool version is invalid", false)
	}
	if handler == nil {
		return NewError(CodeInvalidRequest, "tool handler is required", false)
	}
	if spec.TimeoutMS <= 0 {
		return NewError(CodeInvalidRequest, "tool timeout must be positive", false)
	}
	if len(spec.Parameters) == 0 || !json.Valid(spec.Parameters) {
		return NewError(CodeInvalidRequest, "tool parameters must be valid JSON schema", false)
	}
	if !spec.ReadOnly && !spec.Idempotent {
		return NewError(CodeInvalidRequest, "mutating tools must support idempotency", false)
	}
	key := spec.Name + "@" + spec.Version
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[key]; exists {
		return NewError(CodeConflict, "tool name and version are already registered", false)
	}
	r.tools[key] = RegisteredTool{Spec: spec, Handler: handler, Direct: direct}
	return nil
}

func (r *Registry) Resolve(name, version string) (RegisteredTool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if version != "" {
		tool, ok := r.tools[name+"@"+version]
		if !ok {
			return RegisteredTool{}, NewError(CodeUnknownTool, "tool is not registered", false)
		}
		return tool, nil
	}
	var found []RegisteredTool
	for _, tool := range r.tools {
		if tool.Spec.Name == name {
			found = append(found, tool)
		}
	}
	if len(found) == 0 {
		return RegisteredTool{}, NewError(CodeUnknownTool, "tool is not registered", false)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Spec.Version > found[j].Spec.Version })
	return found[0], nil
}

func (r *Registry) List() []toolv1.ToolSpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	specs := make([]toolv1.ToolSpec, 0, len(r.tools))
	for _, tool := range r.tools {
		specs = append(specs, tool.Spec)
	}
	sort.Slice(specs, func(i, j int) bool {
		if specs[i].Name == specs[j].Name {
			return specs[i].Version < specs[j].Version
		}
		return specs[i].Name < specs[j].Name
	})
	return specs
}

func (r *Registry) catalog() []toolv1.ToolSpec {
	return r.List()
}

func decodeArguments(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, NewError(CodeInvalidRequest, "tool arguments must be a JSON object", false)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, NewError(CodeInvalidRequest, "tool arguments must contain one JSON object", false)
	}
	if object == nil {
		return nil, NewError(CodeInvalidRequest, "tool arguments must be a JSON object", false)
	}
	return object, nil
}

func normalizeVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "v1"
	}
	return version
}

func timeoutDuration(ms int64) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

func validateRequired(spec toolv1.ToolSpec, args map[string]any) error {
	var schema struct {
		Required   []string `json:"required"`
		Properties map[string]struct {
			Type string `json:"type"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(spec.Parameters, &schema); err != nil {
		return fmt.Errorf("decode schema: %w", err)
	}
	for _, field := range schema.Required {
		if _, ok := args[field]; !ok {
			return NewError(CodeInvalidRequest, fmt.Sprintf("missing required field %s", field), false)
		}
	}
	for field, value := range args {
		property, ok := schema.Properties[field]
		if !ok || property.Type == "" {
			continue
		}
		if !matchesJSONType(value, property.Type) {
			return NewError(CodeInvalidRequest, fmt.Sprintf("field %s must be %s", field, property.Type), false)
		}
	}
	return nil
}

func matchesJSONType(value any, want string) bool {
	switch want {
	case "string":
		_, ok := value.(string)
		return ok
	case "number", "integer":
		number, ok := value.(json.Number)
		if !ok {
			return false
		}
		if want == "integer" {
			_, err := number.Int64()
			return err == nil
		}
		_, err := number.Float64()
		return err == nil
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	default:
		return true
	}
}
