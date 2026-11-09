package proxy

import (
	"net/http"
	"net/url"
)

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
