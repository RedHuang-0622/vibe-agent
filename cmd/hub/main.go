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

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 && (args[0] == "--version" || args[0] == "version") {
		fmt.Printf("vibe-agent hub %s\n", version)
		return 0
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(args) > 0 && (args[0] == "serve" || args[0] == "daemon") {
		if err := bootstrap.RunHub(ctx); err != nil {
			fmt.Fprintln(os.Stderr, "hub:", err)
			return 1
		}
		return 0
	}
	client, _, err := bootstrap.HubClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "hub:", err)
		return 1
	}
	if err := operator.RunHubCLI(ctx, operator.NewClient(client), args, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "hub:", err)
		return 1
	}
	return 0
}
