// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: internal/install/install.go
// About: Package install installs or uninstalls an ACME program for the current user (no admin...
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

// Package install installs or uninstalls an ACME program for the current user (no admin rights).
//
// Install copies the exe and its icon to %LOCALAPPDATA%\Programs\ACME Inc\<name>\ with an install.json
// marker, adds a Start-menu shortcut (optionally a desktop one) and an uninstall entry under
// HKCU\...\CurrentVersion\Uninstall\<app id>, so it shows in Apps & features. Uninstall removes all
// three and never touches the shared database. An install that a setup made by ACME Wizard Maker left
// (its marker names the installer), here or in the folder its uninstall entry names, is removed by its
// own uninstall.exe instead, which keeps the files changed since: see Located and Wizard.
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
	// WizardUninstaller is what a setup made by ACME Wizard Maker leaves in the install folder.
	WizardUninstaller = "uninstall.exe"
	// Cancelled is that uninstaller's exit code when the user cancels it in its window.
	Cancelled = 3
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
	_, err := PowerShellOutput(script)
	return err
}

// Reader runs a PowerShell script: -> what it printed.
type Reader func(script string) (string, error)

// PowerShellOutput is the real Reader, run like PowerShell.
func PowerShellOutput(script string) (string, error) {
	cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", "-")
	cmd.Stdin = strings.NewReader(script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	var out, errs strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errs
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("PowerShell: %v: %s", err, strings.TrimSpace(out.String()+errs.String()))
	}
	return strings.TrimSpace(out.String()), nil
}

type marker struct {
	AppID     string   `json:"app_id"`
	Name      string   `json:"name"`
	Version   string   `json:"version"`
	Files     []string `json:"files"`
	Shortcuts []string `json:"shortcuts"`
	Installer string   `json:"installer,omitempty"` // written by ACME Wizard Maker setups, with a sha256 map
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

// Wizard is the uninstaller of an install that a setup made by ACME Wizard Maker left in p.InstallDir
// (its marker names this program and an installer), or "" for this program's own install. Such an
// install is removed by that uninstaller, which takes away only the files still as installed.
func Wizard(p Plan) string {
	m, ok := readMarker(p.InstallDir)
	if !ok || m.AppID != p.AppID || m.Installer == "" {
		return ""
	}
	return filepath.Join(p.InstallDir, WizardUninstaller)
}

// InstallLocation is the folder the uninstall entry names, or "": from root\registry.json in a sandbox,
// else read from the registry through PowerShell.
func InstallLocation(p Plan, read Reader) (string, error) {
	if p.RegistryFile != "" {
		data := map[string]map[string]any{}
		if b, err := os.ReadFile(p.RegistryFile); err == nil {
			_ = json.Unmarshal(b, &data)
		}
		loc, _ := data[p.RegistryKey]["InstallLocation"].(string)
		return loc, nil
	}
	return read(fmt.Sprintf("[Console]::OutputEncoding = [Text.Encoding]::UTF8\n"+
		"(Get-ItemProperty -LiteralPath %s -Name InstallLocation -ErrorAction SilentlyContinue).InstallLocation\n",
		Literal(p.RegistryKey)))
}

// Located is p aimed at the install its uninstall entry names when that lies outside p.InstallDir: a
// setup made by ACME Wizard Maker installs where the user picks. Only such an install (see Wizard) is
// handed on; any other folder still there is refused, so its entry and shortcuts are never cleared.
func Located(p Plan, read Reader) (Plan, error) {
	loc, err := InstallLocation(p, read)
	if err != nil || !filepath.IsAbs(loc) {
		return p, err
	}
	loc = filepath.Clean(loc)
	if strings.EqualFold(loc, filepath.Clean(p.InstallDir)) {
		return p, nil
	}
	if _, err := os.Stat(loc); errors.Is(err, os.ErrNotExist) {
		return p, nil // a stale entry: clearing it orphans nothing
	}
	there := p
	there.InstallDir = loc
	if Wizard(there) == "" {
		return p, fmt.Errorf("%s is installed in %s, which this copy cannot uninstall: use Apps & features", p.Name, loc)
	}
	return there, nil
}

// UninstallArgs are the flags -uninstall passes on to that uninstaller.
func UninstallArgs(quiet, purge bool, root string) []string {
	args := []string{}
	if quiet {
		args = append(args, "-quiet")
	}
	if purge {
		args = append(args, "-purge")
	}
	if root != "" {
		abs, _ := filepath.Abs(root)
		args = append(args, "-root", abs)
	}
	return args
}

// Exec runs a program and waits for it: -> its exit code and what it printed.
type Exec func(exe string, args []string) (int, string, error)

// RunAndWait is the real Exec. The program starts in the temp folder, so it never holds the folder it removes.
func RunAndWait(exe string, args []string) (int, string, error) {
	cmd := exec.Command(exe, args...)
	cmd.Dir = os.TempDir()
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		return exit.ExitCode(), strings.TrimSpace(string(out)), nil
	}
	return 0, strings.TrimSpace(string(out)), err
}

// RunUninstaller removes a wizard-made install (see Wizard) through its uninstaller, run with args;
// -> its exit code (0 done, Cancelled, ...) and how it went. It refuses while running lies in the
// install folder: Windows cannot delete a running exe, so the uninstaller would stop half-way.
func RunUninstaller(p Plan, running string, args []string, run Exec) (int, string, error) {
	exe := Wizard(p)
	if exe == "" {
		return 0, "", fmt.Errorf("%s holds no wizard-made install of %s", p.InstallDir, p.Name)
	}
	self, _ := filepath.Abs(running)
	if rel, err := filepath.Rel(p.InstallDir, self); err == nil && filepath.IsLocal(rel) {
		return 0, "", fmt.Errorf("this copy runs from %s, which its uninstaller removes: uninstall %s from Apps & features, or run %s",
			p.InstallDir, p.Name, exe)
	}
	code, said, err := run(exe, args)
	if err != nil {
		return 0, "", fmt.Errorf("run %s: %w", exe, err)
	}
	if said == "" && code == Cancelled {
		said = "Uninstall cancelled."
	} else if said == "" && code != 0 {
		said = fmt.Sprintf("%s exited with %d", WizardUninstaller, code)
	}
	return code, said, nil
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

// ==== stamp v1 | author=kjFriday | hmac-sha256=b019f82770c4e4df42c115720fb685d4326f4936c1ba92e2a0eca11fcace7bec ====
