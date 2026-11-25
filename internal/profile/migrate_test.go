//go:build linux || darwin

package profile

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestMigrateLegacy(t *testing.T) {
	home := t.TempDir()
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	oldCfg := filepath.Join(home, ".config", "github.com/nomedialoaded/nomediaplayer")
	oldState := filepath.Join(home, ".local", "state", "github.com/nomedialoaded/nomediaplayer")
	os.MkdirAll(oldCfg, 0o700)
	os.MkdirAll(oldState, 0o700)
	os.WriteFile(filepath.Join(oldCfg, "config.toml"), []byte("theme = \"wallpaper\"\n"), 0o600)
	os.WriteFile(filepath.Join(oldState, "podmutt.db"), []byte("db"), 0o600)

	// Old TUI still running: refuse, move nothing.
	oldP, _ := resolveNamed(LegacyAppName, "linux", home, get)
	if err := oldP.Prepare(); err != nil {
		t.Fatal(err)
	}
	lock, err := oldP.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacy("linux", home, get); !errors.Is(err, ErrLegacyRunning) {
		t.Fatalf("running legacy TUI: %v", err)
	}
	if exists(filepath.Join(home, ".config", "nomediaplayer")) {
		t.Fatal("moved while old TUI runs")
	}
	lock.Close()

	if err := MigrateLegacy("linux", home, get); err != nil {
		t.Fatal(err)
	}
	newP, _ := Resolve("linux", home, get)
	if data, _ := os.ReadFile(newP.Config); string(data) != "theme = \"wallpaper\"\n" {
		t.Fatal("config not moved")
	}
	if data, _ := os.ReadFile(filepath.Join(newP.State, "podmutt.db")); string(data) != "db" {
		t.Fatal("state not moved")
	}
	if exists(oldCfg) || exists(oldState) {
		t.Fatal("old dirs left behind")
	}
	// Second run: nothing to do; an existing new dir is never overwritten.
	os.MkdirAll(oldCfg, 0o700)
	if err := MigrateLegacy("linux", home, get); err != nil || !exists(oldCfg) {
		t.Fatal("must not touch when new dir exists", err)
	}
}
