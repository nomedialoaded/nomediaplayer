package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
)

func TestArtistFolder(t *testing.T) {
	cases := []struct {
		have         []string
		name, folder string
		want, reused string
	}{
		{[]string{"G-Unit"}, "G‐Unit", "G‐Unit", "G-Unit", "G-Unit"},
		{[]string{"AC_DC"}, "AC/DC", "AC+DC", "AC_DC", "AC_DC"},
		{[]string{"cults"}, "Cults", "Cults", "cults", "cults"},
		{[]string{"Guns N' Roses"}, "Guns N’ Roses", "Guns N’ Roses", "Guns N' Roses", "Guns N' Roses"},
		{[]string{"Broods", "BROODS"}, "BROODS", "BROODS", "BROODS", "BROODS"}, // exact wins
		{[]string{"Adele"}, "a‐ha", "a‐ha", "a-ha", ""},                        // new: library style
		{[]string{"Adele"}, "AC/DC", "AC+DC", "AC_DC", ""},
		{nil, "The O’Jays", "The O’Jays", "The O'Jays", ""},
	}
	for _, c := range cases {
		dir := t.TempDir()
		for _, h := range c.have {
			if err := os.MkdirAll(filepath.Join(dir, h), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		got, reused := artistFolder(dir, "/music/", lidarr.Artist{Name: c.name, Folder: c.folder})
		if got != c.want || reused != c.reused {
			t.Fatalf("%v + %q: got %q/%q, want %q/%q", c.have, c.name, got, reused, c.want, c.reused)
		}
	}
	if got, reused := artistFolder("", "/music/", lidarr.Artist{Name: "a‐ha", Folder: "a‐ha"}); got != "a-ha" || reused != "" {
		t.Fatalf("no music_dir: %q/%q", got, reused)
	}
}
