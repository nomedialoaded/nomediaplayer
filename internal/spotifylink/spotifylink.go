// Package spotifylink reads a shared Spotify track or album link (nml fork)
// from the public page Spotify serves to link previews: title, artist,
// album and year. No account, no API key, nothing is played or downloaded
// from Spotify; the result is only used to find the music in the own
// library or to ask Lidarr for it.
package spotifylink

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

type Kind string

const (
	Track Kind = "track"
	Album Kind = "album"
)

// Info is what the link points at.
type Info struct {
	Kind    Kind
	ID      string
	Title   string // song title (track) or album title (album)
	Artist  string // first artist
	Artists []string
	Album   string
	Year    string
}

var (
	idRe    = regexp.MustCompile(`^[A-Za-z0-9]{10,40}$`)
	shortRe = regexp.MustCompile(`https://open\.spotify\.com/(?:intl-[a-z]{2}(?:-[a-zA-Z]{2})?/)?(track|album)/([A-Za-z0-9]{10,40})`)
	metaRe  = regexp.MustCompile(`<meta[^>]+(?:property|name)="([a-z:_]+)"[^>]+content="([^"]*)"`)
	albumRe = regexp.MustCompile(`^(?:Listen to )?(.+?) - (?:Album|EP|Single|Compilation) by (.+?)(?: \| Spotify)?$`)
)

var ErrNotSpotify = errors.New("not a Spotify link")

// Find returns the first Spotify link in text (pasted text may hold more).
func Find(text string) (string, bool) {
	for _, f := range strings.Fields(text) {
		f = strings.Trim(f, "<>()[]\"'")
		if _, _, err := Parse(f); err == nil {
			return f, true
		}
		if strings.HasPrefix(f, "https://spotify.link/") || strings.HasPrefix(f, "https://spotify.app.link/") {
			return f, true
		}
	}
	return "", false
}

// Parse recognises open.spotify.com/{track,album}/ID (also /intl-xx/) and
// spotify:track:ID.
func Parse(link string) (Kind, string, error) {
	link = strings.TrimSpace(link)
	if rest, ok := strings.CutPrefix(link, "spotify:"); ok {
		parts := strings.Split(rest, ":")
		if len(parts) == 2 && (parts[0] == "track" || parts[0] == "album") && idRe.MatchString(parts[1]) {
			return Kind(parts[0]), parts[1], nil
		}
		return "", "", ErrNotSpotify
	}
	u, err := url.Parse(link)
	if err != nil || u.Scheme != "https" || u.Host != "open.spotify.com" || u.User != nil {
		return "", "", ErrNotSpotify
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) > 0 && strings.HasPrefix(parts[0], "intl-") {
		parts = parts[1:]
	}
	if len(parts) != 2 || !idRe.MatchString(parts[1]) {
		return "", "", ErrNotSpotify
	}
	switch parts[0] {
	case "track", "album":
		return Kind(parts[0]), parts[1], nil
	case "artist", "playlist", "episode", "show":
		return "", "", fmt.Errorf("Spotify %s links are not supported, only tracks and albums", map[string]string{
			"artist": "artist", "playlist": "playlist", "episode": "episode", "show": "podcast"}[parts[0]])
	}
	return "", "", ErrNotSpotify
}

type Client struct{ HTTP *http.Client }

func New(userAgent string) *Client {
	c := &Client{}
	c.HTTP = &http.Client{
		Timeout:   15 * time.Second,
		Transport: uaTransport{ua: userAgent, rt: http.DefaultTransport},
		// Short links (spotify.link) redirect; only ever to Spotify hosts.
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > 5 || req.URL.Scheme != "https" || !spotifyHost(req.URL.Host) {
				return http.ErrUseLastResponse
			}
			return nil
		},
	}
	return c
}

type uaTransport struct {
	ua string
	rt http.RoundTripper
}

func (t uaTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("User-Agent", t.ua)
	r.Header.Set("Accept-Language", "en") // English page labels ("Song", "Album by")
	return t.rt.RoundTrip(r)
}

func spotifyHost(h string) bool {
	return h == "open.spotify.com" || h == "spotify.link" || h == "spotify.app.link"
}

// Lookup resolves a link (also a spotify.link short link) to its Info.
func (c *Client) Lookup(ctx context.Context, link string) (Info, error) {
	link = strings.TrimSpace(link)
	kind, id, err := Parse(link)
	if err != nil {
		u, perr := url.Parse(link)
		if perr != nil || u.Scheme != "https" || (u.Host != "spotify.link" && u.Host != "spotify.app.link") {
			return Info{}, err
		}
		// Short link: follow it and read the real address.
		page, final, ferr := c.get(ctx, link)
		if ferr != nil {
			return Info{}, ferr
		}
		if kind, id, err = Parse(final); err != nil {
			m := shortRe.FindStringSubmatch(page)
			if m == nil {
				return Info{}, errors.New("short Spotify link does not point to a track or album")
			}
			kind, id = Kind(m[1]), m[2]
		}
	}
	page, _, err := c.get(ctx, "https://open.spotify.com/"+string(kind)+"/"+id)
	if err != nil {
		return Info{}, err
	}
	info, err := parsePage(kind, page)
	info.Kind, info.ID = kind, id
	return info, err
}

func (c *Client) get(ctx context.Context, link string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return "", "", err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", "", errors.New("Spotify unreachable")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return "", "", errors.New("Spotify does not know this link")
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", "", fmt.Errorf("Spotify responded %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return string(data), resp.Request.URL.String(), err
}

func meta(page string) map[string]string {
	out := map[string]string{}
	for _, m := range metaRe.FindAllStringSubmatch(page, -1) {
		if _, seen := out[m[1]]; !seen {
			out[m[1]] = strings.TrimSpace(html.UnescapeString(m[2]))
		}
	}
	return out
}

func splitArtists(s string) []string {
	var out []string
	for _, a := range strings.Split(s, ", ") {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// parsePage reads the preview tags:
//
//	track: og:title "Song", og:description "Artist · Album · Song · 1987"
//	album: og:title "Listen to Album - Album by Artist | Spotify",
//	       og:description "Artist · album · 1987 · 10 songs"
func parsePage(kind Kind, page string) (Info, error) {
	m := meta(page)
	var info Info
	desc := strings.Split(m["og:description"], " · ")
	if date := m["music:release_date"]; len(date) >= 4 {
		info.Year = date[:4]
	}
	switch kind {
	case Track:
		if len(desc) < 4 || m["og:title"] == "" {
			return info, errors.New("Spotify page without track data")
		}
		info.Title = m["og:title"]
		info.Artists = splitArtists(desc[0])
		info.Album = strings.Join(desc[1:len(desc)-2], " · ")
		if info.Year == "" {
			info.Year = desc[len(desc)-1]
		}
	case Album:
		title := m["og:title"]
		if mm := albumRe.FindStringSubmatch(title); mm != nil {
			title = mm[1]
		}
		if title == "" || len(desc) < 2 {
			return info, errors.New("Spotify page without album data")
		}
		info.Title, info.Album = title, title
		info.Artists = splitArtists(desc[0])
	}
	if len(info.Artists) == 0 {
		return info, errors.New("no artist on the Spotify page")
	}
	info.Artist = info.Artists[0]
	return info, nil
}
