package app

// nml: Music Assistant speakers via Home Assistant. Column 1 view "Speakers"
// (t) lists the speakers; Tracks and Details show what the selected one
// plays. Space/Enter pause, n/v next/previous, =/- volume, w hands the
// queue to another speaker. > (anywhere in the Music view) hands the MPD
// song over: same song at the same position plus the rest of the MPD queue
// on a speaker, then MPD pauses.

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/activity"
	"github.com/nomedialoaded/nomediaplayer/internal/homeassistant"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/musicassistant"
	"github.com/nomedialoaded/nomediaplayer/internal/playback"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	speakerPrefix  = "ma:"
	speakerNowID   = "ma-row:now"
	speakerNextID  = "ma-row:next"
	speakerTimeout = 20 * time.Second
	handoffMax     = 500 // songs after the current one that go along (both ways)
	volumeStep     = 0.05
)

// Delays (variables so tests can shorten them).
var (
	speakerPollEvery = 5 * time.Second
	speakerSoon      = 700 * time.Millisecond
	handoffWait      = 12 * time.Second
	handoffStep      = 400 * time.Millisecond
)

type speakerState struct {
	client  *homeassistant.Client
	ma      *musicassistant.Client // whole queues (speaker → this computer)
	err     string                 // configuration problem
	maErr   string
	list    []homeassistant.Speaker
	at      time.Time // when list was fetched
	loaded  bool
	polling bool
	pollErr string
	queues  map[string]speakerQueue
	pick    *speakerPick
	busy    bool // hand-off running
}

type speakerQueue struct {
	current, next *homeassistant.QueueItem
	items         int
}

type speakerPick struct {
	title   string
	options []homeassistant.Speaker
	index   int
	from    string // transfer: source speaker; empty = hand-off from MPD
	take    bool   // take over: speaker → MPD
}

type (
	speakersDone struct {
		list  []homeassistant.Speaker
		queue *speakerQueue
		of    string
		err   error
	}
	speakerPoll  struct{}
	speakerActed struct {
		notice string
		err    error
	}
	speakersForPick struct {
		list []homeassistant.Speaker
		take bool
		err  error
	}
	takeoverStarted struct {
		speaker string
		title   string
		rest    []musicassistant.Item
		err     error
	}
	takeoverQueued struct {
		added, missed int
		err           error
	}
	handoffStarted struct {
		speaker string
		entity  string
		title   string
		rest    []homeassistant.Song
		err     error
	}
	handoffQueued struct {
		speaker       string
		added, missed int
		err           error
	}
)

func newSpeakerState(address, token, maAddress, maToken string) *speakerState {
	ss := &speakerState{queues: map[string]speakerQueue{}}
	c, err := homeassistant.New(address, token)
	if err != nil {
		ss.err = err.Error()
	}
	ss.client = c
	ma, err := musicassistant.New(maAddress, maToken)
	if err != nil {
		ss.maErr = err.Error()
	}
	ss.ma = ma
	return ss
}

func isSpeakerSource(source string) bool { return strings.HasPrefix(source, speakerPrefix) }

func (ss *speakerState) enabled() bool { return ss != nil && ss.client != nil }

func (ss *speakerState) speaker(entity string) (homeassistant.Speaker, bool) {
	for _, s := range ss.list {
		if s.EntityID == entity {
			return s, true
		}
	}
	return homeassistant.Speaker{}, false
}

func speakerIcon(state string) string {
	switch state {
	case "playing":
		return "▶ "
	case "paused":
		return "⏸ "
	case "unavailable", "off":
		return "✕ "
	}
	return "■ "
}

func speakerStateText(state string) string {
	switch state {
	case "playing":
		return "playing"
	case "paused":
		return "paused"
	case "idle":
		return "idle"
	case "off":
		return "off"
	case "unavailable":
		return "unreachable"
	}
	return state
}

func speakerSong(s homeassistant.Speaker) string {
	if s.Title == "" {
		return ""
	}
	if s.Artist != "" {
		return s.Title + " — " + s.Artist
	}
	return s.Title
}

// --- column 1, 2 and details -----------------------------------------------

func (ms *musicState) speakerLibraryItems() []ui.Item {
	ss := ms.speakers
	if !ss.enabled() {
		return nil
	}
	var items []ui.Item
	for _, s := range ss.list {
		items = append(items, ui.Item{ID: speakerPrefix + s.EntityID, Title: speakerIcon(s.State) + s.Name, Channel: speakerStateText(s.State)})
	}
	return items
}

func (ms *musicState) speakerLibraryEmpty() string {
	ss := ms.speakers
	switch {
	case ss == nil || ss.client == nil:
		if ss != nil && ss.err != "" {
			return ss.err + " · t switches"
		}
		return "Home Assistant not set up (ha_url, ha_token) · t switches"
	case ss.pollErr != "":
		return ss.pollErr + " · t switches"
	case !ss.loaded:
		return "Loading speakers… · t switches"
	}
	return "No Music Assistant speakers in Home Assistant · t switches"
}

func (ms *musicState) speakerEpisodes(data *ui.Data) {
	ss := ms.speakers
	entity := strings.TrimPrefix(ms.source, speakerPrefix)
	s, ok := ss.speaker(entity)
	if !ok {
		return
	}
	data.Titles[1] = "Speaker · " + s.Name
	data.EpisodeEmpty = "Nothing playing on " + s.Name + " · > hands over your MPD track"
	if !s.Available() {
		data.EpisodeEmpty = s.Name + " is unreachable"
		return
	}
	if song := speakerSong(s); song != "" {
		right := clock(s.Elapsed(time.Now()))
		if s.Duration > 0 {
			right += " / " + clock(s.Duration)
		}
		title := speakerIcon(s.State) + song
		data.Episodes = append(data.Episodes, ui.Item{ID: speakerNowID, Title: title, PlainTitle: title, Channel: s.Album,
			Duration: right, Current: s.State == "playing"})
	}
	if q, ok := ss.queues[entity]; ok && q.next != nil {
		title := "  Next: " + q.next.Name
		data.Episodes = append(data.Episodes, ui.Item{ID: speakerNextID, Title: title, PlainTitle: title,
			Duration: clock(q.next.Duration), Index: 1})
	}
}

func (m libraryShell) speakerDetails() (string, bool) {
	ms := m.music
	ss := ms.speakers
	if ss == nil || !isSpeakerSource(ms.source) {
		return "", false
	}
	var b strings.Builder
	if s, ok := ss.speaker(strings.TrimPrefix(ms.source, speakerPrefix)); ok {
		fmt.Fprintf(&b, "%s\n\n", s.Name)
		fmt.Fprintf(&b, "%-9s %s\n", "Status", speakerStateText(s.State))
		if s.Title != "" {
			fmt.Fprintf(&b, "%-9s %s\n", "Track", s.Title)
			if s.Artist != "" {
				fmt.Fprintf(&b, "%-9s %s\n", "Artist", s.Artist)
			}
			if s.Album != "" {
				fmt.Fprintf(&b, "%-9s %s\n", "Album", s.Album)
			}
			if s.Duration > 0 {
				fmt.Fprintf(&b, "%-9s %s / %s\n", "Position", clock(s.Elapsed(time.Now())), clock(s.Duration))
			}
		}
		vol := fmt.Sprintf("%d%%", int(math.Round(s.Volume*100)))
		if s.Muted {
			vol += " (gedempt)"
		}
		fmt.Fprintf(&b, "%-9s %s\n", "Volume", vol)
		if q, ok := ss.queues[s.EntityID]; ok && q.items > 0 {
			fmt.Fprintf(&b, "%-9s %d tracks\n", "Queue", q.items)
		}
		b.WriteString("\n")
	}
	b.WriteString("Space   Pause / resume\n")
	b.WriteString("n / v   Next / previous\n")
	b.WriteString("= / -   Volume\n")
	b.WriteString("w       Queue to another speaker\n")
	b.WriteString(">       MPD track to a speaker\n")
	b.WriteString("<       Continue this speaker here\n")
	return b.String(), true
}

// --- loading ------------------------------------------------------------------

// fetchSpeakers runs without the activity spinner (it polls every 5 s).
func (ss *speakerState) fetch(parent context.Context, selected string) tea.Cmd {
	client := ss.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, speakerTimeout)
		defer cancel()
		list, err := client.Speakers(ctx)
		if err != nil {
			return speakersDone{err: err}
		}
		done := speakersDone{list: list}
		if selected != "" {
			for _, s := range list {
				if s.EntityID == selected && s.Available() {
					if cur, next, n, qerr := client.Queue(ctx, selected); qerr == nil {
						done.queue, done.of = &speakerQueue{current: cur, next: next, items: n}, selected
					}
				}
			}
		}
		return done
	}
}

func (m libraryShell) speakersStartPolling() tea.Cmd {
	ss := m.music.speakers
	if !ss.enabled() || ss.polling {
		return nil
	}
	ss.polling = true
	return ss.fetch(m.s.ctx, m.selectedSpeaker())
}

func (m libraryShell) selectedSpeaker() string {
	if isSpeakerSource(m.music.source) {
		return strings.TrimPrefix(m.music.source, speakerPrefix)
	}
	return ""
}

func (m libraryShell) speakerNextPoll() tea.Cmd {
	ms := m.music
	if !ms.active || ms.libView != viewSpeakers {
		return nil
	}
	return tea.Tick(speakerPollEvery, func(time.Time) tea.Msg { return speakerPoll{} })
}

// --- keys ---------------------------------------------------------------------

// speakerKey handles the speaker view's keys and > (hand-off) everywhere.
func (m libraryShell) speakerKey(key string) (libraryShell, tea.Cmd, bool) {
	ms := m.music
	ss := ms.speakers
	if ss == nil {
		return m, nil, false
	}
	if key == ">" {
		return m.openHandoff()
	}
	if key == "<" {
		return m.openTakeover()
	}
	if !isSpeakerSource(ms.source) {
		return m, nil, false
	}
	focus := m.Model.Focus()
	if focus != ui.Podcasts && focus != ui.Episodes && focus != ui.Details {
		return m, nil, false
	}
	entity := strings.TrimPrefix(ms.source, speakerPrefix)
	s, ok := ss.speaker(entity)
	if !ok {
		return m, nil, false
	}
	switch key {
	case "space":
		return m, m.speakerDo("media_play_pause", entity, nil, ""), true
	case "enter":
		if focus == ui.Podcasts {
			return m, nil, false // into column 2 like any list
		}
		return m, m.speakerDo("media_play_pause", entity, nil, ""), true
	case "n":
		return m, m.speakerDo("media_next_track", entity, nil, "Next on "+s.Name), true
	case "v":
		return m, m.speakerDo("media_previous_track", entity, nil, "Previous on "+s.Name), true
	case "=", "+", "-":
		vol := s.Volume + volumeStep
		if key == "-" {
			vol = s.Volume - volumeStep
		}
		vol = math.Round(min(1, max(0, vol))*100) / 100
		for i := range ss.list {
			if ss.list[i].EntityID == entity {
				ss.list[i].Volume = vol // show it right away
			}
		}
		m.renderMusicDetails()
		return m, m.speakerDo("volume_set", entity, map[string]any{"volume_level": vol},
			fmt.Sprintf("Volume %s: %d%%", s.Name, int(math.Round(vol*100)))), true
	case "w":
		var others []homeassistant.Speaker
		for _, o := range ss.list {
			if o.EntityID != entity && o.Available() {
				others = append(others, o)
			}
		}
		if len(others) == 0 {
			m.notice = "No other speaker available"
			m.renderStatus()
			return m, nil, true
		}
		ss.pick = &speakerPick{title: "Hand " + s.Name + "'s queue to", options: others, from: entity}
		m.renderSpeakerPick()
		return m, nil, true
	case "a", "A", "o", "d", "D", "p", "P", "x", "b", "B", "z", "r":
		m.notice = "Speaker · Space pause, n/v, =/- volume, w hand over"
		m.renderStatus()
		return m, nil, true
	}
	return m, nil, false
}

func (m libraryShell) speakerDo(service, entity string, data map[string]any, notice string) tea.Cmd {
	client := m.music.speakers.client
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.s.ctx, speakerTimeout)
		defer cancel()
		return speakerActed{notice: notice, err: client.Media(ctx, service, entity, data)}
	}
}

// openHandoff asks which speaker gets the MPD song (fetches the list first).
func (m libraryShell) openHandoff() (libraryShell, tea.Cmd, bool) {
	ms := m.music
	ss := ms.speakers
	switch {
	case !ss.enabled():
		m.notice = ms.speakerLibraryEmpty()
	case ss.busy:
		m.notice = "Hand-over already running…"
	case !ms.hasCurrent || ms.current.File == "":
		m.notice = "MPD is not playing · start a track first"
	default:
		client := ss.client
		m.notice = "Fetching speakers…"
		m.renderStatus()
		return m, work(m.s.ctx, "Fetching speakers…", func(ctx context.Context) tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, speakerTimeout)
			defer cancel()
			list, err := client.Speakers(ctx)
			return speakersForPick{list: list, err: err}
		}), true
	}
	m.renderStatus()
	return m, nil, true
}

func (m *libraryShell) renderSpeakerPick() {
	pk := m.music.speakers.pick
	if pk == nil {
		m.Model = m.Model.WithDialog(nil)
		return
	}
	var b strings.Builder
	for i, o := range pk.options {
		marker := "  "
		if i == pk.index {
			marker = "› "
		}
		line := marker + o.Name + " · " + speakerStateText(o.State)
		if song := speakerSong(o); song != "" && o.State == "playing" {
			line += " (" + song + ")"
		}
		b.WriteString(line + "\n")
	}
	hint := "j/k choose · Enter hand over · Esc cancel"
	if pk.take {
		hint = "j/k choose · Enter continue here · Esc cancel"
	}
	m.Model = m.Model.WithDialog(&ui.Dialog{Title: pk.title, Body: strings.TrimRight(b.String(), "\n"),
		TruncateBody: true, Scrollable: true, Scroll: max(0, pk.index-8), Hint: hint})
}

func (m libraryShell) speakerPickKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd) {
	ss := m.music.speakers
	pk := ss.pick
	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
	case "esc", "q":
		ss.pick = nil
		m.notice = "Cancelled"
	case "j", "down", "tab":
		pk.index = min(len(pk.options)-1, pk.index+1)
	case "k", "up", "shift+tab":
		pk.index = max(0, pk.index-1)
	case "enter":
		to := pk.options[pk.index]
		ss.pick = nil
		m.renderSpeakerPick()
		if pk.take {
			return m, m.takeover(to)
		}
		if pk.from != "" {
			client, from := ss.client, pk.from
			m.notice = "Queue to " + to.Name + "…"
			return m, work(m.s.ctx, "Handing over queue…", func(ctx context.Context) tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, speakerTimeout)
				defer cancel()
				return speakerActed{notice: "Queue now playing on " + to.Name, err: client.Transfer(ctx, from, to.EntityID)}
			})
		}
		return m, m.handoff(to)
	}
	m.renderSpeakerPick()
	return m, nil
}

// --- hand-off MPD → speaker -------------------------------------------------

// handoffMPD is the part of the MPD client the hand-off uses.
type handoffMPD interface {
	Status(ctx context.Context) (mpd.Status, error)
	CurrentSong(ctx context.Context) (mpd.Song, bool, error)
	Queue(ctx context.Context) ([]mpd.Song, error)
	Pause(ctx context.Context, paused bool) error
}

func (m libraryShell) handoff(to homeassistant.Speaker) tea.Cmd {
	ss := m.music.speakers
	ss.busy = true
	client, mpdc := ss.client, handoffMPD(m.music.client)
	m.notice = "Handing over to " + to.Name + "…"
	return trackedWork(m.s.ctx, "Handing over to "+to.Name+"…", activity.Foreground, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		return runHandoff(ctx, client, mpdc, to)
	})
}

func toSong(s mpd.Song) homeassistant.Song {
	return homeassistant.Song{Title: songTitle(s), Artist: songArtist(s), Album: s.Album}
}

// runHandoff starts the current MPD song on the speaker at MPD's position and
// pauses MPD. The rest of the queue follows separately (handoffQueue).
func runHandoff(ctx context.Context, ha *homeassistant.Client, c handoffMPD, to homeassistant.Speaker) handoffStarted {
	fail := func(err error) handoffStarted { return handoffStarted{speaker: to.Name, entity: to.EntityID, err: err} }
	cur, ok, err := c.CurrentSong(ctx)
	if err != nil {
		return fail(err)
	}
	if !ok {
		return fail(errors.New("MPD is not playing"))
	}
	song := toSong(cur)
	uri, err := ha.Find(ctx, song)
	if err != nil {
		return fail(err)
	}
	if uri == "" {
		return fail(fmt.Errorf("«%s» not found in Music Assistant", song.Title))
	}
	st, err := c.Status(ctx)
	if err != nil {
		return fail(err)
	}
	queue, _ := c.Queue(ctx)
	var rest []homeassistant.Song
	for i := st.SongPos + 1; i >= 1 && i < len(queue) && len(rest) < handoffMax; i++ {
		rest = append(rest, toSong(queue[i]))
	}
	wasPlaying := st.State == "play"
	if wasPlaying {
		if err := c.Pause(ctx, true); err != nil {
			return fail(err)
		}
		if again, err := c.Status(ctx); err == nil {
			st = again // the exact paused position
		}
	}
	resume := func(err error) handoffStarted {
		if wasPlaying {
			_ = c.Pause(context.WithoutCancel(ctx), false)
		}
		return fail(err)
	}
	if err := ha.Play(ctx, to.EntityID, []string{uri}, "replace"); err != nil {
		return resume(err)
	}
	// Wait until the speaker plays this song, then jump to MPD's position.
	deadline := time.Now().Add(handoffWait)
	playing := false
	for time.Now().Before(deadline) {
		list, err := ha.Speakers(ctx)
		if err == nil {
			for _, s := range list {
				if s.EntityID == to.EntityID && s.State == "playing" && (s.MediaID == uri || strings.EqualFold(s.Title, song.Title)) {
					playing = true
				}
			}
		}
		if playing {
			break
		}
		select {
		case <-ctx.Done():
			return resume(ctx.Err())
		case <-time.After(handoffStep):
		}
	}
	if !playing {
		return resume(fmt.Errorf("%s did not start playing", to.Name))
	}
	if st.Elapsed > 2 {
		_ = ha.Media(ctx, "media_seek", to.EntityID, map[string]any{"seek_position": math.Round(st.Elapsed)})
	}
	return handoffStarted{speaker: to.Name, entity: to.EntityID, title: song.Title, rest: rest}
}

// handoffQueue looks up the rest of the MPD queue and adds what it finds.
func handoffQueue(ctx context.Context, ha *homeassistant.Client, entity, name string, rest []homeassistant.Song) handoffQueued {
	var uris []string
	missed := 0
	for _, s := range rest {
		uri, err := ha.Find(ctx, s)
		if err != nil {
			return handoffQueued{speaker: name, added: 0, missed: len(rest), err: err}
		}
		if uri == "" {
			missed++
			continue
		}
		uris = append(uris, uri)
	}
	if len(uris) > 0 {
		if err := ha.Play(ctx, entity, uris, "add"); err != nil {
			return handoffQueued{speaker: name, missed: len(rest), err: err}
		}
	}
	return handoffQueued{speaker: name, added: len(uris), missed: missed}
}

// --- events -------------------------------------------------------------------

func (m libraryShell) updateSpeakers(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	ms := m.music
	ss := ms.speakers
	if ss == nil {
		return m, nil, false
	}
	switch msg := msg.(type) {
	case speakerPoll:
		if ss.polling || !ss.enabled() || !ms.active || ms.libView != viewSpeakers {
			return m, nil, true
		}
		ss.polling = true
		return m, ss.fetch(m.s.ctx, m.selectedSpeaker()), true
	case speakersDone:
		ss.polling = false
		if msg.err != nil {
			ss.pollErr = "Home Assistant: " + msg.err.Error()
		} else {
			ss.pollErr, ss.list, ss.at, ss.loaded = "", msg.list, time.Now(), true
			if msg.queue != nil {
				ss.queues[msg.of] = *msg.queue
			}
		}
		_, hadSelection := m.Model.Selected(ui.Podcasts)
		m.renderMusic()
		var cmds []tea.Cmd
		if ms.active && ms.libView == viewSpeakers && !hadSelection && len(ss.list) > 0 {
			m.Model = m.Model.Select(ui.Podcasts, speakerPrefix+ss.list[0].EntityID)
			m.renderMusic()
			cmds = append(cmds, m.musicSyncSelection())
		}
		cmds = append(cmds, m.speakerNextPoll())
		return m, tea.Batch(cmds...), true
	case speakerActed:
		if msg.err != nil {
			m.notice = "Speaker: " + msg.err.Error()
		} else if msg.notice != "" {
			m.notice = msg.notice
		}
		m.renderStatus()
		return m, tea.Tick(speakerSoon, func(time.Time) tea.Msg { return speakerPoll{} }), true
	case speakersForPick:
		if msg.err != nil {
			m.notice = "Fetching speakers failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		ss.list, ss.at, ss.loaded = msg.list, time.Now(), true
		var options []homeassistant.Speaker
		for _, s := range msg.list {
			if s.Available() {
				options = append(options, s)
			}
		}
		if len(options) == 0 {
			m.notice = "No reachable Music Assistant speaker"
			m.renderStatus()
			return m, nil, true
		}
		if msg.take {
			var playing []homeassistant.Speaker
			for _, s := range options {
				if (s.State == "playing" || s.State == "paused") && s.Queue != "" {
					playing = append(playing, s)
				}
			}
			if len(playing) == 0 {
				m.notice = "No speaker is playing"
				m.renderStatus()
				return m, nil, true
			}
			if len(playing) == 1 {
				return m, m.takeover(playing[0]), true
			}
			ss.pick = &speakerPick{title: "Continue here from", options: playing, take: true}
			m.notice = ""
			m.renderSpeakerPick()
			m.renderStatus()
			return m, nil, true
		}
		title := "«" + songTitle(ms.current) + "» hand over to"
		ss.pick = &speakerPick{title: title, options: options}
		m.notice = ""
		m.renderSpeakerPick()
		m.renderStatus()
		return m, nil, true
	case handoffStarted:
		if msg.err != nil {
			ss.busy = false
			m.notice = "Hand-over failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		m.notice = fmt.Sprintf("«%s» now playing on %s · MPD paused", msg.title, msg.speaker)
		cmds := []tea.Cmd{m.musicRefresh(), tea.Tick(speakerSoon, func(time.Time) tea.Msg { return speakerPoll{} })}
		if len(msg.rest) == 0 {
			ss.busy = false
		} else {
			client, rest, name, entity := ss.client, msg.rest, msg.speaker, msg.entity
			m.notice += fmt.Sprintf(" · looking up %d next tracks…", len(rest))
			cmds = append(cmds, trackedWork(m.s.ctx, "Queue to "+name+"…", activity.Background, func(ctx context.Context) tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				defer cancel()
				return handoffQueue(ctx, client, entity, name, rest)
			}))
		}
		m.renderStatus()
		return m, tea.Batch(cmds...), true
	case takeoverStarted:
		if msg.err != nil {
			ss.busy = false
			m.notice = "Take-over failed: " + msg.err.Error()
			m.renderStatus()
			return m, nil, true
		}
		m.notice = fmt.Sprintf("«%s» now playing here · %s paused", msg.title, msg.speaker)
		cmds := []tea.Cmd{m.musicRefresh(), tea.Tick(speakerSoon, func(time.Time) tea.Msg { return speakerPoll{} })}
		if len(msg.rest) == 0 {
			ss.busy = false
		} else {
			resolve, mc, rest := m.maResolver(), m.music.client, msg.rest
			m.notice += fmt.Sprintf(" · looking up %d next tracks…", len(rest))
			cmds = append(cmds, trackedWork(m.s.ctx, "Taking over queue…", activity.Background, func(ctx context.Context) tea.Msg {
				ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
				defer cancel()
				return takeoverQueue(ctx, resolve, mc, rest)
			}))
		}
		m.renderStatus()
		return m, tea.Batch(cmds...), true
	case takeoverQueued:
		ss.busy = false
		switch {
		case msg.err != nil:
			m.notice = "Taking over the queue failed: " + msg.err.Error()
		case msg.missed > 0:
			m.notice = fmt.Sprintf("%d tracks in the MPD queue · %d not found in MPD", msg.added, msg.missed)
		default:
			m.notice = fmt.Sprintf("%d next tracks in the MPD queue", msg.added)
		}
		m.renderStatus()
		return m, m.musicRefresh(), true
	case handoffQueued:
		ss.busy = false
		switch {
		case msg.err != nil:
			m.notice = "Queue to " + msg.speaker + " failed: " + msg.err.Error()
		case msg.missed > 0:
			m.notice = fmt.Sprintf("%s: %d tracks in the queue · %d not found in Music Assistant", msg.speaker, msg.added, msg.missed)
		default:
			m.notice = fmt.Sprintf("%s: %d next tracks in the queue", msg.speaker, msg.added)
		}
		m.renderStatus()
		return m, tea.Tick(speakerSoon, func(time.Time) tea.Msg { return speakerPoll{} }), true
	}
	return m, nil, false
}

// --- take over speaker → MPD ------------------------------------------------

func (m libraryShell) openTakeover() (libraryShell, tea.Cmd, bool) {
	ms := m.music
	ss := ms.speakers
	switch {
	case !ss.enabled():
		m.notice = ms.speakerLibraryEmpty()
	case ss.ma == nil:
		m.notice = "Music Assistant not set up (ma_url, ma_token)"
		if ss.maErr != "" {
			m.notice = ss.maErr
		}
	case ss.busy:
		m.notice = "Hand-over already running…"
	default:
		// In the speaker view: the selected speaker, right away.
		if s, ok := ss.speaker(m.selectedSpeaker()); ok && ms.libView == viewSpeakers {
			if (s.State != "playing" && s.State != "paused") || s.Queue == "" {
				m.notice = s.Name + " is not playing"
				m.renderStatus()
				return m, nil, true
			}
			return m, m.takeover(s), true
		}
		client := ss.client
		m.notice = "Fetching speakers…"
		m.renderStatus()
		return m, work(m.s.ctx, "Fetching speakers…", func(ctx context.Context) tea.Msg {
			ctx, cancel := context.WithTimeout(ctx, speakerTimeout)
			defer cancel()
			list, err := client.Speakers(ctx)
			return speakersForPick{list: list, take: true, err: err}
		}), true
	}
	m.renderStatus()
	return m, nil, true
}

// takeoverMPD is the part of the MPD client the take-over uses.
type takeoverMPD interface {
	ReplaceAndPlay(ctx context.Context, files []string, start int) error
	SeekTo(ctx context.Context, seconds float64) error
	Add(ctx context.Context, files ...string) error
}

type maResolve func(ctx context.Context, it musicassistant.Item) string

// maResolver finds the MPD file of a Music Assistant track: via its
// Navidrome ID (same files) when there is one, else by tags.
func (m libraryShell) maResolver() maResolve {
	nd, mc := m.music.nd, m.music.client
	return func(ctx context.Context, it musicassistant.Item) string {
		if nd != nil && it.SubsonicID != "" {
			if s, ok, err := nd.client.Song(ctx, it.SubsonicID); err == nil && ok {
				if f := nd.resolve(ctx, mc, s).File; f != "" {
					return f
				}
			}
		}
		title := it.Title
		if title == "" {
			title = it.Name
		}
		artist := ""
		if len(it.Artists) > 0 {
			artist = it.Artists[0]
		}
		for _, pairs := range [][]string{
			{"title", title, "artist", artist, "album", it.Album},
			{"title", title, "artist", artist},
			{"title", title, "albumartist", artist},
		} {
			ok := true
			for i := 1; i < len(pairs); i += 2 {
				ok = ok && pairs[i] != ""
			}
			if !ok {
				continue
			}
			if found, err := mc.FindTags(ctx, pairs...); err == nil && len(found) > 0 {
				return found[0].File
			}
		}
		return ""
	}
}

func (m libraryShell) takeover(from homeassistant.Speaker) tea.Cmd {
	ss := m.music.speakers
	ss.busy = true
	ma, ha, resolve := ss.ma, ss.client, m.maResolver()
	mc := takeoverMPD(m.music.client)
	engine, podcastPlaying := m.s.playback, m.state.Status == playback.Playing
	return trackedWork(m.s.ctx, "Taking over from "+from.Name+"…", activity.Foreground, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if podcastPlaying && engine != nil {
			_ = engine.Pause(ctx)
		}
		return runTakeover(ctx, ma, ha, resolve, mc, from)
	})
}

// runTakeover plays the speaker's current track in MPD at the same position
// and pauses the speaker. The rest of its queue follows (takeoverQueue).
func runTakeover(ctx context.Context, ma *musicassistant.Client, ha *homeassistant.Client, resolve maResolve, c takeoverMPD, from homeassistant.Speaker) takeoverStarted {
	fail := func(err error) takeoverStarted { return takeoverStarted{speaker: from.Name, err: err} }
	q, err := ma.Queue(ctx, from.Queue)
	if err != nil {
		return fail(err)
	}
	if q.CurrentIndex == nil {
		return fail(fmt.Errorf("%s is not playing", from.Name))
	}
	items, err := ma.Items(ctx, from.Queue, *q.CurrentIndex, handoffMax+1)
	if err != nil {
		return fail(err)
	}
	if len(items) == 0 {
		return fail(fmt.Errorf("%s's queue is empty", from.Name))
	}
	cur := items[0]
	file := resolve(ctx, cur)
	name := cur.Title
	if name == "" {
		name = cur.Name
	}
	if file == "" {
		return fail(fmt.Errorf("«%s» not found in the MPD library", name))
	}
	pos := q.Position(time.Now()) // after the lookup: as late as possible
	if err := c.ReplaceAndPlay(ctx, []string{file}, 0); err != nil {
		return fail(err)
	}
	if pos > 2 {
		_ = c.SeekTo(ctx, math.Round(pos))
	}
	if q.State == "playing" {
		if err := ha.Media(ctx, "media_pause", from.EntityID, nil); err != nil {
			return takeoverStarted{speaker: from.Name, title: name, rest: items[1:], err: fmt.Errorf("playing here, but pausing %s failed: %v", from.Name, err)}
		}
	}
	return takeoverStarted{speaker: from.Name, title: name, rest: items[1:]}
}

// takeoverQueue looks up the rest of the speaker's queue in MPD and adds it.
func takeoverQueue(ctx context.Context, resolve maResolve, c takeoverMPD, rest []musicassistant.Item) takeoverQueued {
	var files []string
	missed := 0
	for _, it := range rest {
		if ctx.Err() != nil {
			return takeoverQueued{added: 0, missed: len(rest), err: ctx.Err()}
		}
		if f := resolve(ctx, it); f != "" {
			files = append(files, f)
		} else {
			missed++
		}
	}
	if len(files) > 0 {
		if err := c.Add(ctx, files...); err != nil {
			return takeoverQueued{missed: len(rest), err: err}
		}
	}
	return takeoverQueued{added: len(files), missed: missed}
}
