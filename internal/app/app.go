// Package app is the Notify hub's identity (name, version, credit) and where it keeps its files.
//
// The shared database is the only link between the ACME programs. Its path is chosen in this order:
// the -db flag, the ACME_DB environment variable, the program's saved setting, then the default
// %LOCALAPPDATA%\ACME Inc\acme.db. The setting lives in %LOCALAPPDATA%\ACME Inc\<app id>\settings.json.
package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

const (
	Version   = "0.2.0"
	ID        = "ACME.Notify"
	Name      = "ACME Notify Hub"
	Title     = "ACME Inc. — " + Name + " " + Version
	Publisher = "ACME Inc. (fictional)"
	Credit    = "Icon: Silk by Mark James (famfamfam.com/lab/icons/silk), CC BY 2.5 " +
		"(creativecommons.org/licenses/by/2.5), enlarged by nearest-neighbour."
	company         = "ACME Inc"
	settingsVersion = 1
)

// Env looks up an environment variable; tests pass a map instead of the real environment.
type Env func(string) string

// LocalAppData is %LOCALAPPDATA%, or ~\AppData\Local when it is unset.
func LocalAppData(env Env) string {
	if v := env("LOCALAPPDATA"); v != "" {
		return v
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "AppData", "Local")
}

// BaseDir is %LOCALAPPDATA%\ACME Inc: the shared database and every program's data folder.
func BaseDir(env Env) string { return filepath.Join(LocalAppData(env), company) }

// DefaultDB is the shared database's default path.
func DefaultDB(env Env) string { return filepath.Join(BaseDir(env), "acme.db") }

// DataDir is this program's own folder (settings, WebView2 profile).
func DataDir(env Env) string { return filepath.Join(BaseDir(env), ID) }

func settingsPath(env Env) string { return filepath.Join(DataDir(env), "settings.json") }

// LoadSettings returns the saved settings, or an empty map when missing or not a JSON object.
func LoadSettings(env Env) map[string]any {
	out := map[string]any{}
	b, err := os.ReadFile(settingsPath(env))
	if err != nil || json.Unmarshal(b, &out) != nil || out == nil {
		return map[string]any{}
	}
	return out
}

// SaveSettings merges changes into settings.json, keeping unknown keys; written to a temp file, then renamed.
func SaveSettings(env Env, changes map[string]any) error {
	data := LoadSettings(env)
	for k, v := range changes {
		data[k] = v
	}
	data["version"] = settingsVersion
	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}
	path := settingsPath(env)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ResolveDB picks the shared database: flag, then ACME_DB, then the saved setting, then the default.
func ResolveDB(flag string, env Env) string {
	saved, _ := LoadSettings(env)["db"].(string)
	for _, choice := range []string{flag, env("ACME_DB"), saved} {
		if c := strings.TrimSpace(choice); c != "" {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs
			}
			return c
		}
	}
	return DefaultDB(env)
}
