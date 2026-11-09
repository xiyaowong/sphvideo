//go:build linux

package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

type proxySnapshot struct {
	Backend      string `json:"b"`
	Mode         string `json:"m"`
	HTTPHost     string `json:"hh"`
	HTTPPort     string `json:"hp"`
	HTTPSHost    string `json:"sh"`
	HTTPSPort    string `json:"sp"`
	KDEProxyType string `json:"kt"`
	KDEHTTP      string `json:"kh"`
	KDEHTTPS     string `json:"ks"`
	KDEFTP       string `json:"kf"`
}

func commandExists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func kdeWrite() string {
	for _, name := range []string{"kwriteconfig6", "kwriteconfig5", "kwriteconfig"} {
		if commandExists(name) {
			return name
		}
	}
	return ""
}

func kdeRead() string {
	for _, name := range []string{"kreadconfig6", "kreadconfig5", "kreadconfig"} {
		if commandExists(name) {
			return name
		}
	}
	return ""
}

func detectBackend() string {
	desktop := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + ":" + os.Getenv("DESKTOP_SESSION"))
	kde := kdeWrite() != "" && kdeRead() != ""
	if strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma") {
		if kde {
			return "kde"
		}
	}
	if commandExists("gsettings") {
		return "gnome"
	}
	if kde {
		return "kde"
	}
	return ""
}

func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %v %s", name, err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func gsettingsGet(schema, key string) (string, error) {
	out, err := run("gsettings", "get", schema, key)
	if err != nil {
		return "", err
	}
	return strings.Trim(out, "'\""), nil
}

func gsettingsSet(schema, key, value string) error {
	_, err := run("gsettings", "set", schema, key, value)
	return err
}

func gsettingsSetString(schema, key, value string) error {
	value = strings.ReplaceAll(value, "'", `\'`)
	return gsettingsSet(schema, key, "'"+value+"'")
}

func kdeGet(key string) (string, error) {
	return run(kdeRead(), "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key)
}

func kdeSet(key, value string) error {
	_, err := run(kdeWrite(), "--file", "kioslaverc", "--group", "Proxy Settings", "--key", key, value)
	return err
}

func snapshotProxy() (*proxySnapshot, error) {
	backend := detectBackend()
	if backend == "" {
		return nil, fmt.Errorf("未找到 gsettings 或 KDE 代理配置工具")
	}
	s := &proxySnapshot{Backend: backend}
	switch backend {
	case "gnome":
		var err error
		if s.Mode, err = gsettingsGet("org.gnome.system.proxy", "mode"); err != nil {
			return nil, err
		}
		if s.HTTPHost, err = gsettingsGet("org.gnome.system.proxy.http", "host"); err != nil {
			return nil, err
		}
		if s.HTTPPort, err = gsettingsGet("org.gnome.system.proxy.http", "port"); err != nil {
			return nil, err
		}
		if s.HTTPSHost, err = gsettingsGet("org.gnome.system.proxy.https", "host"); err != nil {
			return nil, err
		}
		if s.HTTPSPort, err = gsettingsGet("org.gnome.system.proxy.https", "port"); err != nil {
			return nil, err
		}
	case "kde":
		var err error
		if s.KDEProxyType, err = kdeGet("ProxyType"); err != nil {
			return nil, err
		}
		if s.KDEHTTP, err = kdeGet("httpProxy"); err != nil {
			return nil, err
		}
		if s.KDEHTTPS, err = kdeGet("httpsProxy"); err != nil {
			return nil, err
		}
		if s.KDEFTP, err = kdeGet("ftpProxy"); err != nil {
			return nil, err
		}
	}
	return s, nil
}

func setSystemProxy(port int) error {
	s, err := snapshotProxy()
	if err != nil {
		return err
	}
	p := strconv.Itoa(port)
	switch s.Backend {
	case "gnome":
		steps := [][3]string{
			{"org.gnome.system.proxy", "mode", "manual"},
			{"org.gnome.system.proxy.http", "host", "127.0.0.1"},
			{"org.gnome.system.proxy.http", "port", p},
			{"org.gnome.system.proxy.https", "host", "127.0.0.1"},
			{"org.gnome.system.proxy.https", "port", p},
		}
		for _, step := range steps {
			var err error
			if step[1] == "port" {
				err = gsettingsSet(step[0], step[1], step[2])
			} else {
				err = gsettingsSetString(step[0], step[1], step[2])
			}
			if err != nil {
				return fmt.Errorf("设置系统代理失败: %w", err)
			}
		}
		notifyDeepin()
	case "kde":
		url := "http://127.0.0.1:" + p
		steps := [][2]string{
			{"ProxyType", "1"},
			{"httpProxy", url},
			{"httpsProxy", url},
			{"ftpProxy", url},
		}
		for _, step := range steps {
			if err := kdeSet(step[0], step[1]); err != nil {
				return fmt.Errorf("设置系统代理失败: %w", err)
			}
		}
		notifyKDE()
	}
	return nil
}

func (s *proxySnapshot) restore() {
	if s == nil {
		return
	}
	switch s.Backend {
	case "gnome":
		if s.Mode != "" {
			_ = gsettingsSetString("org.gnome.system.proxy", "mode", s.Mode)
		}
		_ = gsettingsSetString("org.gnome.system.proxy.http", "host", s.HTTPHost)
		_ = gsettingsSet("org.gnome.system.proxy.http", "port", portOrZero(s.HTTPPort))
		_ = gsettingsSetString("org.gnome.system.proxy.https", "host", s.HTTPSHost)
		_ = gsettingsSet("org.gnome.system.proxy.https", "port", portOrZero(s.HTTPSPort))
		notifyDeepin()
	case "kde":
		proxyType := s.KDEProxyType
		if proxyType == "" {
			proxyType = "0"
		}
		_ = kdeSet("ProxyType", proxyType)
		_ = kdeSet("httpProxy", s.KDEHTTP)
		_ = kdeSet("httpsProxy", s.KDEHTTPS)
		_ = kdeSet("ftpProxy", s.KDEFTP)
		notifyKDE()
	}
}

func portOrZero(v string) string {
	if v == "" {
		return "0"
	}
	return v
}

func notifyKDE() {
	for _, name := range []string{"qdbus6", "qdbus-qt6", "qdbus", "qdbus-qt5"} {
		if !commandExists(name) {
			continue
		}
		_ = exec.Command(name, "org.kde.KIO", "/KIO/Scheduler",
			"org.kde.KIO.Scheduler.reparseSlaveConfiguration", "").Run()
		return
	}
}

func notifyDeepin() {
	if !strings.Contains(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), "deepin") {
		return
	}
	_ = exec.Command("dbus-send", "--session", "--dest=com.deepin.daemon.Proxy",
		"--type=method_call", "/com/deepin/daemon/Proxy",
		"com.deepin.daemon.Proxy.Apply").Run()
}

func parseProxyURL(value string) (string, string) {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "http://")
	value = strings.TrimPrefix(value, "https://")
	value = strings.TrimRight(value, "/")
	host, port, ok := strings.Cut(value, ":")
	if !ok {
		return "", ""
	}
	if _, err := strconv.Atoi(port); err != nil {
		return "", ""
	}
	return host, port
}

func (s *proxySnapshot) upstreamURL() string {
	if s == nil {
		return ""
	}
	host, port := "", ""
	switch s.Backend {
	case "gnome":
		if s.Mode == "manual" {
			host, port = s.HTTPHost, s.HTTPPort
			if host == "" || port == "" || port == "0" {
				host, port = s.HTTPSHost, s.HTTPSPort
			}
		}
	case "kde":
		if s.KDEProxyType == "1" {
			host, port = parseProxyURL(s.KDEHTTP)
			if host == "" || port == "" {
				host, port = parseProxyURL(s.KDEHTTPS)
			}
		}
	}
	if host == "" || port == "" || port == "0" {
		return ""
	}
	return "http://" + net.JoinHostPort(host, port)
}

// clearStaleProxy recovers from a previous run that died before restoring.
func clearStaleProxy(port int) {
	s, err := snapshotProxy()
	if err != nil {
		return
	}
	want := strconv.Itoa(port)
	switch s.Backend {
	case "gnome":
		if s.Mode == "manual" && (s.HTTPHost == "127.0.0.1" || s.HTTPHost == "localhost") && s.HTTPPort == want {
			_ = gsettingsSetString("org.gnome.system.proxy", "mode", "none")
			notifyDeepin()
		}
	case "kde":
		if s.KDEProxyType == "1" {
			host, p := parseProxyURL(s.KDEHTTP)
			if (host == "127.0.0.1" || host == "localhost") && p == want {
				_ = kdeSet("ProxyType", "0")
				notifyKDE()
			}
		}
	}
}

func (s *proxySnapshot) encode() ([]byte, error) {
	return json.Marshal(s)
}

func decodeProxySnapshot(raw []byte) (*proxySnapshot, error) {
	var s proxySnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
