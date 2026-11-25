package app

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
)

func TestLidarrCoversCacheAndClear(t *testing.T) {
	var buf bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 1200, 600))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	src.Set(0, 0, color.Black)
	if err := jpeg.Encode(&buf, src, nil); err != nil {
		t.Fatal(err)
	}
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/MediaCover/Albums/7/cover.jpg" && r.Header.Get("X-Api-Key") == "k" {
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(buf.Bytes())
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()
	client, err := lidarr.New(srv.URL, "k")
	if err != nil {
		t.Fatal(err)
	}
	c := newLidarrCovers(client)
	ctx := context.Background()
	img, err := c.Image(ctx, lidarrCoverPrefix+"7", "")
	if err != nil {
		t.Fatal(err)
	}
	if b := img.Bounds(); b.Dx() != lidarrCoverPx || b.Dy() != lidarrCoverPx/2 {
		t.Fatalf("not shrunk: %v", b)
	}
	if _, err := c.Image(ctx, lidarrCoverPrefix+"7", ""); err != nil || hits.Load() != 1 {
		t.Fatalf("cache miss: hits=%d err=%v", hits.Load(), err)
	}
	// Missing cover is remembered too.
	if _, err := c.Image(ctx, lidarrCoverPrefix+"8", ""); err == nil {
		t.Fatal("cover for 8")
	}
	n := hits.Load()
	if _, err := c.Image(ctx, lidarrCoverPrefix+"8", ""); err == nil || hits.Load() != n {
		t.Fatal("missing cover asked again")
	}
	if c.len() != 2 {
		t.Fatalf("len %d", c.len())
	}
	c.clear()
	if c.len() != 0 {
		t.Fatal("not cleared")
	}
	if _, err := c.Image(ctx, lidarrCoverPrefix+"7", ""); err != nil || hits.Load() != n+1 {
		t.Fatal("not refetched after clear")
	}
	// Cancelled loads are not remembered as missing.
	cctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := c.Image(cctx, lidarrCoverPrefix+"9", ""); err == nil || c.len() != 1 {
		t.Fatalf("cancelled load cached: len=%d", c.len())
	}
}
