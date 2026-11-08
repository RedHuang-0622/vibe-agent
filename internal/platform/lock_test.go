package platform

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFileLockExcludesSecondOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.lock")
	first, err := AcquireFileLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireFileLock(context.Background(), path); err == nil {
		t.Fatal("second lock acquisition must fail")
	}
	if err := first.Release(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireFileLock(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	_ = second.Release()
}
