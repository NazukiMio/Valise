// Command valise-desktop is Valise's desktop interface (mygo native UI,
// docs/UI.md sections 2-7).
package main

import (
	"flag"
	"github.com/egoist/mygo/transfer"
	"io"
	"log"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
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
		// The window has no terminal: OpenSSH must never prompt on one,
		// and the admin passphrase is asked in the window (docs/UI.md 7.3).
		svc.NoTerminalLogin = true
		svc.AdminAuth.Passphrase = app.AdminPassphrase
		svc.OwnerAuth.Passphrase = app.OwnerPassphrase
		svc.OwnerAuth.PIN = app.OwnerPIN
		svc.PasswordAuth.Prompt = app.ServerPassword
		win := mygo.NewWindow(mygo.WindowOptions{Title: "Valise", Width: 1180, Height: 780, MinWidth: 860, MinHeight: 560,
			TitleBarStyle: mygo.TitleBarHidden, Content: ui.View(app.View)})
		app.SetUpdate(win.Update)
		win.OnClose(func(e *mygo.CloseEvent) {
			if !app.RequestClose(win.Close) {
				e.PreventDefault()
			}
		})
		mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
			if !app.RequestClose(mygo.App.Quit) {
				e.PreventDefault()
			}
		})
		app.SetClipboard(func(text string) error { return mygo.Clipboard.Write(transfer.TextData(text)) })
		app.SetImagePicker(func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: gui.T("picker.image.title"), Message: gui.T("picker.image.message"),
				Filters: []mygo.FileFilter{{Name: gui.T("picker.image.filter"), Extensions: []string{"png", "jpg", "jpeg", "webp", "svg"}}}})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		})
		app.SetFolderPicker(func(title, message string) (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title,
				Message: message, Directory: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		})
		app.SetFilePicker(func(title, message string) (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Title: title, Message: message})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		})
		// The install screen's terminal (docs/UI.md 7.5): Ghostty's
		// emulator, joined to the install's input and output.
		app.SetTerminal(func(conn io.ReadWriteCloser) (*terminal.Terminal, error) {
			return terminal.New(terminal.Options{Conn: conn, NoBlink: true})
		})
		app.Start()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
