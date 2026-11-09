//go:build !windows

package proxy

import "os/exec"

func hideWindow(*exec.Cmd) {}
