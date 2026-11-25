package spotifylink

import (
	"context"
	"os"
	"testing"
)

func TestParseAndFind(t *testing.T) {
	for link, want := range map[string]string{
		"https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=abc":  "track 4cOdK2wGLETKBW3PvgPWqT",
		"https://open.spotify.com/intl-nl/track/4cOdK2wGLETKBW3PvgPWqT": "track 4cOdK2wGLETKBW3PvgPWqT",
		"https://open.spotify.com/album/5Z9iiGl2FcIfa3BMiv6OIw":         "album 5Z9iiGl2FcIfa3BMiv6OIw",
		"spotify:track:4cOdK2wGLETKBW3PvgPWqT":                          "track 4cOdK2wGLETKBW3PvgPWqT",
	} {
		k, id, err := Parse(link)
		if err != nil || string(k)+" "+id != want {
			t.Fatal(link, k, id, err)
		}
	}
	for _, bad := range []string{"http://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "https://evil.com/track/4cOdK2wGLETKBW3PvgPWqT",
		"https://open.spotify.com/playlist/37i9dQZF1DXcBWIGoYBM5M", "https://open.spotify.com/track/../../x"} {
		if _, _, err := Parse(bad); err == nil {
			t.Fatal("accepted", bad)
		}
	}
	if l, ok := Find("Luister dit! https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=x 🎵"); !ok || l != "https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT?si=x" {
		t.Fatal(l)
	}
	if _, ok := Find("https://spotify.link/AbCdEf"); !ok {
		t.Fatal("short link")
	}
}

func TestParsePage(t *testing.T) {
	track := `<meta property="og:title" content="Never Gonna Give You Up"/><meta property="og:description" content="Rick Astley, Guest &amp; Co · Whenever You Need Somebody · Song · 1987"/><meta name="music:release_date" content="1987-11-12"/>`
	info, err := parsePage(Track, track)
	if err != nil || info.Title != "Never Gonna Give You Up" || info.Artist != "Rick Astley" || len(info.Artists) != 2 || info.Album != "Whenever You Need Somebody" || info.Year != "1987" {
		t.Fatalf("%+v %v", info, err)
	}
	album := `<meta property="og:title" content="Listen to Born and Raised - Album by John Mayer | Spotify"/><meta property="og:description" content="John Mayer · album · 2012 · 12 songs"/>`
	info, err = parsePage(Album, album)
	if err != nil || info.Title != "Born and Raised" || info.Album != "Born and Raised" || info.Artist != "John Mayer" {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := parsePage(Track, "<html></html>"); err == nil {
		t.Fatal("empty page")
	}
}

// SPOTIFY_LIVE=1 go test -run Live (reads one public page)
func TestLiveLookup(t *testing.T) {
	if os.Getenv("SPOTIFY_LIVE") == "" {
		t.Skip()
	}
	for _, l := range []string{"https://open.spotify.com/track/4cOdK2wGLETKBW3PvgPWqT", "https://open.spotify.com/album/5Z9iiGl2FcIfa3BMiv6OIw"} {
		info, err := New("nomediaplayer/test").Lookup(context.Background(), l)
		t.Logf("%+v %v", info, err)
		if err != nil {
			t.Fatal(err)
		}
	}
}
