package profile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	cfg, err := Load(path)
	if err != nil || cfg != Defaults() {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	for _, tc := range []struct {
		data  string
		valid bool
	}{
		{"refresh_minutes = 15\nmpv_path = '/usr/bin/mpv'\ntelemetry = false\ntheme = 'solarized-dark'", true},
		{"theme = 'nested/path'", false},
		{"# full TOML string syntax\nmpv_path = '''my player'''", true},
		{"[", false}, {"unknown = true", false}, {"refresh_minutes = 0", false}, {"mpv_path = ''", false}, {"telemetry = true", false}, {"refresh_minutes = 'bad'", false},
	} {
		if err := os.WriteFile(path, []byte(tc.data), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := Load(path)
		if tc.valid && tc.data[0] != '#' && (got.RefreshMinutes != 15 || got.MPVPath != "/usr/bin/mpv" || got.Telemetry) {
			t.Fatalf("overrides not loaded: %+v", got)
		}
		if (err == nil) != tc.valid {
			t.Fatalf("%q: %+v %v", tc.data, got, err)
		}
	}
	if _, err := Load(filepath.Dir(path)); err == nil {
		t.Fatal("read directory succeeded")
	}
}

func TestDetectAndSetCountry(t *testing.T) {
	env := map[string]string{}
	get := func(k string) string { return env[k] }
	if got := DetectCountry(get); got != "us" {
		t.Fatalf("expected default us, got %q", got)
	}
	env["LANG"] = "de_DE.UTF-8"
	if got := DetectCountry(get); got != "de" {
		t.Fatalf("expected de from LANG, got %q", got)
	}
	env["LC_ALL"] = "fr_FR.UTF-8"
	if got := DetectCountry(get); got != "fr" {
		t.Fatalf("expected fr from LC_ALL priority, got %q", got)
	}
	env["LC_ALL"] = "gb"
	if got := DetectCountry(get); got != "gb" {
		t.Fatalf("expected gb, got %q", got)
	}

	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := SetCountry(path, "DE"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Country != "de" {
		t.Fatalf("expected country de, got %+v %v", cfg, err)
	}
	if err := SetCountry(path, "jp"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.Country != "jp" {
		t.Fatalf("expected country jp after update, got %+v %v", cfg, err)
	}
	if err := SetCountry(path, "invalid"); err == nil {
		t.Fatal("expected error for invalid country code")
	}
	if err := SetCountry(path, "12"); err == nil {
		t.Fatal("expected error for numeric country code")
	}
}

func TestSetTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.toml")
	if err := SetTheme(path, "solarized-dark"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil || cfg.Theme != "solarized-dark" {
		t.Fatalf("unexpected theme after creation: %+v %v", cfg, err)
	}
	if err := SetTheme(path, "catppuccin"); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.Theme != "catppuccin" {
		t.Fatalf("unexpected theme after update: %+v %v", cfg, err)
	}
	if err := SetTheme(path, "nested/path"); err == nil {
		t.Fatal("expected error for nested path theme")
	}
}

func TestResolve(t *testing.T) {
	env := map[string]string{}
	get := func(key string) string { return env[key] }
	p, err := Resolve("linux", "/home/test", get)
	if err != nil || p.Config != "/home/test/.config/nomediaplayer/config.toml" || p.State != "/home/test/.local/state/nomediaplayer" || p.Runtime != "/home/test/.local/state/nomediaplayer/run" {
		t.Fatalf("%+v %v", p, err)
	}
	env["XDG_CONFIG_HOME"] = "/config"
	env["XDG_STATE_HOME"] = "/state"
	env["XDG_RUNTIME_DIR"] = "/run/user/1"
	p, err = Resolve("linux", "/home/test", get)
	if err != nil || p.Config != "/config/nomediaplayer/config.toml" || p.State != "/state/nomediaplayer" || p.Runtime != "/run/user/1/nomediaplayer" {
		t.Fatalf("%+v %v", p, err)
	}
	env["XDG_STATE_HOME"] = "relative"
	p, _ = Resolve("linux", "/home/test", get)
	if p.State != "/home/test/.local/state/nomediaplayer" {
		t.Fatal(p)
	}
	p, err = Resolve("darwin", "/Users/test", get)
	if err != nil || p.Runtime != "/Users/test/Library/Application Support/nomediaplayer/run" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := Resolve("windows", "/home/test", get); err == nil {
		t.Fatal("platform accepted")
	}
	if _, err := Resolve("linux", "relative", get); err == nil {
		t.Fatal("relative home accepted")
	}
	if (Paths{Config: "/a/b/config.toml"}).ConfigDir() != "/a/b" {
		t.Fatal("unexpected ConfigDir")
	}
	if (Paths{}).ConfigDir() != "" {
		t.Fatal("unexpected empty ConfigDir")
	}
}

func TestEnvOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	secret := filepath.Join(dir, "token")
	if err := os.WriteFile(secret, []byte("s3cret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	env := map[string]string{
		"NOMEDIAPLAYER_NAVIDROME_URL":       "http://nd.example:4533",
		"NOMEDIAPLAYER_MPD_PORT":            "6601",
		"NOMEDIAPLAYER_FLAC_TO_MP3":         "true",
		"NOMEDIAPLAYER_LIDARR_API_KEY_FILE": secret,
	}
	old := getenv
	getenv = func(k string) string { return env[k] }
	defer func() { getenv = old }()

	// Without a file, env still applies.
	cfg, err := Load(path)
	if err != nil || cfg.NavidromeURL != "http://nd.example:4533" || cfg.MPDPort != 6601 || !cfg.FlacToMP3 || cfg.LidarrAPIKey != "s3cret" {
		t.Fatalf("env without file: %+v %v", cfg, err)
	}
	// Env wins over the file.
	if err := os.WriteFile(path, []byte("navidrome_url = 'http://file'\nlidarr_url = 'http://lidarr'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err = Load(path)
	if err != nil || cfg.NavidromeURL != "http://nd.example:4533" || cfg.LidarrURL != "http://lidarr" {
		t.Fatalf("env over file: %+v %v", cfg, err)
	}
	for k, v := range map[string]string{"NOMEDIAPLAYER_MPD_PORT": "x", "NOMEDIAPLAYER_FLAC_TO_MP3": "maybe", "NOMEDIAPLAYER_START_VIEW": "nope"} {
		env = map[string]string{k: v}
		if _, err := Load(path); err == nil {
			t.Fatalf("%s=%s accepted", k, v)
		}
	}
	if keys := EnvKeys(); len(keys) == 0 || keys[0] != "NOMEDIAPLAYER_REFRESH_MINUTES" {
		t.Fatalf("EnvKeys: %v", keys)
	}
}

func TestVariantAppName(t *testing.T) {
	defer func(old string) { AppName = old }(AppName)
	if IsVariant() || EnvPrefix() != "NOMEDIAPLAYER_" || BusSuffix() != "nomediaplayer" {
		t.Fatal("default name", EnvPrefix(), BusSuffix())
	}
	AppName = "nomediaplayer-dev"
	if !IsVariant() || EnvPrefix() != "NOMEDIAPLAYER_DEV_" || BusSuffix() != "nomediaplayer_dev" {
		t.Fatal("variant name", EnvPrefix(), BusSuffix())
	}
	p, err := Resolve("linux", "/home/test", func(string) string { return "" })
	if err != nil || p.Config != "/home/test/.config/nomediaplayer-dev/config.toml" {
		t.Fatal("variant paths", p, err)
	}
	if err := MigrateLegacy("linux", t.TempDir(), func(string) string { return "" }); err != nil {
		t.Fatal("variant never migrates", err)
	}
}

func TestConfigExpandsHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	in := Defaults()
	in.MusicDir, in.WallpaperColors = "~/Music", "~/c.json"
	cfg, err := validate(in)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MusicDir != filepath.Join(home, "Music") || cfg.WallpaperColors != filepath.Join(home, "c.json") {
		t.Fatalf("%q %q", cfg.MusicDir, cfg.WallpaperColors)
	}
}
