package bootstrap

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func EnsureHub(ctx context.Context) error {
	paths, err := ResolvePaths()
	if err != nil {
		return err
	}
	if hubHealthy(ctx, paths.HubState) {
		return nil
	}
	hubPath, err := findHub()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.HubDir, 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(paths.HubDir, "hub.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	command := exec.Command(hubPath, "serve")
	command.Env = os.Environ()
	command.Stdout = logFile
	command.Stderr = logFile
	configureDetached(command)
	if err := command.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	_ = command.Process.Release()
	_ = logFile.Close()
	deadline := time.Now().Add(12 * time.Second)
	for time.Now().Before(deadline) {
		if hubHealthy(ctx, paths.HubState) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return errors.New("hub did not become ready")
}

func hubHealthy(ctx context.Context, statePath string) bool {
	state, err := LoadState(statePath)
	if err != nil {
		return false
	}
	checkCtx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	client := hubClientFromState(state)
	var status struct{}
	return client.Invoke(checkCtx, "hub.status", struct{}{}, &status) == nil
}
