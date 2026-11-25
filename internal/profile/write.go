package profile

// nml: writing config.toml from the setup wizard. Values are updated in
// place (comments and other keys stay), the file is always 0600 and its
// directory 0700, because it can hold passwords and tokens.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/BurntSushi/toml"
)

// Keys lists the supported config.toml keys in struct order.
func Keys() []string {
	t := reflect.TypeOf(Config{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if tag := t.Field(i).Tag.Get("toml"); tag != "" && tag != "telemetry" {
			keys = append(keys, tag)
		}
	}
	return keys
}

// Setting is one key = value pair for SetKeys; Value is a string, bool or int.
type Setting struct {
	Key   string
	Value any
}

// SetKeys updates (or appends) the given keys in config.toml and leaves the
// rest of the file alone. Unknown keys are refused so a typo cannot make the
// config unreadable.
func SetKeys(path string, settings []Setting) error {
	known := map[string]bool{}
	for _, k := range Keys() {
		known[k] = true
	}
	lines := map[string]string{}
	for _, s := range settings {
		if !known[s.Key] {
			return fmt.Errorf("profile: unknown config key %q", s.Key)
		}
		var buf bytes.Buffer
		if err := toml.NewEncoder(&buf).Encode(map[string]any{s.Key: s.Value}); err != nil {
			return err
		}
		lines[s.Key] = strings.TrimSpace(buf.String())
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	_ = os.Chmod(filepath.Dir(path), 0700)
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var out []string
	if len(data) > 0 {
		out = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	done := map[string]bool{}
	for i, line := range out {
		key, _, ok := strings.Cut(strings.TrimSpace(line), "=")
		key = strings.TrimSpace(key)
		if !ok || strings.HasPrefix(key, "#") || lines[key] == "" || done[key] {
			continue
		}
		out[i], done[key] = lines[key], true
	}
	for _, s := range settings {
		if !done[s.Key] {
			out, done[s.Key] = append(out, lines[s.Key]), true
		}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(out, "\n")+"\n"), 0600); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Exists reports whether a config file is present.
func (p Paths) ConfigExists() bool {
	_, err := os.Stat(p.Config)
	return err == nil
}

// EnvConfigured reports whether any NOMEDIAPLAYER_* setting comes from the
// environment (then the setup wizard does not start by itself).
func EnvConfigured() bool {
	for _, k := range EnvKeys() {
		if getenv(k) != "" || getenv(k+"_FILE") != "" {
			return true
		}
	}
	return false
}

// TightenConfig makes an existing config.toml private (0600, dir 0700); it
// reports whether anything had to change.
func (p Paths) TightenConfig() bool {
	changed := false
	if st, err := os.Stat(p.Config); err == nil && st.Mode().Perm()&0077 != 0 {
		changed = os.Chmod(p.Config, 0600) == nil
	}
	if st, err := os.Stat(p.ConfigDir()); err == nil && st.Mode().Perm()&0077 != 0 {
		changed = os.Chmod(p.ConfigDir(), 0700) == nil || changed
	}
	return changed
}
