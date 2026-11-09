//go:build windows

package proxy

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows/registry"
)

const inetSettings = `Software\Microsoft\Windows\CurrentVersion\Internet Settings`

type proxySnapshot struct {
	enableSet   bool
	enable      uint32
	serverSet   bool
	server      string
	overrideSet bool
	override    string
	pacSet      bool
	pac         string
}

func openInetKey(access uint32) (registry.Key, error) {
	return registry.OpenKey(registry.CURRENT_USER, inetSettings, access)
}

func readString(k registry.Key, name string) (string, bool) {
	v, _, err := k.GetStringValue(name)
	return v, err == nil
}

func readDWORD(k registry.Key, name string) (uint32, bool) {
	v, _, err := k.GetIntegerValue(name)
	return uint32(v), err == nil
}

func snapshotProxy() (*proxySnapshot, error) {
	k, err := openInetKey(registry.QUERY_VALUE)
	if err != nil {
		return nil, err
	}
	defer k.Close()
	s := &proxySnapshot{}
	s.enable, s.enableSet = readDWORD(k, "ProxyEnable")
	s.server, s.serverSet = readString(k, "ProxyServer")
	s.override, s.overrideSet = readString(k, "ProxyOverride")
	s.pac, s.pacSet = readString(k, "AutoConfigURL")
	return s, nil
}

func (s *proxySnapshot) upstreamURL() string {
	if s == nil || !s.serverSet || !s.enableSet || s.enable == 0 {
		return ""
	}
	host, port := pickProxyServer(s.server)
	if host == "" || port == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(host, port)
}

// pickProxyServer chooses the HTTP entry of a WinINET ProxyServer value.
func pickProxyServer(v string) (string, string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", ""
	}
	var fallback string
	for _, part := range strings.Split(v, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if i := strings.Index(part, "="); i >= 0 {
			scheme := strings.ToLower(strings.TrimSpace(part[:i]))
			addr := strings.TrimSpace(part[i+1:])
			if fallback == "" {
				fallback = addr
			}
			if scheme == "http" || scheme == "https" {
				if h, p := splitHostPort(addr); h != "" {
					return h, p
				}
			}
			continue
		}
		if fallback == "" {
			fallback = part
		}
	}
	return splitHostPort(fallback)
}

func splitHostPort(addr string) (string, string) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return "", ""
	}
	if h, p, err := net.SplitHostPort(addr); err == nil {
		return h, p
	}
	i := strings.LastIndex(addr, ":")
	if i > 0 && i < len(addr)-1 {
		return addr[:i], addr[i+1:]
	}
	return addr, "80"
}

func (s *proxySnapshot) restore() {
	k, err := openInetKey(registry.SET_VALUE | registry.QUERY_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	if s.enableSet {
		_ = k.SetDWordValue("ProxyEnable", s.enable)
	} else {
		_ = k.DeleteValue("ProxyEnable")
	}
	restoreString(k, "ProxyServer", s.server, s.serverSet)
	restoreString(k, "ProxyOverride", s.override, s.overrideSet)
	restoreString(k, "AutoConfigURL", s.pac, s.pacSet)
	notifyProxyChanged()
}

func restoreString(k registry.Key, name, value string, set bool) {
	if set {
		_ = k.SetStringValue(name, value)
	} else {
		_ = k.DeleteValue(name)
	}
}

func setSystemProxy(port int) error {
	k, err := openInetKey(registry.SET_VALUE | registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	override, ok := readString(k, "ProxyOverride")
	if !ok {
		override = "<local>"
	}
	override = ensureLocalBypass(override)
	if err := k.SetDWordValue("ProxyEnable", 1); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyServer", "127.0.0.1:"+strconv.Itoa(port)); err != nil {
		return err
	}
	if err := k.SetStringValue("ProxyOverride", override); err != nil {
		return err
	}
	// A PAC script would take precedence over our static proxy.
	_ = k.DeleteValue("AutoConfigURL")
	notifyProxyChanged()
	return nil
}

func ensureLocalBypass(v string) string {
	have := map[string]bool{}
	for _, part := range strings.Split(v, ";") {
		have[strings.ToLower(strings.TrimSpace(part))] = true
	}
	for _, want := range []string{"<local>", "127.0.0.1", "localhost"} {
		if !have[want] {
			if strings.TrimSpace(v) != "" {
				v += ";"
			}
			v += want
		}
	}
	return v
}

func proxyPointsTo(port int) bool {
	k, err := openInetKey(registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	enable, ok := readDWORD(k, "ProxyEnable")
	if !ok || enable == 0 {
		return false
	}
	server, ok := readString(k, "ProxyServer")
	if !ok {
		return false
	}
	h, p := pickProxyServer(server)
	return (h == "127.0.0.1" || h == "localhost") && p == strconv.Itoa(port)
}

// clearStaleProxy recovers from a previous run that died before restoring.
func clearStaleProxy(port int) {
	if !proxyPointsTo(port) {
		return
	}
	k, err := openInetKey(registry.SET_VALUE)
	if err != nil {
		return
	}
	defer k.Close()
	_ = k.SetDWordValue("ProxyEnable", 0)
	notifyProxyChanged()
}

var (
	wininet                = syscall.NewLazyDLL("wininet.dll")
	internetSetOptionW     = wininet.NewProc("InternetSetOptionW")
	internetOptionSettings = 39
	internetOptionRefresh  = 37
)

func notifyProxyChanged() {
	for _, opt := range []uintptr{uintptr(internetOptionSettings), uintptr(internetOptionRefresh)} {
		_, _, _ = internetSetOptionW.Call(0, opt, 0, 0)
	}
}

// downloadProxy returns the transport proxy for direct downloads. Only plain
// HTTP proxies are used; SOCKS is left to the TUN or system layer.
func downloadProxy(s *proxySnapshot) func(*http.Request) (*url.URL, error) {
	u := s.upstreamURL()
	if u == "" {
		return nil
	}
	parsed, err := url.Parse(u)
	if err != nil || parsed.Host == "" {
		return nil
	}
	return func(*http.Request) (*url.URL, error) { return parsed, nil }
}
