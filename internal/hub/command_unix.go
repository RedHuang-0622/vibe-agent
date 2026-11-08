//go:build !windows

package hub

import "os/exec"

func configureCommand(*exec.Cmd) {}
