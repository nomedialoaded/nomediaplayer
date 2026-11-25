package app

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/profile"
	"github.com/nomedialoaded/nomediaplayer/internal/subsonic"
)

// Runs only with PODMUTT_ND_LIVE=1: matches every Navidrome favorite and
// playlist entry to the local MPD library and reports the hit rate. Read-only.
func TestNavidromeLiveMatch(t *testing.T) {
	if os.Getenv("PODMUTT_ND_LIVE") == "" {
		t.Skip("live Navidrome test")
	}
	paths, _ := userPaths()
	cfg, err := profile.Load(paths.Config)
	if err != nil {
		t.Fatal(err)
	}
	nd := newND(cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePassword)
	if nd == nil {
		t.Skip("navidrome_url not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	mc := mpd.New("127.0.0.1:6600")
	check := func(name string, list []subsonic.Song) {
		hit := 0
		start := time.Now()
		for _, s := range list {
			if nd.resolve(ctx, mc, s).File != "" {
				hit++
			} else {
				t.Logf("  %s: niet gevonden: %s — %s (%s) path=%s", name, s.Artist, s.Title, s.Album, s.Path)
			}
		}
		t.Logf("%s: %d/%d gekoppeld in %v", name, hit, len(list), time.Since(start).Round(time.Millisecond))
	}
	albums, starred, err := nd.client.Starred(ctx)
	if err != nil {
		t.Fatal(err)
	}
	check("favoriete nummers", starred)
	for _, a := range albums {
		songs, err := nd.client.Album(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		check("album "+a.Name, songs)
	}
	pls, err := nd.client.Playlists(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range pls {
		songs, err := nd.client.Playlist(ctx, p.ID)
		if err != nil {
			t.Fatal(err)
		}
		check("playlist "+p.Name, songs)
	}
	// Reverse lookup (MPD -> Navidrome) for a few favorites.
	for _, s := range starred[:min(5, len(starred))] {
		m := nd.resolve(ctx, mc, s)
		if m.File == "" {
			continue
		}
		found, err := mc.FindTags(ctx, "file", m.File)
		if err != nil || len(found) == 0 {
			t.Errorf("file lookup %s: %v", m.File, err)
			continue
		}
		back, err := ndLookup(ctx, nd.client, found[0])
		if err != nil || back.ID != s.ID {
			t.Errorf("reverse %q: got %q err %v", s.Title, back.ID, err)
		}
	}
}

// PODMUTT_ND_WRITE=1: create a temporary playlist, add/remove a song,
// star/unstar a non-favorite song, then delete the playlist again.
func TestNavidromeLiveWrite(t *testing.T) {
	if os.Getenv("PODMUTT_ND_WRITE") == "" {
		t.Skip("live Navidrome write test")
	}
	paths, _ := userPaths()
	cfg, _ := profile.Load(paths.Config)
	c := subsonic.New(cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePassword)
	ctx := context.Background()
	songs, err := c.SearchSongs(ctx, "Gravity", 5)
	if err != nil || len(songs) == 0 {
		t.Fatal(songs, err)
	}
	a := songs[0]
	if err := c.CreatePlaylist(ctx, "nomedia-test", []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	pls, _ := c.Playlists(ctx)
	id := ""
	for _, p := range pls {
		if p.Name == "nomedia-test" {
			id = p.ID
		}
	}
	defer func() { t.Logf("delete: %v", c.DeletePlaylist(ctx, id)) }()
	if err := c.AddToPlaylist(ctx, id, []string{a.ID}); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Playlist(ctx, id); len(got) != 2 {
		t.Fatalf("after add: %d", len(got))
	}
	if err := c.RemoveFromPlaylist(ctx, id, 1); err != nil {
		t.Fatal(err)
	}
	if got, _ := c.Playlist(ctx, id); len(got) != 1 {
		t.Fatalf("after remove: %d", len(got))
	}
	other, _ := c.SearchSongs(ctx, "Pizza", 5)
	for _, s := range other {
		if !s.Starred {
			if err := c.StarSong(ctx, s.ID, true); err != nil {
				t.Fatal(err)
			}
			if err := c.StarSong(ctx, s.ID, false); err != nil {
				t.Fatal(err)
			}
			t.Logf("star/unstar ok: %s", s.Title)
			break
		}
	}
}
