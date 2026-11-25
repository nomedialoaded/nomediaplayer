package ui

import (
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestHelpPages(t *testing.T) {
	m := resize(New(fixture()), 140, 40)
	m = press(m, "?")
	if !m.help || m.helpPage != 0 {
		t.Fatal("? opens help on page 1")
	}
	view := ansi.Strip(m.View().Content)
	for _, want := range []string{"page 1/5", "Moving", "Playback", "Music ⇄ Podcasts"} {
		if !strings.Contains(view, want) {
			t.Fatalf("page 1 misses %q:\n%s", want, view)
		}
	}
	m = press(m, "l")
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Navidrome") || !strings.Contains(view, "Save the queue as a playlist") {
		t.Fatalf("page 2:\n%s", view)
	}
	m = press(m, "l", "l", "l", "l")
	if m.helpPage != 0 {
		t.Fatal("l wraps around")
	}
	m = press(m, "3")
	if !strings.Contains(ansi.Strip(m.View().Content), "Apple Top 100") {
		t.Fatal("3 jumps to podcasts")
	}
	Changelog = "# Changelog\n\n## v1.0 — 2026-11-04\n\n### Muziek\n- **Zoeken** met `/`.\n"
	defer func() { Changelog = "" }()
	m = withSetupFixture(m)
	m = press(m, "4")
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "Lidarr                  off") {
		t.Fatalf("4 = setup:\n%s", view)
	}
	if next, cmd := m.Update(keyMsg("r")); cmd == nil {
		t.Fatal("r on the Setup page should ask for the Lidarr setup")
	} else if req, ok := cmd().(SetupRequest); !ok || req.Section != "lidarr" || next.(Model).help {
		t.Fatalf("got %#v", cmd())
	}
	m = press(m, "5")
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "v1.0 — 2026-11-04") || !strings.Contains(view, "• Zoeken met /.") {
		t.Fatalf("5 = changelog:\n%s", view)
	}
	m = press(m, "j", "esc")
	if m.help {
		t.Fatal("esc closes")
	}
	// Muziek view opens on the Muziek page; narrow terminal scrolls.
	d := fixture()
	d.Music = true
	m = resize(New(d), 60, 20)
	m = press(m, "?")
	if m.helpPage != helpMusicPage {
		t.Fatal("Muziek opens on page 2")
	}
	view = ansi.Strip(m.View().Content)
	if !strings.Contains(view, "j/k scroll") {
		t.Fatalf("narrow help must scroll:\n%s", view)
	}
	if os.Getenv("SHOW_HELP") != "" {
		for _, w := range []int{140, 90} {
			m := resize(New(fixture()), w, 40)
			for p := 0; p < 4; p++ {
				m = press(m, "?")
				m.helpPage = p
				t.Log("\n" + ansi.Strip(m.View().Content))
				m = press(m, "esc")
			}
		}
	}
}

func withSetupFixture(m Model) Model {
	return m.WithSetupRows([][2]string{{"r", "Lidarr                  off"}})
}
