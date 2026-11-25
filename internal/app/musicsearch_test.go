package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

func TestMusicSearchDialogAndResults(t *testing.T) {
	m := ndTestShell()
	m.renderMusic()
	m, _, handled := m.musicKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if !handled || m.music.searching == nil {
		t.Fatal("/ opens music search")
	}
	m, _ = m.musicSearchKey(tea.KeyPressMsg{Code: tea.KeyTab})
	for _, r := range "adele" {
		m, _ = m.musicSearchKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	m, cmd := m.musicSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.music.searching != nil || cmd == nil {
		t.Fatal("enter runs search")
	}
	want := "search:artist:adele"
	if m.music.source != want {
		t.Fatal("source", m.music.source)
	}
	updated, _, _ := m.updateMusic(musicTracks{source: want, tracks: []mpd.Song{
		{File: "a/1.mp3", Title: "Hello", Artist: "Adele", Album: "25"},
		{File: "b/2.mp3", Title: "Skyfall", Artist: "Adele", Album: "Skyfall"},
	}})
	m = updated
	col := m.Model.Visible(ui.Podcasts)
	if len(col) == 0 || col[0].ID != want || !strings.Contains(col[0].Title, "(2)") {
		t.Fatal("search entry on top", col)
	}
	items := m.Model.Visible(ui.Episodes)
	if len(items) != 2 || !strings.Contains(items[0].Title, "— Adele") {
		t.Fatal("results", items)
	}
	if m.music.sourceLabel() != "⌕ adele · Artist" {
		t.Fatal("label", m.music.sourceLabel())
	}
}

func TestMusicFavoriteKeyIsB(t *testing.T) {
	m := ndTestShell()
	m.music.nd = nil
	m, _, handled := m.ndKey("b")
	if !handled || !strings.Contains(m.notice, "Navidrome") {
		t.Fatal("b = favoriet", m.notice)
	}
	if _, _, handled := m.ndKey("f"); handled {
		t.Fatal("f is filter now")
	}
}

func TestMusicFKeyFilters(t *testing.T) {
	m := ndTestShell()
	m.music.source = "search:artist:john"
	m.music.lastSearch = &searchResult{query: "john", scope: 1, count: 2}
	m.music.tracks = []mpd.Song{
		{File: "a/1.mp3", Title: "Gravity", Artist: "John Mayer", Album: "Continuum"},
		{File: "a/2.mp3", Title: "Daughters", Artist: "John Mayer", Album: "Heavier Things"},
	}
	m.renderMusic()
	m.Model = m.Model.WithFocus(ui.Episodes)
	m, _, handled := m.musicKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if !handled || m.music.searching != nil || !m.Model.Busy() {
		t.Fatal("f opens the column filter, not search")
	}
	for _, r := range "grav" {
		updated, _ := m.musicDelegate(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(libraryShell)
	}
	if items := m.Model.Visible(ui.Episodes); len(items) != 1 || !strings.Contains(items[0].Title, "Gravity") {
		t.Fatal("filtered", items)
	}
}

func TestLibraryViewsSwitchWithT(t *testing.T) {
	m := ndTestShell()
	m.music.albums = []mpd.AlbumRef{{Artist: "John Mayer", Album: "Continuum"}, {Artist: "Adele", Album: "25"}}
	m.music.genres = uniqueGenres([]string{"Pop/Electro Rock;Indie Pop", "pop", "Rock"})
	if strings.Join(m.music.genres, ",") != "Electro Rock,Indie Pop,Pop,Rock" {
		t.Fatal("genres", m.music.genres)
	}
	m.renderMusic()
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 't', Text: "t"})
	col := m.Model.Visible(ui.Podcasts)
	if m.music.libView != viewAlbums || len(col) != 2 || !strings.HasPrefix(col[0].Title, "★ Continuum") {
		t.Fatal("albums view", col)
	}
	if sel, _ := m.Model.Selected(ui.Podcasts); m.music.source != sel.ID {
		t.Fatal("first album selected and loading", m.music.source)
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'T', Text: "T"})
	// Lidarr and speakers are not set up here, so T wraps to Favourites.
	if m.music.libView != viewFavorites {
		t.Fatal("T goes back, wraps past services that are off", m.music.libView)
	}
	if !hasGenre("Pop;Rock", "pop") || hasGenre("Indie Pop", "Pop") {
		t.Fatal("hasGenre")
	}
}

func TestNewSearchClearsOldFilter(t *testing.T) {
	m := ndTestShell()
	m.music.source = "search:artist:john"
	m.music.tracks = []mpd.Song{{File: "a/1.mp3", Title: "Gravity", Artist: "John Mayer"}}
	m.renderMusic()
	m.Model = m.Model.WithFocus(ui.Episodes)
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'f', Text: "f"})
	for _, r := range "zzz" {
		updated, _ := m.musicDelegate(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = updated.(libraryShell)
	}
	updated, _ := m.musicDelegate(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = updated.(libraryShell)
	if len(m.Model.Visible(ui.Episodes)) != 0 {
		t.Fatal("filter zzz should hide everything")
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: '/', Text: "/"})
	if len(m.Model.Visible(ui.Episodes)) != 1 {
		t.Fatal("/ clears the old filter")
	}
}

func TestRepeatCycleAndShuffle(t *testing.T) {
	m := ndTestShell()
	var labels []string
	for i := 0; i < 3; i++ {
		var cmd tea.Cmd
		m, cmd, _ = m.musicKey(tea.KeyPressMsg{Code: 'r', Text: "r"})
		if cmd == nil {
			t.Fatal("r sends the mode to MPD")
		}
		labels = append(labels, m.notice)
	}
	if strings.Join(labels, "|") != "↻ repeat queue|↻ repeat track|shuffle and repeat off" {
		t.Fatal("repeat cycle", labels)
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'z', Text: "z"})
	if !m.music.status.Random || m.music.player().Modes != "⤮ shuffle" {
		t.Fatal("shuffle", m.music.player().Modes)
	}
}

func TestMusicSearchByAlbum(t *testing.T) {
	m := ndTestShell()
	m.music.source = "search:any:daft"
	m.music.lastSearch = &searchResult{query: "daft", scope: 0, count: 4}
	m.music.tracks = []mpd.Song{
		{File: "d/1.mp3", Title: "One More Time", Artist: "Daft Punk", AlbumArtist: "Daft Punk", Album: "Discovery", Track: 1, Duration: 320},
		{File: "d/2.mp3", Title: "Aerodynamic", Artist: "Daft Punk", AlbumArtist: "Daft Punk", Album: "Discovery", Track: 2, Duration: 212},
		{File: "h/1.mp3", Title: "Da Funk", Artist: "Daft Punk", AlbumArtist: "Daft Punk", Album: "Homework", Track: 7, Duration: 328},
		{File: "v/1.mp3", Title: "Get Lucky", Artist: "Daft Punk", AlbumArtist: "Various", Album: "Hits 2013", Track: 3, Duration: 248},
	}
	m.renderMusic()
	m.Model = m.Model.WithFocus(ui.Episodes).Select(ui.Episodes, "track:2")
	m, _, handled := m.musicKey(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if !handled || !m.music.searchByAlbum {
		t.Fatal("w toggles album view")
	}
	items := m.Model.Visible(ui.Episodes)
	if len(items) != 3 || !strings.HasPrefix(items[0].Title, "Discovery — Daft Punk") || items[0].Channel != "2 tracks" {
		t.Fatal("albums", items)
	}
	if sel, _ := m.Model.Selected(ui.Episodes); sel.ID != "salbum:1" {
		t.Fatal("cursor follows the track's album", sel.ID)
	}
	if s, ok := m.musicDetailSong(); !ok || s.Album != "Homework" {
		t.Fatal("detail song = first track of album", s)
	}
	if d, _ := m.searchAlbumDetails(); !strings.Contains(d, "Da Funk") || !strings.Contains(d, "whole album") {
		t.Fatal("album details", d)
	}
	if _, cmd, _ := m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter}); cmd == nil {
		t.Fatal("enter plays the album")
	}
	m.Model = m.Model.Select(ui.Episodes, "salbum:0")
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if m.music.searchByAlbum || len(m.Model.Visible(ui.Episodes)) != 4 {
		t.Fatal("back to tracks")
	}
	if sel, _ := m.Model.Selected(ui.Episodes); sel.ID != "track:0" {
		t.Fatal("cursor on the album's first track", sel.ID)
	}
	m.music.source = "artist:Daft Punk"
	m.music.searchByAlbum = true
	m.renderMusic()
	if len(m.Model.Visible(ui.Episodes)) != 4 {
		t.Fatal("album grouping only for search results")
	}
}
