//go:build windows

package app

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"sphvideo/internal/proxy"
)

type State struct {
	svc *proxy.Service

	mu        sync.Mutex
	status    string
	saveDir   string
	running   bool
	starting  bool
	lastPulse time.Time
}

func New() *State {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads", "视频号")
	s := &State{status: "未启动", saveDir: dir}
	s.svc = proxy.New(dir, s.setStatus)
	return s
}

func (s *State) Stop() {
	s.svc.Stop()
}

func (s *State) setStatus(msg string) {
	s.mu.Lock()
	s.status = msg
	s.mu.Unlock()
}

func (s *State) setSaveDir(dir string) {
	s.mu.Lock()
	s.saveDir = dir
	s.mu.Unlock()
	s.svc.SetSaveDir(dir)
}

func (s *State) snapshot() (status, dir string, running, starting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.saveDir, s.running, s.starting
}

func (s *State) toggle() {
	s.mu.Lock()
	if s.starting {
		s.mu.Unlock()
		return
	}
	if s.running {
		s.status = "正在停止…"
		s.mu.Unlock()
		go func() {
			s.svc.Stop()
			s.mu.Lock()
			s.running = false
			s.mu.Unlock()
		}()
		return
	}
	s.starting = true
	s.status = "正在启动…"
	s.mu.Unlock()

	go func() {
		err := s.svc.Start()
		s.mu.Lock()
		s.starting = false
		if err != nil {
			s.status = "启动失败：" + err.Error()
			s.running = false
		} else {
			s.running = true
		}
		s.mu.Unlock()
	}()
}

func (s *State) View(c *ui.Context) {
	status, dir, running, starting := s.snapshot()
	t := c.Theme()

	ui.Column(c).Fill().Center().Gap(14).Children(func() {
		ui.Text(c, "视频号视频下载").FontSize(22).Bold()
		label := "启动"
		if running {
			label = "停止"
		}
		if ui.PrimaryButton(c, label).Disabled(starting).Clicked() {
			s.toggle()
		}
		ui.Text(c, status).FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).Children(func() {
			ui.Text(c, "保存到："+dir).FontSize(11).TextColor(t.TextMuted)
			if ui.Button(c, "选择目录").Clicked() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
					Title:       "选择保存目录",
					DefaultPath: dir,
					Directory:   true,
				})
				if err == nil && len(paths) > 0 {
					s.setSaveDir(paths[0])
				}
			}
		})
		ui.Text(c, "启动后打开微信 → 视频号 → 任意视频，右下角会出现下载按钮。").FontSize(11).TextColor(t.TextMuted)
	})

	// Background start/stop changes state without an input event; pulse slowly
	// so the window catches up.
	s.mu.Lock()
	pulse := time.Since(s.lastPulse) > 500*time.Millisecond
	if pulse {
		s.lastPulse = time.Now()
	}
	s.mu.Unlock()
	if pulse {
		c.Invalidate()
	}
}
