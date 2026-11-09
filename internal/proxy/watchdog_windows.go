//go:build windows

package proxy

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"strings"
	"syscall"
)

// The watchdog is this executable started with --watchdog. It holds the proxy
// snapshot and CA name and restores both if the main process dies. A normal
// stop sends "done" on stdin, so the watchdog exits untouched.

type watchdogState struct {
	EnableSet   bool   `json:"e"`
	Enable      uint32 `json:"ev"`
	ServerSet   bool   `json:"s"`
	Server      string `json:"sv"`
	OverrideSet bool   `json:"o"`
	Override    string `json:"ov"`
	PacSet      bool   `json:"p"`
	Pac         string `json:"pv"`
}

func RunWatchdog(encoded string) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	var st watchdogState
	if json.Unmarshal(raw, &st) != nil {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	normal := false
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "done" {
			normal = true
			break
		}
	}
	if normal {
		return
	}
	snap := &proxySnapshot{
		enableSet: st.EnableSet, enable: st.Enable,
		serverSet: st.ServerSet, server: st.Server,
		overrideSet: st.OverrideSet, override: st.Override,
		pacSet: st.PacSet, pac: st.Pac,
	}
	snap.restore()
	_ = removeCA()
}

func spawnWatchdog(snap *proxySnapshot) (io.WriteCloser, error) {
	st := watchdogState{
		EnableSet: snap.enableSet, Enable: snap.enable,
		ServerSet: snap.serverSet, Server: snap.server,
		OverrideSet: snap.overrideSet, Override: snap.override,
		PacSet: snap.pacSet, Pac: snap.pac,
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "--watchdog", base64.RawURLEncoding.EncodeToString(raw))
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.Stdout = nil
	cmd.Stderr = nil
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = cmd.Wait() }()
	return in, nil
}
