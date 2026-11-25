// Package profile defines configuration, private paths, and TUI ownership.
// CLI clients resolve paths without preparing directories or acquiring ownership.
package profile

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"syscall"

	"github.com/BurntSushi/toml"
	"github.com/nomedialoaded/nomediaplayer/internal/feedback"
)

type Config struct {
	RefreshMinutes   int    `toml:"refresh_minutes"`
	MPVPath          string `toml:"mpv_path"`
	Telemetry        bool   `toml:"telemetry"`
	Icons            string `toml:"icons"`
	Theme            string `toml:"theme"`
	PlayerDecoration bool   `toml:"player_decoration"`
	Country          string `toml:"country"`
	// nml: resume the unfinished episode automatically at startup (default off).
	ResumeOnStart bool `toml:"resume_on_start"`
	// nml: MPD server for the Music view.
	MPDHost string `toml:"mpd_host"`
	MPDPort int    `toml:"mpd_port"`
	// nml: Navidrome (Subsonic API) for playlists and favorites; empty URL = off.
	NavidromeURL      string `toml:"navidrome_url"`
	NavidromeUser     string `toml:"navidrome_user"`
	NavidromePassword string `toml:"navidrome_password"`
	// nml: view at startup: "music" (default) or "podcasts" ("muziek" = music).
	StartView string `toml:"start_view"`
	// nml: lyrics. MusicDir is MPD's music_directory (for .lrc files next to
	// the songs; empty = read from ~/.config/mpd/mpd.conf). LyricsOnline
	// asks lrclib.net for songs without .lrc (default on).
	MusicDir     string `toml:"music_dir"`
	LyricsOnline bool   `toml:"lyrics_online"`
	// nml: Lidarr (API v1) to add artists/albums and follow downloads; empty
	// URL = off.
	LidarrURL    string `toml:"lidarr_url"`
	LidarrAPIKey string `toml:"lidarr_api_key"`
	// nml: convert FLAC that Lidarr imports to MP3 320 with the local ffmpeg.
	FlacToMP3 bool `toml:"flac_to_mp3"`
	// nml: Home Assistant (Music Assistant speakers, hand-off); empty = off.
	HAURL   string `toml:"ha_url"`
	HAToken string `toml:"ha_token"`
	// Music Assistant server API (take a whole speaker queue back).
	MAURL   string `toml:"ma_url"`
	MAToken string `toml:"ma_token"`
	// nml: Podcasts switches the whole podcast side (M) on or off.
	// PodcastDirectory allows the Apple Podcasts directory (search, charts,
	// ratings); off = subscribe by RSS URL or OPML only, nothing goes to Apple.
	Podcasts         bool `toml:"podcasts"`
	PodcastDirectory bool `toml:"podcast_directory"`
	// nml: colour file for theme = "wallpaper": a JSON object with Material
	// colour roles (primary, surface, onSurface, … or on_surface, …) that a
	// wallpaper tool writes. Empty = the wallpaper theme is not offered.
	WallpaperColors string `toml:"wallpaper_colors"`
	// nml: Podcast Index (podcastindex.org) as an extra podcast search
	// source; your own free key and secret. Empty = off. Also as
	// NOMEDIAPLAYER_PODCAST_INDEX_KEY / _SECRET (env wins).
	PodcastIndexKey    string `toml:"podcast_index_key"`
	PodcastIndexSecret string `toml:"podcast_index_secret"`
}

func Defaults() Config {
	return Config{RefreshMinutes: 30, MPVPath: "mpv", Icons: "unicode", Theme: "colors", Country: DetectCountry(os.Getenv), MPDHost: "127.0.0.1", MPDPort: 6600, StartView: "music", LyricsOnline: true, Podcasts: true, PodcastDirectory: true}
}

// Load applies a TOML file over defaults. An absent file uses defaults.
func Load(path string) (Config, error) {
	cfg := Defaults()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := applyEnv(&cfg, getenv); err != nil {
			return Config{}, err
		}
		return validate(cfg)
	}
	if err != nil {
		return Config{}, &feedback.Fault{Code: feedback.Configuration, Safe: "config.toml could not be read. Check that it is a readable file, not a directory.", Cause: err}
	}
	md, err := toml.Decode(string(data), &cfg)
	if err != nil {
		return Config{}, &feedback.Fault{Code: feedback.Configuration, Safe: "The TOML syntax or a setting's value type is invalid. Check quotes, value types and line syntax.", Cause: err}
	}
	if len(md.Undecoded()) != 0 {
		return Config{}, feedback.New(feedback.Configuration, "An unknown option is present. Supported keys: "+strings.Join(Keys(), ", ")+".")
	}
	if err := applyEnv(&cfg, getenv); err != nil {
		return Config{}, err
	}
	return validate(cfg)
}

func validate(cfg Config) (Config, error) {
	if cfg.RefreshMinutes < 1 || cfg.MPVPath == "" {
		return Config{}, feedback.New(feedback.Configuration, "refresh_minutes must be positive (for example 30); mpv_path must be nonempty (for example mpv).")
	}
	if cfg.Icons != "unicode" && cfg.Icons != "nerd" && cfg.Icons != "plain" {
		return Config{}, feedback.New(feedback.Configuration, "icons must be unicode, nerd or plain.")
	}
	if cfg.Theme == "" {
		cfg.Theme = "colors"
	}
	cfg.MusicDir, cfg.WallpaperColors = expandHome(cfg.MusicDir), expandHome(cfg.WallpaperColors)
	if filepath.Base(cfg.Theme) != cfg.Theme {
		return Config{}, feedback.New(feedback.Configuration, "theme must be a simple name, not a path (for example catppuccin).")
	}
	if cfg.Country == "" {
		cfg.Country = DetectCountry(os.Getenv)
	} else {
		cfg.Country = strings.ToLower(strings.TrimSpace(cfg.Country))
		if len(cfg.Country) != 2 || !isAlpha(cfg.Country) {
			return Config{}, feedback.New(feedback.Configuration, "country must be a 2-letter code (for example us or de).")
		}
	}
	cfg.StartView = strings.ToLower(strings.TrimSpace(cfg.StartView))
	if cfg.StartView == "muziek" {
		cfg.StartView = "music" // older configs
	}
	if !cfg.Podcasts {
		cfg.StartView = "music"
	}
	if cfg.StartView != "music" && cfg.StartView != "podcasts" {
		return Config{}, feedback.New(feedback.Configuration, "start_view must be music or podcasts.")
	}
	if cfg.Telemetry {
		return Config{}, feedback.New(feedback.Configuration, "telemetry is not supported; remove the option or set it to false.")
	}
	return cfg, nil
}

// nml: environment overrides. Every config key can also be set as
// NOMEDIAPLAYER_<KEY> (upper case), e.g. NOMEDIAPLAYER_NAVIDROME_URL or
// NOMEDIAPLAYER_MPD_PORT. They win over config.toml, so a shared config can
// stay free of hosts and secrets. For any key, NOMEDIAPLAYER_<KEY>_FILE
// reads the value from a file instead (trailing newline trimmed), handy for
// tokens kept in a secret store.
// EnvPrefix is NOMEDIAPLAYER_ (NOMEDIAPLAYER_DEV_ for a variant build).
func EnvPrefix() string { return strings.ToUpper(BusSuffix()) + "_" }

var getenv = os.Getenv

// EnvKeys lists the supported environment variable names in config order.
func EnvKeys() []string {
	t := reflect.TypeOf(Config{})
	keys := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if tag := t.Field(i).Tag.Get("toml"); tag != "" && tag != "telemetry" {
			keys = append(keys, EnvPrefix()+strings.ToUpper(tag))
		}
	}
	return keys
}

func applyEnv(cfg *Config, getenv func(string) string) error {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		tag := t.Field(i).Tag.Get("toml")
		if tag == "" {
			continue
		}
		name := EnvPrefix() + strings.ToUpper(tag)
		raw, set := getenv(name), getenv(name) != ""
		if file := getenv(name + "_FILE"); file != "" {
			data, err := os.ReadFile(file)
			if err != nil {
				return &feedback.Fault{Code: feedback.Configuration, Safe: name + "_FILE points to a file that could not be read.", Cause: err}
			}
			raw, set = strings.TrimRight(string(data), "\r\n"), true
		}
		if !set {
			continue
		}
		f := v.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString(raw)
		case reflect.Int:
			n, err := strconv.Atoi(strings.TrimSpace(raw))
			if err != nil {
				return feedback.New(feedback.Configuration, name+" must be a whole number (for example 6600).")
			}
			f.SetInt(int64(n))
		case reflect.Bool:
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				return feedback.New(feedback.Configuration, name+" must be true or false.")
			}
			f.SetBool(b)
		}
	}
	return nil
}

// SetTheme updates or appends the theme setting in config.toml.
func SetTheme(path, themeName string) error {
	if filepath.Base(themeName) != themeName {
		return feedback.New(feedback.Configuration, "theme must be a simple name, not a path (for example catppuccin).")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		content := fmt.Sprintf("theme = %q\n", themeName)
		return os.WriteFile(path, []byte(content), 0600)
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "theme") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == "theme" {
				lines[i] = fmt.Sprintf("theme = %q", themeName)
				found = true
				break
			}
		}
	}
	if !found {
		lines = append(lines, fmt.Sprintf("theme = %q", themeName))
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}

// DetectCountry infers a 2-letter country code from locale environment variables.
func DetectCountry(getenv func(string) string) string {
	if getenv == nil {
		getenv = os.Getenv
	}
	for _, env := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		val := getenv(env)
		if val == "" {
			continue
		}
		part := strings.Split(val, ".")[0]
		sub := strings.Split(part, "_")
		if len(sub) == 2 && len(sub[1]) == 2 && isAlpha(strings.ToLower(sub[1])) {
			return strings.ToLower(sub[1])
		}
		if len(sub) == 1 && len(sub[0]) == 2 && isAlpha(strings.ToLower(sub[0])) {
			return strings.ToLower(sub[0])
		}
	}
	return "us"
}

func isAlpha(s string) bool {
	for _, r := range s {
		if r < 'a' || r > 'z' {
			return false
		}
	}
	return true
}

// SetCountry updates or appends the country setting in config.toml.
func SetCountry(path, country string) error {
	country = strings.ToLower(strings.TrimSpace(country))
	if len(country) != 2 || !isAlpha(country) {
		return feedback.New(feedback.Configuration, "country must be a 2-letter code (for example us or de).")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		content := fmt.Sprintf("country = %q\n", country)
		return os.WriteFile(path, []byte(content), 0600)
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	found := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "country") {
			parts := strings.SplitN(trimmed, "=", 2)
			if len(parts) == 2 && strings.TrimSpace(parts[0]) == "country" {
				lines[i] = fmt.Sprintf("country = %q", country)
				found = true
				break
			}
		}
	}
	if !found {
		if len(lines) > 0 && lines[len(lines)-1] == "" {
			lines[len(lines)-1] = fmt.Sprintf("country = %q", country)
			lines = append(lines, "")
		} else {
			lines = append(lines, fmt.Sprintf("country = %q", country))
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0600)
}

type Paths struct{ Config, State, Runtime string }

func (p Paths) ConfigDir() string {
	if p.Config == "" {
		return ""
	}
	return filepath.Dir(p.Config)
}

// Onboarded reports whether the first-run welcome dialog was dismissed.
func (p Paths) Onboarded() bool {
	if p.State == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(p.State, "onboarded"))
	return err == nil
}

// SetOnboarded records that the first-run welcome dialog was dismissed.
func (p Paths) SetOnboarded() error {
	if p.State == "" {
		return nil
	}
	return os.WriteFile(filepath.Join(p.State, "onboarded"), []byte("1\n"), 0600)
}

// Resolve selects one default user profile. Relative XDG values are ignored.
// Supplying the platform and environment explicitly also makes path policy testable.
func Resolve(platform, home string, getenv func(string) string) (Paths, error) {
	return resolveNamed(AppName, platform, home, getenv)
}

// nml: directory name of the profile. LegacyAppName is the pre-rename name;
// MigrateLegacy moves those directories over once.
//
// AppName can be changed at build time (-ldflags "-X
// github.com/nomedialoaded/nomediaplayer/internal/profile.AppName=nomediaplayer-dev") for a side-by-side
// test install with its own config, state, socket, MPRIS name and env prefix.
var AppName = DefaultAppName

const (
	DefaultAppName = "nomediaplayer"
	LegacyAppName  = "github.com/nomedialoaded/nomediaplayer"
)

// IsVariant: built under another name than the default (test install).
func IsVariant() bool { return AppName != DefaultAppName }

// BusSuffix is AppName as a D-Bus name element ("nomediaplayer_dev").
func BusSuffix() string { return strings.ReplaceAll(AppName, "-", "_") }

func resolveNamed(name, platform, home string, getenv func(string) string) (Paths, error) {
	if !filepath.IsAbs(home) {
		return Paths{}, feedback.New(feedback.Input, "Set HOME to an absolute home directory before starting nomediaplayer.")
	}
	switch platform {
	case "linux":
		base := func(key, fallback string) string {
			if p := getenv(key); filepath.IsAbs(p) {
				return p
			}
			return fallback
		}
		state := filepath.Join(base("XDG_STATE_HOME", filepath.Join(home, ".local", "state")), name)
		runtime := filepath.Join(state, "run")
		if base := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(base) {
			runtime = filepath.Join(base, name)
		}
		return Paths{filepath.Join(base("XDG_CONFIG_HOME", filepath.Join(home, ".config")), name, "config.toml"), state, runtime}, nil
	case "darwin":
		state := filepath.Join(home, "Library", "Application Support", name)
		return Paths{filepath.Join(state, "config.toml"), state, filepath.Join(state, "run")}, nil
	default:
		return Paths{}, feedback.New(feedback.Input, "nomediaplayer supports Linux and macOS; this platform is not supported.")
	}
}

// Prepare creates private state/runtime directories. Existing symlinks and
// directories accessible to other users are rejected instead of silently changed.
func (p Paths) Prepare() error {
	for _, path := range []string{p.State, p.Runtime} {
		if !filepath.IsAbs(path) {
			return fmt.Errorf("profile: directory must be absolute: %s", path)
		}
		if err := os.MkdirAll(path, 0700); err != nil {
			return err
		}
		if err := privateDirectory(path); err != nil {
			return err
		}
	}
	return nil
}

// CheckRuntime validates an existing runtime directory without creating it.
func (p Paths) CheckRuntime() error { return privateDirectory(p.Runtime) }

func privateDirectory(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 || info.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return fmt.Errorf("profile: directory must be owned by current user and private (0700): %s", path)
	}
	return nil
}

// expandHome turns a leading "~/" into the home directory (paths in the
// config file and environment).
func expandHome(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
