package flacconv

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestLocalDir(t *testing.T) {
	cases := map[string]string{
		"/music/Artist/Album":         "/mnt/m/Artist/Album",
		"/music/../etc":               "",
		"/music/A/../../etc":          "",
		"/musicx/A":                   "",
		"/data/usenet/complete/music": "",
	}
	for in, want := range cases {
		got, ok := LocalDir(in, "/music/", "/mnt/m")
		if (want == "") == ok || got != want {
			t.Errorf("%s → %q %v, want %q", in, got, ok, want)
		}
	}
}

func TestAlbums(t *testing.T) {
	got := Albums([]string{"/music/A/X/1.flac", "/music/A/X/2.FLAC", "/music/B/Y/1.mp3", "/music/C/Z/1.flac"})
	if len(got) != 2 || got[0] != "/music/A/X" || got[1] != "/music/C/Z" {
		t.Fatal(got)
	}
}

func TestConvertAlbum(t *testing.T) {
	tools, err := Find()
	if err != nil {
		t.Skip(err)
	}
	dir := t.TempDir()
	trash := t.TempDir()
	tools.Trash = func(_ context.Context, p string) error { return os.Rename(p, filepath.Join(trash, filepath.Base(p))) }
	for _, n := range []string{"01 - Één ‘test’.flac", "02 - b.flac"} {
		cmd := exec.Command(tools.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=f=440:d=3", "-metadata", "title=T "+n, filepath.Join(dir, n))
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatal(string(out))
		}
	}
	os.WriteFile(filepath.Join(dir, "cover.jpg"), []byte("x"), 0o644)
	var steps int
	res, err := tools.Album(context.Background(), dir, func(i, n int, _ string) { steps++ })
	if err != nil || res.Converted != 2 || len(res.Failed) != 0 || steps != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	left, _ := filepath.Glob(filepath.Join(dir, "*"))
	moved, _ := filepath.Glob(filepath.Join(trash, "*.flac"))
	if len(left) != 3 || len(moved) != 2 { // 2 mp3 + cover
		t.Fatal(left, moved)
	}
	if _, err := os.Stat(filepath.Join(dir, lockName)); !os.IsNotExist(err) {
		t.Fatal("lock removed")
	}
	if d, _ := tools.duration(context.Background(), filepath.Join(dir, "02 - b.mp3")); d < 2.9 || d > 3.1 {
		t.Fatal("duration", d)
	}
}

func TestLockBusy(t *testing.T) {
	dir := t.TempDir()
	unlock, err := lock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lock(dir); err != ErrBusy {
		t.Fatal("second lock must wait", err)
	}
	unlock()
	if u, err := lock(dir); err != nil {
		t.Fatal(err)
	} else {
		u()
	}
}
