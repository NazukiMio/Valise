// Command valise-desktop is Valise's desktop interface (mygo native UI,
// docs/UI.md sections 2-7).
package main

import (
	"flag"
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"valise/client/config"
	"valise/ui/gui"
	"valise/ui/service"
)

var version = "0.0.0-dev"

func main() {
	configDir := flag.String("config-dir", "", "Valise 的数据目录（默认为系统位置）")
	flag.Parse()
	// Everything with side effects waits for the app to be ready
	// (docs/UI.md 4).
	mygo.App.WhenReady(func() {
		dirs, err := config.DefaultDirs()
		if *configDir != "" {
			dirs, err = config.Dirs{Data: *configDir, Cache: filepath.Join(*configDir, "cache")}, nil
		}
		var svc *service.Service
		if err == nil {
			svc, err = service.New(dirs, version)
		}
		if err != nil {
			log.Fatal(err)
		}
		app := gui.New(svc)
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Valise", Width: 1180, Height: 780, MinWidth: 860, MinHeight: 560,
			Content: ui.View(app.View)})
		app.SetUpdate(win.Update)
		app.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
