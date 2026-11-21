// Command dalibre is a desktop e-book manager that reads and writes
// calibre libraries.
package main

import (
	"log"
	"os"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app"
	"dalibre/internal/app/theme"
	"dalibre/internal/settings"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		serveMCP()
		return
	}
	// A file opened while Dalibre runs reaches the running app, which adds
	// it to its library, rather than a second app on the same library.
	if !mygo.App.RequestSingleInstanceLock() {
		return
	}
	if err := theme.RegisterFonts(); err != nil {
		log.Fatal(err)
	}
	a := app.New(settings.DefaultPath())
	app.ApplyAppearance(a.Appearance())
	mygo.App.OnOpenFile(a.OpenFile)
	mygo.App.WhenReady(func() {
		setMenu(a)
		mygo.App.OnLocaleChanged(func(string) { setMenu(a) })
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:           "Dalibre",
			Width:           1440,
			Height:          900,
			MinWidth:        1040,
			MinHeight:       600,
			StateKey:        "main",
			TitleBarStyle:   mygo.TitleBarHiddenInset,
			TitleBarHeight:  48,
			BackgroundColor: "light-dark(#f9f9f8, #111110)",
			Content:         ui.View(a.View),
		})
		a.Attach(win)
		keepRunningOnClose(win)
		// Opening a file shows the window it is added in, even after the
		// window was closed: macOS opens files without the reopen event
		// that OnActivate shows it on.
		show := func() {
			if win.IsMinimized() {
				win.Restore()
			}
			win.Show()
			win.Focus()
		}
		mygo.App.OnSecondInstance(func([]string, string) { show() })
		mygo.App.OnOpenFile(func(string) { show() })
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// keepRunningOnClose keeps the app running on macOS when its window is
// closed: the window hides instead, and clicking the Dock icon shows it
// again. Quitting still closes it.
func keepRunningOnClose(win *mygo.Window) {
	if runtime.GOOS != "darwin" {
		return
	}
	quitting := false
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { quitting = true })
	win.OnClose(func(e *mygo.CloseEvent) {
		if quitting {
			return
		}
		e.PreventDefault()
		win.Hide()
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows {
			win.Show()
		}
	})
}

// setMenu adds "Check for Updates…" after About in the macOS app menu,
// keeping the rest of the default menu bar: it opens Settings › General
// and checks. Other systems have no menu bar by default, so they keep
// none; Settings checks there.
func setMenu(a *app.App) {
	if runtime.GOOS != "darwin" {
		return
	}
	mygo.App.SetMenu(mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			{Label: locales.AppUpdatesMenu(), Click: func(*mygo.MenuItem, *mygo.Window) { a.CheckForUpdates() }},
			mygo.Separator(),
			{Role: mygo.RoleServices},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleFileMenu},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	}))
}
