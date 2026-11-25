package homeassistant

import (
	"context"
	"os"
	"testing"
)

// Read-only check against a real Home Assistant: HA_URL=… HA_TOKEN=… go test -run Live
func TestLiveReadOnly(t *testing.T) {
	c, err := New(os.Getenv("HA_URL"), os.Getenv("HA_TOKEN"))
	if c == nil || err != nil {
		t.Skip("HA_URL/HA_TOKEN not set")
	}
	ctx := context.Background()
	list, err := c.Speakers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		t.Logf("%s %q %s vol=%.2f %q", s.EntityID, s.Name, s.State, s.Volume, s.Title)
		if s.Available() {
			cur, next, n, err := c.Queue(ctx, s.EntityID)
			t.Logf("  queue items=%d cur=%v next=%v err=%v", n, cur, next, err)
		}
	}
	uri, err := c.Find(ctx, Song{Title: "Queen of California", Artist: "John Mayer", Album: "Born and Raised"})
	t.Logf("find: %q %v", uri, err)
	if err != nil || uri == "" {
		t.Fatal("search failed")
	}
}
