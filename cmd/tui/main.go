package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"vibe-agent/internal/bootstrap"
	"vibe-agent/internal/operator"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := bootstrap.EnsureHub(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
	hub, _, err := bootstrap.HubClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
	if err := operator.RunTUI(ctx, operator.NewClient(hub), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "tui:", err)
		os.Exit(1)
	}
}
