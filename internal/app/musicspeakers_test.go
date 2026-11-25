package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/homeassistant"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/musicassistant"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

type fakeMA struct {
	mu      sync.Mutex
	calls   []string
	bodies  []map[string]any
	playing string // title the speaker plays after play_media
	unknown map[string]bool
}

func (f *fakeMA) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	data, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(data, &body)
	f.calls = append(f.calls, r.URL.Path)
	f.bodies = append(f.bodies, body)
	switch r.URL.Path {
	case "/api/states":
		state, title := "idle", ""
		if f.playing != "" {
			state, title = "playing", f.playing
		}
		json.NewEncoder(w).Encode([]map[string]any{
			{"entity_id": "media_player.woonkamer", "state": state, "attributes": map[string]any{"friendly_name": "Woonkamer",
				"mass_player_type": "player", "media_title": title, "volume_level": 0.3}},
			{"entity_id": "media_player.bureau", "state": "paused", "attributes": map[string]any{"friendly_name": "Bureau", "mass_player_type": "player"}},
		})
	case "/api/config/config_entries/entry":
		io.WriteString(w, `[{"entry_id":"E1","state":"loaded"}]`)
	case "/api/services/music_assistant/search":
		name, _ := body["name"].(string)
		if f.unknown[name] {
			io.WriteString(w, `{"service_response":{"tracks":[]}}`)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"service_response": map[string]any{"tracks": []map[string]any{
			{"uri": "library://track/" + name, "name": name, "artists": []map[string]any{{"name": body["artist"]}}}}}})
	case "/api/services/music_assistant/play_media":
		if body["enqueue"] == "replace" {
			f.playing, _ = body["media_id"].(string)
			f.playing = strings.TrimPrefix(f.playing, "library://track/")
		}
	}
}

func (f *fakeMA) called(path string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for i, c := range f.calls {
		if c == path {
			out = append(out, f.bodies[i])
		}
	}
	return out
}

type fakeHandoffMPD struct {
	state   string
	elapsed float64
	pauses  []bool
}

func (f *fakeHandoffMPD) Status(context.Context) (mpd.Status, error) {
	return mpd.Status{State: f.state, Elapsed: f.elapsed, SongPos: 1}, nil
}
func (f *fakeHandoffMPD) CurrentSong(context.Context) (mpd.Song, bool, error) {
	return mpd.Song{File: "b.mp3", Title: "Gravity", Artist: "John Mayer", Album: "Continuum"}, true, nil
}
func (f *fakeHandoffMPD) Queue(context.Context) ([]mpd.Song, error) {
	return []mpd.Song{{Title: "Waiting"}, {Title: "Gravity"}, {Title: "Belief", Artist: "John Mayer"}, {Title: "Onbekend"}}, nil
}
func (f *fakeHandoffMPD) Pause(_ context.Context, p bool) error {
	f.pauses = append(f.pauses, p)
	if p {
		f.state = "pause"
	} else {
		f.state = "play"
	}
	return nil
}

func newFakeMA(t *testing.T) (*fakeMA, *homeassistant.Client) {
	f := &fakeMA{unknown: map[string]bool{"Onbekend": true}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	c, err := homeassistant.New(srv.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func TestHandoffPlaysSeeksAndPausesMPD(t *testing.T) {
	handoffStep = time.Millisecond
	f, ha := newFakeMA(t)
	m := &fakeHandoffMPD{state: "play", elapsed: 83.4}
	to := homeassistant.Speaker{EntityID: "media_player.woonkamer", Name: "Woonkamer"}
	res := runHandoff(context.Background(), ha, m, to)
	if res.err != nil || res.title != "Gravity" {
		t.Fatal(res.err, res.title)
	}
	if len(m.pauses) != 1 || !m.pauses[0] {
		t.Fatal("MPD should pause once", m.pauses)
	}
	seek := f.called("/api/services/media_player/media_seek")
	if len(seek) != 1 || seek[0]["seek_position"] != float64(83) || seek[0]["entity_id"] != "media_player.woonkamer" {
		t.Fatal("seek", seek)
	}
	// Only the songs after the current one go along.
	if len(res.rest) != 2 || res.rest[0].Title != "Belief" {
		t.Fatal("rest", res.rest)
	}
	q := handoffQueue(context.Background(), ha, to.EntityID, to.Name, res.rest)
	if q.err != nil || q.added != 1 || q.missed != 1 {
		t.Fatal(q)
	}
	adds := f.called("/api/services/music_assistant/play_media")
	if len(adds) != 2 || adds[1]["enqueue"] != "add" || adds[1]["media_id"] != "library://track/Belief" {
		t.Fatal("enqueue", adds)
	}
}

func TestHandoffNotFoundKeepsMPDPlaying(t *testing.T) {
	f, ha := newFakeMA(t)
	f.unknown["Gravity"] = true
	m := &fakeHandoffMPD{state: "play", elapsed: 10}
	res := runHandoff(context.Background(), ha, m, homeassistant.Speaker{EntityID: "media_player.woonkamer", Name: "Woonkamer"})
	if res.err == nil || !strings.Contains(res.err.Error(), "not found") || len(m.pauses) != 0 {
		t.Fatal(res.err, m.pauses)
	}
	if len(f.called("/api/services/music_assistant/play_media")) != 0 {
		t.Fatal("nothing should play")
	}
}

func TestHandoffResumesMPDWhenSpeakerStaysSilent(t *testing.T) {
	handoffStep, handoffWait = time.Millisecond, 20*time.Millisecond
	defer func() { handoffWait = 12 * time.Second }()
	f, ha := newFakeMA(t)
	m := &fakeHandoffMPD{state: "play"}
	// The speaker reports another title, so it never "starts".
	res := runHandoff(context.Background(), ha, m, homeassistant.Speaker{EntityID: "media_player.bureau", Name: "Bureau"})
	_ = f
	if res.err == nil || len(m.pauses) != 2 || m.pauses[1] {
		t.Fatal("MPD should resume", res.err, m.pauses)
	}
}

func TestSpeakersViewAndKeys(t *testing.T) {
	speakerSoon = time.Millisecond
	f, ha := newFakeMA(t)
	m := ndTestShell()
	m.s.ctx = context.Background()
	m.music.speakers = &speakerState{client: ha, queues: map[string]speakerQueue{}}
	m.music.libView = viewSpeakers
	m.renderMusic()
	cmd := m.speakersStartPolling()
	m, cmd, _ = m.updateSpeakers(cmd())
	col := m.Model.Visible(ui.Podcasts)
	if len(col) != 2 || col[0].Title != "⏸ Bureau" || m.music.source != speakerPrefix+"media_player.bureau" {
		t.Fatal(col, m.music.source)
	}
	m.Model = m.Model.Select(ui.Podcasts, speakerPrefix+"media_player.woonkamer")
	m.music.source = speakerPrefix + "media_player.woonkamer"
	m, cmd, handled := m.musicKey(tea.KeyPressMsg{Code: '=', Text: "="})
	if !handled || cmd == nil {
		t.Fatal("volume key")
	}
	cmd()
	vol := f.called("/api/services/media_player/volume_set")
	if len(vol) != 1 || vol[0]["volume_level"] != 0.35 {
		t.Fatal("volume", vol)
	}
	_, cmd, _ = m.musicKey(tea.KeyPressMsg{Code: ' ', Text: " "})
	if cmd == nil {
		t.Fatal("space")
	}
	cmd()
	if len(f.called("/api/services/media_player/media_play_pause")) != 1 {
		t.Fatal("play_pause not sent")
	}
	// w opens a picker with only the other speaker.
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'w', Text: "w"})
	if pk := m.music.speakers.pick; pk == nil || len(pk.options) != 1 || pk.options[0].Name != "Bureau" {
		t.Fatal("transfer picker", pk)
	}
	m, cmd = m.speakerPickKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd()
	if msg, ok := msg.(speakerActed); !ok || msg.err != nil {
		t.Fatal(msg)
	}
	tr := f.called("/api/services/music_assistant/transfer_queue")
	if len(tr) != 1 || tr[0]["source_player"] != "media_player.woonkamer" || tr[0]["entity_id"] != "media_player.bureau" {
		t.Fatal("transfer", tr)
	}
}

func TestHandoffNeedsMPDSong(t *testing.T) {
	_, ha := newFakeMA(t)
	m := ndTestShell()
	m.music.speakers = &speakerState{client: ha, queues: map[string]speakerQueue{}}
	m, cmd, handled := m.musicKey(tea.KeyPressMsg{Code: '>', Text: ">"})
	if !handled || cmd != nil || !strings.Contains(m.notice, "MPD is not playing") {
		t.Fatal(m.notice)
	}
}

type fakeTakeoverMPD struct {
	played []string
	seek   float64
	added  []string
}

func (f *fakeTakeoverMPD) ReplaceAndPlay(_ context.Context, files []string, _ int) error {
	f.played = files
	return nil
}
func (f *fakeTakeoverMPD) SeekTo(_ context.Context, s float64) error { f.seek = s; return nil }
func (f *fakeTakeoverMPD) Add(_ context.Context, files ...string) error {
	f.added = append(f.added, files...)
	return nil
}

func fakeMAServer(t *testing.T, state string) *musicassistant.Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Command string `json:"command"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Command {
		case "player_queues/get":
			io.WriteString(w, `{"queue_id":"q1","state":"`+state+`","items":4,"current_index":1,"elapsed_time":61.6,"elapsed_time_last_updated":0}`)
		case "player_queues/items":
			io.WriteString(w, `[{"name":"Gravity","media_item":{"name":"Gravity","artists":[{"name":"John Mayer"}]}},
				{"name":"Belief","media_item":{"name":"Belief","artists":[{"name":"John Mayer"}]}},
				{"name":"Onbekend","media_item":{"name":"Onbekend"}}]`)
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := musicassistant.New(srv.URL, "tok")
	return c
}

func testResolve(_ context.Context, it musicassistant.Item) string {
	if it.Title == "Onbekend" {
		return ""
	}
	return it.Title + ".mp3"
}

func TestTakeoverPlaysInMPDAndPausesSpeaker(t *testing.T) {
	f, ha := newFakeMA(t)
	ma := fakeMAServer(t, "playing")
	c := &fakeTakeoverMPD{}
	from := homeassistant.Speaker{EntityID: "media_player.woonkamer", Name: "Woonkamer", Queue: "q1"}
	res := runTakeover(context.Background(), ma, ha, testResolve, c, from)
	if res.err != nil || res.title != "Gravity" || len(c.played) != 1 || c.played[0] != "Gravity.mp3" || c.seek != 62 {
		t.Fatal(res.err, res.title, c.played, c.seek)
	}
	if p := f.called("/api/services/media_player/media_pause"); len(p) != 1 || p[0]["entity_id"] != "media_player.woonkamer" {
		t.Fatal("speaker should pause", p)
	}
	q := takeoverQueue(context.Background(), testResolve, c, res.rest)
	if q.added != 1 || q.missed != 1 || c.added[0] != "Belief.mp3" {
		t.Fatal(q, c.added)
	}
}

func TestTakeoverNotFoundLeavesSpeaker(t *testing.T) {
	f, ha := newFakeMA(t)
	ma := fakeMAServer(t, "playing")
	c := &fakeTakeoverMPD{}
	none := func(context.Context, musicassistant.Item) string { return "" }
	res := runTakeover(context.Background(), ma, ha, none, c, homeassistant.Speaker{EntityID: "media_player.woonkamer", Name: "Woonkamer", Queue: "q1"})
	if res.err == nil || len(c.played) != 0 || len(f.called("/api/services/media_player/media_pause")) != 0 {
		t.Fatal(res.err, c.played)
	}
}

func TestTakeoverNeedsMAConfig(t *testing.T) {
	_, ha := newFakeMA(t)
	m := ndTestShell()
	m.music.speakers = &speakerState{client: ha, queues: map[string]speakerQueue{}}
	m, cmd, _ := m.musicKey(tea.KeyPressMsg{Code: '<', Text: "<"})
	if cmd != nil || !strings.Contains(m.notice, "ma_url") {
		t.Fatal(m.notice)
	}
}
