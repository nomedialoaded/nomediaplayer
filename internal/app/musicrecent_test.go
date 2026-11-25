package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

func TestRecentPlayed(t *testing.T) {
	dir := t.TempDir()
	r := loadRecent(dir)
	now := time.Date(2026, 11, 4, 21, 30, 0, 0, time.Local)
	a := mpd.Song{File: "a.mp3", Title: "Gravity", Artist: "John Mayer", Album: "Continuum"}
	b := mpd.Song{File: "b.mp3", Title: "Hello", Artist: "Adele", Album: "25"}
	if !r.record(a, now.Add(-26*time.Hour)) || r.record(a, now) {
		t.Fatal("same song twice in a row counts once")
	}
	r.record(b, now.Add(-time.Hour))
	r.record(a, now)
	if len(r.entries) != 2 || r.entries[0].Song.File != "a.mp3" {
		t.Fatal("replayed song moves to top", r.entries)
	}
	if got := loadRecent(dir); len(got.entries) != 2 || got.last != "a.mp3" {
		t.Fatal("persisted", got.entries)
	}
	items := r.items(now, "a.mp3")
	if items[0].Title != "Gravity — John Mayer" || items[0].Date != "21:30" || !items[0].Current || items[1].Date != "20:30" {
		t.Fatal("items", items)
	}
	if playedLabel(now.Add(-26*time.Hour), now) != "gisteren" || playedLabel(now.AddDate(0, 0, -10), now) != "25-10" {
		t.Fatal("labels")
	}

	m := ndTestShell()
	m.music.recent = r
	m.renderMusic()
	fav := m.Model.Visible(ui.Favorites)
	if len(fav) != 2 {
		t.Fatal("panel 5 shows recent", fav)
	}
	m.Model = m.Model.WithFocus(ui.Favorites)
	if s, ok := m.musicDetailSong(); !ok || s.File != "a.mp3" {
		t.Fatal("details follow panel 5")
	}
	if _, cmd, _ := m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter plays the song")
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if len(r.entries) != 1 || !strings.Contains(m.notice, "Recent") {
		t.Fatal("d removes", r.entries)
	}

	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	if len(r.entries) != 1 || !m.music.clearRecentArmed {
		t.Fatal("first D only asks", r.entries)
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	if len(r.entries) != 1 {
		t.Fatal("another key in between cancels the clear")
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'D', Text: "D"})
	if len(r.entries) != 0 || len(m.Model.Visible(ui.Favorites)) != 0 || !strings.Contains(m.notice, "cleared") {
		t.Fatal("D twice clears", r.entries)
	}
	if got := loadRecent(dir); len(got.entries) != 0 {
		t.Fatal("clear is saved", got.entries)
	}
}
