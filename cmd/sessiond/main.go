package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"vibe-agent/internal/sessiond"
)

func main() {
	var config sessiond.Config
	flags := flag.NewFlagSet("sessiond", flag.ContinueOnError)
	flags.StringVar(&config.SessionID, "session-id", "", "session identity")
	flags.StringVar(&config.SessionDir, "session-dir", "", "session data directory")
	flags.Uint64Var(&config.Generation, "generation", 1, "process generation")
	flags.StringVar(&config.HubURL, "hub-url", "", "hub control endpoint")
	flags.IntVar(&config.MaxSteps, "max-steps", 0, "maximum ReAct steps")
	flags.IntVar(&config.MaxContextBytes, "max-context-bytes", 0, "automatic compression threshold")
	version := flags.Bool("version", false, "print version")
	if err := flags.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if *version {
		fmt.Println("vibe-agent sessiond 0.1.0")
		return
	}
	config.HubToken = os.Getenv("VIBE_HUB_TOKEN")
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := sessiond.Run(ctx, config); err != nil {
		fmt.Fprintln(os.Stderr, "sessiond:", err)
		os.Exit(1)
	}
}
