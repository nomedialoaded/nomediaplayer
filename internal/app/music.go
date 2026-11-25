package app

// nml: Music view. MPD becomes a second source next to the podcast engine.
// The existing panels are reused: Library = artists, Episodes = tracks of the
// selected artist (album in the Show column), Queue = MPD queue. The player
// bar shows whatever is audible; transport keys follow that source.

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/playback"
	"github.com/nomedialoaded/nomediaplayer/internal/subsonic"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const musicTimeout = 5 * time.Second

type musicState struct {
	client  *mpd.Client
	active  bool
	started bool
	artists []string
	// artistsLoaded is set once MPD answered the artist list, so an empty
	// library can say so instead of "Loading library…".
	artistsLoaded bool
	// source is the selected Library-column ID: "artist:<name>" (MPD) or a
	// Navidrome list (nd:starred, nd:album:<id>, nd:pl:<id>).
	source     string
	tracks     []mpd.Song
	trackND    []subsonic.Song // parallel to tracks for Navidrome sources
	nd         *ndState
	picker     *musicPicker
	searching  *musicSearch
	covers     *musicCovers
	recent     *musicRecent
	lyrics     *lyricsState
	lidarr     *lidarrState
	speakers   *speakerState
	spotify    *spotifyState
	flac       *flacState
	showLyrics bool
	// clearRecentArmed: D pressed once in Recently played; a second D clears it.
	clearRecentArmed bool
	libView          int
	// shownSource: the list the Nummers column (and its filter) belongs to.
	shownSource string
	albums      []mpd.AlbumRef
	genres      []string
	lastSearch  *searchResult
	// searchByAlbum: search results in column 2 grouped per album (w).
	searchByAlbum bool
	queue         []mpd.Song
	status        mpd.Status
	current       mpd.Song
	hasCurrent    bool
	clockAt       time.Time
	err           string
	// routeMPD is read by the transport callback (UI thread) to decide whether
	// Space/seek/volume go to MPD or to the podcast engine.
	routeMPD bool
	// lastMPD: MPD was the most recently audible source (keeps a paused
	// song in the player bar so Space resumes it).
	lastMPD bool
}

type musicArtists struct {
	artists []string
	err     error
}
type musicTracks struct {
	source string
	tracks []mpd.Song
	nd     []subsonic.Song
	err    error
}
type musicStatus struct {
	status  mpd.Status
	current mpd.Song
	has     bool
	queue   []mpd.Song
	err     error
}
type musicIdle struct{ err error }

// musicPoll/musicElapsed keep the player bar moving: MPD only reports state
// changes via idle, and the bar extrapolates at most ui.progressHorizon (2 s)
// past the last sample, so a playing song is re-sampled every second.
type musicPoll struct{}
type musicElapsed struct {
	status mpd.Status
	err    error
}

const musicPollEvery = time.Second

type musicDone struct {
	err    error
	notice string
}

func newMusic(host string, port int) *musicState {
	if host == "" {
		host = "127.0.0.1"
	}
	if port == 0 {
		port = 6600
	}
	client := mpd.New(net.JoinHostPort(host, strconv.Itoa(port)))
	return &musicState{client: client, covers: newMusicCovers(client)}
}

func (ms *musicState) playing() bool { return ms != nil && ms.status.State == "play" }

// --- commands -------------------------------------------------------------

func (m libraryShell) musicLoadArtists() tea.Cmd {
	client := m.music.client
	return work(m.s.ctx, "Loading artists…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		artists, err := client.List(ctx, "albumartist")
		return musicArtists{artists, err}
	})
}

func (m libraryShell) musicLoadTracks(source string) tea.Cmd {
	if isNDSource(source) {
		return m.ndLoadSource(source)
	}
	if isLidarrSource(source) {
		return m.lidarrLoad(source)
	}
	if isSpeakerSource(source) {
		return m.speakersStartPolling()
	}
	if isSearchSource(source) {
		return m.musicLoadSearch(source)
	}
	if strings.HasPrefix(source, albumPrefix) {
		return m.musicLoadAlbum(source)
	}
	if strings.HasPrefix(source, genrePrefix) {
		return m.musicLoadGenre(source)
	}
	if !strings.HasPrefix(source, "artist:") {
		return nil
	}
	artist := strings.TrimPrefix(source, "artist:")
	client := m.music.client
	return work(m.s.ctx, "Loading tracks…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		tracks, err := client.Find(ctx, "albumartist", artist)
		return musicTracks{source: source, tracks: tracks, err: err}
	})
}

func musicFetchStatus(parent context.Context, client *mpd.Client) tea.Msg {
	ctx, cancel := context.WithTimeout(parent, musicTimeout)
	defer cancel()
	st, err := client.Status(ctx)
	if err != nil {
		return musicStatus{err: err}
	}
	cur, has, err := client.CurrentSong(ctx)
	if err != nil {
		return musicStatus{err: err}
	}
	queue, err := client.Queue(ctx)
	return musicStatus{status: st, current: cur, has: has, queue: queue, err: err}
}

func (m libraryShell) musicRefresh() tea.Cmd {
	client, ctx := m.music.client, m.s.ctx
	return func() tea.Msg { return musicFetchStatus(ctx, client) }
}

func musicPollCmd() tea.Cmd {
	return tea.Tick(musicPollEvery, func(time.Time) tea.Msg { return musicPoll{} })
}

func (m libraryShell) musicPollStatus() tea.Cmd {
	client, ctx := m.music.client, m.s.ctx
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		st, err := client.Status(ctx)
		return musicElapsed{status: st, err: err}
	}
}

func (m libraryShell) musicWaitIdle() tea.Cmd {
	addr, ctx := m.music.client.Addr(), m.s.ctx
	return func() tea.Msg {
		_, err := mpd.Idle(ctx, addr, "player", "mixer", "playlist", "database", "options")
		if err != nil && ctx.Err() == nil {
			// MPD down or restarting: back off before the next attempt.
			select {
			case <-time.After(5 * time.Second):
			case <-ctx.Done():
			}
		}
		return musicIdle{err}
	}
}

// musicDo runs one MPD action. When it starts audio, the podcast pauses.
func (m libraryShell) musicDo(label string, startsAudio bool, run func(context.Context, *mpd.Client) error) tea.Cmd {
	client, engine, ctx := m.music.client, m.s.playback, m.s.ctx
	podcastPlaying := m.state.Status == playback.Playing
	return work(ctx, label, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		if startsAudio && podcastPlaying {
			_ = engine.Pause(ctx)
		}
		return musicDone{err: run(ctx, client)}
	})
}

// musicTransport handles player actions while MPD is the routed source.
// It returns nil for actions that are not transport (themes, settings…).
func (ms *musicState) transport(s *session, act ui.Action) tea.Cmd {
	if ms == nil || !ms.routeMPD {
		return nil
	}
	client, engine := ms.client, s.playback
	status, volume := ms.status.State, ms.status.Volume
	var run func(context.Context) error
	switch act.Kind {
	case ui.ActionToggle:
		run = func(ctx context.Context) error {
			if status == "play" {
				return client.Pause(ctx, true)
			}
			if st, err := engine.Snapshot(ctx); err == nil && st.Status == playback.Playing {
				_ = engine.Pause(ctx)
			}
			if status == "pause" {
				return client.Pause(ctx, false)
			}
			return client.Play(ctx)
		}
	case ui.ActionStop:
		run = func(ctx context.Context) error { return client.Stop(ctx) }
	case ui.ActionNext:
		run = func(ctx context.Context) error { return client.Next(ctx) }
	case ui.ActionPrevious:
		run = func(ctx context.Context) error { return client.Previous(ctx) }
	case ui.ActionSeekForward:
		run = func(ctx context.Context) error { return client.SeekBy(ctx, 15) }
	case ui.ActionSeekBackward:
		run = func(ctx context.Context) error { return client.SeekBy(ctx, -15) }
	case "volume-up", "volume-down":
		delta := 5
		if act.Kind == "volume-down" {
			delta = -5
		}
		run = func(ctx context.Context) error { return client.SetVolume(ctx, volume+delta) }
	default:
		return nil
	}
	return work(s.ctx, "Updating music…", func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, musicTimeout)
		defer cancel()
		return musicDone{err: run(ctx)}
	})
}

// --- events ---------------------------------------------------------------

func (m libraryShell) updateMusic(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	if m.music == nil {
		return m, nil, false
	}
	if updated, cmd, handled := m.updateND(msg); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.updateLyrics(msg); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.updateLidarr(msg); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.updateFlac(msg); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.updateSpeakers(msg); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.updateSpotify(msg); handled {
		return updated, cmd, true
	}
	switch msg := msg.(type) {
	case musicArtists:
		if msg.err != nil {
			m.music.err = "MPD not reachable (" + m.music.client.Addr() + "): " + msg.err.Error()
			m.renderMusic()
			return m, nil, true
		}
		m.music.err = ""
		m.music.artists, m.music.artistsLoaded = msg.artists, true
		m.renderMusic()
		return m, m.musicSyncSelection(), true
	case musicCatalog:
		if msg.err == nil {
			m.music.albums, m.music.genres = msg.albums, msg.genres
			m.renderMusic()
		}
		return m, nil, true
	case musicTracks:
		if msg.source != m.music.source {
			return m, nil, true
		}
		if msg.err != nil {
			m.notice = "Loading tracks failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		m.music.tracks, m.music.trackND = msg.tracks, msg.nd
		if msg.source != m.music.shownSource {
			// A filter typed for the previous list would hide the new one.
			m.music.shownSource = msg.source
			m.Model = m.Model.ClearFilter(ui.Episodes)
		}
		if r := m.music.lastSearch; r != nil && isSearchSource(msg.source) {
			r.count = len(msg.tracks)
		}
		m.renderMusic()
		return m, nil, true
	case musicStatus:
		if msg.err != nil {
			m.music.err = "MPD: " + msg.err.Error()
			return m, nil, true
		}
		wasPlaying := m.music.playing()
		m.music.err = ""
		m.music.status, m.music.current, m.music.hasCurrent, m.music.queue = msg.status, msg.current, msg.has, msg.queue
		m.music.clockAt = time.Now()
		if msg.has && msg.status.State == "play" {
			m.music.recent.record(msg.current, time.Now())
		}
		var cmd tea.Cmd
		// Music started elsewhere (rmpc, media keys) while a podcast plays.
		if m.music.playing() && !wasPlaying && m.state.Status == playback.Playing {
			engine := m.s.playback
			cmd = work(m.s.ctx, "Pausing podcast…", func(ctx context.Context) tea.Msg { return playerEdited{err: engine.Pause(ctx)} })
		}
		lyricsCmd := m.musicSyncLyrics()
		if m.music.active {
			m.renderMusic()
		}
		return m, tea.Batch(cmd, lyricsCmd), true
	case musicStartup:
		if m.music.active {
			return m, nil, true
		}
		updated, cmd := m.toggleMusic()
		return updated, cmd, true
	case musicIdle:
		return m, tea.Batch(m.musicRefresh(), m.musicWaitIdle()), true
	case musicPoll:
		if m.music.playing() {
			return m, tea.Batch(m.musicPollStatus(), musicPollCmd()), true
		}
		return m, musicPollCmd(), true
	case musicElapsed:
		// Only refresh the position of the song we already show; any other
		// change (new song, pause, stop) arrives through idle.
		st := msg.status
		if msg.err != nil || !m.music.playing() || st.State != "play" || st.SongPos != m.music.status.SongPos {
			return m, nil, true
		}
		m.music.status.Elapsed, m.music.status.Duration = st.Elapsed, st.Duration
		m.music.clockAt = time.Now()
		if m.music.active {
			m.renderMusic()
		}
		return m, nil, true
	case musicDone:
		if msg.err != nil {
			m.notice = "MPD: " + msg.err.Error()
		} else if msg.notice != "" {
			m.notice = msg.notice
		}
		m.renderStatus()
		return m, m.musicRefresh(), true
	}
	return m, nil, false
}

// podcastStarted pauses MPD when the podcast engine starts audio.
func (m libraryShell) musicAfterPodcast(prev playback.Status) tea.Cmd {
	if m.music == nil || prev == playback.Playing || m.state.Status != playback.Playing || !m.music.playing() {
		return nil
	}
	return m.musicDo("Pausing music…", false, func(ctx context.Context, c *mpd.Client) error { return c.Pause(ctx, true) })
}

// --- mode switching ---------------------------------------------------------

func (m libraryShell) toggleMusic() (libraryShell, tea.Cmd) {
	if m.music == nil {
		m.music = m.newMusicState()
	}
	if m.music.active && !m.s.config.Podcasts {
		m.notice = "Podcasts are off · switch them on with ? → Setup"
		m.renderStatus()
		return m, nil
	}
	m.music.active = !m.music.active
	if !m.music.active {
		m.notice = "Podcasts · M: Music"
		m.shell = m.shell.withPlayback(m.state)
		m.renderLibrary()
		return m, nil
	}
	m.notice = "Music (MPD) · M: Podcasts"
	m.Model = m.Model.WithFocus(ui.Podcasts)
	var cmds []tea.Cmd
	if !m.music.started {
		m.music.started = true
		cmds = append(cmds, m.musicLoadArtists(), m.musicLoadCatalog(), m.musicRefresh(), m.ndLoadLists(), m.flacCheck())
	}
	m.renderMusic()
	return m, tea.Batch(cmds...)
}

type musicStartup struct{}

// musicStartView opens the Music view at launch unless start_view = "podcasts".
func (m libraryShell) musicStartView() tea.Cmd {
	if m.music == nil || m.s.config.StartView == "podcasts" {
		return nil
	}
	return func() tea.Msg { return musicStartup{} }
}

// attachMusic watches MPD from launch so the player bar and auto-pause work
// even before the Music view is opened. Init arms the status/idle commands.
func (m *libraryShell) attachMusic() {
	music, s := m.newMusicState(), m.s
	m.music = music
	s.musicRoute = func(act ui.Action) tea.Cmd { return music.transport(s, act) }
}

func (m libraryShell) newMusicState() *musicState {
	cfg := m.s.config
	ms := newMusic(cfg.MPDHost, cfg.MPDPort)
	ms.nd = newND(cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePassword)
	ms.recent = loadRecent(m.s.paths.State)
	ms.lyrics = newLyricsState(cfg.MusicDir, m.s.paths.State, cfg.LyricsOnline)
	ms.lidarr = newLidarrState(cfg.LidarrURL, cfg.LidarrAPIKey)
	ms.flac = newFlacState(cfg.FlacToMP3, m.s.paths.State)
	ms.speakers = newSpeakerState(cfg.HAURL, cfg.HAToken, cfg.MAURL, cfg.MAToken)
	return ms
}

// --- rendering --------------------------------------------------------------

func songTitle(s mpd.Song) string {
	if strings.TrimSpace(s.Title) != "" {
		return s.Title
	}
	name := s.File
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	return name
}

func songArtist(s mpd.Song) string {
	if s.Artist != "" {
		return s.Artist
	}
	return s.AlbumArtist
}

func songYear(s mpd.Song) string {
	if len(s.Date) >= 4 {
		return s.Date[:4]
	}
	return s.Date
}

func (m *libraryShell) renderMusic() {
	ms := m.music
	data := ui.Data{
		Titles:         [5]string{ms.libraryTitle(), "Tracks", "Queue", "Details", "Recently played"},
		Music:          true,
		EpisodeLabel:   ms.sourceLabel(),
		PodcastEmpty:   "Loading library… (MPD " + ms.client.Addr() + ")",
		EpisodeEmpty:   "Pick an artist or playlist · Enter",
		QueueEmpty:     "Queue empty · a adds a track",
		FavoritesEmpty: "Nothing played yet · what MPD plays shows up here",
	}
	if e := ms.libraryEmpty(); e != "" {
		data.PodcastEmpty = e
	}
	if ms.err != "" {
		data.PodcastEmpty = ms.err
	}
	data.Podcasts = ms.libraryItems()
	current := ""
	if ms.hasCurrent {
		current = ms.current.File
	}
	lyricID := ""
	if ms.showLyrics && ms.lyrics != nil {
		lyricID = ms.lyricsPanel(&data)
	} else {
		data.Favorites = ms.recent.items(time.Now(), current)
		data.Titles[4] = "Recently played ‹L›"
	}
	if isLidarrSource(ms.source) {
		ms.lidarrEpisodes(&data)
	}
	if isSpeakerSource(ms.source) {
		ms.speakerEpisodes(&data)
	}
	if isSearchSource(ms.source) {
		data.Titles[1] = "Tracks ‹w›"
		if ms.searchByAlbum {
			data.Titles[1] = "Albums ‹w›"
		}
	}
	nd := isNDSource(ms.source)
	search := isSearchSource(ms.source) || strings.HasPrefix(ms.source, genrePrefix)
	tracks := ms.tracks
	if ms.showSearchAlbums() {
		data.Episodes = ms.searchAlbumItems()
		tracks = nil
	}
	for i, s := range tracks {
		title := songTitle(s)
		// Mixed lists (favorites, playlists) have no Channel column: show the artist.
		if (nd || search) && !strings.HasPrefix(ms.source, ndAlbumPfx) && songArtist(s) != "" {
			title += " — " + songArtist(s)
		}
		if s.File == "" {
			title = "⚠ " + title
		} else if ms.nd.isStarred(s) {
			title = "★ " + title
		}
		channel := s.Album
		if nd {
			channel = songArtist(s) + " · " + s.Album
		}
		item := ui.Item{ID: "track:" + strconv.Itoa(i), Title: title, PlainTitle: title, Channel: channel, Date: songYear(s),
			Duration: clock(s.Duration), DurationSeconds: s.Duration, Index: i}
		item.Current = ms.hasCurrent && s.File != "" && ms.current.File == s.File
		data.Episodes = append(data.Episodes, item)
	}
	m.Model = m.Model.WithLibrary(data)
	if lyricID != "" {
		m.Model = m.Model.Select(ui.Favorites, lyricID)
	}
	m.renderMusicDetails()
	m.applyMusicPlayback()
	m.renderStatus()
}

// musicDetailSong is the song the Details panel (and its cover) describes.
func (m libraryShell) musicDetailSong() (mpd.Song, bool) {
	var s mpd.Song
	found := false
	switch m.Model.Focus() {
	case ui.Queue:
		if item, ok := m.Model.Selected(ui.Queue); ok {
			s, found = m.musicQueueSong(item.ID)
		}
	case ui.Favorites:
		if item, ok := m.Model.Selected(ui.Favorites); ok {
			s, found = m.musicTrack(item.ID)
		}
	default:
		if item, ok := m.Model.Selected(ui.Episodes); ok {
			s, found = m.musicTrack(item.ID)
			if a, ok := m.searchAlbumAt(item.ID); ok {
				s, found = m.music.tracks[a.tracks[0]], true
			}
		}
	}
	if !found && m.music.hasCurrent {
		s, found = m.music.current, true
	}
	return s, found
}

func (m *libraryShell) renderMusicDetails() {
	if text, ok := m.lidarrDetails(); ok {
		m.Model = m.Model.WithDetails(text)
		return
	}
	if text, ok := m.speakerDetails(); ok {
		m.Model = m.Model.WithDetails(text)
		return
	}
	if text, ok := m.searchAlbumDetails(); ok {
		m.Model = m.Model.WithDetails(text)
		return
	}
	s, found := m.musicDetailSong()
	if !found {
		m.Model = m.Model.WithDetails("Shortcuts\n\n" +
			"Enter  Show tracks / play\n" +
			"t / T  Next / previous library view\n" +
			"/      Search · f filter · w tracks/albums · paste a Spotify link\n" +
			"a      Add to queue · o whole album\n" +
			"Space  Pause · n/v next/previous\n" +
			">  <   To / from a speaker (Music Assistant)\n" +
			"z / r  Shuffle · repeat\n" +
			"?      All keys · i changelog\n" +
			"M      Podcasts")
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", songTitle(s))
	line := func(label, value string) {
		if strings.TrimSpace(value) != "" {
			fmt.Fprintf(&b, "%-9s %s\n", label, value)
		}
	}
	line("Artist", songArtist(s))
	line("Album", s.Album)
	if s.Track > 0 {
		line("Track", strconv.Itoa(s.Track))
	}
	if s.Disc > 1 {
		line("Disc", strconv.Itoa(s.Disc))
	}
	line("Year", songYear(s))
	line("Genre", s.Genre)
	line("Length", clock(s.Duration))
	line("File", s.File)
	if s.File == "" {
		b.WriteString("\nNot found in the MPD library (R updates MPD).\n")
	}
	if m.music.nd != nil && m.music.nd.isStarred(s) {
		b.WriteString("\n★ Favourite in Navidrome\n")
	}
	if m.music.nd != nil {
		b.WriteString("\np playlist · b favourite · B album favourite")
		if strings.HasPrefix(m.music.source, ndPlaylistPf) {
			b.WriteString(" · x remove from playlist")
		}
		b.WriteString("\n")
	}
	m.Model = m.Model.WithDetails(b.String())
}

func (m libraryShell) musicTrackIndex(id string) (int, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(id, "track:"))
	if err != nil || i < 0 || i >= len(m.music.tracks) {
		return 0, false
	}
	return i, true
}

func (m libraryShell) musicTrack(id string) (mpd.Song, bool) {
	if strings.HasPrefix(id, recentPrefix) {
		return m.music.recent.song(id)
	}
	i, ok := m.musicTrackIndex(id)
	if !ok {
		return mpd.Song{}, false
	}
	return m.music.tracks[i], true
}

func (ms *musicState) sourceLabel() string {
	switch {
	case strings.HasPrefix(ms.source, "artist:"):
		return strings.TrimPrefix(ms.source, "artist:")
	case ms.source == ndStarredID:
		return "★ Favourite tracks"
	case strings.HasPrefix(ms.source, ndAlbumPfx) && ms.nd != nil:
		for _, a := range ms.nd.albums {
			if ndAlbumPfx+a.ID == ms.source {
				return "★ " + a.Name
			}
		}
	case strings.HasPrefix(ms.source, ndPlaylistPf) && ms.nd != nil:
		return "≡ " + ms.nd.playlistName(strings.TrimPrefix(ms.source, ndPlaylistPf))
	case strings.HasPrefix(ms.source, albumPrefix):
		artist, album := parseAlbumSource(ms.source)
		if artist != "" {
			return album + " — " + artist
		}
		return album
	case strings.HasPrefix(ms.source, genrePrefix):
		return "Genre: " + strings.TrimPrefix(ms.source, genrePrefix)
	case isSearchSource(ms.source) && ms.lastSearch != nil:
		return "⌕ " + ms.lastSearch.query + " · " + searchScopes[ms.lastSearch.scope].label
	}
	return ""
}

func (m libraryShell) musicQueueSong(id string) (mpd.Song, bool) {
	pos, err := strconv.Atoi(strings.TrimPrefix(id, "mpdq:"))
	if err != nil {
		return mpd.Song{}, false
	}
	for _, s := range m.music.queue {
		if s.Pos == pos {
			return s, true
		}
	}
	return mpd.Song{}, false
}

func (ms *musicState) player() ui.Player {
	st := ms.status
	p := ui.Player{Status: map[string]string{"play": "playing", "pause": "paused"}[st.State], Position: "0:00", Duration: "--:--",
		HasVolume: st.Volume >= 0, Volume: max(0, st.Volume), CanSeek: ms.hasCurrent && st.State != "stop",
		CanNext: st.NextSong >= 0}
	if p.Status == "" {
		p.Status = "stopped"
	}
	p.Modes = musicModes(st)
	if ms.hasCurrent {
		p.HasCurrent = true
		p.Title = "♪ " + songTitle(ms.current)
		p.Channel = songArtist(ms.current)
		if ms.current.Album != "" {
			p.Channel += " · " + ms.current.Album
		}
		duration := st.Duration
		if duration == 0 {
			duration = ms.current.Duration
		}
		p.PositionSeconds, p.DurationSeconds, p.ClockAt = st.Elapsed, duration, ms.clockAt
		p.Position = clock(st.Elapsed)
		if duration > 0 {
			p.Duration = clock(duration)
			p.Progress = st.Elapsed / duration
		}
	}
	return p
}

func (ms *musicState) queueItems() []ui.Item {
	items := make([]ui.Item, 0, len(ms.queue))
	for _, s := range ms.queue {
		item := ui.Item{ID: "mpdq:" + strconv.Itoa(s.Pos), Title: songTitle(s), PlainTitle: songTitle(s), Channel: songArtist(s),
			Duration: clock(s.Duration), DurationSeconds: s.Duration, Index: s.Pos + 1}
		item.Current = ms.hasCurrent && s.Pos == ms.status.SongPos
		items = append(items, item)
	}
	return items
}

// applyMusicPlayback decides which source the player bar and queue show.
// Audible source wins; when nothing plays, the active view decides.
func (m *libraryShell) applyMusicPlayback() {
	ms := m.music
	if ms == nil {
		return
	}
	podcastPlaying := m.state.Status == playback.Playing
	if ms.playing() {
		ms.lastMPD = true
	} else if podcastPlaying {
		ms.lastMPD = false
	}
	useMPD := ms.playing() || (!podcastPlaying && (ms.active || ms.lastMPD))
	wasRouted := ms.routeMPD
	ms.routeMPD = useMPD
	if !useMPD && !ms.active {
		if wasRouted {
			m.shell = m.shell.withPlayback(m.state)
		}
		return
	}
	// withPlayback briefly installs the podcast queue; keep the Music queue
	// selection so it is not clamped to the podcast queue's length.
	keep, hasKeep := m.Model.Selected(ui.Queue)
	m.shell = m.shell.withPlayback(m.state)
	player, queue := m.Model.PlayerData(), m.Model.QueueData()
	if useMPD {
		player = ms.player()
	}
	if ms.active {
		queue = ms.queueItems()
	}
	m.Model = m.Model.WithPlayback(queue, player)
	if ms.active && hasKeep {
		m.Model = m.Model.Select(ui.Queue, keep.ID)
	}
}

// --- keys -------------------------------------------------------------------

func (m libraryShell) musicSyncSelection() tea.Cmd {
	item, ok := m.Model.Selected(ui.Podcasts)
	if !ok {
		return nil
	}
	source := item.ID
	if source == ndErrorID || source == m.music.source {
		return nil
	}
	m.music.source = source
	m.music.tracks, m.music.trackND = nil, nil
	return m.musicLoadTracks(source)
}

func (m libraryShell) musicKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd, bool) {
	focus := m.Model.Focus()
	armed := m.music.clearRecentArmed
	m.music.clearRecentArmed = false // any other key cancels the clear
	if focus == ui.Favorites && m.music.showLyrics {
		if updated, cmd, handled := m.lyricsKey(msg.String()); handled {
			return updated, cmd, true
		}
	}
	if updated, cmd, handled := m.lidarrKey(msg.String()); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.speakerKey(msg.String()); handled {
		return updated, cmd, true
	}
	if updated, cmd, handled := m.ndKey(msg.String()); handled {
		return updated, cmd, true
	}
	switch key := msg.String(); key {
	case "M":
		updated, cmd := m.toggleMusic()
		return updated, cmd, true
	case "enter":
		switch focus {
		case ui.Podcasts:
			m.Model = m.Model.WithFocus(ui.Episodes)
			m.renderMusic()
			return m, tea.Batch(m.musicSyncSelection(), m.lidarrTracksSync()), true
		case ui.Episodes:
			item, ok := m.Model.Selected(ui.Episodes)
			if a, isAlbum := m.searchAlbumAt(item.ID); ok && isAlbum {
				first := m.music.tracks[a.tracks[0]]
				return m, m.musicDo("Playing album…", true, m.musicPlayAlbum("", first.Album, first.AlbumArtist)), true
			}
			s, found := m.musicTrack(item.ID)
			if !ok || !found {
				return m, nil, true
			}
			if s.File == "" {
				m.notice = "Not found in the MPD library"
				m.renderStatus()
				return m, nil, true
			}
			// Search results: only this song, added to the queue and played
			// (o plays its whole album instead).
			if isSearchSource(m.music.source) {
				return m, m.musicAddAndPlay(s.File), true
			}
			// Play the album from this track, like an album player would;
			// a Navidrome list plays as a whole from this track.
			whole := isNDSource(m.music.source) || strings.HasPrefix(m.music.source, genrePrefix)
			idx, _ := m.musicTrackIndex(item.ID)
			var files []string
			start := 0
			for i, t := range m.music.tracks {
				if t.File != "" && (whole || t.Album == s.Album) {
					if i == idx {
						start = len(files)
					}
					files = append(files, t.File)
				}
			}
			return m, m.musicDo("Playing album…", true, func(ctx context.Context, c *mpd.Client) error {
				return c.ReplaceAndPlay(ctx, files, start)
			}), true
		case ui.Favorites:
			item, ok := m.Model.Selected(ui.Favorites)
			s, found := m.musicTrack(item.ID)
			if !ok || !found || s.File == "" {
				return m, nil, true
			}
			return m, m.musicAddAndPlay(s.File), true
		case ui.Queue:
			item, ok := m.Model.Selected(ui.Queue)
			if !ok {
				return m, nil, true
			}
			pos, _ := strconv.Atoi(strings.TrimPrefix(item.ID, "mpdq:"))
			return m, m.musicDo("Playing…", true, func(ctx context.Context, c *mpd.Client) error { return c.PlayPos(ctx, pos) }), true
		}
		return m, nil, false
	case "a", "A":
		var files []string
		list := ui.Episodes
		if focus == ui.Favorites {
			list = ui.Favorites
		}
		if key == "A" {
			for _, item := range m.Model.Visible(list) {
				if a, isAlbum := m.searchAlbumAt(item.ID); isAlbum {
					for _, t := range a.tracks {
						if f := m.music.tracks[t].File; f != "" {
							files = append(files, f)
						}
					}
				}
				if s, ok := m.musicTrack(item.ID); ok && s.File != "" {
					files = append(files, s.File)
				}
			}
		} else if item, ok := m.Model.Selected(list); ok {
			if a, isAlbum := m.searchAlbumAt(item.ID); isAlbum && list == ui.Episodes {
				first := m.music.tracks[a.tracks[0]]
				album, artist := first.Album, first.AlbumArtist
				return m, func() tea.Msg {
					ctx, cancel := context.WithTimeout(m.s.ctx, musicTimeout)
					defer cancel()
					files, _, err := albumFiles(ctx, m.music.client, "", album, artist)
					if err == nil {
						err = m.music.client.Add(ctx, files...)
					}
					return musicDone{err: err, notice: fmt.Sprintf("Album %q (%d tracks) added to the queue", album, len(files))}
				}, true
			}
			if s, ok := m.musicTrack(item.ID); ok && s.File != "" {
				files = append(files, s.File)
			}
		}
		if len(files) == 0 {
			return m, nil, true
		}
		n := len(files)
		return m, func() tea.Msg {
			ctx, cancel := context.WithTimeout(m.s.ctx, musicTimeout)
			defer cancel()
			err := m.music.client.Add(ctx, files...)
			return musicDone{err: err, notice: fmt.Sprintf("%d track(s) added to the queue", n)}
		}, true
	case "d", "delete":
		if focus == ui.Favorites {
			if item, ok := m.Model.Selected(ui.Favorites); ok {
				i, _ := strconv.Atoi(strings.TrimPrefix(item.ID, recentPrefix))
				if m.music.recent.remove(i) {
					m.notice = "Removed from Recently played"
					m.renderMusic()
				}
			}
			return m, nil, true
		}
		if focus != ui.Queue {
			return m, nil, true
		}
		item, ok := m.Model.Selected(ui.Queue)
		if !ok {
			return m, nil, true
		}
		pos, _ := strconv.Atoi(strings.TrimPrefix(item.ID, "mpdq:"))
		return m, m.musicDo("Removing…", false, func(ctx context.Context, c *mpd.Client) error { return c.Delete(ctx, pos) }), true
	case "D":
		if focus == ui.Favorites {
			if m.music.recent == nil || len(m.music.recent.entries) == 0 {
				m.notice = "Recently played is already empty"
			} else if !armed {
				m.music.clearRecentArmed = true
				m.notice = fmt.Sprintf("Clear Recently played (%d tracks)? Press D again · any other key cancels", len(m.music.recent.entries))
			} else {
				m.music.recent.clear()
				m.notice = "Recently played cleared"
				m.renderMusic()
			}
			m.renderStatus()
			return m, nil, true
		}
		return m, m.musicDo("Clearing queue…", false, func(ctx context.Context, c *mpd.Client) error { return c.Clear(ctx) }), true
	case "R":
		m.notice = "Updating the MPD library…"
		m.renderStatus()
		return m, tea.Batch(m.musicDo("Updating library…", false, func(ctx context.Context, c *mpd.Client) error { return c.Update(ctx) }), m.musicLoadArtists(), m.musicLoadCatalog(), m.ndLoadLists()), true
	case "=", "-":
		if m.music.routeMPD {
			kind := ui.ActionKind("volume-up")
			if key == "-" {
				kind = "volume-down"
			}
			return m, m.music.transport(m.s, ui.Action{Kind: kind}), true
		}
		return m, nil, false
	case "/":
		return m.openMusicSearch(), nil, true
	case "L":
		updated, cmd := m.toggleLyrics()
		return updated, cmd, true
	case "f":
		// Filter the focused column (the podcast view's "/"), e.g. within search results.
		updated, cmd := m.musicDelegate(tea.KeyPressMsg{Code: '/', Text: "/"})
		return updated.(libraryShell), cmd, true
	case "o":
		// Whole album of the chosen (or queued/playing) song; replaces the queue.
		s, ok := m.musicDetailSong()
		if !ok || s.File == "" {
			m.notice = "Pick a track first"
			m.renderStatus()
			return m, nil, true
		}
		return m, m.musicDo("Playing album…", true, m.musicPlayAlbum(s.File, s.Album, s.AlbumArtist)), true
	case "z", "r":
		st := m.music.status
		random, repeat, single := st.Random, st.Repeat, st.Single
		if key == "z" {
			random = !random
		} else {
			// off → repeat queue → repeat this song → off
			switch {
			case !repeat:
				repeat, single = true, false
			case !single:
				single = true
			default:
				repeat, single = false, false
			}
		}
		m.music.status.Random, m.music.status.Repeat, m.music.status.Single = random, repeat, single
		label := musicModes(m.music.status)
		if label == "" {
			label = "shuffle and repeat off"
		}
		m.notice = label
		m.renderStatus()
		return m, m.musicDo("Play mode…", false, func(ctx context.Context, c *mpd.Client) error {
			return c.SetModes(ctx, random, repeat, single)
		}), true
	case "w":
		return m.toggleSearchAlbums(), nil, true
	case "t", "T":
		step := 1
		if key == "T" {
			step = -1
		}
		updated, cmd := m.switchLibView(step)
		return updated, cmd, true
	case "i":
		m.Model = m.Model.OpenChangelog()
		return m, nil, true
	case "I", "C", "U", "m", "N", "u", "c":
		m.notice = "Only in Podcasts · M switches"
		m.renderStatus()
		return m, nil, true
	}
	return m, nil, false
}

// musicDelegate runs presentation navigation and follows the artist cursor.
func (m libraryShell) musicDelegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	updated, cmd := m.shell.Update(msg)
	m.shell = updated.(shell)
	sync := m.musicSyncSelection()
	m.renderMusicDetails()
	return m, tea.Batch(cmd, sync, m.lidarrTracksSync())
}

// musicModes names the active MPD play modes for the player title.
func musicModes(st mpd.Status) string {
	var parts []string
	if st.Random {
		parts = append(parts, "⤮ shuffle")
	}
	switch {
	case st.Repeat && st.Single:
		parts = append(parts, "↻ repeat track")
	case st.Repeat:
		parts = append(parts, "↻ repeat queue")
	case st.Single:
		parts = append(parts, "stop after track")
	}
	return strings.Join(parts, " · ")
}

// musicAddAndPlay appends one song to the MPD queue and plays it right away.
func (m libraryShell) musicAddAndPlay(file string) tea.Cmd {
	return m.musicDo("Adding…", true, func(ctx context.Context, c *mpd.Client) error {
		st, err := c.Status(ctx)
		if err != nil {
			return err
		}
		if err := c.Add(ctx, file); err != nil {
			return err
		}
		return c.PlayPos(ctx, st.PlaylistLength)
	})
}
