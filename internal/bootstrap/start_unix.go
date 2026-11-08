//go:build !windows

package bootstrap

import "os/exec"

func configureDetached(*exec.Cmd) {}
