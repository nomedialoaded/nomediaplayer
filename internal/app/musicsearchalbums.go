package app

// nml: search results in the Nummers column can be shown per album (w):
// one row per album with its matching tracks, Enter plays the whole album.

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const searchAlbumPfx = "salbum:"

// searchAlbum is one album among the search results; tracks are indexes
// into musicState.tracks.
type searchAlbum struct {
	artist, album string
	tracks        []int
}

func albumArtistOf(s mpd.Song) string {
	if s.AlbumArtist != "" {
		return s.AlbumArtist
	}
	return s.Artist
}

// searchAlbums groups the current tracks by album artist + album, in the
// order the results are sorted in.
func (ms *musicState) searchAlbums() []searchAlbum {
	var out []searchAlbum
	seen := map[string]int{}
	for i, s := range ms.tracks {
		key := strings.ToLower(albumArtistOf(s)) + "\x00" + strings.ToLower(s.Album)
		j, ok := seen[key]
		if !ok {
			j = len(out)
			seen[key] = j
			out = append(out, searchAlbum{artist: albumArtistOf(s), album: s.Album})
		}
		out[j].tracks = append(out[j].tracks, i)
	}
	return out
}

// showSearchAlbums: column 2 lists albums instead of tracks.
func (ms *musicState) showSearchAlbums() bool {
	return ms.searchByAlbum && isSearchSource(ms.source)
}

func (ms *musicState) searchAlbumItems() []ui.Item {
	var items []ui.Item
	for i, a := range ms.searchAlbums() {
		first := ms.tracks[a.tracks[0]]
		title := a.album
		if title == "" {
			title = "(no album)"
		}
		if a.artist != "" {
			title += " — " + a.artist
		}
		total, current := 0.0, false
		for _, t := range a.tracks {
			total += ms.tracks[t].Duration
			current = current || (ms.hasCurrent && ms.tracks[t].File != "" && ms.tracks[t].File == ms.current.File)
		}
		channel := fmt.Sprintf("%d track", len(a.tracks))
		if len(a.tracks) != 1 {
			channel += "s"
		}
		items = append(items, ui.Item{ID: searchAlbumPfx + strconv.Itoa(i), Title: title, PlainTitle: title,
			Channel: channel, Date: songYear(first), Duration: clock(total), DurationSeconds: total,
			Index: i, Current: current})
	}
	return items
}

func (m libraryShell) searchAlbumAt(id string) (searchAlbum, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(id, searchAlbumPfx))
	albums := m.music.searchAlbums()
	if !strings.HasPrefix(id, searchAlbumPfx) || err != nil || i < 0 || i >= len(albums) {
		return searchAlbum{}, false
	}
	return albums[i], true
}

// toggleSearchAlbums switches column 2 between tracks and albums and keeps
// the cursor on the same album.
func (m libraryShell) toggleSearchAlbums() libraryShell {
	ms := m.music
	if !isSearchSource(ms.source) {
		m.notice = "w switches between tracks and albums in search results (/)"
		m.renderStatus()
		return m
	}
	item, _ := m.Model.Selected(ui.Episodes)
	ms.searchByAlbum = !ms.searchByAlbum
	m.Model = m.Model.ClearFilter(ui.Episodes)
	m.renderMusic()
	target := ""
	albums := ms.searchAlbums()
	if ms.searchByAlbum {
		if i, ok := m.musicTrackIndex(item.ID); ok {
			for j, a := range albums {
				for _, t := range a.tracks {
					if t == i {
						target = searchAlbumPfx + strconv.Itoa(j)
					}
				}
			}
		}
		m.notice = fmt.Sprintf("Search results per album (%d) · Enter plays album · w tracks", len(albums))
	} else {
		if a, ok := m.searchAlbumAt(item.ID); ok {
			target = "track:" + strconv.Itoa(a.tracks[0])
		}
		m.notice = fmt.Sprintf("Search results per track (%d) · w albums", len(ms.tracks))
	}
	if target != "" {
		m.Model = m.Model.Select(ui.Episodes, target)
	}
	m.renderMusicDetails()
	m.renderStatus()
	return m
}

// searchAlbumDetails describes the album under the cursor in Details.
func (m libraryShell) searchAlbumDetails() (string, bool) {
	if f := m.Model.Focus(); f == ui.Queue || f == ui.Favorites {
		return "", false
	}
	item, ok := m.Model.Selected(ui.Episodes)
	if !ok {
		return "", false
	}
	a, ok := m.searchAlbumAt(item.ID)
	if !ok {
		return "", false
	}
	ms := m.music
	first := ms.tracks[a.tracks[0]]
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", item.Title)
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(&b, "%-9s %s\n", label, value)
		}
	}
	line("Artist", a.artist)
	line("Album", a.album)
	line("Year", songYear(first))
	line("Genre", first.Genre)
	line("Found", item.Channel+" · "+item.Duration)
	b.WriteString("\n")
	for _, t := range a.tracks {
		s := ms.tracks[t]
		nr := "  "
		if s.Track > 0 {
			nr = fmt.Sprintf("%2d", s.Track)
		}
		title := songTitle(s)
		if s.Artist != "" && !strings.EqualFold(s.Artist, a.artist) {
			title += " — " + s.Artist
		}
		fmt.Fprintf(&b, "%s  %s  %s\n", nr, clock(s.Duration), title)
	}
	b.WriteString("\nEnter/o play whole album · a album to queue · w tracks\n")
	return b.String(), true
}

// musicPlayAlbum replaces the queue with the whole album of file (from MPD,
// not only the search hits) and starts at file.
func (m libraryShell) musicPlayAlbum(file, album, artist string) func(context.Context, *mpd.Client) error {
	return func(ctx context.Context, c *mpd.Client) error {
		files, start, err := albumFiles(ctx, c, file, album, artist)
		if err != nil {
			return err
		}
		return c.ReplaceAndPlay(ctx, files, start)
	}
}

func albumFiles(ctx context.Context, c *mpd.Client, file, album, artist string) ([]string, int, error) {
	var tracks []mpd.Song
	var err error
	if artist != "" {
		tracks, err = c.FindTags(ctx, "albumartist", artist, "album", album)
	}
	if err == nil && len(tracks) == 0 {
		tracks, err = c.FindTags(ctx, "album", album)
	}
	if err != nil {
		return nil, 0, err
	}
	mpd.SortSongs(tracks)
	files, start := []string{}, 0
	for _, t := range tracks {
		if t.File == file {
			start = len(files)
		}
		files = append(files, t.File)
	}
	if len(files) == 0 && file != "" {
		files = []string{file}
	}
	return files, start, nil
}
