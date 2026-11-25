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

	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

type fakeLidarr struct {
	mu       sync.Mutex
	calls    []string
	added    map[string]any
	queue    string
	gotKey   string
	monitors int
	searches int
}

func (f *fakeLidarr) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gotKey = r.Header.Get("X-Api-Key")
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	body, _ := io.ReadAll(r.Body)
	switch r.Method + " " + r.URL.Path {
	case "GET /api/v1/search":
		io.WriteString(w, `[{"artist":{"artistName":"Khruangbin","foreignArtistId":"kh-1","artistType":"Group","overview":"x"}},
			{"album":{"id":6,"title":"Con Todo","foreignAlbumId":"ct-1","albumType":"Album","releaseDate":"2018-01-26","artist":{"id":7,"artistName":"Khruangbin & Leon","foreignArtistId":"kl-2"}}},
			{"album":{"title":"Mordechai Remixes","foreignAlbumId":"mr-1","albumType":"Album","releaseDate":"2021-01-01","images":[{"coverType":"cover","remoteUrl":"https://x/y-1200.jpg"}],"artist":{"artistName":"Khruangbin","foreignArtistId":"kh-1"}}}]`)
	case "GET /api/v1/artist":
		io.WriteString(w, `[{"id":7,"qualityProfileId":4,"metadataProfileId":1,"rootFolderPath":"/music"},{"id":8,"qualityProfileId":4,"metadataProfileId":1,"rootFolderPath":"/music"}]`)
	case "GET /api/v1/rootfolder":
		io.WriteString(w, `[{"path":"/music"}]`)
	case "GET /api/v1/qualityprofile":
		io.WriteString(w, `[{"id":1},{"id":4}]`)
	case "GET /api/v1/metadataprofile":
		io.WriteString(w, `[{"id":1}]`)
	case "POST /api/v1/artist":
		json.Unmarshal(body, &f.added)
		io.WriteString(w, `{"id":99,"artistName":"Khruangbin","foreignArtistId":"kh-1"}`)
	case "GET /api/v1/album":
		io.WriteString(w, `[{"id":5,"title":"Mordechai","albumType":"Album","releaseDate":"2020-06-26","statistics":{"trackFileCount":0,"totalTrackCount":10}},
			{"id":6,"title":"Con Todo El Mundo","albumType":"Album","releaseDate":"2018-01-26","statistics":{"trackFileCount":10,"totalTrackCount":10}}]`)
	case "PUT /api/v1/album/monitor":
		f.monitors++
	case "POST /api/v1/command":
		if strings.Contains(string(body), "AlbumSearch") {
			f.searches++
		}
	case "GET /api/v1/queue":
		io.WriteString(w, f.queue)
	default:
		http.NotFound(w, r)
	}
}

// run executes a command and its batched children, feeding results back.
func runCmds(t *testing.T, m libraryShell, cmd tea.Cmd, depth int) libraryShell {
	if cmd == nil || depth > 6 {
		return m
	}
	msg := cmd()
	switch msg := msg.(type) {
	case tea.BatchMsg:
		for _, c := range msg {
			m = runCmds(t, m, c, depth+1)
		}
		return m
	case lidarrRecheck, lidarrSearchDone, lidarrAlbumsDone, lidarrQueueDone, lidarrAdded, lidarrRequested, lidarrPoll:
		updated, next, _ := m.updateLidarr(msg)
		return runCmds(t, updated, next, depth+1)
	}
	return m
}

func TestLidarrFlow(t *testing.T) {
	f := &fakeLidarr{queue: `{"records":[{"id":1,"albumId":5,"title":"x","status":"downloading","trackedDownloadState":"downloading","size":100,"sizeleft":25,"album":{"title":"Mordechai"},"artist":{"artistName":"Khruangbin"}}]}`}
	lidarrPollEvery, lidarrRecheckWait, lidarrFirstLook = time.Millisecond, time.Millisecond, time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	defer srv.Close()
	m := ndTestShell()
	m.s.ctx = context.Background()
	m.music.lidarr = newLidarrState(srv.URL, "geheim")

	// / → Tab to Lidarr → search.
	m = m.openMusicSearch()
	m.music.searching.scope, m.music.searching.input = lidarrScope, "khruangbin"
	m, cmd := m.musicSearchKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runCmds(t, m, cmd, 0)
	items := m.Model.Visible(ui.Podcasts)
	if len(items) != 4 || items[0].ID != lidarrQueueID || items[0].Title != "⬇ Downloads (1)" ||
		items[1].Title != "+ Khruangbin" || items[2].Title != "  ♪ Con Todo — Khruangbin & Leon · 2018" ||
		items[3].Title != "+ ♪ Mordechai Remixes — Khruangbin · 2021" {
		t.Fatalf("Lidarr column: %+v", items)
	}

	// An album hit of an artist not in Lidarr: one row in column 2; Enter asks
	// to add the artist and request the album; Details explains, remote cover.
	m.Model = m.Model.Select(ui.Podcasts, lidarrHitPfx+"mr-1").WithFocus(ui.Podcasts)
	m = runCmds(t, m, m.musicSyncSelection(), 0)
	m.renderMusic()
	if eps := m.Model.Visible(ui.Episodes); len(eps) != 1 || eps[0].ID != lidarrHitPfx+"mr-1" {
		t.Fatalf("hit row: %+v", eps)
	}
	if text, _ := m.lidarrDetails(); !strings.HasPrefix(text, "Mordechai Remixes") || !strings.Contains(text, "add the artist") {
		t.Fatalf("hit details: %q", text)
	}
	if key, remote, _ := m.lidarrArtURLs(); key != lidarrCoverPrefix+"mb:mr-1" || remote != "https://x/y-1200.jpg" {
		t.Fatalf("hit cover: %q %q", key, remote)
	}
	m.Model = m.Model.WithFocus(ui.Episodes)
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if c := m.music.lidarr.confirm; c == nil || c.hit == nil || c.hit.Album.ForeignID != "mr-1" {
		t.Fatal("hit asks to add artist + request album")
	}
	m.music.lidarr.confirm = nil
	// An album hit already in Lidarr behaves like a discography row.
	m.Model = m.Model.Select(ui.Podcasts, lidarrHitPfx+"ct-1").WithFocus(ui.Podcasts)
	m = runCmds(t, m, m.musicSyncSelection(), 0)
	m.renderMusic()
	if eps := m.Model.Visible(ui.Episodes); len(eps) != 1 || eps[0].ID != lidarrAlbumPfx+"6" {
		t.Fatalf("hit in Lidarr: %+v", eps)
	}
	if f.gotKey != "geheim" {
		t.Fatal("API key goes in the header")
	}

	// Enter on a new artist asks first, then adds it without downloading.
	m.Model = m.Model.Select(ui.Podcasts, lidarrArtistPfx+"kh-1").WithFocus(ui.Podcasts)
	m = runCmds(t, m, m.musicSyncSelection(), 0)
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.music.lidarr.confirm == nil || len(f.added) != 0 {
		t.Fatal("confirmation before adding")
	}
	m, cmd = m.lidarrConfirmKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = runCmds(t, m, cmd, 0)
	opts, _ := f.added["addOptions"].(map[string]any)
	if f.added["qualityProfileId"] != float64(4) || f.added["rootFolderPath"] != "/music" || opts["monitor"] != "none" ||
		opts["searchForMissingAlbums"] != false || f.added["overview"] != "x" {
		t.Fatalf("artist added with defaults and nothing monitored: %+v", f.added)
	}
	if a, _ := m.music.lidarr.artist(lidarrArtistPfx + "kh-1"); a.ID != 99 {
		t.Fatal("artist now in Lidarr", a)
	}
	eps := m.Model.Visible(ui.Episodes)
	if len(eps) != 2 || eps[0].Title != "  Mordechai" || eps[0].Date != "2020" || eps[1].Title != "✓ Con Todo El Mundo" {
		t.Fatalf("albums after adding: %+v", eps)
	}

	// Details (4) keeps the album under the cursor, so its tracklist scrolls.
	for _, focus := range []ui.Panel{ui.Episodes, ui.Details} {
		m.Model = m.Model.WithFocus(ui.Episodes).Select(ui.Episodes, lidarrAlbumPfx+"5").WithFocus(focus)
		if text, _ := m.lidarrDetails(); !strings.HasPrefix(text, "Mordechai") {
			t.Fatalf("focus %v: details %q", focus, text)
		}
		if key, _, _ := m.lidarrArtURLs(); key != lidarrCoverPrefix+"5" {
			t.Fatalf("focus %v: cover %q", focus, key)
		}
	}
	m.Model = m.Model.WithFocus(ui.Queue)
	if key, _, ok := m.lidarrArtURLs(); key != "" || !ok {
		t.Fatal("elsewhere in Lidarr: logo")
	}

	// Enter on an album: confirm → monitor + AlbumSearch. Owned albums are not requested.
	m.Model = m.Model.WithFocus(ui.Episodes).Select(ui.Episodes, lidarrAlbumPfx+"6")
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.music.lidarr.confirm != nil || !strings.Contains(m.notice, "library") {
		t.Fatal("owned album is not requested again")
	}
	m.Model = m.Model.Select(ui.Episodes, lidarrAlbumPfx+"5")
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.music.lidarr.confirm == nil {
		t.Fatal("album asks first")
	}
	m, _ = m.lidarrConfirmKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.music.lidarr.confirm != nil || f.searches != 0 {
		t.Fatal("Esc cancels")
	}
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m, cmd = m.lidarrConfirmKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	msg := cmd()
	m, _, _ = m.updateLidarr(msg)
	if f.monitors != 1 || f.searches != 1 || !strings.Contains(m.notice, "Downloads") {
		t.Fatal("album monitored and searched", f.monitors, f.searches, m.notice)
	}
	// MPD actions do nothing on Lidarr rows.
	m, _, _ = m.musicKey(tea.KeyPressMsg{Code: 'a', Text: "a"})
	if !strings.Contains(m.notice, "Lidarr") {
		t.Fatal("a is blocked", m.notice)
	}

	// Downloads: progress, then finished → MPD update.
	m.Model = m.Model.WithFocus(ui.Podcasts).Select(ui.Podcasts, lidarrQueueID)
	m.music.source = lidarrQueueID
	m.renderMusic()
	if eps := m.Model.Visible(ui.Episodes); len(eps) != 1 || eps[0].Title != "⬇ Mordechai — Khruangbin" || eps[0].Duration != "75%" {
		t.Fatalf("download row: %+v", eps)
	}
	f.queue = `{"records":[]}`
	m.music.lidarr.polling = true
	m, cmd, _ = m.updateLidarr(m.music.lidarr.fetchQueue(context.Background())())
	if !strings.Contains(m.notice, "Done: Mordechai — Khruangbin") || cmd == nil {
		t.Fatal("finished download updates MPD", m.notice)
	}
}

func TestLidarrOffAndBadURL(t *testing.T) {
	if ls := newLidarrState("", ""); ls.enabled() {
		t.Fatal("empty = off")
	}
	if ls := newLidarrState("ftp://x", "k"); ls.enabled() || ls.err == "" {
		t.Fatal("only http(s)")
	}
	if ls := newLidarrState("http://user:pw@host", "k"); ls.enabled() {
		t.Fatal("no credentials in the URL")
	}
}

func TestLidarrTrackList(t *testing.T) {
	ls := &lidarrState{tracks: map[int][]lidarr.Track{}, tracksErr: map[int]string{}}
	if got := ls.trackList(1); !strings.Contains(got, "Loading") {
		t.Fatalf("not loaded: %q", got)
	}
	ls.tracks[1] = []lidarr.Track{
		{Number: "1", Medium: 1, Title: "Intro", Duration: 61 * time.Second, HasFile: true},
		{Number: "1", Medium: 2, Title: "Outro", Duration: 3599 * time.Second},
	}
	got := ls.trackList(1)
	for _, want := range []string{"CD 1", "CD 2", "✓   1    1:01  Intro", "  1   59:59  Outro", "2 tracks · 1:01:00"} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	ls.tracksErr[2] = "boom"
	if got := ls.trackList(2); !strings.Contains(got, "failed: boom") {
		t.Fatalf("error: %q", got)
	}
}
