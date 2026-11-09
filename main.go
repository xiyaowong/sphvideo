package main

import (
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	_ "sphvideo/locales"
)

type appState struct {
	svc *Service

	mu        sync.Mutex
	status    string
	saveDir   string
	running   bool
	lastPulse time.Time
	starting  bool
}

func newAppState() *appState {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, "Downloads", "视频号")
	s := &appState{
		svc:     &Service{},
		status:  "未启动",
		saveDir: dir,
	}
	s.svc.saveDir = dir
	s.svc.statusFn = s.setStatus
	return s
}

func (s *appState) setStatus(msg string) {
	s.mu.Lock()
	s.status = msg
	s.mu.Unlock()
}

func (s *appState) setSaveDir(dir string) {
	s.mu.Lock()
	s.saveDir = dir
	s.mu.Unlock()
	s.svc.setSaveDir(dir)
}

func (s *appState) snapshot() (status, dir string, running, starting bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.saveDir, s.running, s.starting
}

func (s *appState) toggle() {
	s.mu.Lock()
	if s.starting {
		s.mu.Unlock()
		return
	}
	if s.running {
		s.mu.Unlock()
		s.mu.Lock()
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

func (s *appState) view(c *ui.Context) {
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

	// Background start/stop changes the state without an input event; keep a
	// slow pulse so the window catches up.
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

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--watchdog" {
		runWatchdog(os.Args[2])
		return
	}
	mygo.App.SetName("视频号视频下载")
	state := newAppState()
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { state.svc.Stop() })
	defer state.svc.Stop()

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "视频号视频下载",
			Width:   560,
			Height:  300,
			Content: ui.View(state.view),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
