package profile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetKeysKeepsCommentsAndIsPrivate(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "cfg")
	path := filepath.Join(dir, "config.toml")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\ntheme = \"nord\"\nlidarr_url = \"http://old\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	err := SetKeys(path, []Setting{{"lidarr_url", "http://new:8686"}, {"lidarr_api_key", `a"b`}, {"podcasts", false}, {"mpd_port", 6601}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	got := string(data)
	for _, want := range []string{"# mine", `theme = "nord"`, `lidarr_url = "http://new:8686"`, `lidarr_api_key = "a\"b"`, "podcasts = false", "mpd_port = 6601"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in\n%s", want, got)
		}
	}
	if strings.Contains(got, "http://old") {
		t.Errorf("old value kept:\n%s", got)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0600 {
		t.Errorf("file mode %v", st.Mode().Perm())
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0700 {
		t.Errorf("dir mode %v", st.Mode().Perm())
	}
	cfg, err := Load(path)
	if err != nil || cfg.LidarrAPIKey != `a"b` || cfg.Podcasts || cfg.MPDPort != 6601 {
		t.Fatalf("reload: %+v %v", cfg, err)
	}
	if err := SetKeys(path, []Setting{{"nope", 1}}); err == nil {
		t.Error("unknown key accepted")
	}
}

func TestPodcastsOffForcesMusicStart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_ = os.WriteFile(path, []byte("start_view = \"podcasts\"\npodcasts = false\n"), 0600)
	cfg, err := Load(path)
	if err != nil || cfg.StartView != "music" {
		t.Fatalf("%v %v", cfg.StartView, err)
	}
}
