//go:build windows

package protocol

import (
	"os/exec"
	"syscall"
)

// hide_process_window keeps GUI-initiated FFmpeg runs from opening a console.
func hide_process_window(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
}
