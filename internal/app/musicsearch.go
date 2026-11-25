package app

// nml: search the MPD library from the Music view (z). Results become a
// "⌕" entry at the top of the Artiesten column and play as one list.

import (
	"context"
	"fmt"
	"github.com/nomedialoaded/nomediaplayer/internal/spotifylink"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	searchPrefix = "search:"
	searchLimit  = 500
)

type searchScope struct{ id, label string }

var searchScopes = []searchScope{
	{"any", "All"},
	{"artist", "Artist"},
	{"album", "Album"},
	{"title", "Track"},
	{"genre", "Genre"},
	{"lidarr", "Lidarr"}, // nml: not in MPD → find it in Lidarr
}

const lidarrScope = 5

// musicSearch is the open search dialog.
type musicSearch struct {
	input string
	scope int
}

// searchResult is the last search, kept as a Library-column entry.
type searchResult struct {
	query string
	scope int
	count int
}

func isSearchSource(source string) bool { return strings.HasPrefix(source, searchPrefix) }

func searchSource(scope int, query string) string {
	return searchPrefix + searchScopes[scope].id + ":" + query
}

func (m libraryShell) openMusicSearch() libraryShell {
	ms := m.music
	ms.searching = &musicSearch{}
	// A new search starts without the old column filters.
	m.Model = m.Model.ClearFilter(ui.Podcasts).ClearFilter(ui.Episodes)
	if ms.lastSearch != nil {
		ms.searching.scope = ms.lastSearch.scope
	}
	if ms.libView == viewLidarr {
		ms.searching.scope = lidarrScope
	}
	m.renderMusicSearch()
	return m
}

func (m *libraryShell) renderMusicSearch() {
	sd := m.music.searching
	if sd == nil {
		m.Model = m.Model.WithDialog(nil)
		return
	}
	var tabs []string
	for i, s := range searchScopes {
		if i == lidarrScope && !m.music.lidarrOn() {
			continue // nml: Lidarr not set up
		}
		if i == sd.scope {
			tabs = append(tabs, "["+s.label+"]")
		} else {
			tabs = append(tabs, s.label)
		}
	}
	m.Model = m.Model.WithDialog(&ui.Dialog{
		Title:      "Search music · " + searchScopes[sd.scope].label,
		Body:       strings.Join(tabs, "  ") + "\n" + searchExplain(sd.scope),
		InputLabel: "Search",
		InputValue: sd.input,
		Hint:       "Tab field · Enter search · Esc cancel · Ctrl+U clear",
	})
}

func (m libraryShell) musicSearchKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd) {
	sd := m.music.searching
	switch key := msg.String(); key {
	case "ctrl+c":
		return m, tea.Quit
	case "esc":
		m.music.searching = nil
		m.notice = "Search cancelled"
		m.renderMusicSearch()
		return m, nil
	case "tab", "down", "shift+tab", "up":
		step := 1
		if key == "shift+tab" || key == "up" {
			step = len(searchScopes) - 1
		}
		sd.scope = (sd.scope + step) % len(searchScopes)
		if sd.scope == lidarrScope && !m.music.lidarrOn() {
			sd.scope = (sd.scope + step) % len(searchScopes)
		}
	case "enter":
		query := strings.TrimSpace(sd.input)
		if query == "" {
			return m, nil
		}
		m.music.searching = nil
		m.renderMusicSearch()
		if link, ok := spotifylink.Find(query); ok {
			return m.openSpotify(link)
		}
		if sd.scope == lidarrScope {
			return m.openLidarrResults(query)
		}
		source := searchSource(sd.scope, query)
		m.music.lastSearch = &searchResult{query: query, scope: sd.scope, count: -1}
		m.music.source = source
		m.music.tracks, m.music.trackND = nil, nil
		m.renderMusic()
		m.Model = m.Model.Select(ui.Podcasts, source).WithFocus(ui.Episodes)
		return m, m.musicLoadSearch(source)
	default:
		ti := ui.TextInput{Value: sd.input, Limit: 300}
		ti.EditKey(msg)
		sd.input = ti.Value
	}
	m.renderMusicSearch()
	return m, nil
}

func (m *libraryShell) musicSearchPaste(text string) {
	if sd := m.music.searching; sd != nil {
		ti := ui.TextInput{Value: sd.input, Limit: 300}
		ti.Append(text)
		sd.input = ti.Value
		m.renderMusicSearch()
	}
}

// musicLoadSearch runs the search for a "search:<tag>:<query>" source.
// Artist searches cover both artist and albumartist tags.
func (m libraryShell) musicLoadSearch(source string) tea.Cmd {
	client := m.music.client
	tag, query, _ := strings.Cut(strings.TrimPrefix(source, searchPrefix), ":")
	return work(m.s.ctx, "Searching…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		tags := []string{tag}
		if tag == "artist" {
			tags = []string{"artist", "albumartist"}
		}
		seen := map[string]bool{}
		var songs []mpd.Song
		for _, t := range tags {
			found, err := client.Search(ctx, t, query, searchLimit)
			if err != nil {
				return musicTracks{source: source, err: err}
			}
			for _, s := range found {
				if !seen[s.File] && len(songs) < searchLimit {
					seen[s.File] = true
					songs = append(songs, s)
				}
			}
		}
		mpd.SortSongs(songs)
		return musicTracks{source: source, tracks: songs}
	})
}

func (ms *musicState) searchItem() []ui.Item {
	r := ms.lastSearch
	if r == nil {
		return nil
	}
	title := fmt.Sprintf("⌕ %s · %s", r.query, searchScopes[r.scope].label)
	if r.count >= 0 {
		title += fmt.Sprintf(" (%d)", r.count)
		if r.count >= searchLimit {
			title = fmt.Sprintf("⌕ %s · %s (%d+)", r.query, searchScopes[r.scope].label, searchLimit)
		}
	}
	return []ui.Item{{ID: searchSource(r.scope, r.query), Title: title, Channel: "Search"}}
}

func searchExplain(scope int) string {
	if scope == lidarrScope {
		return "Searches artists and albums in Lidarr (MusicBrainz) · then pick and request albums."
	}
	return "Searches the MPD library (part of a word is enough)."
}

// lidarrOn: Lidarr is set up (the search tab is hidden otherwise).
func (ms *musicState) lidarrOn() bool { return ms.lidarr != nil && ms.lidarr.client != nil }
