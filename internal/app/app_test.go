package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeEnv(dir string, extra map[string]string) Env {
	return func(k string) string {
		if k == "LOCALAPPDATA" {
			return dir
		}
		return extra[k]
	}
}

func TestDefaultIsShared(t *testing.T) {
	dir := t.TempDir()
	env := fakeEnv(dir, nil)
	if got, want := ResolveDB("", env), filepath.Join(dir, "ACME Inc", "acme.db"); got != want {
		t.Fatalf("default = %q, want %q", got, want)
	}
	if got := DataDir(env); got != filepath.Join(dir, "ACME Inc", "ACME.Notify") {
		t.Fatalf("data dir = %q", got)
	}
}

func TestPrecedence(t *testing.T) {
	dir := t.TempDir()
	extra := map[string]string{}
	env := fakeEnv(dir, extra)
	if err := SaveSettings(env, map[string]any{"db": filepath.Join(dir, "saved.db")}); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ flag, envDB, want string }{
		{"", "", "saved.db"},
		{"", filepath.Join(dir, "env.db"), "env.db"},
		{filepath.Join(dir, "flag.db"), filepath.Join(dir, "env.db"), "flag.db"},
		{"", "   ", "saved.db"},
	}
	for _, c := range cases {
		extra["ACME_DB"] = c.envDB
		if got := ResolveDB(c.flag, env); !strings.HasSuffix(got, c.want) {
			t.Errorf("flag %q env %q -> %q, want *%s", c.flag, c.envDB, got, c.want)
		}
	}
}

func TestSettingsKeepUnknownKeysAndTolerateJunk(t *testing.T) {
	env := fakeEnv(t.TempDir(), nil)
	if err := os.MkdirAll(DataDir(env), 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(settingsPath(env), []byte("{not json"), 0o644)
	if len(LoadSettings(env)) != 0 {
		t.Fatal("junk settings were not ignored")
	}
	SaveSettings(env, map[string]any{"db": "a.db", "theme": "dark"})
	SaveSettings(env, map[string]any{"db": "b.db"})
	s := LoadSettings(env)
	if s["db"] != "b.db" || s["theme"] != "dark" || s["version"] != float64(1) {
		t.Fatalf("settings = %v", s)
	}
	entries, _ := os.ReadDir(DataDir(env))
	if len(entries) != 1 {
		t.Fatalf("left behind: %v", entries)
	}
}

func TestTitleCarriesTheVersion(t *testing.T) {
	if !strings.HasSuffix(Title, " "+Version) || !strings.Contains(Credit, "CC BY 2.5") {
		t.Fatalf("title %q credit %q", Title, Credit)
	}
}
