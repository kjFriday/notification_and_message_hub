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
	later := ""
	removed, err := Uninstall(p, rec.run, prog.Exe, func(d string) error { later = d; return nil }, "")
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
	later := ""
	removed, err := Uninstall(p, rec.run, p.ExeTarget, func(d string) error { later = d; return nil }, "")
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
	if _, err := Uninstall(p, (&recorder{}).run, prog.Exe, nil, ""); err == nil {
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
	if _, err := Uninstall(p, PowerShell, prog.Exe, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(p.Shortcuts[0].Path)); !os.IsNotExist(err) {
		t.Fatal("Start-menu folder left behind")
	}
}
