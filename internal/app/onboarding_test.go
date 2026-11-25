package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/nomedialoaded/nomediaplayer/internal/feed"
	"github.com/nomedialoaded/nomediaplayer/internal/profile"
	"github.com/nomedialoaded/nomediaplayer/internal/theme"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

// nml: the first start runs the setup wizard (setup.go).
func freshWizard(t *testing.T, cfg profile.Config, run func(s *session, m *libraryShell)) {
	t.Helper()
	paths := isolatedProfile(t)
	if err := paths.Prepare(); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(filepath.Join(paths.State, "onboarded"))
	_ = os.Remove(paths.Config)
	if err := withSession(cfg, paths, func(s *session) error {
		m, err := newLibrary(s, feed.New(s.store, nil, 0))
		if err != nil {
			t.Fatal(err)
		}
		updateLibrary(&m, tea.WindowSizeMsg{Width: 140, Height: 40})
		if m.setup == nil {
			t.Fatal("expected the setup wizard on a fresh profile")
		}
		run(s, &m)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// wizardKey sends a key and runs a connection test it starts.
func wizardKey(m *libraryShell, key tea.KeyPressMsg) {
	cmd := updateLibrary(m, key)
	for cmd != nil {
		msg := cmd()
		if _, ok := msg.(setupTested); !ok {
			return
		}
		cmd = updateLibrary(m, msg)
	}
}

func wizardText(m *libraryShell, text string) {
	for _, r := range text {
		wizardKey(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
}

func TestSetupWizardSkipKeepsOnlineLookupsOff(t *testing.T) {
	freshWizard(t, profile.Defaults(), func(s *session, m *libraryShell) {
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Welcome to nomediaplayer") {
			t.Fatalf("welcome step missing: %s", view)
		}
		if s.config.PodcastDirectory || s.config.LyricsOnline || m.browse.active {
			t.Fatal("nothing may go online before the wizard has asked")
		}
		if _, cmd, _ := m.updateDiscovery(discoverStartup{}); cmd != nil {
			t.Fatal("no Apple chart request on the first start")
		}
		wizardKey(m, tea.KeyPressMsg{Code: tea.KeyEscape})
		if m.setup != nil || !s.paths.Onboarded() {
			t.Fatal("Esc should close the wizard and remember it")
		}
		cfg, err := profile.Load(s.paths.Config)
		if err != nil {
			t.Fatal(err)
		}
		if cfg.PodcastDirectory || cfg.LyricsOnline {
			t.Fatalf("skipped setup must keep online lookups off: %+v", cfg)
		}
		if st, _ := os.Stat(s.paths.Config); st.Mode().Perm() != 0600 {
			t.Fatalf("config mode %v", st.Mode().Perm())
		}
	})
}

func TestSetupWizardWritesAnswers(t *testing.T) {
	cfg := profile.Defaults()
	cfg.MPDHost, cfg.MPDPort = "127.0.0.1", 1 // nothing listens: the test fails
	freshWizard(t, cfg, func(s *session, m *libraryShell) {
		enter := tea.KeyPressMsg{Code: tea.KeyEnter}
		wizardKey(m, enter) // welcome
		wizardKey(m, enter) // MPD address as is → test fails
		if m.setup.testErr == "" {
			t.Fatal("expected the MPD check to fail")
		}
		wizardText(m, "k")                                         // keep anyway
		wizardKey(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}) // clear music folder
		wizardKey(m, enter)                                        // empty = skip
		wizardText(m, "n")                                         // Navidrome no
		wizardText(m, "y")                                         // Lidarr yes
		wizardText(m, "127.0.0.1:1")
		wizardKey(m, enter)
		wizardText(m, "secret")
		if view := ansi.Strip(m.View().Content); strings.Contains(view, "secret") || !strings.Contains(view, "••••••") {
			t.Fatal("API key should be masked")
		}
		wizardKey(m, enter) // test fails
		wizardText(m, "s")  // skip Lidarr
		wizardText(m, "n")  // speakers no
		wizardText(m, "y")  // podcasts yes
		wizardText(m, "n")  // Apple directory no
		wizardText(m, "n")  // Podcast Index no
		wizardText(m, "n")  // wallpaper colours no
		wizardText(m, "y")  // lyrics online yes
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Ready") {
			t.Fatalf("expected the summary: %s", view)
		}
		wizardKey(m, enter)
		if m.setup != nil {
			t.Fatal("wizard still open")
		}
		got, err := profile.Load(s.paths.Config)
		if err != nil {
			t.Fatal(err)
		}
		if got.MPDPort != 1 || got.LidarrURL != "" || got.LidarrAPIKey != "" || !got.Podcasts || got.PodcastDirectory || !got.LyricsOnline || got.NavidromeURL != "" {
			t.Fatalf("unexpected config: %+v", got)
		}
		if m.music != nil && m.music.viewEnabled(viewLidarr) {
			t.Fatal("Lidarr view should be hidden")
		}
	})
}

func TestSetupWizardWallpaperTheme(t *testing.T) {
	t.Cleanup(func() { theme.SetWallpaperFile("") })
	file := filepath.Join(t.TempDir(), "colors.json")
	os.WriteFile(file, []byte(`{"primary":"#ffb2b9","surface":"#1a1112","on_surface":"#f0dedf"}`), 0o644)
	freshWizard(t, profile.Defaults(), func(s *session, m *libraryShell) {
		wizardKey(m, tea.KeyPressMsg{Code: tea.KeyEscape})
		*m = m.startSetup(setupTheme, false)
		enter := tea.KeyPressMsg{Code: tea.KeyEnter}
		wizardText(m, "y")
		wizardKey(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		wizardText(m, file+".missing")
		wizardKey(m, enter)
		if m.setup.testErr == "" {
			t.Fatal("a missing colour file must fail the check")
		}
		wizardText(m, "e")
		wizardKey(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		wizardText(m, file)
		wizardKey(m, enter)
		if m.setup.testErr != "" {
			t.Fatalf("check failed: %s", m.setup.testErr)
		}
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, "follows the wallpaper") {
			t.Fatalf("expected the summary: %s", view)
		}
		wizardKey(m, enter)
		got, err := profile.Load(s.paths.Config)
		if err != nil {
			t.Fatal(err)
		}
		if got.Theme != theme.Wallpaper || got.WallpaperColors != file || theme.WallpaperColorsPath() != file {
			t.Fatalf("unexpected config: theme=%q file=%q", got.Theme, got.WallpaperColors)
		}
		// Switching it off again falls back to the default theme.
		*m = m.startSetup(setupTheme, false)
		wizardText(m, "n")
		wizardKey(m, enter)
		got, _ = profile.Load(s.paths.Config)
		if got.Theme != "colors" || got.WallpaperColors != "" {
			t.Fatalf("switch off: theme=%q file=%q", got.Theme, got.WallpaperColors)
		}
	})
}

func TestSetupFlacCheck(t *testing.T) {
	cfg := profile.Defaults()
	if err := testFlac(context.Background(), cfg); err != nil {
		t.Fatal("flac_to_mp3 off needs no check")
	}
	cfg.FlacToMP3, cfg.MusicDir = true, filepath.Join(t.TempDir(), "missing")
	t.Setenv("PATH", t.TempDir())
	if err := testFlac(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "ffmpeg") {
		t.Fatalf("expected the ffmpeg error, got %v", err)
	}
}

func TestDetailsEmptyStateGuidance(t *testing.T) {
	librarySession(t, func(s *session) {
		m, err := newLibrary(s, feed.New(s.store, nil, 0))
		if err != nil {
			t.Fatal(err)
		}
		updateLibrary(&m, tea.WindowSizeMsg{Width: 140, Height: 40})
		m.Model = m.Model.WithFocus(ui.Details)
		view := m.View().Content
		// In Discover mode on fresh library: shows Apple Podcasts guidance
		if !strings.Contains(view, "Apple Podcasts") || !strings.Contains(view, "Enter a show to inspect") {
			t.Fatalf("expected discovery guidance in Details view: %s", view)
		}

		// Switch to My Library: shows library guidance
		finishLibrary(&m, m.switchBrowse())
		view = m.View().Content
		if !strings.Contains(view, "Select an episode") || !strings.Contains(view, "Quick keys") {
			t.Fatalf("expected library empty state guidance in Details view: %s", view)
		}
	})
}

func TestCleanServerURL(t *testing.T) {
	for _, c := range [][3]string{
		{"navidrome_url", "http://192.168.1.5:4533/app/#/album/abc/show", "http://192.168.1.5:4533"},
		{"navidrome_url", "192.168.1.5:4533/", "http://192.168.1.5:4533"},
		{"navidrome_url", "https://music.example.org/navidrome/app/", "https://music.example.org/navidrome"},
		{"navidrome_url", "http://host:4533/?x=1", "http://host:4533"},
		{"lidarr_url", "http://host:8686/artist/abc", "http://host:8686"},
		{"ha_url", "http://ha.lan:8123/lovelace/0", "http://ha.lan:8123"},
		{"ma_url", "http://ha.lan:8095/#/home", "http://ha.lan:8095"},
		{"navidrome_url", "", ""},
	} {
		if got := cleanServerURL(c[0], c[1]); got != c[2] {
			t.Errorf("%s %q → %q, want %q", c[0], c[1], got, c[2])
		}
	}
}

func TestSetupWizardPodcastIndex(t *testing.T) {
	var gotKey, gotSecret string
	old := podcastIndexCheck
	podcastIndexCheck = func(_ context.Context, key, secret string) error {
		gotKey, gotSecret = key, secret
		if secret != "mysecret" {
			return errors.New("rejected")
		}
		return nil
	}
	t.Cleanup(func() { podcastIndexCheck = old })
	freshWizard(t, profile.Defaults(), func(s *session, m *libraryShell) {
		wizardKey(m, tea.KeyPressMsg{Code: tea.KeyEscape})
		*m = m.startSetup(setupPodcasts, false)
		enter := tea.KeyPressMsg{Code: tea.KeyEnter}
		wizardText(m, "y") // podcasts
		wizardText(m, "n") // Apple directory
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, "api.podcastindex.org") {
			t.Fatalf("Podcast Index step missing: %s", view)
		}
		wizardText(m, "y")
		wizardText(m, "mykey")
		if view := ansi.Strip(m.View().Content); strings.Contains(view, "mykey") {
			t.Fatal("key must be masked")
		}
		wizardKey(m, enter)
		wizardText(m, "wrong")
		wizardKey(m, enter)
		if m.setup.testErr == "" {
			t.Fatal("a rejected secret must fail the check")
		}
		wizardText(m, "e") // back to the key, keep it, fix the secret
		wizardKey(m, enter)
		wizardKey(m, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		wizardText(m, "mysecret")
		wizardKey(m, enter)
		if m.setup.testErr != "" || gotKey != "mykey" || gotSecret != "mysecret" {
			t.Fatalf("check: err=%q key=%q secret=%q", m.setup.testErr, gotKey, gotSecret)
		}
		if view := ansi.Strip(m.View().Content); !strings.Contains(view, "Podcast Index") {
			t.Fatalf("summary should list Podcast Index: %s", view)
		}
		wizardKey(m, enter)
		got, _ := profile.Load(s.paths.Config)
		if got.PodcastIndexKey != "mykey" || got.PodcastIndexSecret != "mysecret" || !got.Podcasts {
			t.Fatalf("keys not saved: %+v", got)
		}
		// No clears both keys and keeps the podcasts on.
		*m = m.startSetup(setupPodcasts, false)
		wizardText(m, "y")
		wizardText(m, "n")
		wizardText(m, "n")
		wizardKey(m, enter)
		got, _ = profile.Load(s.paths.Config)
		if got.PodcastIndexKey != "" || got.PodcastIndexSecret != "" || !got.Podcasts {
			t.Fatalf("switch off: %+v", got)
		}
	})
}
