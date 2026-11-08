package readdocs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"vibe-agent/internal/tooling"
	toolv1 "vibe-agent/pkg/contracts/tool/v1"
)

const maxDocumentBytes = 4 << 20

type Tool struct {
	roots map[string]string
}

func New(roots map[string]string) (*Tool, error) {
	if len(roots) == 0 {
		return nil, fmt.Errorf("at least one document root is required")
	}
	resolved := make(map[string]string, len(roots))
	for id, root := range roots {
		absolute, err := filepath.Abs(root)
		if err != nil {
			return nil, fmt.Errorf("resolve root %s: %w", id, err)
		}
		resolved[id] = filepath.Clean(absolute)
	}
	return &Tool{roots: resolved}, nil
}

func Spec() toolv1.ToolSpec {
	return toolv1.ToolSpec{
		Name:        "read_docs",
		Version:     "v1",
		Kind:        "local",
		Description: "Read a line range from an allowlisted documentation root",
		Parameters: json.RawMessage(`{
			"type":"object",
			"required":["path"],
			"properties":{
				"root":{"type":"string"},
				"path":{"type":"string"},
				"start_line":{"type":"integer"},
				"end_line":{"type":"integer"}
			}
		}`),
		DirectRequestField: "path",
		ReadOnly:           true,
		TimeoutMS:          3000,
	}
}

type callArgs struct {
	Root      string `json:"root"`
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (t *Tool) Invoke(_ context.Context, call tooling.Call) (any, error) {
	var args callArgs
	if err := json.Unmarshal(call.Arguments, &args); err != nil {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "invalid read_docs arguments", false)
	}
	root, err := t.resolveRoot(args.Root)
	if err != nil {
		return nil, err
	}
	path, err := safePath(root, args.Path)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, tooling.NewError(tooling.CodeNotFound, "document not found", false)
		}
		return nil, tooling.NewError(tooling.CodeDependency, "document cannot be read", true)
	}
	if len(data) > maxDocumentBytes {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "document exceeds size limit", false)
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	if args.StartLine <= 0 {
		args.StartLine = 1
	}
	if args.EndLine <= 0 || args.EndLine > len(lines) {
		args.EndLine = len(lines)
	}
	if args.StartLine > args.EndLine {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "start_line must not exceed end_line", false)
	}
	return map[string]any{
		"path":       filepath.ToSlash(args.Path),
		"start_line": args.StartLine,
		"end_line":   args.EndLine,
		"content":    strings.Join(lines[args.StartLine-1:args.EndLine], "\n"),
	}, nil
}

type Adapter struct{}

func (Adapter) Adapt(raw string) (json.RawMessage, error) {
	path := strings.TrimSpace(raw)
	if path == "" {
		return nil, tooling.NewError(tooling.CodeInvalidRequest, "document path is required", false)
	}
	return json.Marshal(map[string]string{"path": path})
}

func (t *Tool) resolveRoot(id string) (string, error) {
	if id == "" {
		if root, ok := t.roots["workspace"]; ok {
			return root, nil
		}
		for _, root := range t.roots {
			return root, nil
		}
	}
	root, ok := t.roots[id]
	if !ok {
		return "", tooling.NewError(tooling.CodePermissionDenied, "document root is not allowlisted", false)
	}
	return root, nil
}

func safePath(root, relative string) (string, error) {
	if relative == "" || filepath.IsAbs(relative) {
		return "", tooling.NewError(tooling.CodePermissionDenied, "document path must be relative", false)
	}
	candidate := filepath.Join(root, filepath.FromSlash(relative))
	rel, err := filepath.Rel(root, candidate)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", tooling.NewError(tooling.CodePermissionDenied, "document path escapes allowed root", false)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", tooling.NewError(tooling.CodeDependency, "document root is unavailable", true)
	}
	resolvedFile, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if os.IsNotExist(err) {
			return "", tooling.NewError(tooling.CodeNotFound, "document not found", false)
		}
		return "", tooling.NewError(tooling.CodeDependency, "document path cannot be resolved", true)
	}
	rel, err = filepath.Rel(resolvedRoot, resolvedFile)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", tooling.NewError(tooling.CodePermissionDenied, "document symlink escapes allowed root", false)
	}
	return resolvedFile, nil
}
