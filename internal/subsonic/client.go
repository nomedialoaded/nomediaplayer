// Package subsonic is a small Subsonic/OpenSubsonic client for Navidrome
// (nml fork). It covers playlists and favorites (starred) for the Music view;
// playback stays in MPD.
package subsonic

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const clientName = "nomediaplayer"

// Song is one Subsonic child entry.
type Song struct {
	ID, Title, Artist, Album, AlbumID, Path string
	Track, Disc, Year                       int
	Duration                                float64
	Starred                                 bool
}

type Playlist struct {
	ID, Name  string
	SongCount int
}

type Album struct {
	ID, Name, Artist string
	SongCount        int
}

type Client struct {
	base, user, pass string
	http             *http.Client
}

func New(base, user, pass string) *Client {
	return &Client{base: strings.TrimRight(base, "/"), user: user, pass: pass, http: &http.Client{Timeout: 15 * time.Second}}
}

func (c *Client) Base() string { return c.base }

// Error is a Subsonic-level failure (wrong credentials, missing id…).
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return fmt.Sprintf("navidrome: %s (%d)", e.Message, e.Code) }

type rawSong struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Artist   string `json:"artist"`
	Album    string `json:"album"`
	AlbumID  string `json:"albumId"`
	Path     string `json:"path"`
	Track    int    `json:"track"`
	Disc     int    `json:"discNumber"`
	Year     int    `json:"year"`
	Duration int    `json:"duration"`
	Starred  string `json:"starred"`
}

func (r rawSong) song() Song {
	return Song{ID: r.ID, Title: r.Title, Artist: r.Artist, Album: r.Album, AlbumID: r.AlbumID, Path: r.Path,
		Track: r.Track, Disc: r.Disc, Year: r.Year, Duration: float64(r.Duration), Starred: r.Starred != ""}
}

func songs(raw []rawSong) []Song {
	out := make([]Song, 0, len(raw))
	for _, r := range raw {
		out = append(out, r.song())
	}
	return out
}

type response struct {
	Status string `json:"status"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	Playlists *struct {
		Playlist []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			SongCount int    `json:"songCount"`
		} `json:"playlist"`
	} `json:"playlists"`
	Song     *rawSong `json:"song"`
	Playlist *struct {
		ID    string    `json:"id"`
		Entry []rawSong `json:"entry"`
	} `json:"playlist"`
	Starred2 *struct {
		Album []struct {
			ID        string `json:"id"`
			Name      string `json:"name"`
			Artist    string `json:"artist"`
			SongCount int    `json:"songCount"`
		} `json:"album"`
		Song []rawSong `json:"song"`
	} `json:"starred2"`
	Album *struct {
		Song []rawSong `json:"song"`
	} `json:"album"`
	SearchResult3 *struct {
		Song []rawSong `json:"song"`
	} `json:"searchResult3"`
}

func (c *Client) call(ctx context.Context, endpoint string, params url.Values) (*response, error) {
	salt := make([]byte, 6)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	s := hex.EncodeToString(salt)
	sum := md5.Sum([]byte(c.pass + s))
	q := url.Values{"u": {c.user}, "t": {hex.EncodeToString(sum[:])}, "s": {s}, "v": {"1.16.1"}, "c": {clientName}, "f": {"json"}}
	for k, v := range params {
		q[k] = v
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/rest/"+endpoint+"?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		// Do not leak the token-bearing URL into messages.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			return nil, fmt.Errorf("navidrome %s: %w", endpoint, uerr.Err)
		}
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("navidrome %s: HTTP %d", endpoint, resp.StatusCode)
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		return nil, fmt.Errorf("navidrome %s: got a web page instead of the Subsonic API; use just the server address, e.g. http://host:4533", endpoint)
	}
	var body struct {
		R response `json:"subsonic-response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, fmt.Errorf("navidrome %s: %w", endpoint, err)
	}
	if body.R.Status != "ok" {
		if body.R.Error != nil {
			return nil, &Error{Code: body.R.Error.Code, Message: body.R.Error.Message}
		}
		return nil, fmt.Errorf("navidrome %s: status %q", endpoint, body.R.Status)
	}
	return &body.R, nil
}

func (c *Client) Ping(ctx context.Context) error {
	_, err := c.call(ctx, "ping", nil)
	return err
}

func (c *Client) Playlists(ctx context.Context) ([]Playlist, error) {
	r, err := c.call(ctx, "getPlaylists", nil)
	if err != nil {
		return nil, err
	}
	var out []Playlist
	if r.Playlists != nil {
		for _, p := range r.Playlists.Playlist {
			out = append(out, Playlist{ID: p.ID, Name: p.Name, SongCount: p.SongCount})
		}
	}
	return out, nil
}

func (c *Client) Playlist(ctx context.Context, id string) ([]Song, error) {
	r, err := c.call(ctx, "getPlaylist", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Playlist == nil {
		return nil, nil
	}
	return songs(r.Playlist.Entry), nil
}

// Starred returns favorite albums and songs.
func (c *Client) Starred(ctx context.Context) ([]Album, []Song, error) {
	r, err := c.call(ctx, "getStarred2", nil)
	if err != nil {
		return nil, nil, err
	}
	if r.Starred2 == nil {
		return nil, nil, nil
	}
	var albums []Album
	for _, a := range r.Starred2.Album {
		albums = append(albums, Album{ID: a.ID, Name: a.Name, Artist: a.Artist, SongCount: a.SongCount})
	}
	return albums, songs(r.Starred2.Song), nil
}

func (c *Client) Album(ctx context.Context, id string) ([]Song, error) {
	r, err := c.call(ctx, "getAlbum", url.Values{"id": {id}})
	if err != nil {
		return nil, err
	}
	if r.Album == nil {
		return nil, nil
	}
	return songs(r.Album.Song), nil
}

// Song fetches one song by its Navidrome ID.
func (c *Client) Song(ctx context.Context, id string) (Song, bool, error) {
	r, err := c.call(ctx, "getSong", url.Values{"id": {id}})
	if err != nil {
		return Song{}, false, err
	}
	if r.Song == nil {
		return Song{}, false, nil
	}
	return r.Song.song(), true, nil
}

func (c *Client) SearchSongs(ctx context.Context, query string, count int) ([]Song, error) {
	r, err := c.call(ctx, "search3", url.Values{"query": {query}, "songCount": {strconv.Itoa(count)}, "albumCount": {"0"}, "artistCount": {"0"}})
	if err != nil {
		return nil, err
	}
	if r.SearchResult3 == nil {
		return nil, nil
	}
	return songs(r.SearchResult3.Song), nil
}

// CreatePlaylist makes a new playlist with the given songs (may be empty).
func (c *Client) CreatePlaylist(ctx context.Context, name string, songIDs []string) error {
	_, err := c.call(ctx, "createPlaylist", url.Values{"name": {name}, "songId": songIDs})
	return err
}

func (c *Client) AddToPlaylist(ctx context.Context, id string, songIDs []string) error {
	_, err := c.call(ctx, "updatePlaylist", url.Values{"playlistId": {id}, "songIdToAdd": songIDs})
	return err
}

// RemoveFromPlaylist removes entries by their 0-based index in the playlist.
func (c *Client) RemoveFromPlaylist(ctx context.Context, id string, index int) error {
	_, err := c.call(ctx, "updatePlaylist", url.Values{"playlistId": {id}, "songIndexToRemove": {strconv.Itoa(index)}})
	return err
}

func (c *Client) StarSong(ctx context.Context, id string, star bool) error {
	return c.star(ctx, star, url.Values{"id": {id}})
}

func (c *Client) StarAlbum(ctx context.Context, id string, star bool) error {
	return c.star(ctx, star, url.Values{"albumId": {id}})
}

func (c *Client) star(ctx context.Context, star bool, params url.Values) error {
	endpoint := "unstar"
	if star {
		endpoint = "star"
	}
	_, err := c.call(ctx, endpoint, params)
	return err
}

func (c *Client) DeletePlaylist(ctx context.Context, id string) error {
	_, err := c.call(ctx, "deletePlaylist", url.Values{"id": {id}})
	return err
}
