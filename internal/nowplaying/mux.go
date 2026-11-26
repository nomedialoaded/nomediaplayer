// Package nowplaying (nml) merges the podcast player and MPD into one
// "now playing" source for the desktop media controls (MPRIS on Linux, Now
// Playing / media keys on macOS).
//
// It follows the same rule as the player bar: the audible source wins; when
// nothing plays, the source that played last stays (a paused song keeps the
// media keys). Media-key actions go to that source.
package nowplaying

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/playback"
	"github.com/nomedialoaded/nomediaplayer/internal/store"
)

// Podcast is the podcast playback engine (*playback.Engine).
type Podcast interface {
	Play(context.Context) error
	Pause(context.Context) error
	Stop(context.Context) error
	Next(context.Context) error
	Snapshot(context.Context) (playback.State, error)
	Toggle(context.Context, int64) error
	Watch(context.Context) (<-chan playback.State, error)
	SeekBy(context.Context, float64) error
}

// Music is the MPD command client (*mpd.Client).
type Music interface {
	Status(context.Context) (mpd.Status, error)
	CurrentSong(context.Context) (mpd.Song, bool, error)
	Play(context.Context) error
	Pause(context.Context, bool) error
	Stop(context.Context) error
	Next(context.Context) error
	Previous(context.Context) error
	SeekBy(context.Context, float64) error
}

// Idle blocks until MPD reports a change (mpd.Idle on its own connection).
type Idle func(context.Context) error

// MusicIDBase keeps MPD song IDs apart from podcast episode IDs in the
// track IDs that desktops see.
const MusicIDBase int64 = 1 << 40

const (
	retryMin = time.Second
	retryMax = 30 * time.Second
)

type musicSnap struct {
	ok     bool // MPD answered and has a current song
	status mpd.Status
	song   mpd.Song
}

func (s musicSnap) playing() bool { return s.ok && s.status.State == "play" }

// Mux implements the backends of internal/mpris and internal/macmedia.
type Mux struct {
	podcast Podcast
	music   Music
	idle    Idle

	mu        sync.Mutex
	lastMusic bool // MPD was the most recently audible source
	useMusic  bool // actions go to MPD
	revision  uint64
}

// New returns a Mux. music and idle may be nil (no MPD): then it only
// passes the podcast player through.
func New(podcast Podcast, music Music, idle Idle) *Mux {
	return &Mux{podcast: podcast, music: music, idle: idle}
}

// NewMPD connects the Mux to the MPD server at addr with its own connections,
// so the media controls never wait behind the TUI's MPD commands.
func NewMPD(podcast Podcast, addr string) *Mux {
	client := mpd.New(addr)
	idle := func(ctx context.Context) error {
		_, err := mpd.Idle(ctx, addr, "player", "playlist", "options")
		return err
	}
	return New(podcast, client, idle)
}

// choose decides the source and returns the merged state. It mirrors the
// player bar (internal/app applyMusicPlayback).
func (m *Mux) choose(pod playback.State, mus musicSnap) playback.State {
	m.mu.Lock()
	defer m.mu.Unlock()
	podPlaying := pod.Status == playback.Playing
	switch {
	case mus.playing():
		m.lastMusic = true
	case podPlaying:
		m.lastMusic = false
	}
	use := mus.playing() || (!podPlaying && mus.ok && (m.lastMusic || pod.Current == nil))
	m.useMusic = use
	m.revision++
	if !use {
		pod.Revision = m.revision
		return pod
	}
	return musicState(mus, m.revision)
}

func musicState(mus musicSnap, revision uint64) playback.State {
	st := playback.State{Revision: revision, ClockAt: time.Now(), Volume: mus.status.Volume}
	switch mus.status.State {
	case "play":
		st.Status = playback.Playing
	case "pause":
		st.Status = playback.Paused
	default:
		st.Status = playback.Stopped
	}
	duration := mus.status.Duration
	if duration <= 0 {
		duration = mus.song.Duration
	}
	st.Current = &store.Episode{
		ID:              MusicIDBase + int64(mus.song.ID),
		Title:           songTitle(mus.song),
		FeedTitle:       songArtist(mus.song),
		DurationSeconds: int64(duration),
		PositionSeconds: mus.status.Elapsed,
	}
	st.CanSeek = duration > 0
	if mus.status.NextSong >= 0 {
		// Only the count matters to the media controls (CanGoNext).
		st.Queue = []store.Episode{{ID: MusicIDBase - 1, Title: "next"}}
	}
	return st
}

func songTitle(s mpd.Song) string {
	if t := strings.TrimSpace(s.Title); t != "" {
		return t
	}
	base := path.Base(s.File)
	return strings.TrimSuffix(base, path.Ext(base))
}

func songArtist(s mpd.Song) string {
	if a := strings.TrimSpace(s.Artist); a != "" {
		return a
	}
	return strings.TrimSpace(s.AlbumArtist)
}

func (m *Mux) fetchMusic(ctx context.Context) (musicSnap, error) {
	if m.music == nil {
		return musicSnap{}, nil
	}
	status, err := m.music.Status(ctx)
	if err != nil {
		return musicSnap{}, err
	}
	song, ok, err := m.music.CurrentSong(ctx)
	if err != nil {
		return musicSnap{}, err
	}
	return musicSnap{ok: ok && status.State != "", status: status, song: song}, nil
}

// Watch merges both sources into one coalesced stream of states.
func (m *Mux) Watch(ctx context.Context) (<-chan playback.State, error) {
	pods, err := m.podcast.Watch(ctx)
	if err != nil {
		return nil, err
	}
	out := make(chan playback.State, 1)
	musics := make(chan musicSnap, 1)
	if m.music != nil && m.idle != nil {
		go m.followMusic(ctx, musics)
	}
	go func() {
		defer close(out)
		var pod playback.State
		var mus musicSnap
		for {
			select {
			case <-ctx.Done():
				return
			case st, ok := <-pods:
				if !ok {
					return
				}
				pod = st
			case mus = <-musics:
			}
			publish(out, m.choose(pod, mus))
		}
	}()
	return out, nil
}

// publish replaces an unread state so the reader always gets the newest.
func publish[T any](ch chan T, v T) {
	for {
		select {
		case ch <- v:
			return
		default:
		}
		select {
		case <-ch:
		default:
		}
	}
}

// followMusic sends a snapshot after every MPD change; when MPD is down it
// reports "no music" and retries with a growing pause.
func (m *Mux) followMusic(ctx context.Context, out chan musicSnap) {
	wait := retryMin
	for ctx.Err() == nil {
		fetchCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		snap, err := m.fetchMusic(fetchCtx)
		cancel()
		if err == nil {
			publish(out, snap)
			if err = m.waitMusic(ctx, snap.playing()); err == nil {
				wait = retryMin
				continue
			}
		} else {
			publish(out, musicSnap{})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, retryMax)
	}
}

// progressEvery refreshes the position while a song plays: MPD's idle only
// reports changes, and desktops show the position we last published.
const progressEvery = 2 * time.Second

// waitMusic waits for an MPD change; while playing at most progressEvery.
func (m *Mux) waitMusic(ctx context.Context, playing bool) error {
	if !playing {
		return m.idle(ctx)
	}
	tick, cancel := context.WithTimeout(ctx, progressEvery)
	defer cancel()
	err := m.idle(tick)
	if err != nil && ctx.Err() == nil && tick.Err() != nil {
		return nil // the refresh tick, not a failure
	}
	return err
}

func (m *Mux) toMusic() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.useMusic && m.music != nil
}

func (m *Mux) Play(ctx context.Context) error {
	if !m.toMusic() {
		return m.podcast.Play(ctx)
	}
	if st, err := m.music.Status(ctx); err == nil && st.State == "pause" {
		return m.music.Pause(ctx, false)
	}
	return m.music.Play(ctx)
}

func (m *Mux) Pause(ctx context.Context) error {
	if !m.toMusic() {
		return m.podcast.Pause(ctx)
	}
	return m.music.Pause(ctx, true)
}

func (m *Mux) Stop(ctx context.Context) error {
	if !m.toMusic() {
		return m.podcast.Stop(ctx)
	}
	return m.music.Stop(ctx)
}

func (m *Mux) Next(ctx context.Context) error {
	if !m.toMusic() {
		return m.podcast.Next(ctx)
	}
	return m.music.Next(ctx)
}

// Previous only exists for music; podcasts have no history to go back to.
func (m *Mux) Previous(ctx context.Context) error {
	if !m.toMusic() {
		return nil
	}
	return m.music.Previous(ctx)
}

// MusicActive reports whether the media controls currently drive MPD.
func (m *Mux) MusicActive() bool { return m.toMusic() }

func (m *Mux) Toggle(ctx context.Context, selected int64) error {
	if !m.toMusic() {
		return m.podcast.Toggle(ctx, selected)
	}
	st, err := m.music.Status(ctx)
	if err != nil {
		return err
	}
	switch st.State {
	case "play":
		return m.music.Pause(ctx, true)
	case "pause":
		return m.music.Pause(ctx, false)
	default:
		return m.music.Play(ctx)
	}
}

func (m *Mux) SeekBy(ctx context.Context, delta float64) error {
	if !m.toMusic() {
		return m.podcast.SeekBy(ctx, delta)
	}
	return m.music.SeekBy(ctx, delta)
}

// Snapshot returns the merged state right now.
func (m *Mux) Snapshot(ctx context.Context) (playback.State, error) {
	pod, err := m.podcast.Snapshot(ctx)
	if err != nil {
		return playback.State{}, err
	}
	mus, _ := m.fetchMusic(ctx) // MPD down = podcasts only
	return m.choose(pod, mus), nil
}
