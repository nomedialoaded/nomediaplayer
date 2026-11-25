package lidarr

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Read-only check against a real Lidarr: LIDARR_URL + LIDARR_KEY.
func TestLiveReadOnly(t *testing.T) {
	c, err := New(os.Getenv("LIDARR_URL"), os.Getenv("LIDARR_KEY"))
	if c == nil || err != nil {
		t.Skip("LIDARR_URL/LIDARR_KEY not set")
	}
	d, err := c.Defaults(t.Context())
	t.Logf("defaults %+v err %v", d, err)
	hits, err := c.Search(t.Context(), "khruangbin")
	if err != nil || len(hits) == 0 {
		t.Fatal(hits, err)
	}
	for _, h := range hits[:min(6, len(hits))] {
		if h.Artist != nil {
			t.Logf("artist %d %q %s", h.Artist.ID, h.Artist.Name, h.Artist.Type)
		} else {
			t.Logf("album %d %q — %s (%s)", h.Album.Album.ID, h.Album.Album.Title, h.Album.Artist.Name, h.Album.Album.Year)
		}
	}
	albums, err := c.Albums(t.Context(), 327)
	t.Logf("albums %d err %v first %+v", len(albums), err, albums[:min(3, len(albums))])
	q, err := c.Queue(t.Context())
	for _, x := range q {
		t.Logf("queue %+v", x)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestLiveImportedSince(t *testing.T) {
	c, _ := New(os.Getenv("LIDARR_URL"), os.Getenv("LIDARR_KEY"))
	if c == nil {
		t.Skip("LIDARR_URL/LIDARR_KEY not set")
	}
	im, err := c.ImportedSince(t.Context(), time.Now().Add(-24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	flac := 0
	for _, i := range im {
		if strings.HasSuffix(strings.ToLower(i.Path), ".flac") {
			flac++
		}
	}
	roots, err := c.RootFolders(t.Context())
	t.Logf("%d imports (24 h), %d flac, roots %v err %v", len(im), flac, roots, err)
}
