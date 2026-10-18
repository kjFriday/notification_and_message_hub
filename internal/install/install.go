// Package install installs or uninstalls an ACME program for the current user (no admin rights).
//
// Install copies the exe and its icon to %LOCALAPPDATA%\Programs\ACME Inc\<name>\ with an install.json
// marker, adds a Start-menu shortcut (optionally a desktop one) and an uninstall entry under
// HKCU\...\CurrentVersion\Uninstall\<app id>, so it shows in Apps & features. Uninstall removes all
// three and never touches the shared database.
//
// Three layers, so tests never touch the real system: Plan (pure), Script (pure PowerShell, fed on
// stdin) and Install/Uninstall with a replaceable Runner. With a sandbox root everything lands under
// it and the registry becomes root\registry.json.
package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	UninstallKey = `HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall`
	Marker       = "install.json"
)

// Program is what gets installed.
type Program struct {
	AppID, Name, Version, Publisher string
	Exe                             string // the running exe, copied as-is
	ExeName                         string // its name once installed, e.g. acme-cms.exe
	Icon                            []byte // app.ico, written beside the exe
}

// Shortcut is one .lnk file.
type Shortcut struct {
	Path, Target, Args, WorkDir, Icon, Description string
}

// Value is one registry value: a string, or a DWORD when IsDWord.
type Value struct {
	Name    string
	Text    string
	Number  int
	IsDWord bool
}

// Plan is everything Install and Uninstall will do, as plain data.
type Plan struct {
	AppID, Name, Version      string
	InstallDir, ProgramsDir   string
	ExeSource, ExeTarget      string
	IconTarget                string
	Icon                      []byte
	Shortcuts                 []Shortcut
	RegistryKey, RegistryFile string // RegistryFile set only in a sandbox
	Registry                  []Value
}

// Locations returns the programs, Start-menu and desktop folders, and the sandbox registry file ("" for real).
func Locations(env func(string) string, root string) (programs, startMenu, desktop, registry string) {
	if root != "" {
		root, _ = filepath.Abs(root)
		return filepath.Join(root, "Programs", "ACME Inc"), filepath.Join(root, "Start Menu", "ACME Inc"),
			filepath.Join(root, "Desktop"), filepath.Join(root, "registry.json")
	}
	home, _ := os.UserHomeDir()
	or := func(k, def string) string {
		if v := env(k); v != "" {
			return v
		}
		return def
	}
	local := or("LOCALAPPDATA", filepath.Join(home, "AppData", "Local"))
	roaming := or("APPDATA", filepath.Join(home, "AppData", "Roaming"))
	return filepath.Join(local, "Programs", "ACME Inc"),
		filepath.Join(roaming, "Microsoft", "Windows", "Start Menu", "Programs", "ACME Inc"),
		filepath.Join(or("USERPROFILE", home), "Desktop"), ""
}

func quote(s string) string {
	if strings.ContainsAny(s, " \t") && !strings.HasPrefix(s, `"`) {
		return `"` + s + `"`
	}
	return s
}

// MakePlan computes the plan. today is passed in so tests are stable.
func MakePlan(p Program, env func(string) string, root string, desktop bool, size int64, today time.Time) Plan {
	programs, startMenu, desk, registry := Locations(env, root)
	dir := filepath.Join(programs, p.Name)
	exe := filepath.Join(dir, p.ExeName)
	icon := filepath.Join(dir, "app.ico")
	uninstall := quote(exe) + " -uninstall"
	if root != "" {
		abs, _ := filepath.Abs(root)
		uninstall += " -root " + quote(abs)
	}
	links := []string{filepath.Join(startMenu, p.Name+".lnk")}
	if desktop {
		links = append(links, filepath.Join(desk, p.Name+".lnk"))
	}
	plan := Plan{AppID: p.AppID, Name: p.Name, Version: p.Version, InstallDir: dir, ProgramsDir: programs,
		ExeSource: p.Exe, ExeTarget: exe, IconTarget: icon, Icon: p.Icon,
		RegistryKey: UninstallKey + `\` + p.AppID, RegistryFile: registry}
	for _, l := range links {
		plan.Shortcuts = append(plan.Shortcuts, Shortcut{Path: l, Target: exe, WorkDir: dir, Icon: icon,
			Description: p.Name + " " + p.Version})
	}
	plan.Registry = []Value{
		{Name: "DisplayName", Text: p.Name + " " + p.Version},
		{Name: "DisplayVersion", Text: p.Version},
		{Name: "Publisher", Text: p.Publisher},
		{Name: "DisplayIcon", Text: icon},
		{Name: "InstallLocation", Text: dir},
		{Name: "UninstallString", Text: uninstall},
		{Name: "QuietUninstallString", Text: uninstall + " -quiet"},
		{Name: "InstallDate", Text: today.Format("20060102")},
		{Name: "NoModify", Number: 1, IsDWord: true},
		{Name: "NoRepair", Number: 1, IsDWord: true},
		{Name: "EstimatedSize", Number: int(size/1024) + 1, IsDWord: true},
	}
	return plan
}

// Literal is a PowerShell single-quoted string: nothing inside is expanded; ' is doubled.
func Literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// Script is the PowerShell that creates (or removes) the shortcuts and, unless sandboxed, the registry entry.
func Script(p Plan, remove bool) string {
	var b strings.Builder
	line := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	line("$ErrorActionPreference = 'Stop'")
	if remove {
		for _, s := range p.Shortcuts {
			line("Remove-Item -LiteralPath %s -Force -ErrorAction SilentlyContinue", Literal(s.Path))
		}
		line("$f = %s; if ((Test-Path -LiteralPath $f) -and -not (Get-ChildItem -LiteralPath $f)) { Remove-Item -LiteralPath $f }",
			Literal(filepath.Dir(p.Shortcuts[0].Path)))
		if p.RegistryFile == "" {
			line("Remove-Item -LiteralPath %s -Recurse -ErrorAction SilentlyContinue", Literal(p.RegistryKey))
		}
		return b.String()
	}
	line("$shell = New-Object -ComObject WScript.Shell")
	for _, s := range p.Shortcuts {
		line("New-Item -ItemType Directory -Force -Path %s | Out-Null", Literal(filepath.Dir(s.Path)))
		line("$l = $shell.CreateShortcut(%s)", Literal(s.Path))
		line("$l.TargetPath = %s", Literal(s.Target))
		line("$l.Arguments = %s", Literal(s.Args))
		line("$l.WorkingDirectory = %s", Literal(s.WorkDir))
		line("$l.IconLocation = %s", Literal(s.Icon+",0"))
		line("$l.Description = %s", Literal(s.Description))
		line("$l.Save()")
	}
	if p.RegistryFile == "" {
		key := Literal(p.RegistryKey)
		line("New-Item -Path %s -Force | Out-Null", key)
		for _, v := range p.Registry {
			kind, val := "String", v.Text
			if v.IsDWord {
				kind, val = "DWord", fmt.Sprint(v.Number)
			}
			line("New-ItemProperty -Path %s -Name %s -Value %s -PropertyType %s -Force | Out-Null", key, Literal(v.Name), Literal(val), kind)
		}
	}
	return b.String()
}

// Runner runs a PowerShell script.
type Runner func(script string) error

// PowerShell is the real Runner: the script goes on stdin, so no quoting reaches the command line.
func PowerShell(script string) error {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Stdin = strings.NewReader(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("PowerShell: %v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

type marker struct {
	AppID     string   `json:"app_id"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Files     []string `json:"files"`
	Shortcuts []string `json:"shortcuts"`
}

func readMarker(dir string) (marker, bool) {
	var m marker
	b, err := os.ReadFile(filepath.Join(dir, Marker))
	return m, err == nil && json.Unmarshal(b, &m) == nil
}

func registryValues(p Plan) map[string]any {
	out := map[string]any{}
	for _, v := range p.Registry {
		if v.IsDWord {
			out[v.Name] = v.Number
		} else {
			out[v.Name] = v.Text
		}
	}
	return out
}

func editRegistryFile(path string, edit func(map[string]any)) error {
	data := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &data)
	}
	edit(data)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(data, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o755)
}

// Install copies the exe and icon, writes the marker, then the shortcuts and registry entry.
// Reinstalling over the same program is fine; a folder owned by another program is refused.
func Install(p Plan, run Runner) error {
	if m, ok := readMarker(p.InstallDir); ok && m.AppID != p.AppID {
		return fmt.Errorf("%s belongs to %s", p.InstallDir, m.AppID)
	}
	if err := os.MkdirAll(p.InstallDir, 0o755); err != nil {
		return err
	}
	same, _ := filepath.Abs(p.ExeSource)
	if !strings.EqualFold(same, p.ExeTarget) {
		if err := copyFile(p.ExeSource, p.ExeTarget); err != nil {
			return fmt.Errorf("copy %s: %w (is it running?)", filepath.Base(p.ExeTarget), err)
		}
	}
	if err := os.WriteFile(p.IconTarget, p.Icon, 0o644); err != nil {
		return err
	}
	var links []string
	for _, s := range p.Shortcuts {
		links = append(links, s.Path)
	}
	m := marker{AppID: p.AppID, Name: p.Name, Version: p.Version,
		Files: []string{filepath.Base(p.ExeTarget), "app.ico"}, Shortcuts: links}
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(p.InstallDir, Marker), b, 0o644); err != nil {
		return err
	}
	if p.RegistryFile != "" {
		if err := editRegistryFile(p.RegistryFile, func(d map[string]any) { d[p.RegistryKey] = registryValues(p) }); err != nil {
			return err
		}
	}
	return run(Script(p, false))
}

// SafeToRemove reports whether InstallDir is under ProgramsDir and its marker names this program.
func SafeToRemove(p Plan) bool {
	dir, _ := filepath.Abs(p.InstallDir)
	programs, _ := filepath.Abs(p.ProgramsDir)
	rel, err := filepath.Rel(programs, dir)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return false
	}
	m, ok := readMarker(dir)
	return ok && m.AppID == p.AppID
}

// Uninstall removes shortcuts, registry entry and install folder. When the running exe lives in that
// folder it removes the rest and returns the folder as later: the caller passes it to DeleteAfterExit
// as its very last action, after any message box. purge, when set, is this program's data folder
// (never the shared database). Returns whether a folder was removed.
func Uninstall(p Plan, run Runner, running string, purge string) (removed bool, later string, err error) {
	m, _ := readMarker(p.InstallDir)
	known := map[string]bool{}
	for _, s := range p.Shortcuts {
		known[s.Path] = true
	}
	for _, extra := range m.Shortcuts {
		if !known[extra] {
			p.Shortcuts = append(p.Shortcuts, Shortcut{Path: extra})
		}
	}
	if err := run(Script(p, true)); err != nil {
		return false, "", err
	}
	if p.RegistryFile != "" {
		if err := editRegistryFile(p.RegistryFile, func(d map[string]any) { delete(d, p.RegistryKey) }); err != nil {
			return false, "", err
		}
	}
	if purge != "" {
		_ = os.RemoveAll(purge)
	}
	if _, err := os.Stat(p.InstallDir); errors.Is(err, os.ErrNotExist) {
		return false, "", nil
	}
	if !SafeToRemove(p) {
		return false, "", fmt.Errorf("refusing to delete %s: no matching %s", p.InstallDir, Marker)
	}
	exe, _ := filepath.Abs(running)
	if strings.EqualFold(filepath.Dir(exe), p.InstallDir) {
		// this exe can't delete itself while it runs: remove the rest now, the folder once we exit
		entries, _ := os.ReadDir(p.InstallDir)
		names := []string{}
		for _, e := range entries {
			if !strings.EqualFold(e.Name(), filepath.Base(exe)) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			_ = os.RemoveAll(filepath.Join(p.InstallDir, n))
		}
		return true, p.InstallDir, nil
	}
	return true, "", os.RemoveAll(p.InstallDir)
}

// DeleteAfterExit starts a hidden cmd that waits a moment, then removes dir (retrying for about 4
// seconds). Start it as this process's very last action: it gives up while the exe still runs.
func DeleteAfterExit(dir string) error {
	rd := fmt.Sprintf(`rd /s /q "%s"`, dir)
	line := fmt.Sprintf(`cmd.exe /d /c "ping -n 3 127.0.0.1 >nul & %s & ping -n 2 127.0.0.1 >nul & %s & ping -n 2 127.0.0.1 >nul & %s"`, rd, rd, rd)
	cmd := exec.Command("cmd.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: line, HideWindow: true, CreationFlags: 0x08000000}
	cmd.Dir = os.TempDir()
	return cmd.Start()
}
