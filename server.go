//go:build windows

package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ltaoo/echo"
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
	certPEM       []byte
	keyPEM        []byte
	certDER       []byte
	version       string

	saveDir  string
	statusFn func(string)
}

func (s *Service) setStatus(msg string) {
	s.mu.Lock()
	fn := s.statusFn
	s.mu.Unlock()
	if fn != nil {
		fn(msg)
	}
}

func (s *Service) setSaveDir(dir string) {
	s.mu.Lock()
	s.saveDir = dir
	s.mu.Unlock()
}

func (s *Service) Running() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.running
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

	certPEM, keyPEM, der, err := generateCA()
	if err != nil {
		ln.Close()
		return fmt.Errorf("生成临时证书失败: %w", err)
	}
	if err := removeCA(); err != nil {
		// Best effort: an old copy is harmless.
	}
	if err := installCA(der); err != nil {
		ln.Close()
		return fmt.Errorf("安装临时证书失败: %w", err)
	}

	snap, err := snapshotProxy()
	if err != nil {
		_ = removeCA()
		ln.Close()
		return fmt.Errorf("读取系统代理失败: %w", err)
	}

	e, err := echo.NewEchoWithOptions(certPEM, keyPEM, &echo.Options{
		InterceptOnlyMatched: true,
		UpstreamProxy:        snap.upstreamURL(),
	})
	if err != nil {
		_ = removeCA()
		ln.Close()
		return fmt.Errorf("启动代理失败: %w", err)
	}

	s.mu.Lock()
	s.port = port
	s.version = fmt.Sprintf("%d", time.Now().Unix())
	s.echo = e
	s.proxySnapshot = snap
	s.certPEM, s.keyPEM, s.certDER = certPEM, keyPEM, der
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
		server.Close()
		_ = removeCA()
		return fmt.Errorf("设置系统代理失败: %w", err)
	}
	if wd, err := spawnWatchdog(snap); err == nil {
		s.mu.Lock()
		s.wdIn = wd
		s.mu.Unlock()
	}
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
	_ = removeCA()
	if wd != nil {
		_, _ = io.WriteString(wd, "done\n")
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
			ctx.SetResponseBody(injectHTML(body, port, version))
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
			if strings.Contains(path, "virtual_svg-icons-register.publish") {
				body = patchChannelsJS(body, version)
			} else {
				body = patchImports(body, version)
			}
			ctx.SetResponseBody(body)
		},
	}
}

func patchImports(js, version string) string {
	js = reJSFrom.ReplaceAllString(js, `from"$1.js?v=`+version+`"`)
	js = reJSDependency.ReplaceAllString(js, `"js/$1.js?v=`+version+`"`)
	js = reJSLazy.ReplaceAllString(js, `import("$1.js?v=`+version+`")`)
	return reJSImport.ReplaceAllString(js, `import"$1.js?v=`+version+`"`)
}
