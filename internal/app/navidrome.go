package app

// nml: Navidrome (Subsonic API) in the Music view. Navidrome is the single
// source of truth for playlists and favorites; nomediaplayer reads them live and
// writes every edit straight back. Playback stays in MPD: Navidrome songs are
// matched to MPD files (real path when Navidrome reports it, else by tags).

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/subsonic"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const ndTimeout = 20 * time.Second

// Library-column IDs for Navidrome entries.
const (
	ndStarredID  = "nd:starred"
	ndAlbumPfx   = "nd:album:"
	ndPlaylistPf = "nd:pl:"
	ndErrorID    = "nd:err"
)

type ndState struct {
	client    *subsonic.Client
	playlists []subsonic.Playlist
	albums    []subsonic.Album
	starred   []subsonic.Song
	starKeys  map[string]bool
	err       string
	loaded    bool

	mu    sync.Mutex
	files map[string]string // navidrome song id -> MPD file ("" = not found)
}

func newND(url, user, pass string) *ndState {
	if strings.TrimSpace(url) == "" {
		return nil
	}
	return &ndState{client: subsonic.New(url, user, pass), files: map[string]string{}, starKeys: map[string]bool{}}
}

func songKey(title, album string) string {
	return strings.ToLower(strings.TrimSpace(title)) + "\x00" + strings.ToLower(strings.TrimSpace(album))
}

func (nd *ndState) isStarred(s mpd.Song) bool {
	return nd != nil && nd.starKeys[songKey(s.Title, s.Album)]
}

// isStarredAlbum matches an MPD album to a Navidrome favorite by name/artist.
func (nd *ndState) isStarredAlbum(album, artist string) bool {
	if nd == nil {
		return false
	}
	for _, a := range nd.albums {
		if strings.EqualFold(a.Name, album) && (artist == "" || strings.EqualFold(a.Artist, artist)) {
			return true
		}
	}
	return false
}

func (nd *ndState) albumStarred(id string) bool {
	if nd == nil {
		return false
	}
	for _, a := range nd.albums {
		if a.ID == id {
			return true
		}
	}
	return false
}

func (nd *ndState) playlistName(id string) string {
	for _, p := range nd.playlists {
		if p.ID == id {
			return p.Name
		}
	}
	return ""
}

// --- messages ---------------------------------------------------------------

type ndLists struct {
	playlists []subsonic.Playlist
	albums    []subsonic.Album
	starred   []subsonic.Song
	err       error
}

type ndEdited struct {
	err          error
	notice       string
	reloadTracks bool
}

// --- loading ----------------------------------------------------------------

func (m libraryShell) ndLoadLists() tea.Cmd {
	nd := m.music.nd
	if nd == nil {
		return nil
	}
	client := nd.client
	return work(m.s.ctx, "Loading Navidrome…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, ndTimeout)
		defer cancel()
		playlists, err := client.Playlists(ctx)
		if err != nil {
			return ndLists{err: err}
		}
		albums, starred, err := client.Starred(ctx)
		return ndLists{playlists: playlists, albums: albums, starred: starred, err: err}
	})
}

// ndLoadSource fetches a Navidrome list and matches it to MPD files.
func (m libraryShell) ndLoadSource(source string) tea.Cmd {
	nd, mc := m.music.nd, m.music.client
	if nd == nil {
		return nil
	}
	return work(m.s.ctx, "Loading tracks…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, ndTimeout)
		defer cancel()
		var list []subsonic.Song
		var err error
		switch {
		case source == ndStarredID:
			_, list, err = nd.client.Starred(ctx)
		case strings.HasPrefix(source, ndAlbumPfx):
			list, err = nd.client.Album(ctx, strings.TrimPrefix(source, ndAlbumPfx))
		case strings.HasPrefix(source, ndPlaylistPf):
			list, err = nd.client.Playlist(ctx, strings.TrimPrefix(source, ndPlaylistPf))
		}
		if err != nil {
			return musicTracks{source: source, err: err}
		}
		tracks := make([]mpd.Song, len(list))
		for i, s := range list {
			tracks[i] = nd.resolve(ctx, mc, s)
		}
		return musicTracks{source: source, tracks: tracks, nd: list}
	})
}

// resolve finds the MPD file for one Navidrome song. File stays "" when the
// song is not in the MPD library.
func (nd *ndState) resolve(ctx context.Context, mc *mpd.Client, s subsonic.Song) mpd.Song {
	out := mpd.Song{Title: s.Title, Artist: s.Artist, Album: s.Album, Track: s.Track, Disc: s.Disc, Duration: s.Duration, Pos: -1, ID: -1}
	if s.Year > 0 {
		out.Date = strconv.Itoa(s.Year)
	}
	nd.mu.Lock()
	file, cached := nd.files[s.ID]
	nd.mu.Unlock()
	if cached {
		out.File = file
		return out
	}
	file = nd.match(ctx, mc, s)
	if ctx.Err() == nil {
		nd.mu.Lock()
		nd.files[s.ID] = file
		nd.mu.Unlock()
	}
	out.File = file
	return out
}

func (nd *ndState) match(ctx context.Context, mc *mpd.Client, s subsonic.Song) string {
	// 1. Real path (Navidrome "Report Real Path"), possibly with a library
	//    prefix such as /music/: try the path and its shorter suffixes.
	p := strings.TrimPrefix(s.Path, "/")
	for i := 0; i < 3 && p != ""; i++ {
		if found, err := mc.FindTags(ctx, "file", p); err == nil && len(found) > 0 {
			return found[0].File
		}
		_, rest, ok := strings.Cut(p, "/")
		if !ok {
			break
		}
		p = rest
	}
	// 2. Tags, most specific first.
	if s.Title == "" {
		return ""
	}
	for _, pairs := range [][]string{
		{"title", s.Title, "artist", s.Artist, "album", s.Album},
		{"title", s.Title, "album", s.Album},
		{"title", s.Title, "artist", s.Artist},
	} {
		if pairs[3] == "" || (len(pairs) > 4 && pairs[5] == "") {
			continue
		}
		found, err := mc.FindTags(ctx, pairs...)
		if err != nil || len(found) == 0 {
			continue
		}
		best, diff := found[0].File, math.MaxFloat64
		for _, f := range found {
			if d := math.Abs(f.Duration - s.Duration); d < diff {
				best, diff = f.File, d
			}
		}
		return best
	}
	return ""
}

// lookup finds the Navidrome song for an MPD song (title search, then the
// best album/artist/duration match).
func ndLookup(ctx context.Context, client *subsonic.Client, s mpd.Song) (subsonic.Song, error) {
	title := strings.TrimSpace(s.Title)
	if title == "" {
		return subsonic.Song{}, fmt.Errorf("track without a title tag")
	}
	found, err := client.SearchSongs(ctx, title, 50)
	if err != nil {
		return subsonic.Song{}, err
	}
	best, score := subsonic.Song{}, 0
	for _, f := range found {
		if !strings.EqualFold(strings.TrimSpace(f.Title), title) {
			continue
		}
		sc := 1
		if strings.EqualFold(f.Album, s.Album) {
			sc += 4
		}
		if a := songArtist(s); a != "" && (strings.EqualFold(f.Artist, a) || strings.Contains(strings.ToLower(f.Artist), strings.ToLower(a))) {
			sc += 2
		}
		if s.Duration > 0 && math.Abs(f.Duration-s.Duration) <= 3 {
			sc++
		}
		if sc > score {
			best, score = f, sc
		}
	}
	if score < 3 {
		return subsonic.Song{}, fmt.Errorf("%q not found in Navidrome", title)
	}
	return best, nil
}

// --- rendering --------------------------------------------------------------

func (ms *musicState) ndItems() []ui.Item { return ms.ndItemsOf(true, true) }

// ndItemsOf lists favorites and/or playlists for the Library column.
func (ms *musicState) ndItemsOf(favorites, playlists bool) []ui.Item {
	nd := ms.nd
	if nd == nil {
		return nil
	}
	if nd.err != "" {
		return []ui.Item{{ID: ndErrorID, Title: "⚠ Navidrome: " + nd.err, Channel: "Navidrome"}}
	}
	if !nd.loaded {
		return []ui.Item{{ID: ndErrorID, Title: "… loading Navidrome", Channel: "Navidrome"}}
	}
	var items []ui.Item
	if favorites {
		items = append(items, ui.Item{ID: ndStarredID, Title: fmt.Sprintf("★ Favourite tracks (%d)", len(nd.starred)), Channel: "Favourites"})
		for _, a := range nd.albums {
			items = append(items, ui.Item{ID: ndAlbumPfx + a.ID, Title: "★ " + a.Name + " — " + a.Artist, Channel: "Favourite album"})
		}
	}
	for _, p := range nd.playlists {
		if !playlists {
			break
		}
		items = append(items, ui.Item{ID: ndPlaylistPf + p.ID, Title: fmt.Sprintf("≡ %s (%d)", p.Name, p.SongCount), Channel: "Playlist"})
	}
	return items
}

func isNDSource(source string) bool { return strings.HasPrefix(source, "nd:") && source != ndErrorID }

// --- events -----------------------------------------------------------------

func (m libraryShell) updateND(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	if m.music == nil || m.music.nd == nil {
		return m, nil, false
	}
	nd := m.music.nd
	switch msg := msg.(type) {
	case ndLists:
		if msg.err != nil {
			nd.err = msg.err.Error()
			if m.music.active {
				m.renderMusic()
			}
			return m, nil, true
		}
		nd.err, nd.loaded = "", true
		nd.playlists, nd.albums, nd.starred = msg.playlists, msg.albums, msg.starred
		nd.starKeys = map[string]bool{}
		for _, s := range msg.starred {
			nd.starKeys[songKey(s.Title, s.Album)] = true
		}
		if m.music.active {
			m.renderMusic()
			return m, m.musicSyncSelection(), true
		}
		return m, nil, true
	case ndEdited:
		if msg.err != nil {
			m.notice = "Navidrome: " + msg.err.Error()
		} else {
			m.notice = msg.notice
		}
		m.renderStatus()
		cmds := []tea.Cmd{m.ndLoadLists()}
		if msg.reloadTracks && isNDSource(m.music.source) {
			cmds = append(cmds, m.ndLoadSource(m.music.source))
		}
		return m, tea.Batch(cmds...), true
	}
	return m, nil, false
}

// ndDo runs one Navidrome edit and refreshes the lists afterwards.
func (m libraryShell) ndDo(label string, reloadTracks bool, run func(context.Context, *subsonic.Client) (string, error)) tea.Cmd {
	client := m.music.nd.client
	return work(m.s.ctx, label, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, ndTimeout)
		defer cancel()
		notice, err := run(ctx, client)
		return ndEdited{err: err, notice: notice, reloadTracks: reloadTracks}
	})
}

// --- targets ----------------------------------------------------------------

// ndTarget returns the song the edit keys act on: the selected track or
// queue entry, else the current song.
func (m libraryShell) ndTarget() (mpd.Song, *subsonic.Song, bool) {
	switch m.Model.Focus() {
	case ui.Episodes:
		if item, ok := m.Model.Selected(ui.Episodes); ok {
			if i, ok := m.musicTrackIndex(item.ID); ok {
				var known *subsonic.Song
				if i < len(m.music.trackND) {
					s := m.music.trackND[i]
					known = &s
				}
				return m.music.tracks[i], known, true
			}
		}
	case ui.Queue:
		if item, ok := m.Model.Selected(ui.Queue); ok {
			if s, ok := m.musicQueueSong(item.ID); ok {
				return s, nil, true
			}
		}
	}
	if m.music.hasCurrent {
		return m.music.current, nil, true
	}
	return mpd.Song{}, nil, false
}

func resolveTarget(ctx context.Context, client *subsonic.Client, s mpd.Song, known *subsonic.Song) (subsonic.Song, error) {
	if known != nil && known.ID != "" {
		return *known, nil
	}
	return ndLookup(ctx, client, s)
}

// --- keys -------------------------------------------------------------------

type musicPicker struct {
	title   string
	options []pickOption
	index   int
	// naming: text input for a new playlist name.
	naming bool
	input  string
	// what to add: one song, or the whole MPD queue.
	song      mpd.Song
	known     *subsonic.Song
	wholeList bool
}

type pickOption struct{ id, label string }

const pickNew = "\x00new"

func (m libraryShell) ndKey(key string) (libraryShell, tea.Cmd, bool) {
	ms := m.music
	if ms.nd == nil {
		switch key {
		case "p", "P", "x", "b", "B":
			m.notice = "Navidrome not set up (navidrome_url in config.toml)"
			m.renderStatus()
			return m, nil, true
		}
		return m, nil, false
	}
	switch key {
	case "p":
		s, known, ok := m.ndTarget()
		if !ok {
			return m, nil, true
		}
		opts := []pickOption{{pickNew, "+ New playlist…"}}
		for _, p := range ms.nd.playlists {
			opts = append(opts, pickOption{p.ID, fmt.Sprintf("%s (%d)", p.Name, p.SongCount)})
		}
		ms.picker = &musicPicker{title: "Add to playlist: " + songTitle(s), options: opts, song: s, known: known}
		m.renderPicker()
		return m, nil, true
	case "P":
		if len(ms.queue) == 0 {
			m.notice = "Queue is empty"
			m.renderStatus()
			return m, nil, true
		}
		ms.picker = &musicPicker{title: fmt.Sprintf("Save queue (%d) as playlist", len(ms.queue)), naming: true, wholeList: true}
		m.renderPicker()
		return m, nil, true
	case "x":
		if m.Model.Focus() != ui.Episodes || !strings.HasPrefix(ms.source, ndPlaylistPf) {
			m.notice = "x works in a Navidrome playlist (Tracks column)"
			m.renderStatus()
			return m, nil, true
		}
		item, ok := m.Model.Selected(ui.Episodes)
		i, found := m.musicTrackIndex(item.ID)
		if !ok || !found {
			return m, nil, true
		}
		id := strings.TrimPrefix(ms.source, ndPlaylistPf)
		name, title := ms.nd.playlistName(id), songTitle(ms.tracks[i])
		return m, m.ndDo("Removing from playlist…", true, func(ctx context.Context, c *subsonic.Client) (string, error) {
			return fmt.Sprintf("%q uit %q gehaald", title, name), c.RemoveFromPlaylist(ctx, id, i)
		}), true
	case "b":
		s, known, ok := m.ndTarget()
		if !ok {
			return m, nil, true
		}
		star := !ms.nd.isStarred(s)
		return m, m.ndDo("Updating favourite…", ms.source == ndStarredID, func(ctx context.Context, c *subsonic.Client) (string, error) {
			song, err := resolveTarget(ctx, c, s, known)
			if err != nil {
				return "", err
			}
			if star {
				return fmt.Sprintf("★ %q is a favourite", song.Title), c.StarSong(ctx, song.ID, true)
			}
			return fmt.Sprintf("%q is no longer a favourite", song.Title), c.StarSong(ctx, song.ID, false)
		}), true
	case "B":
		if m.Model.Focus() == ui.Podcasts {
			if item, ok := m.Model.Selected(ui.Podcasts); ok && strings.HasPrefix(item.ID, ndAlbumPfx) {
				id := strings.TrimPrefix(item.ID, ndAlbumPfx)
				return m, m.ndDo("Updating favourite album…", false, func(ctx context.Context, c *subsonic.Client) (string, error) {
					return "Album is no longer a favourite", c.StarAlbum(ctx, id, false)
				}), true
			}
		}
		s, known, ok := m.ndTarget()
		if !ok {
			return m, nil, true
		}
		return m, m.ndDo("Updating favourite album…", false, func(ctx context.Context, c *subsonic.Client) (string, error) {
			song, err := resolveTarget(ctx, c, s, known)
			if err != nil {
				return "", err
			}
			if song.AlbumID == "" {
				return "", fmt.Errorf("no album known for %q", song.Title)
			}
			if ms.nd.albumStarred(song.AlbumID) {
				return fmt.Sprintf("Album %q is no longer a favourite", song.Album), c.StarAlbum(ctx, song.AlbumID, false)
			}
			return fmt.Sprintf("★ Album %q is a favourite", song.Album), c.StarAlbum(ctx, song.AlbumID, true)
		}), true
	}
	return m, nil, false
}

func (m *libraryShell) renderPicker() {
	pk := m.music.picker
	if pk == nil {
		m.Model = m.Model.WithDialog(nil)
		return
	}
	if pk.naming {
		m.Model = m.Model.WithDialog(&ui.Dialog{
			Title:      pk.title,
			Body:       "Name of the new playlist in Navidrome.",
			InputLabel: "Name",
			InputValue: pk.input,
			Hint:       "Esc cancel · Enter save · Ctrl+U clear",
		})
		return
	}
	var b strings.Builder
	for i, o := range pk.options {
		marker := "  "
		if i == pk.index {
			marker = "› "
		}
		b.WriteString(marker + o.label + "\n")
	}
	m.Model = m.Model.WithDialog(&ui.Dialog{
		Title:      pk.title,
		Body:       strings.TrimRight(b.String(), "\n"),
		Scrollable: true,
		Scroll:     max(0, pk.index-8),
		Hint:       "j/k choose · Enter add · Esc cancel",
	})
}

func (m libraryShell) musicPickerKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd) {
	pk := m.music.picker
	key := msg.String()
	if key == "ctrl+c" {
		return m, tea.Quit
	}
	if key == "esc" {
		m.music.picker = nil
		m.notice = "Cancelled"
		m.renderPicker()
		m.renderStatus()
		return m, nil
	}
	if pk.naming {
		if key == "enter" {
			name := strings.TrimSpace(pk.input)
			if name == "" {
				return m, nil
			}
			m.music.picker = nil
			m.renderPicker()
			return m, m.ndCreate(name, pk)
		}
		ti := ui.TextInput{Value: pk.input, Limit: 120}
		ti.EditKey(msg)
		pk.input = ti.Value
		m.renderPicker()
		return m, nil
	}
	switch key {
	case "j", "down", "tab":
		pk.index = min(len(pk.options)-1, pk.index+1)
	case "k", "up", "shift+tab":
		pk.index = max(0, pk.index-1)
	case "g", "home":
		pk.index = 0
	case "G", "end":
		pk.index = len(pk.options) - 1
	case "enter":
		opt := pk.options[pk.index]
		if opt.id == pickNew {
			pk.naming, pk.title = true, "New playlist with: "+songTitle(pk.song)
			m.renderPicker()
			return m, nil
		}
		m.music.picker = nil
		m.renderPicker()
		s, known, id, name := pk.song, pk.known, opt.id, m.music.nd.playlistName(opt.id)
		reload := m.music.source == ndPlaylistPf+id
		return m, m.ndDo("Adding to playlist…", reload, func(ctx context.Context, c *subsonic.Client) (string, error) {
			song, err := resolveTarget(ctx, c, s, known)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("%q added to %q", song.Title, name), c.AddToPlaylist(ctx, id, []string{song.ID})
		})
	}
	m.renderPicker()
	return m, nil
}

func (m libraryShell) ndCreate(name string, pk *musicPicker) tea.Cmd {
	var songs []mpd.Song
	var known []*subsonic.Song
	if pk.wholeList {
		songs = append(songs, m.music.queue...)
		known = make([]*subsonic.Song, len(songs))
	} else {
		songs, known = []mpd.Song{pk.song}, []*subsonic.Song{pk.known}
	}
	return m.ndDo("Playlist maken…", false, func(ctx context.Context, c *subsonic.Client) (string, error) {
		var ids []string
		for i, s := range songs {
			if song, err := resolveTarget(ctx, c, s, known[i]); err == nil {
				ids = append(ids, song.ID)
			}
		}
		if len(ids) == 0 {
			return "", fmt.Errorf("none of the tracks found in Navidrome")
		}
		if err := c.CreatePlaylist(ctx, name, ids); err != nil {
			return "", err
		}
		notice := fmt.Sprintf("Playlist %q created (%d tracks)", name, len(ids))
		if missing := len(songs) - len(ids); missing > 0 {
			notice += " · " + strconv.Itoa(missing) + " not found in Navidrome"
		}
		return notice, nil
	})
}

func (m *libraryShell) musicPickerPaste(text string) {
	if pk := m.music.picker; pk != nil && pk.naming {
		ti := ui.TextInput{Value: pk.input, Limit: 120}
		ti.Append(text)
		pk.input = ti.Value
		m.renderPicker()
	}
}
