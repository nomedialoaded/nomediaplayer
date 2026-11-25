package app

import (
	"testing"

	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/playback"
	"github.com/nomedialoaded/nomediaplayer/internal/store"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

func TestStartupDoesNotAutoplayByDefault(t *testing.T) {
	m := libraryShell{shell: shell{s: &session{}}}
	m.state.Current = &store.Episode{ID: 1}
	if m.resumeStartup() != nil {
		t.Fatal("startup resumed without resume_on_start")
	}
	m = libraryShell{shell: shell{s: &session{}}}
	m.s.config.ResumeOnStart = true
	m.state.Current = &store.Episode{ID: 1}
	if m.resumeStartup() == nil {
		t.Fatal("resume_on_start did not resume")
	}
}

func TestMusicRoutingFollowsAudibleSource(t *testing.T) {
	ms := newMusic("", 0)
	m := libraryShell{shell: shell{s: &session{}}, music: ms}
	m.applyMusicPlayback()
	if ms.routeMPD {
		t.Fatal("idle MPD outside Muziek must not take transport")
	}
	ms.status.State = "play"
	m.applyMusicPlayback()
	if !ms.routeMPD {
		t.Fatal("playing MPD must take transport")
	}
	ms.status.State = "pause"
	m.applyMusicPlayback()
	if !ms.routeMPD || m.Model.PlayerData().Status != "paused" {
		t.Fatal("paused MPD must stay in the bar until a podcast plays", m.Model.PlayerData())
	}
	ms.active = true
	m.state.Status = playback.Playing
	m.applyMusicPlayback()
	if ms.routeMPD {
		t.Fatal("playing podcast must keep transport in Muziek view")
	}
	m.state.Status = playback.Paused
	ms.active = false
	m.applyMusicPlayback()
	if ms.routeMPD {
		t.Fatal("after a podcast played, podcast view must own transport")
	}
	ms.active = true
	m.applyMusicPlayback()
	if !ms.routeMPD {
		t.Fatal("Muziek view with silent podcast must route to MPD")
	}
	if m.Model.PlayerData().Status != "paused" {
		t.Fatal("player bar does not show MPD", m.Model.PlayerData())
	}
}

func TestMusicPlayerAndQueueProjection(t *testing.T) {
	ms := newMusic("", 0)
	ms.status = mpd.Status{State: "play", Elapsed: 30, Duration: 120, Volume: 80, SongPos: 1, NextSong: 2}
	ms.current, ms.hasCurrent = mpd.Song{File: "a/b.mp3", Title: "B", Artist: "X", Album: "Y"}, true
	ms.queue = []mpd.Song{{File: "a/a.mp3", Pos: 0}, {File: "a/b.mp3", Title: "B", Pos: 1}}
	p := ms.player()
	if p.Status != "playing" || p.Title != "♪ B" || p.Channel != "X · Y" || p.Progress != 0.25 || !p.CanNext || !p.CanSeek || p.Volume != 80 {
		t.Fatal("player projection", p)
	}
	items := ms.queueItems()
	if len(items) != 2 || items[0].Title != "a.mp3" || !items[1].Current || items[1].ID != "mpdq:1" {
		t.Fatal("queue projection", items)
	}
	if got := ms.transport(&session{}, ui.Action{Kind: ui.ActionToggle}); got != nil {
		t.Fatal("transport must stay with podcast while not routed")
	}
}
