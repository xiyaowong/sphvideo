//go:build !windows

package wxchannelsadapter

import "os/exec"

func hide_process_window(*exec.Cmd) {}
