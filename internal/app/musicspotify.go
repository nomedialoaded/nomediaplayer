package app

// nml: shared Spotify links. Paste a Spotify track/album link in the Music view (or
// in /): the app reads title, artist and album from Spotify's public link
// preview (no account) and offers what fits: play it from the own library
// when it is there, or ask Lidarr for the album — or the single, when one
// exists, because Lidarr downloads releases, not loose songs.

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/spotifylink"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const spotifyMaxHits = 6

// Variables so tests can shorten them.
var (
	spotifyAlbumWait = 90 * time.Second
	spotifyAlbumStep = 3 * time.Second
)

type spotifyState struct {
	client *spotifylink.Client
	pick   *spotifyPick
}

type spotifyPick struct {
	info    spotifylink.Info
	options []spotifyOption
	index   int
}

type spotifyOption struct {
	label string
	play  string   // MPD file: play it
	album []string // MPD files: play the whole album
	queue string   // MPD file: add to the queue
	hit   *lidarr.AlbumHit
}

type spotifyFound struct {
	info  spotifylink.Info
	local []mpd.Song
	hits  []lidarr.AlbumHit
	lErr  error // Lidarr lookup failed (local result still usable)
	err   error
}

func (ms *musicState) spotifyState() *spotifyState {
	if ms.spotify == nil {
		ms.spotify = &spotifyState{client: spotifylink.New("nomediaplayer/" + Version)}
	}
	return ms.spotify
}

// openSpotify looks the link up in the background.
func (m libraryShell) openSpotify(link string) (libraryShell, tea.Cmd) {
	ms := m.music
	st := ms.spotifyState()
	client, mc := st.client, ms.client
	var lc *lidarr.Client
	if ms.lidarr.enabled() {
		lc = ms.lidarr.client
	}
	m.notice = "Spotify-link opzoeken…"
	m.renderStatus()
	return m, work(m.s.ctx, "Spotify-link opzoeken…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 40*time.Second)
		defer cancel()
		info, err := client.Lookup(ctx, link)
		if err != nil {
			return spotifyFound{err: err}
		}
		found := spotifyFound{info: info}
		found.local = findLocal(ctx, mc, info)
		if lc != nil {
			found.hits, found.lErr = lidarrHits(ctx, lc, info)
		}
		return found
	})
}

// findLocal: the song (or album) in the MPD library.
func findLocal(ctx context.Context, c *mpd.Client, info spotifylink.Info) []mpd.Song {
	if c == nil {
		return nil
	}
	match := func(list []mpd.Song) []mpd.Song {
		var out []mpd.Song
		for _, s := range list {
			if s.File == "" {
				continue
			}
			if info.Kind == spotifylink.Track && !sameTitle(songTitle(s), info.Title) {
				continue
			}
			if !artistMatches(songArtist(s)+" / "+s.AlbumArtist, info.Artists) {
				continue
			}
			out = append(out, s)
		}
		return out
	}
	if info.Kind == spotifylink.Album {
		for _, tag := range []string{"albumartist", "artist"} {
			if list, err := c.FindTags(ctx, "album", info.Album, tag, info.Artist); err == nil && len(list) > 0 {
				mpd.SortSongs(list)
				return list
			}
		}
		list, _ := c.Search(ctx, "album", baseTitle(info.Album), 300)
		var out []mpd.Song
		for _, s := range match(list) {
			if sameTitle(s.Album, info.Album) {
				out = append(out, s)
			}
		}
		mpd.SortSongs(out)
		return out
	}
	if list, err := c.FindTags(ctx, "title", info.Title, "artist", info.Artist); err == nil && len(list) > 0 {
		return preferAlbum(list, info.Album)
	}
	if base := baseTitle(info.Title); base != info.Title {
		if list, err := c.FindTags(ctx, "title", base, "artist", info.Artist); err == nil && len(list) > 0 {
			return preferAlbum(list, info.Album)
		}
	}
	list, _ := c.Search(ctx, "title", baseTitle(info.Title), 100)
	return preferAlbum(match(list), info.Album)
}

// Edition and credit additions Spotify (and some taggers) put in titles;
// they do not make it another recording. Live, remix, acoustic, … stay.
var titleExtras = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\s+-\s+(?:\d{4}\s+)?(?:digital(?:ly)?\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?$`),
	regexp.MustCompile(`(?i)\s+-\s+(?:single|album|mono|stereo|original)(?:\s+(?:version|mix))?$`),
	regexp.MustCompile(`(?i)\s*[(\[](?:\d{4}\s+)?(?:digital(?:ly)?\s+)?remaster(?:ed)?(?:\s+\d{4})?(?:\s+version)?[)\]]`),
	regexp.MustCompile(`(?i)\s*[(\[](?:feat\.?|ft\.?|featuring|with)\s+[^)\]]*[)\]]`),
	regexp.MustCompile(`(?i)\s*[(\[](?:\d+(?:th|st|nd|rd)\s+|\d{4}\s+)?(?:deluxe|expanded|anniversary|special|bonus track|super deluxe)[^)\]]*(?:edition|version)?[)\]]`),
	regexp.MustCompile(`(?i)\s+-\s+(?:deluxe|expanded|\d+(?:th|st|nd|rd)\s+anniversary)(?:\s+(?:edition|version))?$`),
}

// baseTitle drops those additions: "Song - Remastered 2011" → "Song".
func baseTitle(s string) string {
	for range 2 { // "Song (feat. X) - Remastered"
		for _, re := range titleExtras {
			s = re.ReplaceAllString(s, "")
		}
	}
	return strings.TrimSpace(s)
}

func sameTitle(a, b string) bool {
	return fold(a) == fold(b) || fold(baseTitle(a)) == fold(baseTitle(b))
}

func preferAlbum(list []mpd.Song, album string) []mpd.Song {
	sort.SliceStable(list, func(i, j int) bool {
		return sameTitle(list[i].Album, album) && !sameTitle(list[j].Album, album)
	})
	return list
}

func fold(s string) string {
	r := strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"", "‐", "-", "–", "-")
	return strings.ToLower(strings.Join(strings.Fields(r.Replace(s)), " "))
}

func artistMatches(have string, want []string) bool {
	h := fold(have)
	for _, a := range want {
		if a = fold(a); a != "" && strings.Contains(h, a) {
			return true
		}
	}
	return false
}

// lidarrHits finds the album and, for a song, its single.
func lidarrHits(ctx context.Context, c *lidarr.Client, info spotifylink.Info) ([]lidarr.AlbumHit, error) {
	album, song := baseTitle(info.Album), baseTitle(info.Title)
	terms := []string{info.Artist + " " + album}
	if info.Kind == spotifylink.Track && fold(song) != fold(album) {
		terms = append(terms, info.Artist+" "+song)
	}
	seen := map[string]bool{}
	type scored struct {
		hit   lidarr.AlbumHit
		score float64
	}
	var list []scored
	var firstErr error
	for _, term := range terms {
		hits, err := c.LookupAlbums(ctx, term)
		if err != nil && ctx.Err() == nil { // Lidarr's first metadata lookup is sometimes slow
			hits, err = c.LookupAlbums(ctx, term)
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		for _, h := range hits {
			if seen[h.Album.ForeignID] || !artistMatches(h.Artist.Name, info.Artists) {
				continue
			}
			seen[h.Album.ForeignID] = true
			score := 0.0
			title := fold(baseTitle(h.Album.Title))
			fAlbum, fSong := fold(album), fold(song)
			switch {
			case info.Kind == spotifylink.Track && title == fSong && h.Album.Type != "Album":
				score += 5 // the single (or EP) of this song: just this song, first
			case title == fAlbum:
				score += 4
				if info.Kind == spotifylink.Track && h.Album.Type == "Single" {
					score -= 1 // the album's title-track single, not this song
				}
			case info.Kind == spotifylink.Track && title == fSong:
				score += 3
			case strings.Contains(title, fAlbum) || strings.Contains(title, fSong):
				score += 1
			default:
				continue
			}
			if h.Album.Year != "" && h.Album.Year == info.Year {
				score += 1
			}
			if len(h.Album.Secondary) > 0 { // live, compilation, …
				score -= 1.5
			}
			list = append(list, scored{h, score})
		}
	}
	sort.SliceStable(list, func(i, j int) bool { return list[i].score > list[j].score })
	var out []lidarr.AlbumHit
	for _, s := range list {
		if len(out) == spotifyMaxHits {
			break
		}
		out = append(out, s.hit)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

func hitLabel(h lidarr.AlbumHit) string {
	kind := h.Album.Type
	if len(h.Album.Secondary) > 0 {
		kind = strings.Join(h.Album.Secondary, "/")
	}
	detail := strings.TrimSpace(strings.Join(nonEmpty(kind, h.Album.Year), ", "))
	label := "⬇ Lidarr: " + h.Album.Title
	if detail != "" {
		label += " (" + detail + ")"
	}
	switch {
	case h.Album.Have():
		label = "✓ " + strings.TrimPrefix(label, "⬇ ") + " · already in Lidarr"
	case h.Album.Monitored:
		label += " · already requested"
	case h.Artist.ID == 0:
		label += " · artist will be added"
	}
	return label
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func spotifyHeading(info spotifylink.Info) string {
	if info.Kind == spotifylink.Album {
		return fmt.Sprintf("Spotify-album: «%s» — %s", info.Title, strings.Join(info.Artists, ", "))
	}
	s := fmt.Sprintf("Spotify: «%s» — %s", info.Title, strings.Join(info.Artists, ", "))
	if d := strings.Join(nonEmpty(info.Album, info.Year), ", "); d != "" {
		s += " (" + d + ")"
	}
	return s
}

func (m *libraryShell) renderSpotifyPick() {
	st := m.music.spotify
	if st == nil || st.pick == nil {
		m.Model = m.Model.WithDialog(nil)
		return
	}
	pk := st.pick
	var b strings.Builder
	for i, o := range pk.options {
		marker := "  "
		if i == pk.index {
			marker = "› "
		}
		b.WriteString(marker + o.label + "\n")
	}
	m.Model = m.Model.WithDialog(&ui.Dialog{Title: spotifyHeading(pk.info), Body: strings.TrimRight(b.String(), "\n"),
		TruncateBody: true, Scrollable: true, Scroll: max(0, pk.index-8),
		Hint: "j/k choose · Enter run · Esc cancel · Lidarr fetches whole releases, not single tracks"})
}

func (m libraryShell) spotifyPickKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd) {
	st := m.music.spotify
	pk := st.pick
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		st.pick = nil
		m.notice = "Cancelled"
	case "j", "down", "tab":
		pk.index = min(len(pk.options)-1, pk.index+1)
	case "k", "up", "shift+tab":
		pk.index = max(0, pk.index-1)
	case "enter":
		o := pk.options[pk.index]
		st.pick = nil
		m.renderSpotifyPick()
		switch {
		case len(o.album) > 0:
			files := o.album
			m.notice = "Album from your library: " + pk.info.Title
			return m, m.musicDo("Playing album…", true, func(ctx context.Context, c *mpd.Client) error { return c.ReplaceAndPlay(ctx, files, 0) })
		case o.play != "":
			m.notice = "Playing from your library: " + pk.info.Title
			return m, m.musicAddAndPlay(o.play)
		case o.queue != "":
			file := o.queue
			return m, m.musicDo("Adding…", false, func(ctx context.Context, c *mpd.Client) error { return c.Add(ctx, file) })
		case o.hit != nil:
			if o.hit.Album.Have() {
				m.notice = "Already in Lidarr; MPD may not have scanned it yet (R)"
				return m, nil
			}
			return m, m.spotifyRequest(*o.hit)
		}
	}
	m.renderSpotifyPick()
	return m, nil
}

// spotifyRequest asks Lidarr for the release; adds the artist first when
// needed (monitor none) and waits for its discography.
func (m libraryShell) spotifyRequest(h lidarr.AlbumHit) tea.Cmd {
	ls := m.music.lidarr
	client, defaults, musicDir := ls.client, ls.defaults, m.music.musicDir()
	label := fmt.Sprintf("«%s» — %s", h.Album.Title, h.Artist.Name)
	m.notice = "Requesting in Lidarr: " + label
	return work(m.s.ctx, "Requesting album…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, spotifyAlbumWait+30*time.Second)
		defer cancel()
		id, renamed, err := ensureAlbum(ctx, client, defaults, h, musicDir)
		if err != nil {
			return lidarrRequested{label: label, renamed: renamed, err: err}
		}
		if id == 0 { // adopted folder already holds the album
			return lidarrRequested{label: label, renamed: renamed, have: true}
		}
		return lidarrRequested{label: label, renamed: renamed, err: client.RequestAlbum(ctx, id)}
	})
}

// ensureAlbum returns the album's Lidarr ID, adding the artist first (and
// using a look-alike library folder: renamed is its name). After an
// adoption it waits for Lidarr's first scan; id 0 means the album was
// already on disk and needs no download.
func ensureAlbum(ctx context.Context, c *lidarr.Client, defaults *lidarr.Defaults, h lidarr.AlbumHit, musicDir string) (id int, renamed string, err error) {
	if h.Album.ID > 0 {
		return h.Album.ID, "", nil
	}
	artistID := h.Artist.ID
	if artistID == 0 {
		if defaults == nil {
			d, err := c.Defaults(ctx)
			if err != nil {
				return 0, "", err
			}
			defaults = &d
		}
		aid, old, err := addArtistAdopting(ctx, c, *defaults, h.Artist, musicDir)
		if err != nil {
			return 0, "", err
		}
		artistID, renamed = aid, old
	}
	id, err = waitForAlbum(ctx, c, artistID, h)
	if err != nil || renamed == "" {
		return id, renamed, err
	}
	if err := c.WaitArtistRefresh(ctx, artistID, spotifyAlbumStep); err != nil {
		return id, renamed, nil // could not tell; let Lidarr decide
	}
	if albums, err := c.Albums(ctx, artistID); err == nil {
		for _, a := range albums {
			if a.ID == id && a.Have() {
				return 0, renamed, nil
			}
		}
	}
	return id, renamed, nil
}

func waitForAlbum(ctx context.Context, c *lidarr.Client, artistID int, h lidarr.AlbumHit) (int, error) {
	deadline := time.Now().Add(spotifyAlbumWait)
	for {
		albums, err := c.Albums(ctx, artistID)
		if err == nil {
			for _, a := range albums {
				if a.ForeignID == h.Album.ForeignID {
					return a.ID, nil
				}
			}
		}
		if time.Now().After(deadline) {
			if h.Artist.ID > 0 {
				return 0, errors.New("this release is outside the artist's metadata profile in Lidarr (e.g. singles switched off); pick the album")
			}
			return 0, errors.New("Lidarr has not fetched the discography yet; try again in a moment")
		}
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		case <-time.After(spotifyAlbumStep):
		}
	}
}

func (m libraryShell) updateSpotify(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	found, ok := msg.(spotifyFound)
	if !ok || m.music == nil {
		return m, nil, false
	}
	if found.err != nil {
		m.notice = "Spotify-link: " + found.err.Error()
		m.renderStatus()
		return m, nil, true
	}
	info := found.info
	var options []spotifyOption
	if len(found.local) > 0 {
		s := found.local[0]
		if info.Kind == spotifylink.Album {
			files := make([]string, 0, len(found.local))
			for _, t := range found.local {
				files = append(files, t.File)
			}
			options = append(options, spotifyOption{label: fmt.Sprintf("▶ Play album from your library (%d tracks, replaces the queue)", len(files)), album: files})
		} else {
			where := s.Album
			if where == "" {
				where = s.File
			}
			options = append(options,
				spotifyOption{label: "▶ Play from your library · " + where, play: s.File},
				spotifyOption{label: "+ Add to queue", queue: s.File})
		}
	}
	for i := range found.hits {
		h := found.hits[i]
		options = append(options, spotifyOption{label: hitLabel(h), hit: &h})
	}
	if len(options) == 0 {
		switch {
		case !m.music.lidarr.enabled():
			m.notice = spotifyHeading(info) + " · not in your library (Lidarr not set up)"
		case found.lErr != nil:
			m.notice = spotifyHeading(info) + " · Lidarr: " + found.lErr.Error()
		default:
			m.notice = spotifyHeading(info) + " · not in your library and not found via Lidarr (/ → Tab Lidarr to search yourself)"
		}
		m.renderStatus()
		return m, nil, true
	}
	m.music.spotifyState().pick = &spotifyPick{info: info, options: options}
	m.notice = ""
	m.renderSpotifyPick()
	m.renderStatus()
	return m, nil, true
}
