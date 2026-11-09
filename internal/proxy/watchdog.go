package proxy

import (
	"bufio"
	"encoding/base64"
	"io"
	"os"
	"os/exec"
	"strings"
)

// The watchdog is this executable started with --watchdog. It holds the proxy
// snapshot and restores it, along with the CA, if the main process dies. A
// normal stop sends "done" on stdin, so the watchdog exits untouched.

func RunWatchdog(encoded string) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return
	}
	snap, err := decodeProxySnapshot(raw)
	if err != nil {
		return
	}
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "done" {
			return
		}
	}
	snap.restore()
	_ = removeCA()
}

func spawnWatchdog(snap *proxySnapshot) (io.WriteCloser, error) {
	raw, err := snap.encode()
	if err != nil {
		return nil, err
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(exe, "--watchdog", base64.RawURLEncoding.EncodeToString(raw))
	hideWindow(cmd)
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
