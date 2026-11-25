package app

// nml: flac_to_mp3. FLAC that Lidarr imports is turned into MP3 320 by the
// machine running nomediaplayer (ffmpeg on the music share). Checked at
// start, after each finished download and every 15 minutes, from Lidarr's
// import history since the last check (kept in <state>/flac-check.json).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/activity"
	"github.com/nomedialoaded/nomediaplayer/internal/flacconv"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
)

const (
	flacStateFile = "flac-check.json"
	flacFirstLook = 7 * 24 * time.Hour // first run: imports of the last week
)

var flacEvery = 15 * time.Minute

type flacState struct {
	tools   flacconv.Tools
	err     string
	path    string
	Since   time.Time `json:"since"`
	Pending []string  `json:"pending"` // Lidarr album folders that failed, retried
	running bool
	queue   []flacAlbum
}

type (
	flacCheckDone struct {
		started time.Time
		albums  []flacAlbum
		err     error
	}
	flacAlbum struct {
		lidarrDir, localDir string
	}
	flacAlbumDone struct {
		album flacAlbum
		res   flacconv.Result
		err   error
	}
	flacTick struct{}
)

func newFlacState(enabled bool, stateDir string) *flacState {
	if !enabled {
		return nil
	}
	fs := &flacState{}
	tools, err := flacconv.Find()
	fs.tools = tools
	if err != nil {
		fs.err = err.Error()
		if tools.FFmpeg == "" || tools.FFprobe == "" {
			return fs
		}
	}
	if stateDir != "" {
		fs.path = filepath.Join(stateDir, flacStateFile)
		if data, err := os.ReadFile(fs.path); err == nil {
			_ = json.Unmarshal(data, fs)
		}
	}
	if fs.Since.IsZero() {
		fs.Since = time.Now().Add(-flacFirstLook)
	}
	return fs
}

func (fs *flacState) usable() bool {
	return fs != nil && fs.tools.FFmpeg != "" && fs.tools.FFprobe != ""
}

func (fs *flacState) save() {
	if fs.path == "" {
		return
	}
	data, err := json.Marshal(fs)
	if err != nil {
		return
	}
	tmp := fs.path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, fs.path)
	}
}

func (fs *flacState) addPending(dir string) {
	for _, p := range fs.Pending {
		if p == dir {
			return
		}
	}
	fs.Pending = append(fs.Pending, dir)
}

func (fs *flacState) dropPending(dir string) {
	out := fs.Pending[:0]
	for _, p := range fs.Pending {
		if p != dir {
			out = append(out, p)
		}
	}
	fs.Pending = out
}

// flacCheck looks for new FLAC imports (does nothing while one runs).
func (m libraryShell) flacCheck() tea.Cmd {
	ms := m.music
	if ms == nil || ms.lidarr == nil || !ms.lidarr.enabled() || !ms.flac.usable() || ms.flac.running {
		return nil
	}
	musicDir := ""
	if ms.lyrics != nil && ms.lyrics.finder != nil {
		musicDir = ms.lyrics.finder.MusicDir
	}
	if musicDir == "" {
		ms.flac.err = "music_dir unknown: set music_dir to use flac_to_mp3"
		return nil
	}
	fs, client := ms.flac, ms.lidarr.client
	fs.running = true
	since, pending := fs.Since, append([]string(nil), fs.Pending...)
	return func() tea.Msg {
		started := time.Now()
		ctx, cancel := context.WithTimeout(m.s.ctx, lidarrTimeout)
		defer cancel()
		roots, err := client.RootFolders(ctx)
		if err != nil {
			return flacCheckDone{started: started, err: err}
		}
		imported, err := client.ImportedSince(ctx, since.Add(-time.Minute))
		if err != nil {
			return flacCheckDone{started: started, err: err}
		}
		var paths []string
		for _, im := range imported {
			paths = append(paths, im.Path)
		}
		dirs := flacconv.Albums(paths)
		for _, p := range pending {
			if !containsString(dirs, p) {
				dirs = append(dirs, p)
			}
		}
		var albums []flacAlbum
		for _, d := range dirs {
			for _, root := range roots {
				if local, ok := flacconv.LocalDir(d, root, musicDir); ok {
					if hasFlac(local) {
						albums = append(albums, flacAlbum{lidarrDir: d, localDir: local})
					}
					break
				}
			}
		}
		return flacCheckDone{started: started, albums: albums}
	}
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func hasFlac(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if strings.EqualFold(filepath.Ext(e.Name()), ".flac") {
			return true
		}
	}
	return false
}

// flacConvert runs one album in the background.
func (m *libraryShell) flacConvert(a flacAlbum) tea.Cmd {
	tools, client := m.music.flac.tools, m.music.lidarr.client
	m.notice = "FLAC → MP3: " + filepath.Base(a.localDir) + "…"
	m.renderStatus()
	return trackedWork(m.s.ctx, "FLAC → MP3…", activity.Background, func(ctx context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		res, err := tools.Album(ctx, a.localDir, nil)
		if err == nil && res.Converted+res.Skipped > 0 {
			rctx, rcancel := context.WithTimeout(context.Background(), lidarrTimeout)
			_ = client.RescanFolder(rctx, a.lidarrDir)
			rcancel()
		}
		return flacAlbumDone{album: a, res: res, err: err}
	})
}

func (m libraryShell) updateFlac(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	ms := m.music
	if ms == nil || ms.flac == nil {
		return m, nil, false
	}
	fs := ms.flac
	switch msg := msg.(type) {
	case flacTick:
		return m, m.flacCheck(), true
	case flacCheckDone:
		if msg.err != nil {
			fs.running = false
			fs.err = "FLAC-controle: " + msg.err.Error()
			return m, m.flacNext(), true
		}
		fs.err = ""
		fs.Since = msg.started
		for _, a := range msg.albums {
			fs.addPending(a.lidarrDir) // until it is done
		}
		fs.save()
		if len(msg.albums) == 0 {
			fs.running = false
			return m, m.flacNext(), true
		}
		fs.queue = msg.albums
		return m, m.flacConvert(fs.queue[0]), true
	case flacAlbumDone:
		name := filepath.Base(msg.album.localDir)
		switch {
		case errors.Is(msg.err, flacconv.ErrBusy):
			fs.dropPending(msg.album.lidarrDir) // the other device does it
		case msg.err != nil:
			m.notice = "FLAC → MP3 failed: " + name + " (" + msg.err.Error() + ")"
		case len(msg.res.Failed) > 0:
			m.notice = fmt.Sprintf("FLAC → MP3: %s · %d converted, %d failed (%s)", name, msg.res.Converted, len(msg.res.Failed), msg.res.Failed[0])
		default:
			fs.dropPending(msg.album.lidarrDir)
			m.notice = fmt.Sprintf("FLAC → MP3: %s · %d tracks converted, FLAC moved to trash", name, msg.res.Converted+msg.res.Skipped)
			if msg.res.Kept > 0 {
				m.notice = fmt.Sprintf("FLAC → MP3: %s · %d converted, FLAC kept (no trash available)", name, msg.res.Converted)
			}
		}
		fs.save()
		m.renderStatus()
		fs.queue = fs.queue[1:]
		cmds := []tea.Cmd{m.musicDo("Updating library…", false, func(ctx context.Context, c *mpd.Client) error { return c.Update(ctx) })}
		if len(fs.queue) > 0 {
			cmds = append(cmds, m.flacConvert(fs.queue[0]))
		} else {
			fs.running = false
			cmds = append(cmds, m.flacNext())
		}
		return m, tea.Batch(cmds...), true
	}
	return m, nil, false
}

func (m libraryShell) flacNext() tea.Cmd {
	return tea.Tick(flacEvery, func(time.Time) tea.Msg { return flacTick{} })
}
