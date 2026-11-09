package main

import (
	"log"
	"os"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"sphvideo/internal/app"
	"sphvideo/internal/proxy"
	_ "sphvideo/locales"
)

func main() {
	if len(os.Args) == 3 && os.Args[1] == "--watchdog" {
		proxy.RunWatchdog(os.Args[2])
		return
	}
	mygo.App.SetName("视频号视频下载")
	state := app.New()
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { state.Stop() })
	defer state.Stop()

	mygo.App.WhenReady(func() {
		mygo.NewWindow(mygo.WindowOptions{
			Title:   "视频号视频下载",
			Width:   560,
			Height:  300,
			Content: ui.View(state.View),
		})
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
