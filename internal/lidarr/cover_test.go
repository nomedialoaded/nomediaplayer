package lidarr

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"testing"
)

func jpegOf(t *testing.T, w, h int) []byte {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h)), nil); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestAlbumCoverGuards(t *testing.T) {
	big, small, thumb := jpegOf(t, 2000, 2000), jpegOf(t, 600, 600), jpegOf(t, 250, 250)
	var local []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/MediaCover/Albums/1/cover.jpg":
			if local == nil {
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(local)
		case "/remote/x-250.jpg":
			w.Header().Set("Content-Type", "image/jpeg")
			_, _ = w.Write(thumb)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "k")
	remote := srv.URL + "/remote/x-1200.jpg"
	for _, tc := range []struct {
		name  string
		local []byte
		want  []byte
	}{
		{"normal local", small, small},
		{"too many pixels → thumbnail", big, thumb},
		{"missing → thumbnail", nil, thumb},
	} {
		local = tc.local
		got, err := c.AlbumCover(context.Background(), 1, remote, 3<<20)
		if err != nil || !bytes.Equal(got, tc.want) {
			t.Fatalf("%s: %d bytes, err %v", tc.name, len(got), err)
		}
	}
	local = nil
	if _, err := c.AlbumCover(context.Background(), 1, "", 3<<20); err != ErrNoCover {
		t.Fatalf("no cover: %v", err)
	}
}
