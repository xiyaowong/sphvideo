//go:build !windows

package protocol

import "os/exec"

func hide_process_window(*exec.Cmd) {}
