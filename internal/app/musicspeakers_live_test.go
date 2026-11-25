package app

import (
	"context"
	"os"
	"testing"

	"github.com/nomedialoaded/nomediaplayer/internal/musicassistant"
)

// Read-only: MA queue items → MPD files via Navidrome.
// ND_URL ND_USER ND_PASS MA_URL MA_TOKEN MA_QUEUE go test -run LiveMAResolve
func TestLiveMAResolve(t *testing.T) {
	ma, _ := musicassistant.New(os.Getenv("MA_URL"), os.Getenv("MA_TOKEN"))
	if ma == nil || os.Getenv("ND_URL") == "" {
		t.Skip("not configured")
	}
	m := ndTestShell()
	m.music.client = newMusic("127.0.0.1", 6600).client
	m.music.nd = newND(os.Getenv("ND_URL"), os.Getenv("ND_USER"), os.Getenv("ND_PASS"))
	items, err := ma.Items(context.Background(), os.Getenv("MA_QUEUE"), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	resolve := m.maResolver()
	for _, it := range items {
		f := resolve(context.Background(), it)
		t.Logf("%q → %q", it.Title, f)
		if f == "" {
			t.Error("not found", it.Title)
		}
	}
}
