//go:build darwin

package proxy

import (
	"encoding/json"
	"fmt"
	"net"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

type proxySnapshot struct {
	Service       string `json:"svc"`
	WebEnabled    bool   `json:"we"`
	WebHost       string `json:"wh"`
	WebPort       string `json:"wp"`
	SecureEnabled bool   `json:"se"`
	SecureHost    string `json:"sh"`
	SecurePort    string `json:"sp"`
	AutoURL       string `json:"au"`
	AutoEnabled   bool   `json:"ae"`
}

type networkProxy struct {
	Enabled bool
	Server  string
	Port    string
}

var serviceUUID = regexp.MustCompile(`^[0-9A-Fa-f]{8}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{4}-[0-9A-Fa-f]{12}$`)

func scutilField(key, field string) string {
	cmd := exec.Command("scutil")
	cmd.Stdin = strings.NewReader("show " + key + "\n")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.TrimSpace(k) == field {
			return strings.Trim(strings.TrimSpace(v), `"`)
		}
	}
	return ""
}

func primaryService() (string, error) {
	for _, key := range []string{"State:/Network/Global/IPv4", "State:/Network/Global/IPv6"} {
		uuid := scutilField(key, "PrimaryService")
		if uuid == "" || !serviceUUID.MatchString(uuid) {
			continue
		}
		if name := scutilField("Setup:/Network/Service/"+uuid, "UserDefinedName"); name != "" {
			return name, nil
		}
	}
	out, err := exec.Command("networksetup", "-listallnetworkservices").Output()
	if err == nil {
		var first string
		for i, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if i == 0 || line == "" || strings.HasPrefix(line, "*") {
				continue
			}
			if strings.EqualFold(line, "Wi-Fi") || strings.EqualFold(line, "Ethernet") {
				return line, nil
			}
			if first == "" {
				first = line
			}
		}
		if first != "" {
			return first, nil
		}
	}
	return "", fmt.Errorf("未找到主网络服务，请确认网络已连接")
}

func readNetworkProxy(service string, secure bool) (networkProxy, error) {
	command := "-getwebproxy"
	if secure {
		command = "-getsecurewebproxy"
	}
	out, err := exec.Command("networksetup", command, service).CombinedOutput()
	if err != nil {
		return networkProxy{}, fmt.Errorf("读取系统代理失败: %v %s", err, strings.TrimSpace(string(out)))
	}
	var p networkProxy
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "enabled":
			p.Enabled = strings.EqualFold(strings.TrimSpace(v), "yes") || strings.TrimSpace(v) == "1"
		case "server":
			p.Server = strings.TrimSpace(v)
		case "port":
			p.Port = strings.TrimSpace(v)
		}
	}
	return p, nil
}

func readAutoProxy(service string) (string, bool) {
	out, err := exec.Command("networksetup", "-getautoproxyurl", service).CombinedOutput()
	if err != nil {
		return "", false
	}
	var url string
	var enabled bool
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "url":
			url = strings.TrimSpace(v)
			if strings.EqualFold(url, "(null)") {
				url = ""
			}
		case "enabled":
			enabled = strings.EqualFold(strings.TrimSpace(v), "yes") || strings.TrimSpace(v) == "1"
		}
	}
	return url, enabled
}

func snapshotProxy() (*proxySnapshot, error) {
	service, err := primaryService()
	if err != nil {
		return nil, err
	}
	web, err := readNetworkProxy(service, false)
	if err != nil {
		return nil, err
	}
	secure, err := readNetworkProxy(service, true)
	if err != nil {
		return nil, err
	}
	autoURL, autoEnabled := readAutoProxy(service)
	return &proxySnapshot{
		Service:       service,
		WebEnabled:    web.Enabled,
		WebHost:       web.Server,
		WebPort:       web.Port,
		SecureEnabled: secure.Enabled,
		SecureHost:    secure.Server,
		SecurePort:    secure.Port,
		AutoURL:       autoURL,
		AutoEnabled:   autoEnabled,
	}, nil
}

func setSystemProxy(port int) error {
	service, err := primaryService()
	if err != nil {
		return err
	}
	p := strconv.Itoa(port)
	steps := [][]string{
		{"-setautoproxystate", service, "off"},
		{"-setwebproxy", service, "127.0.0.1", p},
		{"-setsecurewebproxy", service, "127.0.0.1", p},
		{"-setwebproxystate", service, "on"},
		{"-setsecurewebproxystate", service, "on"},
	}
	for _, args := range steps {
		if out, err := exec.Command("networksetup", args...).CombinedOutput(); err != nil {
			return fmt.Errorf("设置系统代理失败: %v %s", err, strings.TrimSpace(string(out)))
		}
	}
	return nil
}

func (s *proxySnapshot) restore() {
	if s == nil {
		return
	}
	restore := func(host, port string, enabled bool, setProxy, setState string) {
		if host != "" && port != "" {
			_, _ = exec.Command("networksetup", setProxy, s.Service, host, port).CombinedOutput()
		}
		state := "off"
		if enabled {
			state = "on"
		}
		_, _ = exec.Command("networksetup", setState, s.Service, state).CombinedOutput()
	}
	restore(s.WebHost, s.WebPort, s.WebEnabled, "-setwebproxy", "-setwebproxystate")
	restore(s.SecureHost, s.SecurePort, s.SecureEnabled, "-setsecurewebproxy", "-setsecurewebproxystate")
	if s.AutoURL != "" {
		_, _ = exec.Command("networksetup", "-setautoproxyurl", s.Service, s.AutoURL).CombinedOutput()
	}
	autoState := "off"
	if s.AutoEnabled {
		autoState = "on"
	}
	_, _ = exec.Command("networksetup", "-setautoproxystate", s.Service, autoState).CombinedOutput()
}

func (s *proxySnapshot) upstreamURL() string {
	if s == nil {
		return ""
	}
	host, port := "", ""
	if s.WebEnabled {
		host, port = s.WebHost, s.WebPort
	}
	if host == "" || port == "" {
		if s.SecureEnabled {
			host, port = s.SecureHost, s.SecurePort
		}
	}
	if host == "" || port == "" {
		return ""
	}
	return "http://" + net.JoinHostPort(host, port)
}

// clearStaleProxy recovers from a previous run that died before restoring.
func clearStaleProxy(port int) {
	service, err := primaryService()
	if err != nil {
		return
	}
	want := strconv.Itoa(port)
	for _, secure := range []bool{false, true} {
		p, err := readNetworkProxy(service, secure)
		if err != nil {
			continue
		}
		if (p.Server == "127.0.0.1" || p.Server == "localhost") && p.Port == want {
			command := "-setwebproxystate"
			if secure {
				command = "-setsecurewebproxystate"
			}
			_, _ = exec.Command("networksetup", command, service, "off").CombinedOutput()
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
