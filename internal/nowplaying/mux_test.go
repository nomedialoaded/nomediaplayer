package nowplaying

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/playback"
	"github.com/nomedialoaded/nomediaplayer/internal/store"
)

type fakePodcast struct {
	mu      sync.Mutex
	state   playback.State
	calls   []string
	updates chan playback.State
}

func (f *fakePodcast) rec(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return nil
}
func (f *fakePodcast) Play(context.Context) error            { return f.rec("play") }
func (f *fakePodcast) Pause(context.Context) error           { return f.rec("pause") }
func (f *fakePodcast) Stop(context.Context) error            { return f.rec("stop") }
func (f *fakePodcast) Next(context.Context) error            { return f.rec("next") }
func (f *fakePodcast) Toggle(context.Context, int64) error   { return f.rec("toggle") }
func (f *fakePodcast) SeekBy(context.Context, float64) error { return f.rec("seek") }
func (f *fakePodcast) Snapshot(context.Context) (playback.State, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state, nil
}
func (f *fakePodcast) Watch(ctx context.Context) (<-chan playback.State, error) {
	if f.updates == nil {
		f.updates = make(chan playback.State, 4)
	}
	f.updates <- f.state
	return f.updates, nil
}

type fakeMPD struct {
	mu     sync.Mutex
	status mpd.Status
	song   mpd.Song
	hasCur bool
	down   bool
	calls  []string
}

func (f *fakeMPD) rec(c string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, c)
	return nil
}
func (f *fakeMPD) Status(context.Context) (mpd.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return mpd.Status{}, errors.New("down")
	}
	return f.status, nil
}
func (f *fakeMPD) CurrentSong(context.Context) (mpd.Song, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.song, f.hasCur, nil
}
func (f *fakeMPD) Play(context.Context) error { return f.rec("play") }
func (f *fakeMPD) Pause(_ context.Context, on bool) error {
	if on {
		return f.rec("pause")
	}
	return f.rec("resume")
}
func (f *fakeMPD) Stop(context.Context) error            { return f.rec("stop") }
func (f *fakeMPD) Next(context.Context) error            { return f.rec("next") }
func (f *fakeMPD) Previous(context.Context) error        { return f.rec("previous") }
func (f *fakeMPD) SeekBy(context.Context, float64) error { return f.rec("seek") }

func episode() *store.Episode {
	return &store.Episode{ID: 7, Title: "Episode", FeedTitle: "Show", DurationSeconds: 600}
}

func song(state string) (*fakeMPD, mpd.Status) {
	st := mpd.Status{State: state, Elapsed: 12, Duration: 200, SongPos: 0, NextSong: 1, Volume: 50}
	return &fakeMPD{status: st, song: mpd.Song{ID: 3, Title: "Song", Artist: "Artist", File: "a/b.mp3"}, hasCur: true}, st
}

func TestPlayingMusicWinsAndGetsTheKeys(t *testing.T) {
	ctx := context.Background()
	music, _ := song("play")
	pod := &fakePodcast{state: playback.State{Current: episode(), Status: playback.Paused}}
	m := New(pod, music, nil)
	st, err := m.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st.Current == nil || st.Current.Title != "Song" || st.Current.FeedTitle != "Artist" || st.Status != playback.Playing {
		t.Fatalf("want music, got %+v", st.Current)
	}
	if st.Current.ID < MusicIDBase || len(st.Queue) != 1 || !st.CanSeek || st.Current.DurationSeconds != 200 {
		t.Fatalf("music state: %+v queue=%d", st, len(st.Queue))
	}
	for _, act := range []func(context.Context) error{m.Pause, m.Next, m.Stop, m.Previous} {
		_ = act(ctx)
	}
	_ = m.Toggle(ctx, 0)
	_ = m.SeekBy(ctx, 15)
	want := []string{"pause", "next", "stop", "previous", "pause", "seek"}
	if len(music.calls) != len(want) || len(pod.calls) != 0 {
		t.Fatalf("music calls %v, podcast calls %v", music.calls, pod.calls)
	}
	for i := range want {
		if music.calls[i] != want[i] {
			t.Fatalf("music calls %v, want %v", music.calls, want)
		}
	}
}

func TestPlayingPodcastWins(t *testing.T) {
	ctx := context.Background()
	music, _ := song("pause")
	pod := &fakePodcast{state: playback.State{Current: episode(), Status: playback.Playing}}
	m := New(pod, music, nil)
	st, _ := m.Snapshot(ctx)
	if st.Current == nil || st.Current.ID != 7 {
		t.Fatalf("want podcast, got %+v", st.Current)
	}
	_ = m.Toggle(ctx, 0)
	_ = m.Previous(ctx) // no history for podcasts: no-op
	if len(pod.calls) != 1 || pod.calls[0] != "toggle" || len(music.calls) != 0 {
		t.Fatalf("podcast %v music %v", pod.calls, music.calls)
	}
}

func TestLastAudibleSourceKeepsTheKeysWhenPaused(t *testing.T) {
	ctx := context.Background()
	music, _ := song("play")
	pod := &fakePodcast{state: playback.State{Current: episode(), Status: playback.Paused}}
	m := New(pod, music, nil)
	m.Snapshot(ctx) // music audible
	music.status.State = "pause"
	st, _ := m.Snapshot(ctx)
	if st.Current == nil || st.Current.ID < MusicIDBase || st.Status != playback.Paused {
		t.Fatalf("paused song should keep the keys, got %+v", st.Current)
	}
	_ = m.Toggle(ctx, 0)
	if music.calls[len(music.calls)-1] != "resume" {
		t.Fatalf("toggle on paused song: %v", music.calls)
	}
	// Podcast starts playing: it takes over, also after it pauses again.
	pod.state.Status = playback.Playing
	m.Snapshot(ctx)
	pod.state.Status = playback.Paused
	if st, _ := m.Snapshot(ctx); st.Current == nil || st.Current.ID != 7 {
		t.Fatalf("podcast played last, got %+v", st.Current)
	}
}

func TestMPDDownOrEmptyMeansPodcastsOnly(t *testing.T) {
	ctx := context.Background()
	music, _ := song("play")
	music.down = true
	pod := &fakePodcast{state: playback.State{Current: episode(), Status: playback.Paused}}
	m := New(pod, music, nil)
	if st, _ := m.Snapshot(ctx); st.Current == nil || st.Current.ID != 7 {
		t.Fatalf("MPD down: want podcast, got %+v", st.Current)
	}
	music.down, music.hasCur = false, false
	if st, _ := m.Snapshot(ctx); st.Current == nil || st.Current.ID != 7 {
		t.Fatalf("no current song: want podcast, got %+v", st.Current)
	}
	if st, _ := New(pod, nil, nil).Snapshot(ctx); st.Current == nil || st.Current.ID != 7 {
		t.Fatal("no MPD configured")
	}
}

func TestPausedSongShownWhenNoPodcast(t *testing.T) {
	music, _ := song("pause")
	m := New(&fakePodcast{}, music, nil)
	if st, _ := m.Snapshot(context.Background()); st.Current == nil || st.Current.Title != "Song" {
		t.Fatalf("want paused song, got %+v", st.Current)
	}
}

func TestSongTitleFallsBackToFileName(t *testing.T) {
	if got := songTitle(mpd.Song{File: "Artist/Album/01 - Track.flac"}); got != "01 - Track" {
		t.Fatal(got)
	}
	if got := songArtist(mpd.Song{AlbumArtist: "Band"}); got != "Band" {
		t.Fatal(got)
	}
}

func TestWatchFollowsMPDChanges(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	music, _ := song("pause")
	pod := &fakePodcast{state: playback.State{Current: episode(), Status: playback.Playing}}
	changed := make(chan struct{})
	idle := func(ctx context.Context) error {
		select {
		case <-changed:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	m := New(pod, music, idle)
	states, err := m.Watch(ctx)
	if err != nil {
		t.Fatal(err)
	}
	waitFor := func(what string, ok func(playback.State) bool) {
		t.Helper()
		deadline := time.After(2 * time.Second)
		for {
			select {
			case st := <-states:
				if ok(st) {
					return
				}
			case <-deadline:
				t.Fatalf("timeout waiting for %s", what)
			}
		}
	}
	waitFor("podcast", func(st playback.State) bool { return st.Current != nil && st.Current.ID == 7 })
	// The podcast is paused and the song starts (auto-pause in the app).
	pod.state.Status = playback.Paused
	pod.updates <- pod.state
	music.mu.Lock()
	music.status.State = "play"
	music.mu.Unlock()
	changed <- struct{}{}
	waitFor("music", func(st playback.State) bool {
		return st.Current != nil && st.Current.ID >= MusicIDBase && st.Status == playback.Playing
	})
	if !m.MusicActive() {
		t.Fatal("actions should go to MPD")
	}
}

func TestPositionRefreshesWhilePlaying(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	music, _ := song("play")
	var mu sync.Mutex
	idles := 0
	idle := func(ctx context.Context) error { // MPD never reports a change
		mu.Lock()
		idles++
		mu.Unlock()
		<-ctx.Done()
		return ctx.Err()
	}
	m := New(&fakePodcast{}, music, idle)
	if err := m.waitMusic(ctx, true); err != nil {
		t.Fatalf("refresh tick must not count as an error: %v", err)
	}
	stop, cancelStop := context.WithCancel(ctx)
	cancelStop()
	if err := m.waitMusic(stop, true); err == nil {
		t.Fatal("cancel must end the wait with an error")
	}
	mu.Lock()
	defer mu.Unlock()
	if idles != 2 {
		t.Fatalf("idle calls: %d", idles)
	}
}
