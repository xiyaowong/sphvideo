package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"sphvideo/internal/proxy"
	"wx_channel/sphgui"
)

const apiPort = 2022

type State struct {
	win *mygo.Window

	mu       sync.Mutex
	status   string
	saveDir  string
	running  bool
	starting bool
	stopping bool

	repairing     bool
	lastResult    string
	lastPulse     time.Time
	startedAt     time.Time
	pageConnected bool

	advancedOpen bool

	svc   *sphgui.App
	guard *proxy.Guard
}

func New() *State {
	return &State{saveDir: defaultSaveDir()}
}

func (s *State) AttachWindow(w *mygo.Window) {
	s.mu.Lock()
	s.win = w
	s.mu.Unlock()
}

func (s *State) windowFocused() bool {
	s.mu.Lock()
	w := s.win
	s.mu.Unlock()
	return w != nil && w.IsFocused()
}

func (s *State) Stop() {
	s.stopService()
}

func (s *State) ActiveDownloads() int {
	s.mu.Lock()
	app := s.svc
	s.mu.Unlock()
	if app == nil {
		return 0
	}
	return len(app.Snapshot().Active)
}

func (s *State) Toggle() {
	s.toggle()
}

func (s *State) setStatus(msg string) {
	s.mu.Lock()
	s.status = msg
	s.mu.Unlock()
}

func (s *State) snapshot() (status, dir string, running, starting, stopping, repairing bool, lastResult string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status, s.saveDir, s.running, s.starting, s.stopping, s.repairing, s.lastResult
}

func (s *State) appSnapshot() sphgui.Snapshot {
	s.mu.Lock()
	app := s.svc
	s.mu.Unlock()
	if app == nil {
		return sphgui.Snapshot{}
	}
	return app.Snapshot()
}

func (s *State) toggle() {
	s.mu.Lock()
	if s.starting || s.stopping || s.repairing {
		s.mu.Unlock()
		return
	}
	if s.running {
		previous := s.status
		s.stopping = true
		s.status = "正在停止…"
		s.mu.Unlock()
		if !s.confirmCancelDownloads() {
			s.mu.Lock()
			s.stopping = false
			s.status = previous
			s.mu.Unlock()
			return
		}
		go func() {
			err := s.stopService()
			s.mu.Lock()
			s.stopping = false
			if err != nil {
				s.status = "已停止，但临时证书清理失败。请在「说明」里点「修复网络」。"
			} else {
				s.status = "已停止"
			}
			s.mu.Unlock()
		}()
		return
	}
	s.starting = true
	s.status = "正在启动…"
	s.mu.Unlock()

	go func() {
		err := s.startService()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.starting = false
		if err != nil {
			s.running = false
			s.status = "启动失败：" + friendlyStartError(err)
		}
	}()
}

func (s *State) confirmCancelDownloads() bool {
	n := s.ActiveDownloads()
	if n == 0 {
		return true
	}
	res, err := mygo.Dialog.Message(mygo.MessageOptions{
		Type:               mygo.MessageWarning,
		Title:              "停止会取消下载",
		Message:            fmt.Sprintf("还有 %d 个文件正在下载。停止后网络设置会恢复，未完成的任务会被取消。", n),
		Buttons:            []string{"停止并取消下载", "继续下载"},
		DefaultButton:      1,
		CancelButton:       1,
		DestructiveButtons: []int{0},
	})
	if err != nil {
		return false
	}
	return res.Button == 0
}

func (s *State) startService() error {
	s.mu.Lock()
	dir := s.saveDir
	s.mu.Unlock()

	if err := proxy.EnsurePortFree(apiPort); err != nil {
		return err
	}
	proxyPort, err := proxy.PickProxyPort()
	if err != nil {
		return err
	}
	guard, err := proxy.Prepare(proxyPort)
	if err != nil {
		return err
	}
	app, err := sphgui.Start(sphgui.Options{
		Version:         version(),
		SaveDir:         dir,
		APIPort:         apiPort,
		ProxyPort:       proxyPort,
		CertPEM:         guard.CertPEM(),
		KeyPEM:          guard.KeyPEM(),
		OnDownload:      s.onDownload,
		OnPageConnected: s.onPageConnected,
	})
	if err != nil {
		_ = guard.Close()
		return err
	}

	s.mu.Lock()
	s.guard = guard
	s.svc = app
	s.running = true
	s.status = ""
	s.startedAt = time.Now()
	s.pageConnected = false
	s.mu.Unlock()
	return nil
}

func (s *State) stopService() error {
	s.mu.Lock()
	app := s.svc
	guard := s.guard
	s.svc = nil
	s.guard = nil
	s.running = false
	s.pageConnected = false
	s.startedAt = time.Time{}
	s.mu.Unlock()

	if app != nil {
		app.Stop()
	}
	if guard != nil {
		return guard.Close()
	}
	return nil
}

func (s *State) repair() {
	s.mu.Lock()
	if s.starting || s.stopping || s.repairing {
		s.mu.Unlock()
		return
	}
	running := s.running
	s.mu.Unlock()
	if running && !s.confirmCancelDownloads() {
		return
	}
	s.mu.Lock()
	s.repairing = true
	s.status = "正在恢复网络…"
	s.mu.Unlock()

	go func() {
		s.stopService()
		err := proxy.Repair()
		s.mu.Lock()
		defer s.mu.Unlock()
		s.repairing = false
		if err != nil {
			s.status = "修复网络失败：" + err.Error()
			return
		}
		s.status = "网络设置已恢复。"
	}()
}

func (s *State) onPageConnected(connected bool) {
	s.mu.Lock()
	s.pageConnected = connected
	s.mu.Unlock()
}

func (s *State) onDownload(ev sphgui.DownloadEvent) {
	s.mu.Lock()
	if ev.Finished {
		if ev.Path != "" {
			s.lastResult = ev.Path
		}
		s.status = ""
	} else if ev.Error != "" {
		s.status = ev.Error
	}
	s.mu.Unlock()

	if !ev.Finished || ev.Path == "" {
		return
	}
	if s.windowFocused() {
		return
	}
	note := mygo.NewNotification(mygo.NotificationOptions{
		ID:    fmt.Sprintf("sph-done-%d", ev.TaskID),
		Title: "下载完成",
		Body:  filepath.Base(ev.Path),
	})
	note.OnClick(func() {
		mygo.Shell.ShowItemInFolder(ev.Path)
	})
	_ = note.Show()
}

func (s *State) View(c *ui.Context) {
	status, dir, running, starting, stopping, repairing, lastResult := s.snapshot()
	info := s.appSnapshot()
	t := c.Theme()

	ui.Scroll(c).Fill().Children(func() {
		ui.Column(c).Padding(24).Gap(12).MaxWidth(680).Children(func() {
			ui.Text(c, "视频号视频下载").FontSize(22).Bold()

			if !running && !starting && !stopping && !repairing {
				s.viewIntro(c)
			} else {
				s.viewRunning(c, t, info, lastResult, starting, stopping, repairing)
			}

			ui.Divider(c)
			s.viewSaveDir(c, t, dir)

			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				label := "启动"
				if starting {
					label = "正在启动…"
				} else if repairing {
					label = "正在恢复…"
				} else if stopping {
					label = "正在停止…"
				} else if running {
					label = "停止"
				}
				if ui.PrimaryButton(c, label).Disabled(starting || stopping || repairing).Clicked() {
					s.toggle()
				}
			})

			if status != "" {
				ui.Text(c, status).FontSize(12).TextColor(t.TextMuted).Wrap()
			}

			if running {
				ui.Text(c, "关闭窗口会停止服务、恢复网络并退出。").
					FontSize(11).TextColor(t.TextMuted).Wrap()
			}

			ui.Collapsible(c, "说明", &s.advancedOpen, func() {
				s.viewAbout(c, t)
			})

			ui.Text(c, "非官方工具 · 仅用于个人备份 · "+version()).
				FontSize(11).TextColor(t.TextMuted).Wrap()
		})
	})

	// Background start, stop and download changes happen without an input
	// event; pulse so the window catches up.
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

func (s *State) viewIntro(c *ui.Context) {
	ui.Text(c, "点「启动」，然后打开电脑版微信 → 视频号。页面右下角会出现下载按钮，视频号首页右侧也会出现下载入口。").
		FontSize(14).Wrap()
}

func (s *State) viewAbout(c *ui.Context, t *ui.Theme) {
	ui.Text(c, "下载只保存在本机；代理只处理视频号相关流量，不读取聊天记录或其他网站，也不上传内容。").
		FontSize(12).TextColor(t.TextMuted).Wrap().MaxWidth(600)
	ui.Text(c, "使用期间会临时修改系统代理并安装临时证书；停止或退出后自动恢复、删除。").
		FontSize(12).TextColor(t.TextMuted).Wrap().MaxWidth(600)
	ui.Text(c, "公司网络或 VPN 下，可能暂时影响内网访问。安全软件询问联网时请选择允许。").
		FontSize(12).TextColor(t.TextMuted).Wrap().MaxWidth(600)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, "修复网络").Clicked() {
			s.repair()
		}
		ui.Text(c, "停止后无法上网时，点这里恢复。").
			FontSize(11).TextColor(t.TextMuted)
	})
}

func (s *State) viewRunning(c *ui.Context, t *ui.Theme, info sphgui.Snapshot, lastResult string, starting, stopping, repairing bool) {
	label := "已就绪"
	color := t.Success
	if starting {
		label = "正在启动…"
		color = t.Warning
	} else if repairing {
		label = "正在恢复…"
		color = t.Warning
	} else if stopping {
		label = "正在停止…"
		color = t.Warning
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "●").FontSize(14).TextColor(color)
		ui.Text(c, label).FontSize(16).Bold()
	})

	if starting {
		ui.Text(c, "正在准备下载服务…").FontSize(13).TextColor(t.TextMuted).Wrap()
	} else if repairing || stopping {
		ui.Text(c, "正在恢复系统设置…").FontSize(13).TextColor(t.TextMuted).Wrap()
	} else {
		ui.Text(c, "打开电脑版微信 → 视频号，在页面右下角下载。").
			FontSize(13).Wrap()

		if !info.PageConnected {
			ui.Text(c, "等待视频号页面…如果微信已打开，请完全退出后重新打开。").
				FontSize(12).TextColor(t.Warning).Wrap()
		}
	}

	if len(info.Active) > 0 {
		job := info.Active[0]
		title := fmt.Sprintf("正在下载 %d 个文件", len(info.Active))
		if len(info.Active) == 1 && job.Title != "" {
			title = "正在下载：" + job.Title
		}
		ui.Text(c, title).FontSize(13).Bold().MaxWidth(520).SingleLine().Tooltip(title)
		if job.Fraction >= 0 {
			ui.Progress(c, job.Fraction).Width(520)
		} else {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Spinner(c)
				ui.Text(c, "准备中…").FontSize(12).TextColor(t.TextMuted)
			})
		}
		if job.Detail != "" {
			ui.Text(c, job.Detail).FontSize(11).TextColor(t.TextMuted)
		}
	} else if lastResult != "" {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "上次保存："+filepath.Base(lastResult)).
				FontSize(12).TextColor(t.TextMuted).MaxWidth(420).SingleLine().Tooltip(lastResult)
			if ui.Button(c, "打开文件夹").Clicked() {
				mygo.Shell.ShowItemInFolder(lastResult)
			}
		})
	}

	s.mu.Lock()
	startedAt := s.startedAt
	s.mu.Unlock()
	if !startedAt.IsZero() && time.Since(startedAt) > 20*time.Hour {
		ui.Text(c, "已连续运行 20 小时，建议停止后重新启动。").
			FontSize(12).TextColor(t.Warning).Wrap()
	}
}

func (s *State) viewSaveDir(c *ui.Context, t *ui.Theme, dir string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "下载保存到").FontSize(12).TextColor(t.TextMuted)
		ui.Text(c, dir).FontSize(12).MaxWidth(340).SingleLine().Tooltip(dir)
		if ui.Button(c, "更改").Clicked() {
			s.chooseDir(dir)
		}
		if ui.Button(c, "打开").Clicked() {
			_ = os.MkdirAll(dir, 0o755)
			_ = mygo.Shell.OpenPath(dir)
		}
	})
}

func (s *State) chooseDir(current string) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Title:       "选择保存目录",
		DefaultPath: current,
		Directory:   true,
	})
	if err == nil && len(paths) > 0 {
		s.setSaveDir(paths[0])
	}
}

func (s *State) setSaveDir(dir string) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		s.setStatus("无法使用这个文件夹，请选择其他文件夹。")
		return
	}
	if err := testWritable(dir); err != nil {
		s.setStatus("这个文件夹无法写入，请选择其他文件夹。")
		return
	}
	s.mu.Lock()
	s.saveDir = dir
	app := s.svc
	s.mu.Unlock()
	if app != nil {
		app.SetSaveDir(dir)
	}
	s.setStatus("已切换保存目录。")
}

func testWritable(dir string) error {
	f, err := os.CreateTemp(dir, ".sphvideo-write-test-*")
	if err != nil {
		return err
	}
	name := f.Name()
	_ = f.Close()
	return os.Remove(name)
}

func friendlyStartError(err error) string {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "端口"):
		return "本地端口被占用，请关闭占用该端口的程序后重试。"
	case strings.Contains(msg, "证书"):
		return "无法安装临时证书。请确认程序以管理员身份运行，并检查安全软件的拦截提示。"
	case strings.Contains(msg, "代理"):
		return "无法修改系统代理。请检查安全软件是否拦截本程序。"
	case strings.Contains(msg, "守护"):
		return "清理进程启动失败。请检查安全软件是否拦截本程序。"
	case strings.Contains(msg, "数据库"):
		return "下载数据库初始化失败：" + msg
	case strings.Contains(msg, "适配器"):
		return "视频号组件初始化失败：" + msg
	}
	return msg
}

func version() string {
	return "v0.1.0" // Keep in sync with mygo.json.
}

func defaultSaveDir() string {
	base := ""
	if p, err := mygo.App.Path(mygo.PathDownloads); err == nil && p != "" {
		base = p
	}
	if base == "" {
		if home, err := os.UserHomeDir(); err == nil {
			base = filepath.Join(home, "Downloads")
		}
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(base, "视频号")
}
