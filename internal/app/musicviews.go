package app

// nml: the Library column in the Music view shows one kind of list at a time
// (t / T): Artiesten, Albums, Genres, Playlists, Favorieten. The last search
// (⌕) stays on top in every view.

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	viewArtists = iota
	viewAlbums
	viewGenres
	viewPlaylists
	viewFavorites
	viewLidarr
	viewSpeakers
)

var libViewNames = []string{"Artists", "Albums", "Genres", "Playlists", "Favourites", "Lidarr", "Speakers"}

const (
	albumPrefix = "album:"
	genrePrefix = "genre:"
)

type musicCatalog struct {
	albums []mpd.AlbumRef
	genres []string
	err    error
}

func albumSource(a mpd.AlbumRef) string { return albumPrefix + a.Artist + "\x00" + a.Album }

func parseAlbumSource(source string) (artist, album string) {
	artist, album, _ = strings.Cut(strings.TrimPrefix(source, albumPrefix), "\x00")
	return artist, album
}

// splitGenres turns raw tag values like "Pop/Electro Rock;Indie Pop" into
// single genres.
func splitGenres(value string) []string {
	var out []string
	for _, part := range strings.FieldsFunc(value, func(r rune) bool { return r == ';' || r == '/' || r == ',' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func uniqueGenres(raw []string) []string {
	seen := map[string]string{}
	for _, v := range raw {
		for _, g := range splitGenres(v) {
			if _, ok := seen[strings.ToLower(g)]; !ok {
				seen[strings.ToLower(g)] = g
			}
		}
	}
	out := make([]string, 0, len(seen))
	for _, g := range seen {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out
}

func hasGenre(value, genre string) bool {
	for _, g := range splitGenres(value) {
		if strings.EqualFold(g, genre) {
			return true
		}
	}
	return false
}

func (m libraryShell) musicLoadCatalog() tea.Cmd {
	client := m.music.client
	return work(m.s.ctx, "Loading albums…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		albums, err := client.ListAlbums(ctx)
		if err != nil {
			return musicCatalog{err: err}
		}
		raw, err := client.List(ctx, "genre")
		return musicCatalog{albums: albums, genres: uniqueGenres(raw), err: err}
	})
}

func (m libraryShell) musicLoadAlbum(source string) tea.Cmd {
	client := m.music.client
	artist, album := parseAlbumSource(source)
	return work(m.s.ctx, "Loading album…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		tracks, err := client.FindTags(ctx, "albumartist", artist, "album", album)
		if err == nil && len(tracks) == 0 {
			tracks, err = client.FindTags(ctx, "album", album)
		}
		mpd.SortSongs(tracks)
		return musicTracks{source: source, tracks: tracks, err: err}
	})
}

// musicLoadGenre finds songs whose genre tag contains this genre as one of
// its parts ("Pop" matches "Pop;Rock", not "Indie Pop").
func (m libraryShell) musicLoadGenre(source string) tea.Cmd {
	client := m.music.client
	genre := strings.TrimPrefix(source, genrePrefix)
	return work(m.s.ctx, "Loading genre…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 2*musicTimeout)
		defer cancel()
		found, err := client.Search(ctx, "genre", genre, 0)
		var tracks []mpd.Song
		for _, s := range found {
			if hasGenre(s.Genre, genre) {
				tracks = append(tracks, s)
			}
		}
		return musicTracks{source: source, tracks: tracks, err: err}
	})
}

// libraryItems builds the Library column for the current view.
func (ms *musicState) libraryItems() []ui.Item {
	items := ms.searchItem()
	switch ms.libView {
	case viewAlbums:
		for _, a := range ms.albums {
			title := a.Album
			if a.Artist != "" {
				title += " — " + a.Artist
			}
			if ms.nd.isStarredAlbum(a.Album, a.Artist) {
				title = "★ " + title
			}
			items = append(items, ui.Item{ID: albumSource(a), Title: title, Channel: a.Artist})
		}
	case viewGenres:
		for _, g := range ms.genres {
			items = append(items, ui.Item{ID: genrePrefix + g, Title: g, Channel: "Genre"})
		}
	case viewPlaylists:
		items = append(items, ms.ndItemsOf(false, true)...)
	case viewFavorites:
		items = append(items, ms.ndItemsOf(true, false)...)
	case viewLidarr:
		return ms.lidarrLibraryItems() // the MPD search entry does not belong here
	case viewSpeakers:
		return ms.speakerLibraryItems()
	default:
		for _, artist := range ms.artists {
			items = append(items, ui.Item{ID: "artist:" + artist, Title: artist, Channel: artist})
		}
	}
	return items
}

func (ms *musicState) libraryEmpty() string {
	switch ms.libView {
	case viewPlaylists, viewFavorites:
		if ms.nd == nil {
			return "Navidrome not set up (navidrome_url) · t switches"
		}
		return "Nothing found in Navidrome · t switches"
	case viewAlbums, viewGenres:
		return "Loading… · t switches"
	case viewLidarr:
		return ms.lidarrLibraryEmpty()
	case viewSpeakers:
		return ms.speakerLibraryEmpty()
	}
	return ""
}

// libraryTitle shows the active view and its neighbours, e.g.
// "Albums ‹t›" in a narrow column.
func (ms *musicState) libraryTitle() string {
	return libViewNames[ms.libView] + " ‹t›"
}

// switchLibView moves to the next/previous view and selects its first entry
// (keeps the current tracks until the cursor moves).
func (m libraryShell) switchLibView(step int) (libraryShell, tea.Cmd) {
	ms := m.music
	for range libViewNames {
		ms.libView = (ms.libView + step + len(libViewNames)) % len(libViewNames)
		if ms.viewEnabled(ms.libView) {
			break
		}
	}
	m.Model = m.Model.ClearFilter(ui.Podcasts)
	m.renderMusic()
	m.Model = m.Model.WithFocus(ui.Podcasts)
	items := m.Model.Visible(ui.Podcasts)
	// Skip the search entry: land on the first entry of the new view.
	for _, it := range items {
		if !isSearchSource(it.ID) {
			m.Model = m.Model.Select(ui.Podcasts, it.ID)
			break
		}
	}
	m.notice = "Column 1: " + libViewNames[ms.libView] + " · t next, T previous"
	m.renderStatus()
	if ms.libView == viewSpeakers {
		return m, tea.Batch(m.musicSyncSelection(), m.speakersStartPolling())
	}
	return m, m.musicSyncSelection()
}

// viewEnabled: views of services that are not set up are skipped by t / T
// (switch them on with ? → Setup). A service with a wrong address stays
// visible so its error can be read.
func (ms *musicState) viewEnabled(view int) bool {
	switch view {
	case viewPlaylists, viewFavorites:
		return ms.nd != nil
	case viewLidarr:
		return ms.lidarr != nil && (ms.lidarr.client != nil || ms.lidarr.err != "")
	case viewSpeakers:
		return ms.speakers != nil && (ms.speakers.client != nil || ms.speakers.err != "")
	}
	return true
}

func (ms *musicState) libViewHint() string {
	next := libViewNames[(ms.libView+1)%len(libViewNames)]
	return fmt.Sprintf("t: %s", next)
}
