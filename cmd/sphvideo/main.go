package main

import (
	_ "embed"
	"log"
	"os"
	"sync/atomic"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"sphvideo/internal/app"
	"sphvideo/internal/proxy"
	_ "sphvideo/locales"
)

//go:embed tray.png
var trayIcon []byte

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--watchdog" {
		proxy.RunWatchdog(os.Args[2])
		return
	}
	mygo.App.SetName("视频号视频下载")
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}

	state := app.New()
	defer state.Stop()
	var winPtr atomic.Pointer[mygo.Window]
	quitting := false

	showWindow := func() {
		w := winPtr.Load()
		if w == nil {
			return
		}
		w.Show()
		w.Restore()
		w.Focus()
	}

	mygo.App.OnSecondInstance(func(args []string, workingDir string) {
		showWindow()
	})

	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if !quitting && state.ActiveDownloads() > 0 {
			res, err := mygo.Dialog.Message(mygo.MessageOptions{
				Type:               mygo.MessageWarning,
				Title:              "退出会取消下载",
				Message:            "还有文件正在下载。退出后网络设置会恢复，未完成的文件会被删除。",
				Buttons:            []string{"退出并取消下载", "继续下载"},
				DefaultButton:      1,
				CancelButton:       1,
				DestructiveButtons: []int{0},
			})
			if err != nil || res.Button != 0 {
				e.PreventDefault()
				showWindow()
				return
			}
		}
		quitting = true
		state.Stop()
	})

	mygo.App.WhenReady(func() {
		w := mygo.NewWindow(mygo.WindowOptions{
			Title:     "视频号视频下载",
			Width:     680,
			Height:    420,
			MinWidth:  600,
			MinHeight: 320,
			Content:   ui.View(state.View),
		})
		winPtr.Store(w)
		state.AttachWindow(w)

		if setupTray(state, showWindow) != nil {
			w.OnClose(func(e *mygo.CloseEvent) {
				if quitting {
					return
				}
				e.PreventDefault()
				w.Hide()
				state.NotifyHidden()
			})
		}
	})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func setupTray(state *app.State, show func()) *mygo.Tray {
	if len(trayIcon) == 0 {
		return nil
	}
	menu := mygo.NewMenu([]*mygo.MenuItem{
		{Label: "显示窗口", Click: func(*mygo.MenuItem, *mygo.Window) { show() }},
		{Label: "停止并恢复网络", Click: func(*mygo.MenuItem, *mygo.Window) { state.StopFromTray() }},
		{Label: "修复网络", Click: func(*mygo.MenuItem, *mygo.Window) { state.RepairFromTray() }},
		mygo.Separator(),
		{Label: "退出", Click: func(*mygo.MenuItem, *mygo.Window) { mygo.App.Quit() }},
	})
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:      "main",
		Icon:    trayIcon,
		ToolTip: "视频号视频下载",
		Menu:    menu,
	})
	if err != nil {
		return nil
	}
	tray.OnClick(func() { show() })
	return tray
}
