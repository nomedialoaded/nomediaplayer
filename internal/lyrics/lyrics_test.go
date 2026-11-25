package lyrics

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseLRC(t *testing.T) {
	l := Parse("[ar:Cat Stevens]\n[00:13.92]Now that I've lost\n[00:17.27][01:02.00]You say\n[00:26.67]\n")
	if !l.Synced || len(l.Lines) != 4 || l.Lines[0].Text != "Now that I've lost" || l.Lines[3].At != 62 {
		t.Fatalf("%+v", l)
	}
	if l.LineAt(5) != -1 || l.LineAt(14) != 0 || l.LineAt(18) != 1 || l.LineAt(999) != 3 {
		t.Fatal("LineAt")
	}
	p := Parse("\n\nHello [world]\nline two\n\n")
	if p.Synced || len(p.Lines) != 2 || p.Lines[0].Text != "Hello [world]" {
		t.Fatalf("plain %+v", p)
	}
}

func TestFindLRCNextToSong(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "A"), 0o755)
	os.WriteFile(filepath.Join(dir, "A", "01 - Song.lrc"), []byte("[00:01.00]hi"), 0o644)
	f := &Finder{MusicDir: dir}
	l, err := f.Find(context.Background(), Song{File: "A/01 - Song.mp3", Artist: "x", Title: "y"})
	if err != nil || l.Source != "lrc" || len(l.Lines) != 1 {
		t.Fatalf("%+v %v", l, err)
	}
	if l, _ := f.Find(context.Background(), Song{File: "A/02.mp3", Artist: "x", Title: "y"}); !l.Empty() {
		t.Fatal("offline miss is empty")
	}
}

func TestMusicDirFromMPD(t *testing.T) {
	p := filepath.Join(t.TempDir(), "mpd.conf")
	os.WriteFile(p, []byte("# c\nmusic_directory    \"~/Music\"\n"), 0o644)
	if got := MusicDirFromMPD(p, "/home/x"); got != "/home/x/Music" {
		t.Fatal(got)
	}
}

func TestZZLiveLRCLIB(t *testing.T) {
	if os.Getenv("LRCLIB_LIVE") == "" {
		t.Skip()
	}
	f := &Finder{Online: true, CacheDir: t.TempDir(), UserAgent: "nomediaplayer/1.0"}
	l, err := f.Find(context.Background(), Song{Artist: "John Mayer", Title: "Gravity", Album: "Continuum", Duration: 245})
	t.Logf("synced=%v lines=%d src=%s err=%v", l.Synced, len(l.Lines), l.Source, err)
	if len(l.Lines) > 2 {
		t.Log(l.Lines[1])
	}
	l2, _ := f.Find(context.Background(), Song{Artist: "John Mayer", Title: "Gravity", Album: "Continuum", Duration: 245})
	t.Log("cached:", len(l2.Lines))
}
