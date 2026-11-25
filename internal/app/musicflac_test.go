package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/nomedialoaded/nomediaplayer/internal/flacconv"
)

func TestFlacToMP3AfterLidarrImport(t *testing.T) {
	tools, err := flacconv.Find()
	if err != nil {
		t.Skip(err)
	}
	music := t.TempDir()
	album := filepath.Join(music, "Artiest", "Artiest - Album")
	os.MkdirAll(album, 0o755)
	if out, err := exec.Command(tools.FFmpeg, "-v", "error", "-f", "lavfi", "-i", "sine=d=2", filepath.Join(album, "01 - x.flac")).CombinedOutput(); err != nil {
		t.Fatal(string(out))
	}
	var mu sync.Mutex
	var rescans []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/api/v1/rootfolder":
			io.WriteString(w, `[{"path":"/music"}]`)
		case "/api/v1/history/since":
			io.WriteString(w, `[{"albumId":1,"date":"2026-11-05T10:00:00Z","quality":{"quality":{"name":"FLAC"}},"data":{"importedPath":"/music/Artiest/Artiest - Album/01 - x.flac"}},
				{"albumId":2,"date":"2026-11-05T10:00:00Z","quality":{"quality":{"name":"MP3-320"}},"data":{"importedPath":"/music/B/C/01.mp3"}},
				{"albumId":3,"data":{"importedPath":"/music/../../etc/passwd.flac"}}]`)
		case "/api/v1/command":
			b, _ := io.ReadAll(r.Body)
			rescans = append(rescans, string(b))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	trash := t.TempDir()
	m := ndTestShell()
	m.s.ctx = context.Background()
	m.music.lidarr = newLidarrState(srv.URL, "k")
	m.music.lyrics = newLyricsState(music, "", false)
	m.music.flac = newFlacState(true, t.TempDir())
	m.music.flac.tools.Trash = func(_ context.Context, p string) error { return os.Rename(p, filepath.Join(trash, filepath.Base(p))) }

	cmd := m.flacCheck()
	if cmd == nil || m.flacCheck() != nil {
		t.Fatal("one check at a time")
	}
	msg := cmd().(flacCheckDone)
	if msg.err != nil || len(msg.albums) != 1 || msg.albums[0].localDir != album {
		t.Fatalf("only the FLAC album inside the music dir: %+v", msg)
	}
	m, cmd, _ = m.updateFlac(msg)
	done := cmd().(flacAlbumDone)
	if done.err != nil || done.res.Converted != 1 {
		t.Fatalf("%+v", done)
	}
	m, _, _ = m.updateFlac(done)
	if _, err := os.Stat(filepath.Join(album, "01 - x.mp3")); err != nil {
		t.Fatal("mp3 written")
	}
	if _, err := os.Stat(filepath.Join(trash, "01 - x.flac")); err != nil {
		t.Fatal("flac in trash")
	}
	if len(rescans) != 1 || !strings.Contains(rescans[0], "RescanFolders") || !strings.Contains(rescans[0], "/music/Artiest/Artiest - Album") {
		t.Fatal("Lidarr rescans the album", rescans)
	}
	if !strings.Contains(m.notice, "1 tracks converted") || m.music.flac.running || len(m.music.flac.Pending) != 0 {
		t.Fatal("finished", m.notice, m.music.flac.Pending)
	}
	// Saved: the next start does not look at old imports again.
	again := newFlacState(true, filepath.Dir(m.music.flac.path))
	if !again.Since.Equal(m.music.flac.Since) {
		t.Fatal("since persisted", again.Since, m.music.flac.Since)
	}
}

func TestFlacOff(t *testing.T) {
	if newFlacState(false, t.TempDir()) != nil {
		t.Fatal("off by default")
	}
	m := ndTestShell()
	if m.flacCheck() != nil {
		t.Fatal("no check without Lidarr / flac_to_mp3")
	}
}
