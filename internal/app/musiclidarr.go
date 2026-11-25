package app

// nml: Lidarr in the Music view. Column 1 view "Lidarr" (t) shows the
// download queue and the artists of the last Lidarr search (/ → Tab Lidarr).
// Enter on an artist that is not in Lidarr adds it without downloading;
// Enter on an album asks Lidarr to search and download just that album.
// Finished downloads make MPD update its library (Lidarr and MPD share the
// music folder).

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	lidarrPrefix    = "lidarr:"
	lidarrQueueID   = "lidarr:queue"
	lidarrArtistPfx = "lidarr:artist:"
	lidarrAlbumPfx  = "lidarr:album:"
	lidarrDLPfx     = "lidarr:dl:"
	lidarrHitPfx    = "lidarr:hit:" // + MusicBrainz release group (album search result)
	lidarrTimeout   = 20 * time.Second
)

// Polling delays (variables so tests can shorten them).
var (
	lidarrPollEvery   = 10 * time.Second
	lidarrRecheckWait = 3 * time.Second
	lidarrFirstLook   = 5 * time.Second
)

type lidarrState struct {
	client   *lidarr.Client
	err      string // configuration problem
	query    string
	artists  []lidarr.Artist
	hits     []lidarr.AlbumHit         // album search results
	order    []string                  // column 1 IDs in Lidarr's relevance order
	albums   map[string][]lidarr.Album // by foreign artist ID
	loading  map[string]bool
	queue    []lidarr.QueueItem
	queueErr string
	polling  bool
	defaults *lidarr.Defaults
	confirm  *lidarrConfirm
	// waiting: artists just added whose discography Lidarr is still fetching.
	waiting map[string]int
	// tracks per Lidarr album ID, loaded when the cursor rests on the album.
	tracks       map[int][]lidarr.Track
	tracksErr    map[int]string
	tracksLoaded map[int]bool // requested (in flight or done)
	covers       *lidarrCovers
}

type lidarrConfirm struct {
	title, body string
	artist      *lidarr.Artist
	album       *lidarr.Album
	artistName  string
	hit         *lidarr.AlbumHit // add the artist, then request this album
}

type (
	lidarrSearchDone struct {
		query string
		hits  []lidarr.SearchHit
		err   error
	}
	lidarrAlbumsDone struct {
		foreign string
		albums  []lidarr.Album
		err     error
	}
	lidarrQueueDone struct {
		queue []lidarr.QueueItem
		err   error
	}
	lidarrAdded struct {
		foreign  string
		id       int
		defaults *lidarr.Defaults
		renamed  string // existing library folder the artist now uses
		err      error
	}
	lidarrRequested struct {
		label   string
		renamed string
		have    bool // already in the library after adopting its folder
		err     error
	}
	lidarrTracksDone struct {
		album  int
		tracks []lidarr.Track
		err    error
	}
	lidarrPoll    struct{}
	lidarrRecheck struct{ foreign string }
)

func newLidarrState(address, key string) *lidarrState {
	ls := &lidarrState{albums: map[string][]lidarr.Album{}, loading: map[string]bool{}, waiting: map[string]int{},
		tracks: map[int][]lidarr.Track{}, tracksErr: map[int]string{}, tracksLoaded: map[int]bool{}}
	c, err := lidarr.New(address, key)
	if err != nil {
		ls.err = err.Error()
	}
	ls.client = c
	if c != nil {
		ls.covers = newLidarrCovers(c)
	}
	return ls
}

func isLidarrSource(source string) bool { return strings.HasPrefix(source, lidarrPrefix) }

func (ls *lidarrState) enabled() bool { return ls != nil && ls.client != nil }

// artist resolves an artist source, or the artist of an album hit.
func (ls *lidarrState) artist(source string) (*lidarr.Artist, bool) {
	if h, ok := ls.hit(source); ok {
		for i := range ls.artists { // prefer the artist row (kept up to date)
			if ls.artists[i].ForeignID == h.Artist.ForeignID {
				return &ls.artists[i], true
			}
		}
		return &h.Artist, true
	}
	foreign := strings.TrimPrefix(source, lidarrArtistPfx)
	for i := range ls.artists {
		if ls.artists[i].ForeignID == foreign {
			return &ls.artists[i], true
		}
	}
	for i := range ls.hits {
		if ls.hits[i].Artist.ForeignID == foreign {
			return &ls.hits[i].Artist, true
		}
	}
	return nil, false
}

func (ls *lidarrState) hit(source string) (*lidarr.AlbumHit, bool) {
	if !strings.HasPrefix(source, lidarrHitPfx) {
		return nil, false
	}
	foreign := strings.TrimPrefix(source, lidarrHitPfx)
	for i := range ls.hits {
		if ls.hits[i].Album.ForeignID == foreign {
			return &ls.hits[i], true
		}
	}
	return nil, false
}

// hitAlbum is the Lidarr album of a hit (fresh statistics once the artist's
// albums are loaded); ok=false when the album is not in Lidarr.
func (ls *lidarrState) hitAlbum(h *lidarr.AlbumHit) (*lidarr.Album, bool) {
	list := ls.albums[h.Artist.ForeignID]
	for i := range list {
		if list[i].ForeignID == h.Album.ForeignID {
			return &list[i], true
		}
	}
	if h.Album.ID > 0 {
		return &h.Album, true
	}
	return nil, false
}

// setArtistID marks an artist as added everywhere it is listed.
func (ls *lidarrState) setArtistID(foreign string, id int) {
	for i := range ls.artists {
		if ls.artists[i].ForeignID == foreign {
			ls.artists[i].ID = id
		}
	}
	for i := range ls.hits {
		if ls.hits[i].Artist.ForeignID == foreign {
			ls.hits[i].Artist.ID = id
		}
	}
}

func (ls *lidarrState) album(source, id string) (*lidarr.Album, bool) {
	n, err := strconv.Atoi(strings.TrimPrefix(id, lidarrAlbumPfx))
	if err != nil {
		return nil, false
	}
	if h, ok := ls.hit(source); ok {
		if al, found := ls.hitAlbum(h); found && al.ID == n {
			return al, true
		}
		return nil, false
	}
	list := ls.albums[strings.TrimPrefix(source, lidarrArtistPfx)]
	for i := range list {
		if list[i].ID == n {
			return &list[i], true
		}
	}
	return nil, false
}

// active downloads (not imported yet).
func (ls *lidarrState) activeCount() int { return len(ls.queue) }

// --- column 1 and 2 ---------------------------------------------------------

func (ms *musicState) lidarrLibraryItems() []ui.Item {
	ls := ms.lidarr
	if !ls.enabled() {
		return nil
	}
	title := "⬇ Downloads"
	if n := ls.activeCount(); n > 0 {
		title += fmt.Sprintf(" (%d)", n)
	}
	items := []ui.Item{{ID: lidarrQueueID, Title: title, Channel: "Lidarr"}}
	for _, id := range ls.order {
		if h, ok := ls.hit(id); ok {
			items = append(items, ui.Item{ID: id, Title: ls.hitTitle(h), Channel: h.Artist.Name})
			continue
		}
		a, ok := ls.artist(id)
		if !ok {
			continue
		}
		mark := "+ "
		if a.InLidarr() {
			mark = "✓ "
		}
		name := a.Name
		if a.Disambiguation != "" {
			name += " (" + a.Disambiguation + ")"
		}
		items = append(items, ui.Item{ID: id, Title: mark + name, Channel: a.Name})
	}
	return items
}

// hitTitle: "✓ ♪ Discovery — Daft Punk · 2001". Marks as for albums; + =
// artist not in Lidarr yet, – = album outside the artist's metadata profile.
func (ls *lidarrState) hitTitle(h *lidarr.AlbumHit) string {
	mark := "  "
	switch al, ok := ls.hitAlbum(h); {
	case !h.Artist.InLidarr():
		mark = "+ "
	case !ok:
		mark = "– "
	case al.Have():
		mark = "✓ "
	case al.TrackFiles > 0:
		mark = "◐ "
	case al.Monitored:
		mark = "⬇ "
	}
	title := "♪ " + h.Album.Title + " — " + h.Artist.Name
	kind := h.Album.Type
	if len(h.Album.Secondary) > 0 {
		kind = strings.Join(h.Album.Secondary, "/")
	}
	if kind != "" && kind != "Album" {
		title += " (" + kind + ")"
	}
	if h.Album.Year != "" {
		title += " · " + h.Album.Year
	}
	return mark + title
}

func (ms *musicState) lidarrLibraryEmpty() string {
	ls := ms.lidarr
	if ls == nil || ls.client == nil {
		if ls != nil && ls.err != "" {
			return ls.err + " · t switches"
		}
		return "Lidarr not set up (lidarr_url, lidarr_api_key) · t switches"
	}
	return ""
}

func albumLabel(a lidarr.Album) string {
	title := a.Title
	kind := a.Type
	if len(a.Secondary) > 0 {
		kind = strings.Join(a.Secondary, "/")
	}
	if kind != "" && kind != "Album" {
		title += " (" + kind + ")"
	}
	switch {
	case a.Have():
		return "✓ " + title
	case a.TrackFiles > 0:
		return fmt.Sprintf("◐ %s %d/%d", title, a.TrackFiles, a.TotalTracks)
	case a.Monitored:
		return "⬇ " + title
	}
	return "  " + title
}

func queueLabel(q lidarr.QueueItem) (title, right string) {
	name := q.Album
	if q.Artist != "" {
		name += " — " + q.Artist
	}
	switch {
	case q.Status == "failed":
		return "⚠ " + name, "failed"
	case q.Problem != "" && (q.State == "importPending" || q.State == "importFailed"):
		return "⚠ " + name, "import"
	case q.Problem != "" || q.State == "importFailed" || q.Status == "warning":
		return "⚠ " + name, "fout"
	case q.State == "importPending" || q.State == "importing":
		return "⧗ " + name, "import"
	case q.Status == "queued" || q.Status == "delay" || q.Status == "paused":
		return "… " + name, q.Status
	}
	return "⬇ " + name, fmt.Sprintf("%d%%", int(q.Progress()*100+0.5))
}

// lidarrEpisodes fills the Nummers column for a Lidarr source.
func (ms *musicState) lidarrEpisodes(data *ui.Data) {
	ls := ms.lidarr
	data.Titles[1] = "Lidarr"
	switch {
	case ms.source == lidarrQueueID:
		data.Titles[1] = "Downloads"
		data.EpisodeEmpty = "Nothing in Lidarr's download queue"
		if ls.queueErr != "" {
			data.EpisodeEmpty = ls.queueErr
		}
		for i, q := range ls.queue {
			title, right := queueLabel(q)
			data.Episodes = append(data.Episodes, ui.Item{ID: lidarrDLPfx + strconv.Itoa(q.ID), Title: title, PlainTitle: title,
				Duration: right, Index: i})
		}
	case strings.HasPrefix(ms.source, lidarrHitPfx):
		h, ok := ls.hit(ms.source)
		if !ok {
			return
		}
		data.Titles[1] = "Album · " + h.Artist.Name
		if al, found := ls.hitAlbum(h); found {
			right := "–" // not "--:--": no track count yet
			if al.TotalTracks > 0 {
				right = fmt.Sprintf("%d nr", al.TotalTracks)
			}
			title := albumLabel(*al)
			data.Episodes = append(data.Episodes, ui.Item{ID: lidarrAlbumPfx + strconv.Itoa(al.ID), Title: title, PlainTitle: title,
				Date: al.Year, Duration: right})
			return
		}
		title := albumLabel(h.Album)
		data.Episodes = append(data.Episodes, ui.Item{ID: ms.source, Title: title, PlainTitle: title, Date: h.Album.Year, Duration: "–"})
	case strings.HasPrefix(ms.source, lidarrArtistPfx):
		a, ok := ls.artist(ms.source)
		if !ok {
			return
		}
		data.Titles[1] = "Albums · " + a.Name
		switch {
		case !a.InLidarr():
			data.EpisodeEmpty = "Not in Lidarr yet · Enter in column 1 adds the artist (downloads nothing yet)"
		case ls.waiting[a.ForeignID] > 0:
			data.EpisodeEmpty = "Lidarr is fetching the discography…"
		case ls.loading[a.ForeignID]:
			data.EpisodeEmpty = "Loading albums…"
		default:
			data.EpisodeEmpty = "No albums in Lidarr"
		}
		for i, al := range ls.albums[a.ForeignID] {
			title := albumLabel(al)
			right := "–" // not "--:--": no track count yet
			if al.TotalTracks > 0 {
				right = fmt.Sprintf("%d nr", al.TotalTracks)
			}
			data.Episodes = append(data.Episodes, ui.Item{ID: lidarrAlbumPfx + strconv.Itoa(al.ID), Title: title, PlainTitle: title,
				Date: al.Year, Duration: right, Index: i})
		}
	}
}

func (m libraryShell) lidarrDetails() (string, bool) {
	ms := m.music
	ls := ms.lidarr
	if !isLidarrSource(ms.source) || ls == nil {
		return "", false
	}
	var b strings.Builder
	// Details keeps showing the album while it is focused itself (4), so
	// its tracklist can be scrolled.
	if f := m.Model.Focus(); f == ui.Episodes || f == ui.Details || strings.HasPrefix(ms.source, lidarrHitPfx) {
		item, ok := m.Model.Selected(ui.Episodes)
		if ok && strings.HasPrefix(item.ID, lidarrHitPfx) {
			if h, found := ls.hit(item.ID); found {
				return hitDetails(h), true
			}
		}
		if ok && strings.HasPrefix(item.ID, lidarrAlbumPfx) {
			if al, found := ls.album(ms.source, item.ID); found {
				fmt.Fprintf(&b, "%s\n\n", al.Title)
				fmt.Fprintf(&b, "%-9s %s\n", "Type", strings.TrimSpace(al.Type+" "+strings.Join(al.Secondary, "/")))
				fmt.Fprintf(&b, "%-9s %s\n", "Year", al.Year)
				fmt.Fprintf(&b, "%-9s %d of %d\n", "Tracks", al.TrackFiles, al.TotalTracks)
				switch {
				case al.Have():
					b.WriteString("\n✓ In your library\n")
				case al.Monitored:
					b.WriteString("\n⬇ Requested in Lidarr · Enter searches again\n")
				default:
					b.WriteString("\nEnter: let Lidarr search for and download this album\n")
				}
				b.WriteString(ls.trackList(al.ID))
				return b.String(), true
			}
		}
		if ok && strings.HasPrefix(item.ID, lidarrDLPfx) {
			for _, q := range ls.queue {
				if lidarrDLPfx+strconv.Itoa(q.ID) == item.ID {
					fmt.Fprintf(&b, "%s\n\n", q.Album)
					fmt.Fprintf(&b, "%-9s %s\n", "Artist", q.Artist)
					fmt.Fprintf(&b, "%-9s %s · %s\n", "Status", q.Status, q.State)
					if q.Size > 0 {
						fmt.Fprintf(&b, "%-9s %d%% of %.0f MB\n", "Progress", int(q.Progress()*100+0.5), q.Size/1e6)
					}
					if q.TimeLeft != "" && q.SizeLeft > 0 {
						fmt.Fprintf(&b, "%-9s %s\n", "Left", q.TimeLeft)
					}
					if q.Problem != "" {
						fmt.Fprintf(&b, "\n⚠ %s\n", q.Problem)
						if hint := problemHint(q.Problem); hint != "" {
							fmt.Fprintf(&b, "\n%s\n", hint)
						}
					}
					return b.String(), true
				}
			}
		}
	}
	b.WriteString("Lidarr\n\n")
	b.WriteString("/ → Tab Lidarr   Search an artist or album\n")
	b.WriteString("Enter (+)        Add the artist (downloads nothing)\n")
	b.WriteString("Enter (album)    Search for and download the album\n")
	b.WriteString("⬇ Downloads      Progress; done = MPD updated\n\n")
	b.WriteString("✓ in library · ◐ partly · ⬇ requested\n")
	return b.String(), true
}

// hitDetails describes an album search result that is not in Lidarr.
func hitDetails(h *lidarr.AlbumHit) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", h.Album.Title)
	fmt.Fprintf(&b, "%-9s %s\n", "Artist", h.Artist.Name)
	fmt.Fprintf(&b, "%-9s %s\n", "Type", strings.TrimSpace(h.Album.Type+" "+strings.Join(h.Album.Secondary, "/")))
	fmt.Fprintf(&b, "%-9s %s\n", "Year", h.Album.Year)
	if h.Artist.InLidarr() {
		b.WriteString("\n– Not in Lidarr: outside this artist's metadata profile (e.g. live albums, compilations or bootlegs are switched off). Change the profile in Lidarr or pick another release.\n")
	} else {
		b.WriteString("\n+ Artist not in Lidarr yet\n")
		b.WriteString("Enter: add the artist (downloads nothing else) and request this album\n")
	}
	b.WriteString("\nTracks show up once the album is in Lidarr.\n")
	return b.String()
}

// trackList renders the album's songs for Details (✓ = file on disk).
func (ls *lidarrState) trackList(albumID int) string {
	tracks, ok := ls.tracks[albumID]
	switch {
	case ls.tracksErr[albumID] != "":
		return "\nLoading tracks failed: " + ls.tracksErr[albumID] + "\n"
	case !ok:
		return "\nLoading tracks…\n"
	case len(tracks) == 0:
		return "\nLidarr has no tracks for this album (yet)\n"
	}
	var b strings.Builder
	b.WriteString("\nNummers\n")
	multi := tracks[len(tracks)-1].Medium > 1
	medium := 0
	var total time.Duration
	for _, t := range tracks {
		if multi && t.Medium != medium {
			medium = t.Medium
			fmt.Fprintf(&b, "  CD %d\n", medium)
		}
		mark := " "
		if t.HasFile {
			mark = "✓"
		}
		fmt.Fprintf(&b, "%s %3s  %6s  %s\n", mark, t.Number, trackDuration(t.Duration), t.Title)
		total += t.Duration
	}
	fmt.Fprintf(&b, "\n%d tracks · %s\n", len(tracks), trackDuration(total))
	return b.String()
}

func trackDuration(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	if s >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
	}
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

// lidarrTracksSync loads the tracklist of the album under the cursor once.
func (m libraryShell) lidarrTracksSync() tea.Cmd {
	ms := m.music
	ls := ms.lidarr
	hitSource := strings.HasPrefix(ms.source, lidarrHitPfx)
	if !ls.enabled() || (!strings.HasPrefix(ms.source, lidarrArtistPfx) && !hitSource) ||
		(m.Model.Focus() != ui.Episodes && !hitSource) {
		return nil
	}
	item, ok := m.Model.Selected(ui.Episodes)
	if !ok || !strings.HasPrefix(item.ID, lidarrAlbumPfx) {
		return nil
	}
	al, found := ls.album(ms.source, item.ID)
	if !found || ls.tracksLoaded[al.ID] {
		return nil
	}
	ls.tracksLoaded[al.ID] = true
	client, id := ls.client, al.ID
	return work(m.s.ctx, "Tracks from Lidarr…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, lidarrTimeout)
		defer cancel()
		tracks, err := client.Tracks(ctx, id)
		return lidarrTracksDone{album: id, tracks: tracks, err: err}
	})
}

// --- loading ------------------------------------------------------------------

func (m libraryShell) lidarrLoad(source string) tea.Cmd {
	ls := m.music.lidarr
	if !ls.enabled() {
		return nil
	}
	if source == lidarrQueueID {
		return m.lidarrStartPolling()
	}
	a, ok := ls.artist(source)
	if !ok || !a.InLidarr() || ls.loading[a.ForeignID] {
		return nil
	}
	if _, loaded := ls.albums[a.ForeignID]; loaded && strings.HasPrefix(source, lidarrHitPfx) {
		return nil // already known; artist rows reload on purpose
	}
	ls.loading[a.ForeignID] = true
	client, id, foreign := ls.client, a.ID, a.ForeignID
	return work(m.s.ctx, "Albums from Lidarr…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, lidarrTimeout)
		defer cancel()
		albums, err := client.Albums(ctx, id)
		return lidarrAlbumsDone{foreign: foreign, albums: albums, err: err}
	})
}

func (m libraryShell) lidarrSearch(query string) tea.Cmd {
	ls := m.music.lidarr
	client := ls.client
	return work(m.s.ctx, "Searching Lidarr…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, lidarrTimeout)
		defer cancel()
		hits, err := client.Search(ctx, query)
		return lidarrSearchDone{query: query, hits: hits, err: err}
	})
}

// lidarrFetchQueue runs without the activity spinner (it polls).
func (ls *lidarrState) fetchQueue(parent context.Context) tea.Cmd {
	client := ls.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, lidarrTimeout)
		defer cancel()
		q, err := client.Queue(ctx)
		return lidarrQueueDone{queue: q, err: err}
	}
}

func (m libraryShell) lidarrStartPolling() tea.Cmd {
	ls := m.music.lidarr
	if !ls.enabled() || ls.polling {
		return nil
	}
	ls.polling = true
	return ls.fetchQueue(m.s.ctx)
}

// openLidarrSearch runs a Lidarr search from the / dialog.
func (m libraryShell) openLidarrResults(query string) (libraryShell, tea.Cmd) {
	ms := m.music
	if !ms.lidarr.enabled() {
		m.notice = ms.lidarrLibraryEmpty()
		m.renderStatus()
		return m, nil
	}
	ms.libView = viewLidarr
	ms.lidarr.query = query
	m.Model = m.Model.ClearFilter(ui.Podcasts).WithFocus(ui.Podcasts)
	m.notice = "Searching Lidarr: " + query
	m.renderMusic()
	return m, tea.Batch(m.lidarrSearch(query), m.lidarrStartPolling())
}

// --- keys ---------------------------------------------------------------------

// lidarrKey handles Enter (and blocks MPD actions) for Lidarr rows.
func (m libraryShell) lidarrKey(key string) (libraryShell, tea.Cmd, bool) {
	ms := m.music
	ls := ms.lidarr
	if ls == nil || !isLidarrSource(ms.source) {
		return m, nil, false
	}
	focus := m.Model.Focus()
	switch key {
	case "enter":
		switch focus {
		case ui.Podcasts:
			if a, ok := ls.artist(ms.source); ok && !a.InLidarr() {
				picked := *a
				ls.confirm = &lidarrConfirm{title: "Add artist to Lidarr",
					body:   fmt.Sprintf("Add %s to Lidarr?\nNothing is downloaded yet: you pick the albums afterwards.", a.Name),
					artist: &picked}
				m.renderLidarrConfirm()
				return m, nil, true
			}
			return m, nil, false // focus the albums like any list
		case ui.Episodes:
			item, ok := m.Model.Selected(ui.Episodes)
			if !ok {
				return m, nil, true
			}
			if strings.HasPrefix(item.ID, lidarrDLPfx) {
				return m, nil, true
			}
			if h, isHit := ls.hit(item.ID); isHit {
				return m.lidarrConfirmHit(h)
			}
			al, found := ls.album(ms.source, item.ID)
			if !found {
				return m, nil, true
			}
			if al.Have() {
				m.notice = "Already in your library"
				m.renderStatus()
				return m, nil, true
			}
			a, _ := ls.artist(ms.source)
			picked := *al
			verb := "search for and download"
			if al.Monitored {
				verb = "search again for"
			}
			detail := al.Year
			if al.TotalTracks > 0 {
				detail += fmt.Sprintf(", %d tracks", al.TotalTracks)
			}
			ls.confirm = &lidarrConfirm{title: "Request album in Lidarr",
				body:  fmt.Sprintf("«%s» — %s (%s)\nLidarr will %s this album.", al.Title, a.Name, strings.TrimPrefix(detail, ", "), verb),
				album: &picked, artistName: a.Name}
			m.renderLidarrConfirm()
			return m, nil, true
		}
	case "a", "A", "o", "p", "b", "B", "x":
		if focus == ui.Episodes || focus == ui.Podcasts {
			m.notice = "Lidarr list · Enter requests the album"
			m.renderStatus()
			return m, nil, true
		}
	}
	return m, nil, false
}

func (m *libraryShell) renderLidarrConfirm() {
	c := m.music.lidarr.confirm
	if c == nil {
		m.Model = m.Model.WithDialog(nil)
		return
	}
	m.Model = m.Model.WithDialog(&ui.Dialog{Title: c.title, Body: c.body, Hint: "Enter ja · Esc nee"})
}

func (m libraryShell) lidarrConfirmKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd) {
	ls := m.music.lidarr
	c := ls.confirm
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "enter", "y", "j":
		ls.confirm = nil
		m.renderLidarrConfirm()
		client, defaults, musicDir := ls.client, ls.defaults, m.music.musicDir()
		if c.hit != nil {
			return m, m.spotifyRequest(*c.hit)
		}
		if c.artist != nil {
			a := *c.artist
			m.notice = "Adding to Lidarr: " + a.Name
			return m, work(m.s.ctx, "Adding artist…", func(ctx context.Context) tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, lidarrTimeout)
				defer cancel()
				if defaults == nil {
					d, err := client.Defaults(ctx)
					if err != nil {
						return lidarrAdded{foreign: a.ForeignID, err: err}
					}
					defaults = &d
				}
				id, renamed, err := addArtistAdopting(ctx, client, *defaults, a, musicDir)
				return lidarrAdded{foreign: a.ForeignID, id: id, defaults: defaults, renamed: renamed, err: err}
			})
		}
		al, label := *c.album, fmt.Sprintf("«%s» — %s", c.album.Title, c.artistName)
		m.notice = "Requesting: " + label
		return m, work(m.s.ctx, "Requesting album…", func(ctx context.Context) tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, lidarrTimeout)
			defer cancel()
			return lidarrRequested{label: label, err: client.RequestAlbum(ctx, al.ID)}
		})
	case "esc", "n", "q":
		ls.confirm = nil
		m.notice = "Cancelled"
		m.renderLidarrConfirm()
	}
	return m, nil
}

// lidarrConfirmHit: an album whose artist is not in Lidarr is added first.
func (m libraryShell) lidarrConfirmHit(h *lidarr.AlbumHit) (libraryShell, tea.Cmd, bool) {
	ls := m.music.lidarr
	if h.Artist.InLidarr() {
		m.notice = "This album is outside the artist's metadata profile in Lidarr"
		m.renderStatus()
		return m, nil, true
	}
	picked := *h
	ls.confirm = &lidarrConfirm{title: "Add artist and request album",
		body: fmt.Sprintf("Add %s to Lidarr (nothing else is downloaded)\nand search for and download «%s» (%s)?", h.Artist.Name, h.Album.Title, h.Album.Year),
		hit:  &picked}
	m.renderLidarrConfirm()
	return m, nil, true
}

// --- events -------------------------------------------------------------------

func (m libraryShell) updateLidarr(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	ms := m.music
	ls := ms.lidarr
	if ls == nil {
		return m, nil, false
	}
	switch msg := msg.(type) {
	case lidarrSearchDone:
		if msg.query != ls.query {
			return m, nil, true
		}
		if msg.err != nil {
			m.notice = "Lidarr search failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		ls.artists, ls.hits, ls.order = nil, nil, nil
		for _, h := range msg.hits {
			if h.Artist != nil {
				ls.artists = append(ls.artists, *h.Artist)
				ls.order = append(ls.order, lidarrArtistPfx+h.Artist.ForeignID)
			} else {
				ls.hits = append(ls.hits, *h.Album)
				ls.order = append(ls.order, lidarrHitPfx+h.Album.Album.ForeignID)
			}
		}
		m.notice = fmt.Sprintf("Lidarr: %d artist(s), %d album(s) for %q · ✓ in Lidarr, + not added yet", len(ls.artists), len(ls.hits), msg.query)
		// A refresh (after a request) keeps the cursor where it was.
		keep := false
		for _, id := range ls.order {
			keep = keep || id == ms.source
		}
		m.renderMusic()
		if ms.libView == viewLidarr && len(ls.order) > 0 && !keep {
			m.Model = m.Model.Select(ui.Podcasts, ls.order[0])
			m.renderMusic()
			return m, m.musicSyncSelection(), true
		}
		return m, nil, true
	case lidarrAlbumsDone:
		delete(ls.loading, msg.foreign)
		if msg.err != nil {
			m.notice = "Loading albums from Lidarr failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		ls.albums[msg.foreign] = msg.albums
		if n, waiting := ls.waiting[msg.foreign]; waiting {
			if len(msg.albums) == 0 && n < 15 {
				ls.waiting[msg.foreign] = n + 1
				foreign := msg.foreign
				m.renderMusic()
				return m, tea.Tick(lidarrRecheckWait, func(time.Time) tea.Msg { return lidarrRecheck{foreign: foreign} }), true
			}
			delete(ls.waiting, msg.foreign)
		}
		m.renderMusic()
		return m, m.lidarrTracksSync(), true
	case lidarrTracksDone:
		if msg.err != nil {
			ls.tracksErr[msg.album] = msg.err.Error()
			delete(ls.tracksLoaded, msg.album) // try again next time
		} else {
			delete(ls.tracksErr, msg.album)
			ls.tracks[msg.album] = msg.tracks
		}
		m.renderMusicDetails()
		return m, nil, true
	case lidarrRecheck:
		if _, ok := ls.waiting[msg.foreign]; !ok {
			return m, nil, true
		}
		return m, m.lidarrLoad(lidarrArtistPfx + msg.foreign), true
	case lidarrAdded:
		if msg.err != nil {
			m.notice = "Adding failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		ls.defaults = msg.defaults
		ls.setArtistID(msg.foreign, msg.id)
		for i := range ls.artists {
			if ls.artists[i].ForeignID == msg.foreign {
				ls.artists[i].ID = msg.id
				m.notice = ls.artists[i].Name + " is in Lidarr · pick an album in column 2"
				if msg.renamed != "" {
					m.notice = fmt.Sprintf("%s is in Lidarr · using your existing folder «%s», what you already had counts", ls.artists[i].Name, msg.renamed)
				}
			}
		}
		ls.waiting[msg.foreign] = 1
		m.renderMusic()
		foreign := msg.foreign
		cmds := []tea.Cmd{tea.Tick(lidarrRecheckWait, func(time.Time) tea.Msg { return lidarrRecheck{foreign: foreign} })}
		if msg.renamed != "" {
			cmds = append(cmds, m.musicDo("Updating library…", false, func(ctx context.Context, c *mpd.Client) error { return c.Update(ctx) }))
		}
		return m, tea.Batch(cmds...), true
	case lidarrRequested:
		if msg.err != nil {
			m.notice = "Request failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		m.notice = "Lidarr is searching for " + msg.label + " · follow it in ⬇ Downloads"
		var cmds []tea.Cmd
		if msg.renamed != "" {
			m.notice = fmt.Sprintf("Existing folder «%s» · ", msg.renamed) + m.notice
			cmds = append(cmds, m.musicDo("Updating library…", false, func(ctx context.Context, c *mpd.Client) error { return c.Update(ctx) }))
		}
		if msg.have {
			m.notice = fmt.Sprintf("You already had %s (folder «%s») · nothing downloaded", msg.label, msg.renamed)
			m.renderMusic()
			return m, tea.Batch(cmds...), true
		}
		if strings.HasPrefix(ms.source, lidarrHitPfx) && ls.query != "" {
			cmds = append(cmds, m.lidarrSearch(ls.query)) // artist/album IDs are new
		} else if a, ok := ls.artist(ms.source); ok {
			cmds = append(cmds, m.lidarrLoad(lidarrArtistPfx+a.ForeignID))
		}
		// The search takes a moment; look at the queue soon.
		cmds = append(cmds, tea.Tick(lidarrFirstLook, func(time.Time) tea.Msg { return lidarrPoll{} }))
		m.renderMusic()
		return m, tea.Batch(cmds...), true
	case lidarrPoll:
		if ls.polling || !ls.enabled() {
			return m, nil, true
		}
		ls.polling = true
		return m, ls.fetchQueue(m.s.ctx), true
	case lidarrQueueDone:
		ls.polling = false
		if msg.err != nil {
			ls.queueErr = "Loading downloads failed: " + msg.err.Error()
		} else {
			ls.queueErr = ""
			finished := finishedDownloads(ls.queue, msg.queue)
			ls.queue = msg.queue
			if len(finished) > 0 {
				m.notice = "⬇ Done: " + strings.Join(finished, ", ") + " · updating the MPD library"
				var cmds []tea.Cmd
				cmds = append(cmds, m.musicDo("Updating library…", false, func(ctx context.Context, c *mpd.Client) error { return c.Update(ctx) }),
					m.musicLoadCatalog(), m.musicLoadArtists())
				if a, ok := ls.artist(ms.source); ok {
					cmds = append(cmds, m.lidarrLoad(lidarrArtistPfx+a.ForeignID))
				}
				cmds = append(cmds, m.lidarrNextPoll(), m.flacCheck())
				m.renderMusic()
				return m, tea.Batch(cmds...), true
			}
		}
		m.renderMusic()
		return m, m.lidarrNextPoll(), true
	}
	return m, nil, false
}

// lidarrNextPoll keeps watching while the Lidarr view is open or something
// is still downloading.
func (m libraryShell) lidarrNextPoll() tea.Cmd {
	ms := m.music
	if !(ms.active && ms.libView == viewLidarr) && len(ms.lidarr.queue) == 0 {
		return nil
	}
	return tea.Tick(lidarrPollEvery, func(time.Time) tea.Msg { return lidarrPoll{} })
}

// finishedDownloads: items that left the queue without a problem (imported).
func finishedDownloads(before, after []lidarr.QueueItem) []string {
	still := map[int]bool{}
	for _, q := range after {
		still[q.ID] = true
	}
	var done []string
	for _, q := range before {
		if !still[q.ID] && q.Problem == "" && q.Status != "failed" {
			name := q.Album
			if q.Artist != "" {
				name += " — " + q.Artist
			}
			done = append(done, name)
		}
	}
	return done
}

// problemHint explains Lidarr's most common import messages.
func problemHint(problem string) string {
	switch {
	case strings.Contains(problem, "No files found are eligible for import"):
		return "Downloaded, but Lidarr cannot import the files: usually Lidarr cannot see the download client's folder (path missing in the container), or the format does not match the quality profile."
	case strings.Contains(problem, "Not an upgrade") || strings.Contains(problem, "quality"):
		return "The format does not match the quality profile in Lidarr."
	}
	return ""
}
