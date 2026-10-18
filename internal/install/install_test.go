// ==== ACME Inc. portfolio ===============================================
// Project: notification_and_message_hub | File: internal/install/install_test.go
// About: Tests for package install.
// Copyright (c) 2026 kjFriday | SPDX-License-Identifier: AGPL-3.0-only
// You may change this file under the AGPL-3.0. The stamp at the end marks
// the version kjFriday wrote. Verify with the portfolio's stamp.py --check.
// Reused code, if any, is mapped in PROVENANCE.md.
// ========================================================================

package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type recorder struct{ scripts []string }

func (r *recorder) run(s string) error { r.scripts = append(r.scripts, s); return nil }

func setup(t *testing.T) (Program, string) {
	t.Helper()
	dir := t.TempDir()
	exe := filepath.Join(dir, "build", "acme-test.exe")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("MZ fake exe"), 0o755)
	return Program{AppID: "ACME.Test", Name: "ACME Test App", Version: "0.2.0", Publisher: "ACME Inc. (fictional)",
		Exe: exe, ExeName: "acme-test.exe", Icon: []byte{0, 0, 1, 0}}, filepath.Join(dir, "sandbox")
}

var day = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

func noEnv(string) string { return "" }

func value(p Plan, name string) Value {
	for _, v := range p.Registry {
		if v.Name == name {
			return v
		}
	}
	return Value{}
}

func TestPlan(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, true, 4096, day)
	if p.InstallDir != filepath.Join(root, "Programs", "ACME Inc", "ACME Test App") {
		t.Fatalf("install dir %q", p.InstallDir)
	}
	if len(p.Shortcuts) != 2 || p.Shortcuts[0].Target != p.ExeTarget || !strings.HasSuffix(p.Shortcuts[0].Icon, "app.ico") {
		t.Fatalf("shortcuts %+v", p.Shortcuts)
	}
	if v := value(p, "DisplayVersion"); v.Text != "0.2.0" {
		t.Fatalf("version %+v", v)
	}
	if v := value(p, "InstallDate"); v.Text != "20261017" {
		t.Fatalf("date %+v", v)
	}
	if v := value(p, "NoModify"); !v.IsDWord || v.Number != 1 {
		t.Fatalf("NoModify %+v", v)
	}
	if v := value(p, "EstimatedSize"); v.Number != 5 {
		t.Fatalf("size %+v", v)
	}
	if u := value(p, "UninstallString").Text; !strings.Contains(u, " -uninstall -root ") {
		t.Fatalf("uninstall %q", u)
	}
	if p.RegistryKey != UninstallKey+`\ACME.Test` {
		t.Fatalf("key %q", p.RegistryKey)
	}
}

func TestRealLocations(t *testing.T) {
	env := map[string]string{"LOCALAPPDATA": `C:\L`, "APPDATA": `C:\R`, "USERPROFILE": `C:\U`}
	programs, start, _, reg := Locations(func(k string) string { return env[k] }, "")
	if programs != `C:\L\Programs\ACME Inc` || start != `C:\R\Microsoft\Windows\Start Menu\Programs\ACME Inc` || reg != "" {
		t.Fatalf("%q %q %q", programs, start, reg)
	}
}

func TestScriptQuotingAndRegistry(t *testing.T) {
	prog, _ := setup(t)
	env := map[string]string{"LOCALAPPDATA": `C:\O'Brien`, "APPDATA": `C:\R`}
	p := MakePlan(prog, func(k string) string { return env[k] }, "", false, 1, day)
	s := Script(p, false)
	for _, want := range []string{
		`'C:\O''Brien\Programs\ACME Inc\ACME Test App\app.ico,0'`,
		`-Name 'NoModify' -Value '1' -PropertyType DWord`,
		`-Name 'DisplayVersion' -Value '0.2.0' -PropertyType String`,
		`New-Item -Path 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ACME.Test' -Force`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %s", want)
		}
	}
	r := Script(p, true)
	if !strings.Contains(r, "Remove-Item -LiteralPath 'HKCU:") || strings.Contains(r, "CreateShortcut") {
		t.Fatal("removal script is wrong")
	}
}

func TestSandboxNeverTouchesRegistry(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	if strings.Contains(Script(p, false), "HKCU") || strings.Contains(Script(p, true), "HKCU") {
		t.Fatal("sandbox script writes the registry")
	}
}

func TestRoundTrip(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, true, 1, day)
	rec := &recorder{}
	db := filepath.Join(root, "acme.db")
	os.MkdirAll(root, 0o755)
	os.WriteFile(db, nil, 0o644)
	if err := Install(p, rec.run); err != nil {
		t.Fatal(err)
	}
	if err := Install(p, rec.run); err != nil { // reinstall
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p.ExeTarget); string(b) != "MZ fake exe" {
		t.Fatal("exe not copied")
	}
	var reg map[string]map[string]any
	b, _ := os.ReadFile(p.RegistryFile)
	json.Unmarshal(b, &reg)
	if reg[p.RegistryKey]["DisplayVersion"] != "0.2.0" {
		t.Fatalf("registry %v", reg)
	}
	removed, later, err := Uninstall(p, rec.run, prog.Exe, "")
	if err != nil || !removed || later != "" {
		t.Fatalf("uninstall: %v %v %q", removed, err, later)
	}
	if _, err := os.Stat(p.InstallDir); !os.IsNotExist(err) {
		t.Fatal("install dir left behind")
	}
	if _, err := os.Stat(db); err != nil {
		t.Fatal("the database was removed")
	}
	b, _ = os.ReadFile(p.RegistryFile)
	if strings.TrimSpace(string(b)) != "{}" {
		t.Fatalf("registry left: %s", b)
	}
	if len(rec.scripts) != 3 {
		t.Fatalf("%d scripts", len(rec.scripts))
	}
}

func TestRunningFromInstallDirDeletesLater(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	rec := &recorder{}
	Install(p, rec.run)
	// Uninstall only names the folder: the caller starts DeleteAfterExit once its message box is gone
	removed, later, err := Uninstall(p, rec.run, p.ExeTarget, "")
	if err != nil || !removed || later != p.InstallDir {
		t.Fatalf("%v %v %q", removed, err, later)
	}
	entries, _ := os.ReadDir(p.InstallDir)
	if len(entries) != 1 || entries[0].Name() != "acme-test.exe" {
		t.Fatalf("left: %v", entries)
	}
}

func TestGuardRefusesForeignFolder(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	os.MkdirAll(p.InstallDir, 0o755)
	os.WriteFile(filepath.Join(p.InstallDir, "keep.txt"), []byte("not ours"), 0o644)
	if _, _, err := Uninstall(p, (&recorder{}).run, prog.Exe, ""); err == nil {
		t.Fatal("removed a folder without a marker")
	}
	os.WriteFile(filepath.Join(p.InstallDir, Marker), []byte(`{"app_id":"ACME.Other"}`), 0o644)
	if err := Install(p, (&recorder{}).run); err == nil {
		t.Fatal("installed over another program")
	}
	if _, err := os.Stat(filepath.Join(p.InstallDir, "keep.txt")); err != nil {
		t.Fatal("foreign file deleted")
	}
}

func TestRealShortcutInSandbox(t *testing.T) {
	if testing.Short() {
		t.Skip("runs PowerShell")
	}
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	if err := Install(p, PowerShell); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p.Shortcuts[0].Path); err != nil {
		t.Fatal("no shortcut:", err)
	}
	if _, _, err := Uninstall(p, PowerShell, prog.Exe, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(p.Shortcuts[0].Path)); !os.IsNotExist(err) {
		t.Fatal("Start-menu folder left behind")
	}
}

// wizardInstall lays out what a setup made by ACME Wizard Maker leaves: its files, uninstall.exe and a
// marker with sha256 and installer, then changes notes.txt as a user would.
func wizardInstall(t *testing.T, p Plan, appID string) {
	t.Helper()
	os.MkdirAll(p.InstallDir, 0o755)
	files := []string{"acme-test.exe", "notes.txt", "app.ico", WizardUninstaller}
	for _, f := range files {
		os.WriteFile(filepath.Join(p.InstallDir, f), []byte("as installed"), 0o644)
	}
	b, _ := json.Marshal(map[string]any{"app_id": appID, "name": p.Name, "version": p.Version, "files": files,
		"shortcuts": []string{}, "sha256": map[string]string{"notes.txt": "e3b0"}, "installer": "ACME Wizard Maker 0.1.0"})
	os.WriteFile(filepath.Join(p.InstallDir, Marker), b, 0o644)
	os.WriteFile(filepath.Join(p.InstallDir, "notes.txt"), []byte("changed by the user"), 0o644)
}

func TestWizardInstallIsTheUninstallersToRemove(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	if Wizard(p) != "" {
		t.Fatal("no install yet")
	}
	Install(p, (&recorder{}).run)
	if Wizard(p) != "" {
		t.Fatal("the program's own install is not wizard-made")
	}
	wizardInstall(t, p, "ACME.Other")
	if Wizard(p) != "" {
		t.Fatal("another program's uninstaller would run")
	}
	wizardInstall(t, p, "ACME.Test")
	if Wizard(p) != filepath.Join(p.InstallDir, "uninstall.exe") {
		t.Fatalf("uninstaller %q", Wizard(p))
	}
}

func TestUninstallArgs(t *testing.T) {
	if a := UninstallArgs(false, false, ""); len(a) != 0 {
		t.Fatalf("%q", a)
	}
	abs, _ := filepath.Abs("sb")
	if a := strings.Join(UninstallArgs(true, true, "sb"), "|"); a != "-quiet|-purge|-root|"+abs {
		t.Fatalf("%q", a)
	}
}

func TestRunUninstallerKeepsChangedFiles(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	wizardInstall(t, p, "ACME.Test")
	var got []string
	fake := func(exe string, args []string) (int, string, error) {
		got = append([]string{exe}, args...)
		return 4, "Setup or uninstall of ACME Test App is already running.", nil
	}
	code, said, err := RunUninstaller(p, prog.Exe, UninstallArgs(true, false, root), fake)
	if err != nil || code != 4 || !strings.Contains(said, "already running") {
		t.Fatalf("%d %q %v", code, said, err)
	}
	abs, _ := filepath.Abs(root)
	if strings.Join(got, "|") != filepath.Join(p.InstallDir, "uninstall.exe")+"|-quiet|-root|"+abs {
		t.Fatalf("ran %q", got)
	}
	if b, _ := os.ReadFile(filepath.Join(p.InstallDir, "notes.txt")); string(b) != "changed by the user" {
		t.Fatal("the changed file was touched")
	}
	cancel := func(string, []string) (int, string, error) { return Cancelled, "", nil }
	if code, said, _ := RunUninstaller(p, prog.Exe, nil, cancel); code != Cancelled || said != "Uninstall cancelled." {
		t.Fatalf("cancel: %d %q", code, said)
	}
}

func TestRunUninstallerRefusesFromTheFolderItRemoves(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	wizardInstall(t, p, "ACME.Test")
	ran := false
	fake := func(string, []string) (int, string, error) { ran = true; return 0, "", nil }
	if _, _, err := RunUninstaller(p, strings.ToUpper(p.ExeTarget), nil, fake); err == nil || ran {
		t.Fatalf("ran the uninstaller from inside: %v", err)
	}
	if _, _, err := RunUninstaller(MakePlan(prog, noEnv, filepath.Join(root, "x"), false, 1, day), prog.Exe, nil, fake); err == nil || ran {
		t.Fatal("ran an uninstaller that is not there")
	}
}

// entry writes the sandbox uninstall entry with InstallLocation dir, as a setup would.
func entry(t *testing.T, p Plan, dir string) {
	t.Helper()
	if err := editRegistryFile(p.RegistryFile, func(d map[string]any) { d[p.RegistryKey] = map[string]any{"InstallLocation": dir} }); err != nil {
		t.Fatal(err)
	}
}

func TestLocatedFindsAWizardInstallElsewhere(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	none := func(string) (string, error) { t.Fatal("read the real registry in a sandbox"); return "", nil }
	if got, err := Located(p, none); err != nil || got.InstallDir != p.InstallDir {
		t.Fatalf("no entry: %q %v", got.InstallDir, err)
	}
	elsewhere := p
	elsewhere.InstallDir = filepath.Join(root, "Apps", "Journal")
	entry(t, p, elsewhere.InstallDir)
	if got, err := Located(p, none); err != nil || got.InstallDir != p.InstallDir {
		t.Fatalf("a stale entry: %q %v", got.InstallDir, err)
	}
	wizardInstall(t, elsewhere, "ACME.Test")
	got, err := Located(p, none)
	if err != nil || got.InstallDir != elsewhere.InstallDir || Wizard(got) != filepath.Join(elsewhere.InstallDir, "uninstall.exe") {
		t.Fatalf("%q %v", got.InstallDir, err)
	}
	var ran []string
	fake := func(exe string, args []string) (int, string, error) {
		ran = append([]string{exe}, args...)
		return 0, "", nil
	}
	if code, _, err := RunUninstaller(got, prog.Exe, UninstallArgs(true, false, root), fake); err != nil || code != 0 || ran[0] != Wizard(got) {
		t.Fatalf("%d %v %q", code, err, ran)
	}
}

func TestLocatedNeverClearsAnInstallElsewhere(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, root, false, 1, day)
	elsewhere := p
	elsewhere.InstallDir = filepath.Join(root, "Apps", "Journal")
	entry(t, p, elsewhere.InstallDir)
	none := func(string) (string, error) { return "", nil }
	for _, id := range []string{"ACME.Other", ""} {
		wizardInstall(t, elsewhere, id)
		if id == "" {
			os.Remove(filepath.Join(elsewhere.InstallDir, Marker))
		}
		if got, err := Located(p, none); err == nil || got.InstallDir != p.InstallDir {
			t.Fatalf("%q: %q %v", id, got.InstallDir, err)
		}
	}
	if b, _ := os.ReadFile(p.RegistryFile); !strings.Contains(string(b), "InstallLocation") {
		t.Fatal("the entry was cleared")
	}
}

func TestLocatedReadsTheRealRegistry(t *testing.T) {
	prog, root := setup(t)
	p := MakePlan(prog, noEnv, "", false, 1, day)
	dir := filepath.Join(root, "Apps", "Journal")
	var script string
	read := func(s string) (string, error) { script = s; return dir, nil }
	wizardInstall(t, Plan{InstallDir: dir, Name: p.Name, Version: p.Version}, "ACME.Test")
	got, err := Located(p, read)
	if err != nil || got.InstallDir != dir {
		t.Fatalf("%q %v", got.InstallDir, err)
	}
	if !strings.Contains(script, `Get-ItemProperty -LiteralPath 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\ACME.Test' -Name InstallLocation`) {
		t.Fatalf("script %s", script)
	}
	if _, err := Located(p, func(string) (string, error) { return "", os.ErrPermission }); err == nil {
		t.Fatal("a failed read was ignored")
	}
	if got, _ := Located(p, func(string) (string, error) { return "Apps", nil }); got.InstallDir != p.InstallDir {
		t.Fatal("followed a relative InstallLocation")
	}
}

func TestPowerShellOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("runs PowerShell")
	}
	if out, err := PowerShellOutput("[Console]::OutputEncoding = [Text.Encoding]::UTF8\n'M' + [char]0xF6 + 'tley'\n"); err != nil || out != "Mötley" {
		t.Fatalf("%q %v", out, err)
	}
	prog, _ := setup(t)
	p := MakePlan(prog, noEnv, "", false, 1, day)
	p.RegistryKey = UninstallKey + `\ACME.NoSuchProgram.Test`
	if loc, err := InstallLocation(p, PowerShellOutput); err != nil || loc != "" {
		t.Fatalf("%q %v", loc, err)
	}
}

func TestRunAndWaitReturnsTheExitCode(t *testing.T) {
	code, said, err := RunAndWait("cmd.exe", []string{"/d", "/c", "echo kept & exit 4"})
	if err != nil || code != 4 || said != "kept" {
		t.Fatalf("%d %q %v", code, said, err)
	}
	if _, _, err := RunAndWait(filepath.Join(t.TempDir(), "uninstall.exe"), nil); err == nil {
		t.Fatal("a missing exe ran")
	}
}

// ==== stamp v1 | author=kjFriday | hmac-sha256=799513a8c560878f28beb1390111f45afe8f03298d4a8d102d927810d9b42adb ====
