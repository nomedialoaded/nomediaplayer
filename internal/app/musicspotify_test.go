package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"

	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
	"github.com/nomedialoaded/nomediaplayer/internal/spotifylink"
)

// Read-only: Spotify page → MPD library + Lidarr lookup (nothing requested).
// SPOTIFY_LINK=… LIDARR_URL=… LIDARR_KEY=… go test -run LiveSpotify
func TestLiveSpotifyChain(t *testing.T) {
	link := os.Getenv("SPOTIFY_LINK")
	lc, _ := lidarr.New(os.Getenv("LIDARR_URL"), os.Getenv("LIDARR_KEY"))
	if link == "" || lc == nil {
		t.Skip()
	}
	ctx := context.Background()
	info, err := spotifylink.New("nomediaplayer/test").Lookup(ctx, link)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("info %+v", info)
	for _, s := range findLocal(ctx, newMusic("127.0.0.1", 6600).client, info) {
		t.Logf("local %s", s.File)
	}
	hits, err := lidarrHits(ctx, lc, info)
	for _, h := range hits {
		t.Logf("%s  [album id %d, artist id %d]", hitLabel(h), h.Album.ID, h.Artist.ID)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestSpotifyHitsPreferSongSingle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		artist := `{"artistName":"Rick Astley","foreignArtistId":"ra","id":286}`
		if strings.Contains(r.URL.Query().Get("term"), "Whenever") {
			io.WriteString(w, `[{"id":1,"foreignAlbumId":"alb","title":"Whenever You Need Somebody","albumType":"Album","releaseDate":"1987-11-12","artist":`+artist+`},
				{"id":2,"foreignAlbumId":"wsingle","title":"Whenever You Need Somebody","albumType":"Single","releaseDate":"1987-10-01","artist":`+artist+`},
				{"id":9,"foreignAlbumId":"other","title":"Something Else","albumType":"Album","artist":`+artist+`}]`)
			return
		}
		io.WriteString(w, `[{"id":3,"foreignAlbumId":"nsingle","title":"Never Gonna Give You Up","albumType":"Single","releaseDate":"1987-07-27","artist":`+artist+`},
			{"id":0,"foreignAlbumId":"parody","title":"Never Gonna Give You Up","albumType":"Single","artist":{"artistName":"Someone","foreignArtistId":"x"}},
			{"id":4,"foreignAlbumId":"best","title":"The Best of Rick Astley: Never Gonna Give You Up","albumType":"Album","secondaryTypes":["Compilation"],"artist":`+artist+`}]`)
	}))
	defer srv.Close()
	lc, _ := lidarr.New(srv.URL, "k")
	info := spotifylink.Info{Kind: spotifylink.Track, Title: "Never Gonna Give You Up", Artist: "Rick Astley", Artists: []string{"Rick Astley"},
		Album: "Whenever You Need Somebody", Year: "1987"}
	hits, err := lidarrHits(context.Background(), lc, info)
	var got []string
	for _, h := range hits {
		got = append(got, h.Album.ForeignID)
	}
	if err != nil || strings.Join(got, ",") != "nsingle,alb,wsingle,best" {
		t.Fatal(got, err)
	}
}

func TestSpotifyEnsureAlbumAddsArtistAndWaits(t *testing.T) {
	spotifyAlbumStep = time.Millisecond
	var albumCalls, added int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "POST /api/v1/artist":
			added++
			io.WriteString(w, `{"id":99}`)
		case "GET /api/v1/album":
			albumCalls++
			if albumCalls < 3 {
				io.WriteString(w, `[]`) // discography still loading
				return
			}
			io.WriteString(w, `[{"id":41,"foreignAlbumId":"other","title":"X"},{"id":42,"foreignAlbumId":"want","title":"Y"}]`)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	lc, _ := lidarr.New(srv.URL, "k")
	h := lidarr.AlbumHit{Album: lidarr.Album{ForeignID: "want"}, Artist: lidarr.Artist{Name: "New", ForeignID: "n1"}}
	id, _, err := ensureAlbum(context.Background(), lc, &lidarr.Defaults{QualityProfileID: 4, MetadataProfileID: 1, RootFolder: "/music"}, h, "")
	if err != nil || id != 42 || added != 1 || albumCalls != 3 {
		t.Fatal(id, err, added, albumCalls)
	}
}

func TestSpotifyPickerOptions(t *testing.T) {
	m := ndTestShell()
	m.music.lidarr = newLidarrState("http://127.0.0.1:1", "k")
	info := spotifylink.Info{Kind: spotifylink.Track, Title: "Gravity", Artist: "John Mayer", Artists: []string{"John Mayer"}, Album: "Continuum"}
	found := spotifyFound{info: info, local: []mpd.Song{{File: "jm/gravity.mp3", Title: "Gravity", Album: "Continuum"}},
		hits: []lidarr.AlbumHit{{Album: lidarr.Album{ID: 5, Title: "Continuum", Type: "Album", Year: "2006"}, Artist: lidarr.Artist{ID: 1, Name: "John Mayer"}}}}
	m, _, handled := m.updateSpotify(found)
	pk := m.music.spotify.pick
	if !handled || pk == nil || len(pk.options) != 3 || pk.options[0].play != "jm/gravity.mp3" || pk.options[2].hit == nil {
		t.Fatal(pk)
	}
	m, cmd := m.spotifyPickKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if cmd != nil || m.music.spotify.pick != nil {
		t.Fatal("esc")
	}
	// Nothing anywhere: a notice, no dialog.
	m, _, _ = m.updateSpotify(spotifyFound{info: info})
	if m.music.spotify.pick != nil || !strings.Contains(m.notice, "not in your library") {
		t.Fatal(m.notice)
	}
}

func TestBaseTitle(t *testing.T) {
	for in, want := range map[string]string{
		"Under Pressure - Remastered 2011":    "Under Pressure",
		"Let It Be - 2009 Remaster":           "Let It Be",
		"Gravity (feat. Someone)":             "Gravity",
		"Heroes - 2017 Remaster (feat. X)":    "Heroes",
		"Thriller (25th Anniversary Edition)": "Thriller",
		"Rumours (Super Deluxe)":              "Rumours",
		"Born and Raised - Single Version":    "Born and Raised",
		"Gravity - Live at the Nokia Theatre": "Gravity - Live at the Nokia Theatre",
		"Bad Guy (Acoustic)":                  "Bad Guy (Acoustic)",
		"Something - Remix":                   "Something - Remix",
	} {
		if got := baseTitle(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
	if !sameTitle("Under Pressure", "Under Pressure - Remastered 2011") || sameTitle("Gravity", "Gravity - Live") {
		t.Fatal("sameTitle")
	}
}
