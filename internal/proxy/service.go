package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ltaoo/echo"

	"sphvideo/internal/download"
	"sphvideo/internal/inject"
)

const defaultPort = 20230

type Service struct {
	mu       sync.Mutex
	running  bool
	port     int
	server   *http.Server
	listener net.Listener
	echo     *echo.Echo
	wdIn     io.WriteCloser

	proxySnapshot *proxySnapshot
	version       string
	saveDir       string
	statusFn      func(string)
}

func New(saveDir string, status func(string)) *Service {
	return &Service{saveDir: saveDir, statusFn: status}
}

func (s *Service) setStatus(msg string) {
	s.mu.Lock()
	fn := s.statusFn
	s.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

func (s *Service) SetSaveDir(dir string) {
	s.mu.Lock()
	s.saveDir = dir
	s.mu.Unlock()
}

func (s *Service) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return nil
	}
	s.mu.Unlock()

	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", defaultPort))
	if err != nil {
		return fmt.Errorf("端口 %d 被占用，请关闭占用它的程序后重试", defaultPort)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	// A proxy pointing here at startup is left over from a crashed run.
	clearStaleProxy(port)

	snap, err := snapshotProxy()
	if err != nil {
		ln.Close()
		return fmt.Errorf("读取系统代理失败: %w", err)
	}
	// Spawn cleanup before touching the certificate store, so a crash at any
	// later point restores the proxy and removes the CA.
	wd, err := spawnWatchdog(snap)
	if err != nil {
		ln.Close()
		return fmt.Errorf("启动清理守护进程失败: %w", err)
	}
	stopWatchdog := func() {
		_, _ = io.WriteString(wd, "done\n")
		_ = wd.Close()
	}

	certPEM, keyPEM, der, err := generateCA()
	if err != nil {
		stopWatchdog()
		ln.Close()
		return fmt.Errorf("生成临时证书失败: %w", err)
	}
	_ = removeCA()
	if err := installCA(der); err != nil {
		_ = removeCA()
		stopWatchdog()
		ln.Close()
		return fmt.Errorf("安装临时证书失败: %w", err)
	}

	e, err := echo.NewEchoWithOptions(certPEM, keyPEM, &echo.Options{
		InterceptOnlyMatched: true,
		UpstreamProxy:        snap.upstreamURL(),
	})
	if err != nil {
		_ = removeCA()
		stopWatchdog()
		ln.Close()
		return fmt.Errorf("启动代理失败: %w", err)
	}

	s.mu.Lock()
	s.port = port
	s.version = fmt.Sprintf("%d", time.Now().Unix())
	s.echo = e
	s.proxySnapshot = snap
	s.listener = ln
	s.mu.Unlock()

	e.AddPlugin(s.channelsPlugin())
	e.AddPlugin(s.resPlugin())

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__sph/download" {
			s.handleDownload(w, r)
			return
		}
		e.ServeHTTP(w, r)
	})
	server := &http.Server{Handler: handler}
	s.mu.Lock()
	s.server = server
	s.mu.Unlock()

	if err := setSystemProxy(port); err != nil {
		snap.restore()
		_ = removeCA()
		stopWatchdog()
		server.Close()
		_ = ln.Close()
		_ = e.Close()
		return fmt.Errorf("设置系统代理失败: %w", err)
	}
	s.mu.Lock()
	s.wdIn = wd
	s.mu.Unlock()
	go func() {
		_ = server.Serve(ln)
	}()

	s.mu.Lock()
	s.running = true
	s.mu.Unlock()
	s.setStatus(fmt.Sprintf("已启动（127.0.0.1:%d），请打开微信视频号", port))
	return nil
}

func (s *Service) Stop() {
	s.mu.Lock()
	if !s.running && s.server == nil {
		s.mu.Unlock()
		return
	}
	snap := s.proxySnapshot
	server := s.server
	e := s.echo
	wd := s.wdIn
	s.server = nil
	s.listener = nil
	s.echo = nil
	s.wdIn = nil
	s.running = false
	s.mu.Unlock()

	// Restore the user's proxy first, so the network is usable immediately.
	if snap != nil {
		snap.restore()
	}
	if server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = server.Shutdown(ctx)
		cancel()
		_ = server.Close()
	}
	if e != nil {
		_ = e.Close()
	}
	caErr := removeCA()
	if wd != nil {
		if caErr == nil {
			_, _ = io.WriteString(wd, "done\n")
		}
		// On a failed CA removal, closing without "done" lets the watchdog retry.
		_ = wd.Close()
	}
	s.setStatus("已停止，代理和证书已恢复")
}

func (s *Service) channelsPlugin() *echo.Plugin {
	return &echo.Plugin{
		Match: "channels.weixin.qq.com",
		OnResponse: func(ctx *echo.Context) {
			if !strings.Contains(ctx.Req.URL.Path, "/web/pages/") {
				return
			}
			body, err := ctx.GetResponseBody()
			if err != nil || !strings.Contains(body, "<head") {
				return
			}
			ctx.DelResponseHeader("Content-Security-Policy")
			ctx.DelResponseHeader("Content-Security-Policy-Report-Only")
			s.mu.Lock()
			port, version := s.port, s.version
			s.mu.Unlock()
			ctx.SetResponseBody(inject.HTML(body, port, version))
		},
	}
}

func (s *Service) resPlugin() *echo.Plugin {
	return &echo.Plugin{
		Match: "res.wx.qq.com",
		OnResponse: func(ctx *echo.Context) {
			path := ctx.Req.URL.Path
			if !strings.HasSuffix(path, ".js") || strings.Contains(path, "wasm_video_decode") {
				return
			}
			body, err := ctx.GetResponseBody()
			if err != nil || body == "" {
				return
			}
			s.mu.Lock()
			version := s.version
			s.mu.Unlock()
			body = inject.ChannelsJS(body, version)
			ctx.SetResponseBody(body)
		},
	}
}

type downloadResponse struct {
	OK    bool   `json:"ok"`
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Allow-Private-Network", "true")
	h.Set("Access-Control-Max-Age", "600")
}

func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, downloadResponse{Error: "POST only"})
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" && origin != "https://channels.weixin.qq.com" {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	var req download.Request
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, downloadResponse{Error: "bad request"})
		return
	}
	s.mu.Lock()
	dir := s.saveDir
	snap := s.proxySnapshot
	s.mu.Unlock()
	path, err := download.Save(req, dir, downloadProxy(snap), s.setStatus)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, downloadResponse{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, downloadResponse{OK: true, Path: path})
}
