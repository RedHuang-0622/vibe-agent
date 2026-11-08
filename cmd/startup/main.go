package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"vibe-agent/internal/bootstrap"
	"vibe-agent/internal/platform"
	sessionv1 "vibe-agent/pkg/contracts/session/v1"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: startup run|install|status|uninstall")
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = bootstrap.EnsureHub(ctx)
	case "install":
		executable, executableErr := os.Executable()
		if executableErr != nil {
			err = executableErr
			break
		}
		err = platform.InstallAutostart(executable)
	case "uninstall":
		err = platform.UninstallAutostart()
	case "status":
		path, installed, statusErr := platform.AutostartStatus()
		if statusErr != nil {
			err = statusErr
			break
		}
		fmt.Printf("autostart=%v path=%s\n", installed, path)
		hub, _, hubErr := bootstrap.HubClient()
		if hubErr != nil {
			fmt.Println("hub=unavailable")
			break
		}
		var status struct {
			Endpoint string `json:"endpoint"`
			Version  string `json:"version"`
		}
		err = hub.Invoke(ctx, sessionv1.MethodHubStatus, struct{}{}, &status)
		if err == nil {
			fmt.Printf("hub=ready endpoint=%s version=%s\n", status.Endpoint, status.Version)
		}
	case "--version", "version":
		fmt.Println("vibe-agent startup 0.1.0")
	default:
		fmt.Fprintf(os.Stderr, "unknown startup command %q\n", os.Args[1])
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "startup:", err)
		os.Exit(1)
	}
}
