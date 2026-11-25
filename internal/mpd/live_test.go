package mpd

import (
	"context"
	"os"
	"testing"
)

// Runs only with PODMUTT_MPD_LIVE=1 against a local MPD.
func TestLive(t *testing.T) {
	if os.Getenv("PODMUTT_MPD_LIVE") == "" {
		t.Skip("live MPD test")
	}
	ctx := context.Background()
	c := New("127.0.0.1:6600")
	artists, err := c.List(ctx, "albumartist")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%d artists, first: %v", len(artists), artists[:min(5, len(artists))])
	if len(artists) > 0 {
		songs, err := c.Find(ctx, "albumartist", artists[0])
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range songs[:min(3, len(songs))] {
			t.Logf("%+v", s)
		}
	}
	st, err := c.Status(ctx)
	t.Logf("status %+v %v", st, err)
	q, err := c.Queue(ctx)
	t.Logf("queue %d %v", len(q), err)
}
