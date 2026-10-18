package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"acme/hub/icon"
	"acme/hub/internal/app"
	"acme/hub/internal/desktop"
	"acme/hub/internal/install"
	"acme/hub/internal/run"
)

const exeName = "acme-notify.exe"

type options struct {
	db, addr, root, smoke              string
	mock, serve, version, inst, uninst bool
	desktopShortcut, quiet, purge      bool
}

func parse(args []string, out io.Writer) (options, error) {
	var o options
	fs := flag.NewFlagSet("acme-notify", flag.ContinueOnError)
	fs.SetOutput(out)
	fs.StringVar(&o.db, "db", "", `shared database (default: ACME_DB, the saved setting, or %LOCALAPPDATA%\ACME Inc\acme.db)`)
	fs.BoolVar(&o.mock, "mock", false, "show the embedded snapshot, not the database")
	fs.BoolVar(&o.serve, "serve", false, "no window: serve the pages on -addr (for development)")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8730", "with -serve: listen address")
	fs.BoolVar(&o.version, "version", false, "print the version")
	fs.BoolVar(&o.inst, "install", false, "install for this user (Start menu, Apps & features)")
	fs.BoolVar(&o.desktopShortcut, "desktop-shortcut", false, "with -install: add a desktop shortcut")
	fs.BoolVar(&o.uninst, "uninstall", false, "uninstall (the database is kept)")
	fs.BoolVar(&o.quiet, "quiet", false, "no message boxes")
	fs.BoolVar(&o.purge, "purge", false, "with -uninstall: also remove this program's settings")
	fs.StringVar(&o.root, "root", "", `sandbox: install under this folder (registry goes to registry.json)`)
	fs.StringVar(&o.smoke, "smoke", "", "open the window, write a JSON report to this file, close")
	return o, fs.Parse(args)
}

// tell prints, and also shows a message box unless -quiet (a windowsgui exe has no console).
func tell(o options, msg string) {
	fmt.Println(msg)
	if !o.quiet {
		desktop.MessageBox(app.Name, msg)
	}
}

func program() (install.Program, error) {
	exe, err := os.Executable()
	if err != nil {
		return install.Program{}, err
	}
	return install.Program{AppID: app.ID, Name: app.Name, Version: app.Version, Publisher: app.Publisher,
		Exe: exe, ExeName: exeName, Icon: icon.ICO}, nil
}

// runInstaller installs or uninstalls: -> a folder to remove once this exe has exited ("" for none).
func runInstaller(o options) (string, error) {
	prog, err := program()
	if err != nil {
		return "", err
	}
	info, _ := os.Stat(prog.Exe)
	var size int64
	if info != nil {
		size = info.Size()
	}
	plan := install.MakePlan(prog, os.Getenv, o.root, o.desktopShortcut, size, time.Now())
	if o.uninst {
		purge := ""
		if o.purge {
			purge = app.DataDir(os.Getenv)
		}
		removed, later, err := install.Uninstall(plan, install.PowerShell, prog.Exe, purge)
		if err != nil {
			return "", err
		}
		msg := app.Name + " uninstalled. The database is kept."
		if !removed {
			msg = app.Name + " was not installed; shortcuts and the uninstall entry are cleared."
		}
		tell(o, msg)
		return later, nil
	}
	if err := install.Install(plan, install.PowerShell); err != nil {
		return "", err
	}
	tell(o, fmt.Sprintf("%s %s installed in %s. Find it in the Start menu under ACME Inc.", app.Name, app.Version, plan.InstallDir))
	return "", nil
}

func main() {
	o, err := parse(os.Args[1:], os.Stderr)
	if err != nil {
		os.Exit(2)
	}
	if o.version {
		tell(o, app.Name+" "+app.Version)
		return
	}
	if o.inst || o.uninst {
		later, err := runInstaller(o)
		if err == nil && later != "" {
			// the very last action, after the message box: the cmd removes the folder once this exe has exited
			err = install.DeleteAfterExit(later)
		}
		if err != nil {
			tell(o, app.Name+": "+err.Error())
			os.Exit(1)
		}
		return
	}
	h := &hub{dbPath: app.ResolveDB(o.db, os.Getenv), forced: o.mock, outDir: filepath.Join(app.DataDir(os.Getenv), "outbox")}
	h.settings = func(p string) error { return app.SaveSettings(os.Getenv, map[string]any{"db": p}) }
	handler := routes(h)
	if o.serve {
		log.Printf("%s on http://%s/ (%s)", app.Title, o.addr, h.source().Text())
		log.Fatal(http.ListenAndServe(o.addr, handler))
	}
	opts := desktop.Options{Title: app.Title, Width: 1040, Height: 720, DataPath: app.DataDir(os.Getenv) + `\WebView2`, Handler: handler}
	if o.smoke != "" {
		seen := make(chan [2]any, 1)
		opts.Bind = map[string]any{"acmeSmoke": func(title string, rows int) { seen <- [2]any{title, rows} }}
		opts.Ready = func(w desktop.Window) { go smoke(o.smoke, h, w, seen) }
	}
	if err := desktop.Run(opts); err != nil {
		tell(o, app.Name+": "+err.Error())
		os.Exit(1)
	}
}

// smoke waits for the page to show posts, then writes the report and closes the window.
func smoke(file string, h *hub, w desktop.Window, seen chan [2]any) {
	time.Sleep(2 * time.Second)
	w.Eval("acmeSmoke(document.title, document.querySelectorAll('tr').length)")
	page := [2]any{"", 0}
	select {
	case page = <-seen:
	case <-time.After(10 * time.Second):
	}
	src := h.source()
	events, _, _ := run.Load(src)
	source := "db"
	if src.Mock {
		source = "mock"
	}
	report := map[string]any{"app_id": app.ID, "version": app.Version, "title": app.Title, "icon_set": w.IconSet(),
		"source": source, "db": h.dbPath, "counts": run.Queue(events), "page_title": page[0], "rows": page[1]}
	b, _ := json.MarshalIndent(report, "", "  ")
	os.WriteFile(file, b, 0o644)
	w.Close()
}
