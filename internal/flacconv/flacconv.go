// Package flacconv turns FLAC albums that Lidarr imported into MP3 320 on
// the machine running nomediaplayer (nml fork). It works on the music folder
// as MPD sees it: ffmpeg writes the MP3 next to the FLAC, the length is
// checked, and only then the FLAC goes to the trash (never deleted
// outright). A lock file in the album folder keeps a second nomediaplayer
// (another computer on the same shared folder) from converting the same
// album at the same time.
package flacconv

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	lockName  = ".nomediaplayer-flac.lock"
	lockStale = 2 * time.Hour
	Bitrate   = "320k"
)

// Tools are the programs used; empty fields are looked up on PATH.
type Tools struct {
	FFmpeg, FFprobe string
	// Trash moves a file to the trash; nil = gio trash, else trash-put.
	Trash func(ctx context.Context, path string) error
	Nice  bool // run ffmpeg with nice -n 10 when available
}

// Find fills in the tools; an error means ffmpeg/ffprobe are missing.
func Find() (Tools, error) {
	var t Tools
	var err error
	if t.FFmpeg, err = exec.LookPath("ffmpeg"); err != nil {
		return t, errors.New("ffmpeg not found")
	}
	if t.FFprobe, err = exec.LookPath("ffprobe"); err != nil {
		return t, errors.New("ffprobe not found")
	}
	_, nerr := exec.LookPath("nice")
	t.Nice = nerr == nil
	switch {
	case lookOK("gio"):
		t.Trash = func(ctx context.Context, p string) error { return run(ctx, "gio", "trash", "--", p) }
	case lookOK("trash-put"):
		t.Trash = func(ctx context.Context, p string) error { return run(ctx, "trash-put", "--", p) }
	default:
		return t, errors.New("no trash tool (gio or trash-put) found; FLAC is kept")
	}
	return t, nil
}

func lookOK(name string) bool { _, err := exec.LookPath(name); return err == nil }

func run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return fmt.Errorf("%s: %v %s", name, err, msg)
	}
	return nil
}

// LocalDir maps a folder as Lidarr sees it (root e.g. /music) to the local
// music directory. It refuses anything that would leave musicDir.
func LocalDir(lidarrPath, lidarrRoot, musicDir string) (string, bool) {
	root := strings.TrimRight(lidarrRoot, "/")
	if root == "" || musicDir == "" || !strings.HasPrefix(lidarrPath, root+"/") {
		return "", false
	}
	rel := filepath.Clean(strings.TrimPrefix(lidarrPath, root+"/"))
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || filepath.IsAbs(rel) {
		return "", false
	}
	return filepath.Join(musicDir, rel), true
}

// Albums groups imported FLAC paths by album folder (Lidarr paths).
func Albums(paths []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range paths {
		if !strings.EqualFold(filepath.Ext(p), ".flac") {
			continue
		}
		dir := filepath.Dir(p)
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	sort.Strings(out)
	return out
}

// Result of one album.
type Result struct {
	Converted int
	Skipped   int // MP3 already there and right
	Kept      int // FLAC left in place (trash failed)
	Failed    []string
}

// ErrBusy: another nomediaplayer is converting this album.
var ErrBusy = errors.New("already being converted by another nomediaplayer")

// Album converts every .flac in dir. progress(i, n, name) is called per track.
func (t Tools) Album(ctx context.Context, dir string, progress func(i, n int, name string)) (Result, error) {
	var res Result
	unlock, err := lock(dir)
	if err != nil {
		return res, err
	}
	defer unlock()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return res, err
	}
	var flacs []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.EqualFold(filepath.Ext(e.Name()), ".flac") {
			flacs = append(flacs, e.Name())
		}
	}
	for i, name := range flacs {
		if ctx.Err() != nil {
			return res, ctx.Err()
		}
		if progress != nil {
			progress(i+1, len(flacs), name)
		}
		in := filepath.Join(dir, name)
		out := strings.TrimSuffix(in, filepath.Ext(in)) + ".mp3"
		want, err := t.duration(ctx, in)
		if err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		}
		if got, err := t.duration(ctx, out); err == nil && math.Abs(got-want) < 0.5 {
			res.Skipped++
		} else if err := t.convert(ctx, in, out, want); err != nil {
			res.Failed = append(res.Failed, name+": "+err.Error())
			continue
		} else {
			res.Converted++
		}
		if t.Trash == nil {
			res.Kept++
			continue
		}
		if err := t.Trash(ctx, in); err != nil {
			res.Kept++
			res.Failed = append(res.Failed, name+": prullenbak: "+err.Error())
		}
	}
	return res, nil
}

func (t Tools) convert(ctx context.Context, in, out string, want float64) error {
	tmp := filepath.Join(filepath.Dir(out), "."+filepath.Base(out)+".part")
	defer os.Remove(tmp)
	args := []string{"-nostdin", "-v", "error", "-y", "-i", in,
		"-map", "0:a:0", "-map", "0:v?", "-c:a", "libmp3lame", "-b:a", Bitrate,
		"-c:v", "copy", "-disposition:v", "attached_pic",
		"-map_metadata", "0", "-id3v2_version", "3", "-f", "mp3", tmp}
	name := t.FFmpeg
	if t.Nice {
		args = append([]string{"-n", "10", t.FFmpeg}, args...)
		name = "nice"
	}
	if err := run(ctx, name, args...); err != nil {
		return err
	}
	got, err := t.duration(ctx, tmp)
	if err != nil {
		return err
	}
	if math.Abs(got-want) >= 0.5 {
		return fmt.Errorf("length mismatch (%.1f s instead of %.1f s)", got, want)
	}
	return os.Rename(tmp, out)
}

func (t Tools) duration(ctx context.Context, path string) (float64, error) {
	if _, err := os.Stat(path); err != nil {
		return 0, err
	}
	out, err := exec.CommandContext(ctx, t.FFprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", path).Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe: %v", err)
	}
	d, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil || d <= 0 {
		return 0, errors.New("onbekende lengte")
	}
	return d, nil
}

// lock creates the lock file; a lock older than lockStale is taken over.
func lock(dir string) (func(), error) {
	path := filepath.Join(dir, lockName)
	for attempt := 0; attempt < 2; attempt++ {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			host, _ := os.Hostname()
			fmt.Fprintf(f, "%s %d %s\n", host, os.Getpid(), time.Now().Format(time.RFC3339))
			f.Close()
			return func() { os.Remove(path) }, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		st, serr := os.Stat(path)
		if serr != nil || time.Since(st.ModTime()) < lockStale {
			return nil, ErrBusy
		}
		_ = os.Remove(path)
	}
	return nil, ErrBusy
}
