package proxy

import (
	"fmt"
	"io"
	"net"
)

const (
	defaultPort = 20230
	portTries   = 10
)

// Guard owns the system proxy snapshot and the temporary CA for one run.
// Prepare installs both; Close restores them.
type Guard struct {
	snap    *proxySnapshot
	wd      io.WriteCloser
	certPEM []byte
	keyPEM  []byte
}

// PickProxyPort finds a free local port for the interception proxy.
func PickProxyPort() (int, error) {
	for port := defaultPort; port < defaultPort+portTries; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return port, nil
		}
	}
	return 0, fmt.Errorf("本地端口被占用，请重启电脑后再试")
}

// EnsurePortFree reports whether a local port can be bound.
func EnsurePortFree(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("本地端口 %d 被占用，请关闭占用该端口的程序后重试", port)
	}
	return ln.Close()
}

// Prepare snapshots and replaces the system proxy, spawns the cleanup
// watchdog, and installs a temporary root certificate.
func Prepare(proxyPort int) (*Guard, error) {
	clearStaleProxyRange()
	snap, err := snapshotProxy()
	if err != nil {
		return nil, fmt.Errorf("读取系统代理失败: %w", err)
	}
	wd, err := spawnWatchdog(snap)
	if err != nil {
		return nil, fmt.Errorf("启动清理守护进程失败: %w", err)
	}
	// Closing the watchdog without "done" leaves it to restore the proxy and
	// remove the CA if startup fails.
	abort := func() {
		if wd != nil {
			_ = wd.Close()
		}
	}

	certPEM, keyPEM, der, err := generateCA()
	if err != nil {
		abort()
		return nil, fmt.Errorf("生成临时证书失败: %w", err)
	}
	_ = removeCA()
	if err := installCA(der); err != nil {
		_ = removeCA()
		abort()
		return nil, fmt.Errorf("安装临时证书失败: %w", err)
	}
	if err := setSystemProxy(proxyPort); err != nil {
		_ = removeCA()
		snap.restore()
		abort()
		return nil, fmt.Errorf("设置系统代理失败: %w", err)
	}
	return &Guard{snap: snap, wd: wd, certPEM: certPEM, keyPEM: keyPEM}, nil
}

func (g *Guard) CertPEM() []byte { return g.certPEM }

func (g *Guard) KeyPEM() []byte { return g.keyPEM }

// Close restores the proxy, removes the CA, and releases the watchdog.
func (g *Guard) Close() error {
	if g == nil {
		return nil
	}
	if g.snap != nil {
		g.snap.restore()
		g.snap = nil
	}
	caErr := removeCA()
	if g.wd != nil {
		if caErr == nil {
			_, _ = io.WriteString(g.wd, "done\n")
		}
		_ = g.wd.Close()
		g.wd = nil
	}
	return caErr
}

func clearStaleProxyRange() {
	for port := defaultPort; port < defaultPort+portTries; port++ {
		clearStaleProxy(port)
	}
}

// Repair restores the proxy and removes the temporary CA after a crash, or
// when the user's network is broken after closing the app.
func Repair() error {
	clearStaleProxyRange()
	return removeCA()
}
