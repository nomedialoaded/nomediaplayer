package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

func TestLyricsPanel(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "A"), 0o755)
	os.WriteFile(filepath.Join(dir, "A", "1.lrc"), []byte("[00:01.00]een\n[00:05.00]twee\n[00:09.00]drie\n"), 0o644)
	m := ndTestShell()
	m.music.lyrics = newLyricsState(dir, "", false)
	m.music.hasCurrent = true
	m.music.current = mpd.Song{File: "A/1.mp3", Title: "Lied", Artist: "X"}
	m.music.status = mpd.Status{State: "play", Elapsed: 6}
	m.music.clockAt = time.Now()
	m.renderMusic()

	m, cmd, _ := m.musicKey(tea.KeyPressMsg{Code: 'L', Text: "L"})
	if !m.music.showLyrics || cmd == nil || !m.music.lyrics.loading {
		t.Fatal("L shows lyrics and loads them")
	}
	data, _ := m.music.lyrics.finder.Find(t.Context(), lyricsSongOf(m.music.current))
	m, _, _ = m.updateLyrics(lyricsLoaded{file: "A/1.mp3", data: data})
	items := m.Model.Visible(ui.Favorites)
	if len(items) != 3 || items[1].Date != "0:05" || !items[1].Current {
		t.Fatal("lyric lines with sung line marked", items)
	}
	if sel, _ := m.Model.Selected(ui.Favorites); sel.ID != "lyr:1" {
		t.Fatal("cursor follows the sung line", sel.ID)
	}
	var d ui.Data
	m.music.lyricsPanel(&d)
	if d.Titles[4] != "Lyrics · Lied (.lrc) ‹L›" {
		t.Fatal("panel title", d.Titles[4])
	}
	m.Model = m.Model.WithFocus(ui.Favorites)
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.music.lyrics.follow {
		t.Fatal("moving stops following")
	}
	m, cmd, _ = m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.music.lyrics.follow || cmd == nil {
		t.Fatal("enter seeks and follows again")
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if !strings.Contains(m.notice, "Lyrics") {
		t.Fatal("d does nothing in lyrics", m.notice)
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'L', Text: "L"})
	if m.music.showLyrics {
		t.Fatal("L back to recent")
	}
}
