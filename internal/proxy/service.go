package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ltaoo/echo"

	"sphvideo/internal/download"
	"sphvideo/internal/inject"
)

const (
	defaultPort = 20230
	portTries   = 10
)

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

	jobs      *download.Manager
	startedAt time.Time
	lastPing  time.Time
	feedSeen  bool
	lastJob   download.JobSnapshot
	onJobDone func(download.JobSnapshot)
	reveal    func(string) error
}

func New(saveDir string, status func(string)) *Service {
	s := &Service{saveDir: saveDir, statusFn: status, jobs: download.NewManager()}
	s.jobs.SetOnDone(func(js download.JobSnapshot) {
		s.mu.Lock()
		s.lastJob = js
		fn := s.onJobDone
		s.mu.Unlock()
		if fn != nil {
			fn(js)
		}
	})
	return s
}

func (s *Service) SetOnJobDone(fn func(download.JobSnapshot)) {
	s.mu.Lock()
	s.onJobDone = fn
	s.mu.Unlock()
}

func (s *Service) SetReveal(fn func(string) error) {
	s.mu.Lock()
	s.reveal = fn
	s.mu.Unlock()
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

	ln, port, err := listenLocal()
	if err != nil {
		return err
	}
	// A proxy pointing here at startup is left over from a crashed run.
	clearStaleProxyRange()

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
		switch r.URL.Path {
		case "/__sph/download":
			s.handleDownload(w, r)
		case "/__sph/job":
			s.handleJob(w, r)
		case "/__sph/cancel":
			s.handleCancel(w, r)
		case "/__sph/reveal":
			s.handleReveal(w, r)
		case "/__sph/ping":
			s.handlePing(w, r)
		default:
			e.ServeHTTP(w, r)
		}
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
	s.running = true
	s.startedAt = time.Now()
	s.lastPing = time.Time{}
	s.feedSeen = false
	s.mu.Unlock()
	go func() {
		_ = server.Serve(ln)
	}()

	s.setStatus("")
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
	s.startedAt = time.Time{}
	s.lastPing = time.Time{}
	s.feedSeen = false
	s.mu.Unlock()

	s.jobs.CancelAll()

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
	if caErr != nil {
		s.setStatus("已停止，但临时证书清理失败。请在「说明」里点「修复网络」。")
		return
	}
	s.setStatus("已停止")
}

type Info struct {
	Running   bool
	Port      int
	StartedAt time.Time
	LastPing  time.Time
	FeedSeen  bool
	Active    []download.JobSnapshot
	Last      download.JobSnapshot
}

func (s *Service) Info() Info {
	s.mu.Lock()
	info := Info{
		Running:   s.running,
		Port:      s.port,
		StartedAt: s.startedAt,
		LastPing:  s.lastPing,
		FeedSeen:  s.feedSeen,
		Last:      s.lastJob,
	}
	s.mu.Unlock()
	info.Active = s.jobs.Active()
	return info
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
	OK    bool                  `json:"ok"`
	ID    string                `json:"id,omitempty"`
	Path  string                `json:"path,omitempty"`
	Error string                `json:"error,omitempty"`
	Job   *download.JobSnapshot `json:"job,omitempty"`
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func cors(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "Content-Type")
	h.Set("Access-Control-Allow-Private-Network", "true")
	h.Set("Access-Control-Max-Age", "600")
}

func allowedOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	return origin == "" || origin == "https://channels.weixin.qq.com"
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
	if !allowedOrigin(r) {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	var req download.Request
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := dec.Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, downloadResponse{Error: "请求无效"})
		return
	}
	switch strings.TrimSpace(req.Kind) {
	case "", "video", "cover", "images":
	default:
		writeJSON(w, http.StatusBadRequest, downloadResponse{Error: "不支持的下载类型"})
		return
	}
	s.mu.Lock()
	running := s.running
	dir := s.saveDir
	snap := s.proxySnapshot
	s.mu.Unlock()
	if !running {
		writeJSON(w, http.StatusConflict, downloadResponse{Error: "下载服务已停止，请回到软件窗口点「启动」"})
		return
	}
	job := s.jobs.Start(req, dir, downloadProxy(snap))
	writeJSON(w, http.StatusOK, downloadResponse{OK: true, ID: job.ID})
}

func (s *Service) handleJob(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, downloadResponse{Error: "GET only"})
		return
	}
	if !allowedOrigin(r) {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	snap, ok := s.jobs.Get(r.URL.Query().Get("id"))
	if !ok {
		writeJSON(w, http.StatusNotFound, downloadResponse{Error: "下载任务不存在"})
		return
	}
	writeJSON(w, http.StatusOK, downloadResponse{OK: true, Job: &snap})
}

func (s *Service) handleCancel(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, downloadResponse{Error: "POST only"})
		return
	}
	if !allowedOrigin(r) {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, downloadResponse{Error: "请求无效"})
		return
	}
	if !s.jobs.Cancel(req.ID) {
		writeJSON(w, http.StatusConflict, downloadResponse{Error: "下载已经结束"})
		return
	}
	writeJSON(w, http.StatusOK, downloadResponse{OK: true})
}

func (s *Service) handleReveal(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, downloadResponse{Error: "POST only"})
		return
	}
	if !allowedOrigin(r) {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, downloadResponse{Error: "请求无效"})
		return
	}
	snap, ok := s.jobs.Get(req.ID)
	if !ok || snap.State != download.JobDone || snap.Path == "" {
		writeJSON(w, http.StatusNotFound, downloadResponse{Error: "没有可打开的文件"})
		return
	}
	s.mu.Lock()
	dir, reveal := s.saveDir, s.reveal
	s.mu.Unlock()
	if !withinDir(dir, snap.Path) {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	if reveal == nil {
		writeJSON(w, http.StatusInternalServerError, downloadResponse{Error: "无法打开文件夹"})
		return
	}
	if err := reveal(snap.Path); err != nil {
		writeJSON(w, http.StatusInternalServerError, downloadResponse{Error: "无法打开文件夹"})
		return
	}
	writeJSON(w, http.StatusOK, downloadResponse{OK: true, Path: snap.Path})
}

func (s *Service) handlePing(w http.ResponseWriter, r *http.Request) {
	cors(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, downloadResponse{Error: "GET only"})
		return
	}
	if !allowedOrigin(r) {
		writeJSON(w, http.StatusForbidden, downloadResponse{Error: "forbidden"})
		return
	}
	s.mu.Lock()
	s.lastPing = time.Now()
	s.feedSeen = r.URL.Query().Get("feed") == "1"
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, downloadResponse{OK: true})
}

func withinDir(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func listenLocal() (net.Listener, int, error) {
	for port := defaultPort; port < defaultPort+portTries; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return ln, port, nil
		}
	}
	return nil, 0, fmt.Errorf("本地端口被占用，请重启电脑后再试")
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
