package app

// nml: panel 5 in the Music view switches (L) between Recently played and the lyrics
// of the playing song. Synced lyrics follow MPD's position; moving through
// the text stops following, Enter jumps the song to that line and follows
// again.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/lyrics"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	lyricPrefix  = "lyr:"
	lyricsTickMs = 400
)

type lyricsState struct {
	finder  *lyrics.Finder
	file    string // song the lyrics belong to
	data    lyrics.Lyrics
	loading bool
	err     string
	follow  bool
	line    int
	ticking bool
}

type lyricsLoaded struct {
	file string
	data lyrics.Lyrics
	err  error
}

type lyricsTick struct{}

func newLyricsState(musicDir, stateDir string, online bool) *lyricsState {
	if musicDir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			musicDir = lyrics.MusicDirFromMPD(filepath.Join(home, ".config", "mpd", "mpd.conf"), home)
		}
	}
	cache := ""
	if stateDir != "" {
		cache = filepath.Join(stateDir, "lyrics")
	}
	return &lyricsState{finder: &lyrics.Finder{MusicDir: musicDir, CacheDir: cache, Online: online,
		UserAgent: "nomediaplayer/" + Version}, follow: true, line: -1}
}

// elapsed estimates MPD's position now (status is only refreshed on events).
func (ms *musicState) elapsed() float64 {
	e := ms.status.Elapsed
	if ms.status.State == "play" && !ms.clockAt.IsZero() {
		e += time.Since(ms.clockAt).Seconds()
	}
	return e
}

// musicSyncLyrics loads lyrics for the playing song and keeps the follow
// ticker running while it is useful.
func (m libraryShell) musicSyncLyrics() tea.Cmd {
	ms := m.music
	ls := ms.lyrics
	if ms == nil || ls == nil || !ms.showLyrics {
		return nil
	}
	var cmds []tea.Cmd
	if ms.hasCurrent && ms.current.File != ls.file {
		ls.file, ls.data, ls.err, ls.loading, ls.line, ls.follow = ms.current.File, lyrics.Lyrics{}, "", true, -1, true
		s, finder, file := ms.current, ls.finder, ms.current.File
		cmds = append(cmds, work(m.s.ctx, "Looking up lyrics…", func(ctx context.Context) tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			data, err := finder.Find(ctx, lyricsSongOf(s))
			return lyricsLoaded{file: file, data: data, err: err}
		}))
	}
	if !ls.ticking && ls.data.Synced && ms.playing() {
		ls.ticking = true
		cmds = append(cmds, lyricsTickCmd())
	}
	return tea.Batch(cmds...)
}

func lyricsTickCmd() tea.Cmd {
	return tea.Tick(lyricsTickMs*time.Millisecond, func(time.Time) tea.Msg { return lyricsTick{} })
}

func (m libraryShell) updateLyrics(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	if m.music == nil || m.music.lyrics == nil {
		return m, nil, false
	}
	ms, ls := m.music, m.music.lyrics
	switch msg := msg.(type) {
	case lyricsLoaded:
		if msg.file != ls.file {
			return m, nil, true
		}
		ls.loading, ls.data = false, msg.data
		if msg.err != nil {
			ls.err = "Fetching lyrics failed (" + msg.err.Error() + ")"
		}
		ls.line = ls.data.LineAt(ms.elapsed())
		if ms.active {
			m.renderMusic()
		}
		return m, m.musicSyncLyrics(), true
	case lyricsTick:
		ls.ticking = false
		if !ms.showLyrics || !ls.data.Synced || !ms.playing() {
			return m, nil, true
		}
		if line := ls.data.LineAt(ms.elapsed()); line != ls.line {
			ls.line = line
			if ms.active {
				m.renderMusic()
			}
		}
		ls.ticking = true
		return m, lyricsTickCmd(), true
	}
	return m, nil, false
}

// lyricsKey handles keys while panel 5 shows lyrics.
func (m libraryShell) lyricsKey(key string) (libraryShell, tea.Cmd, bool) {
	ls := m.music.lyrics
	switch key {
	case "j", "k", "up", "down", "g", "G", "home", "end", "ctrl+d", "ctrl+u", "pgup", "pgdown":
		ls.follow = false // the delegate moves the cursor
		return m, nil, false
	case "enter":
		item, ok := m.Model.Selected(ui.Favorites)
		i, err := strconv.Atoi(strings.TrimPrefix(item.ID, lyricPrefix))
		if !ok || err != nil || !ls.data.Synced || i < 0 || i >= len(ls.data.Lines) {
			ls.follow = true
			m.renderMusic()
			return m, nil, true
		}
		at := ls.data.Lines[i].At
		ls.follow, ls.line = true, i
		m.renderMusic()
		return m, m.musicDo("To line…", false, func(ctx context.Context, c *mpd.Client) error { return c.SeekTo(ctx, at) }), true
	case "a", "A", "o", "d", "D", "delete":
		m.notice = "In Lyrics · L back to Recently played"
		m.renderStatus()
		return m, nil, true
	}
	return m, nil, false
}

func (m libraryShell) toggleLyrics() (libraryShell, tea.Cmd) {
	ms := m.music
	ms.showLyrics = !ms.showLyrics
	if ms.showLyrics {
		ms.lyrics.follow = true
		m.notice = "Column 5: Lyrics · L back"
	} else {
		m.notice = "Column 5: Recently played · L lyrics"
	}
	cmd := m.musicSyncLyrics() // marks loading before the first render
	m.renderMusic()
	return m, cmd
}

// lyricsPanel fills panel 5 with the lyrics of the playing song.
func (ms *musicState) lyricsPanel(data *ui.Data) (selectID string) {
	ls := ms.lyrics
	title := "Lyrics ‹L›"
	if ms.hasCurrent {
		title = "Lyrics · " + songTitle(ms.current) + " ‹L›"
	}
	data.Titles[4] = title
	data.FavoritesHeader = [3]string{"Tekst", "", "Tijd"}
	data.FavoritesCentered = true
	switch {
	case !ms.hasCurrent:
		data.FavoritesEmpty = "Nothing is playing · L back to Recently played"
		return ""
	case ls.loading:
		data.FavoritesEmpty = "Looking up lyrics…"
		return ""
	case ls.err != "":
		data.FavoritesEmpty = ls.err
		return ""
	case ls.data.Instrumental:
		data.FavoritesEmpty = "♪ Instrumentaal"
		return ""
	case ls.data.Empty():
		data.FavoritesEmpty = "No lyrics found"
		if !ls.finder.Online {
			data.FavoritesEmpty += " (no .lrc; lyrics_online is off)"
		}
		return ""
	}
	for i, l := range ls.data.Lines {
		text := l.Text
		if text == "" {
			text = "·"
		}
		item := ui.Item{ID: fmt.Sprintf("%s%d", lyricPrefix, i), Title: text, PlainTitle: text, Index: i, Current: i == ls.line}
		if ls.data.Synced {
			item.Date = clock(l.At)
		}
		data.Favorites = append(data.Favorites, item)
	}
	source := map[string]string{"lrc": ".lrc", "lrclib": "lrclib.net"}[ls.data.Source]
	if source != "" {
		data.Titles[4] = strings.TrimSuffix(title, " ‹L›") + " (" + source + ") ‹L›"
	}
	if ls.follow && ls.line >= 0 {
		return fmt.Sprintf("%s%d", lyricPrefix, ls.line)
	}
	return ""
}

func lyricsSongOf(s mpd.Song) lyrics.Song {
	return lyrics.Song{File: s.File, Artist: songArtist(s), Title: songTitle(s), Album: s.Album, Duration: s.Duration}
}
