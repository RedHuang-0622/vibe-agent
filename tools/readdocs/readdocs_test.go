package readdocs

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"vibe-agent/internal/tooling"
)

func TestReadDocsLineRangeAndTraversal(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "doc.txt"), []byte("one\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tool, err := New(map[string]string{"workspace": root})
	if err != nil {
		t.Fatal(err)
	}
	args, _ := json.Marshal(map[string]any{"path": "doc.txt", "start_line": 2, "end_line": 3})
	payload, err := tool.Invoke(context.Background(), tooling.Call{Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	content := payload.(map[string]any)["content"].(string)
	if content != "two\nthree" {
		t.Fatalf("content=%q", content)
	}
	badArgs, _ := json.Marshal(map[string]any{"path": "../secret.txt"})
	if _, err := tool.Invoke(context.Background(), tooling.Call{Arguments: badArgs}); err == nil {
		t.Fatal("traversal must be denied")
	}
}
