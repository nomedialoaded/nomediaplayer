// Package lidarr talks to Lidarr's API v1 (nml fork): find artists, add them
// without downloading, request single albums and follow the download queue.
// The API key only travels in the X-Api-Key header and never appears in
// errors.
package lidarr

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	base *url.URL
	key  string
	HTTP *http.Client
}

// New checks the address; an empty URL or key means Lidarr is off (nil).
func New(address, key string) (*Client, error) {
	if strings.TrimSpace(address) == "" || strings.TrimSpace(key) == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(address), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("lidarr_url must be http(s)://host:port")
	}
	return &Client{base: u, key: strings.TrimSpace(key), HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

// Artist is a search result or an artist in Lidarr (ID > 0).
type Artist struct {
	ID             int
	Name           string
	ForeignID      string
	Disambiguation string
	Type           string
	Folder         string          // folder Lidarr creates under the root (its naming), e.g. "G‐Unit"
	Path           string          // nml: artist folder to use instead (full Lidarr path); "" = Lidarr's own
	raw            json.RawMessage // lookup resource, posted back when adding
}

func (a Artist) InLidarr() bool { return a.ID > 0 }

type Album struct {
	ID          int
	ForeignID   string // MusicBrainz release group
	Title       string
	Type        string // Album, EP, Single, …
	Secondary   []string
	Year        string
	Monitored   bool
	TrackFiles  int
	TotalTracks int
	Cover       string // remote cover URL (Lidarr's image proxy); local copy via AlbumCover
}

// Have: every track is on disk.
func (a Album) Have() bool { return a.TotalTracks > 0 && a.TrackFiles >= a.TotalTracks }

// QueueItem is one download in Lidarr's queue.
type QueueItem struct {
	ID       int
	Album    string
	Artist   string
	AlbumID  int
	Status   string // queued, downloading, completed, failed, …
	State    string // trackedDownloadState: downloading, importPending, importFailed, imported, …
	Size     float64
	SizeLeft float64
	TimeLeft string
	Problem  string
}

// Progress in 0..1.
func (q QueueItem) Progress() float64 {
	if q.Size <= 0 {
		return 0
	}
	return max(0, min(1, (q.Size-q.SizeLeft)/q.Size))
}

// Defaults are the profile and folder new artists get: what most artists in
// this Lidarr already use.
type Defaults struct {
	QualityProfileID  int
	MetadataProfileID int
	RootFolder        string
}

type wireArtist struct {
	ID                int    `json:"id"`
	ArtistName        string `json:"artistName"`
	ForeignArtistID   string `json:"foreignArtistId"`
	Disambiguation    string `json:"disambiguation"`
	ArtistType        string `json:"artistType"`
	QualityProfileID  int    `json:"qualityProfileId"`
	MetadataProfileID int    `json:"metadataProfileId"`
	RootFolderPath    string `json:"rootFolderPath"`
	Folder            string `json:"folder"`
}

func (w wireArtist) artist(raw json.RawMessage) Artist {
	return Artist{ID: w.ID, Name: w.ArtistName, ForeignID: w.ForeignArtistID, Disambiguation: w.Disambiguation, Type: w.ArtistType, Folder: w.Folder, raw: raw}
}

// Search finds artists by name, also the artists of albums that match.
// SearchHit is one search result: an artist or an album (exactly one set).
type SearchHit struct {
	Artist *Artist
	Album  *AlbumHit
}

// MaxAlbumHits caps the albums a search returns (Lidarr sends ~20 results).
const MaxAlbumHits = 25

// Search finds artists and albums (MusicBrainz via Lidarr), in Lidarr's
// relevance order. Each artist appears once.
func (c *Client) Search(ctx context.Context, term string) ([]SearchHit, error) {
	var results []struct {
		Artist json.RawMessage `json:"artist"`
		Album  *wireAlbum      `json:"album"`
	}
	if err := c.do(ctx, http.MethodGet, "search?"+url.Values{"term": {term}}.Encode(), nil, &results); err != nil {
		return nil, err
	}
	var out []SearchHit
	seenArtist, seenAlbum := map[string]bool{}, map[string]bool{}
	albums := 0
	for _, r := range results {
		if r.Album != nil {
			h, ok := r.Album.hit()
			if !ok || seenAlbum[h.Album.ForeignID] || albums >= MaxAlbumHits {
				continue
			}
			seenAlbum[h.Album.ForeignID] = true
			albums++
			out = append(out, SearchHit{Album: &h})
			continue
		}
		var w wireArtist
		if len(r.Artist) == 0 || json.Unmarshal(r.Artist, &w) != nil || w.ForeignArtistID == "" || seenArtist[w.ForeignArtistID] {
			continue
		}
		seenArtist[w.ForeignArtistID] = true
		a := w.artist(r.Artist)
		out = append(out, SearchHit{Artist: &a})
	}
	return out, nil
}

// Albums of an artist that is in Lidarr, studio albums first.
func (c *Client) Albums(ctx context.Context, artistID int) ([]Album, error) {
	var wire []wireAlbum
	if err := c.do(ctx, http.MethodGet, "album?artistId="+strconv.Itoa(artistID), nil, &wire); err != nil {
		return nil, err
	}
	out := make([]Album, 0, len(wire))
	for _, w := range wire {
		out = append(out, w.album())
	}
	SortAlbums(out)
	return out, nil
}

// Track is one song on an album in Lidarr (the release Lidarr has chosen).
type Track struct {
	Number   string // as on the release, e.g. "3" or "A1"
	Medium   int
	Title    string
	Duration time.Duration
	HasFile  bool
}

// Tracks of an album that is in Lidarr, in release order.
func (c *Client) Tracks(ctx context.Context, albumID int) ([]Track, error) {
	var wire []struct {
		TrackNumber   string `json:"trackNumber"`
		AbsoluteTrack int    `json:"absoluteTrackNumber"`
		Medium        int    `json:"mediumNumber"`
		Title         string `json:"title"`
		Duration      int64  `json:"duration"` // ms
		HasFile       bool   `json:"hasFile"`
	}
	if err := c.do(ctx, http.MethodGet, "track?albumId="+strconv.Itoa(albumID), nil, &wire); err != nil {
		return nil, err
	}
	sort.SliceStable(wire, func(i, j int) bool {
		if wire[i].Medium != wire[j].Medium {
			return wire[i].Medium < wire[j].Medium
		}
		return wire[i].AbsoluteTrack < wire[j].AbsoluteTrack
	})
	out := make([]Track, 0, len(wire))
	for _, w := range wire {
		out = append(out, Track{Number: w.TrackNumber, Medium: w.Medium, Title: w.Title,
			Duration: time.Duration(w.Duration) * time.Millisecond, HasFile: w.HasFile})
	}
	return out, nil
}

// ErrNoCover: neither Lidarr nor the remote source has a cover.
var ErrNoCover = errors.New("no album art")

// Cover size guards: Lidarr keeps covers at up to 1200 px (~150–450 kB), but
// a local copy above CoverLocalMax bytes or CoverMaxSide pixels is skipped
// for the remote 250 px thumbnail (~20 kB), so one huge file can never slow
// the UI down.
const (
	CoverLocalMax = 1 << 20
	CoverMaxSide  = 1600
)

// AlbumCover returns the album's cover image bytes (at most limit bytes).
// Lidarr's own copy on the LAN comes first (fast); when it is missing or
// too large, the remote URL is asked for its 250 px thumbnail.
func (c *Client) AlbumCover(ctx context.Context, albumID int, remote string, limit int64) ([]byte, error) {
	data, err := []byte(nil), ErrNoCover
	if albumID > 0 { // only albums in Lidarr have a local copy
		local := c.base.String() + "/MediaCover/Albums/" + strconv.Itoa(albumID) + "/cover.jpg"
		data, err = c.fetchImage(ctx, local, true, min(limit, CoverLocalMax))
	}
	if err == nil && !tooLarge(data) {
		return data, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if remote != "" {
		if small := strings.Replace(remote, "-1200.", "-250.", 1); small != remote {
			if thumb, terr := c.fetchImage(ctx, small, false, limit); terr == nil {
				return thumb, nil
			}
		}
	}
	if err == nil {
		return data, nil // large but within limits: better than nothing
	}
	return nil, ErrNoCover
}

// tooLarge reads only the image header.
func tooLarge(data []byte) bool {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	return err != nil || cfg.Width > CoverMaxSide || cfg.Height > CoverMaxSide
}

func (c *Client) fetchImage(ctx context.Context, address string, auth bool, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	if auth {
		req.Header.Set("X-Api-Key", c.key)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") || resp.ContentLength > limit {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, ErrNoCover
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrNoCover
	}
	return data, nil
}

// AlbumHit is an album from Lidarr's lookup with its artist. Album.ID is 0
// when the album is not in Lidarr yet (Artist.ID 0: artist not either).
type AlbumHit struct {
	Album  Album
	Artist Artist
}

// LookupAlbums searches albums (MusicBrainz via Lidarr), e.g. "artist album".
// wireAlbum is an album resource (album list, lookup and search).
type wireAlbum struct {
	ID             int             `json:"id"`
	ForeignID      string          `json:"foreignAlbumId"`
	Title          string          `json:"title"`
	AlbumType      string          `json:"albumType"`
	SecondaryTypes []string        `json:"secondaryTypes"`
	ReleaseDate    string          `json:"releaseDate"`
	Monitored      bool            `json:"monitored"`
	Artist         json.RawMessage `json:"artist"`
	Statistics     struct {
		TrackFileCount  int `json:"trackFileCount"`
		TotalTrackCount int `json:"totalTrackCount"`
	} `json:"statistics"`
	Images []struct {
		CoverType string `json:"coverType"`
		RemoteURL string `json:"remoteUrl"`
	} `json:"images"`
}

func (w wireAlbum) album() Album {
	year := ""
	if len(w.ReleaseDate) >= 4 {
		year = w.ReleaseDate[:4]
	}
	cover := ""
	for _, im := range w.Images {
		if im.CoverType == "cover" {
			cover = im.RemoteURL
			break
		}
	}
	return Album{ID: w.ID, ForeignID: w.ForeignID, Title: w.Title, Type: w.AlbumType, Secondary: w.SecondaryTypes, Year: year,
		Monitored: w.Monitored, TrackFiles: w.Statistics.TrackFileCount, TotalTracks: w.Statistics.TotalTrackCount, Cover: cover}
}

// hit pairs the album with its artist; false without a usable artist.
func (w wireAlbum) hit() (AlbumHit, bool) {
	var wa wireArtist
	if len(w.Artist) == 0 || json.Unmarshal(w.Artist, &wa) != nil || wa.ForeignArtistID == "" || w.ForeignID == "" {
		return AlbumHit{}, false
	}
	return AlbumHit{Album: w.album(), Artist: wa.artist(w.Artist)}, true
}

func (c *Client) LookupAlbums(ctx context.Context, term string) ([]AlbumHit, error) {
	var wire []wireAlbum
	if err := c.do(ctx, http.MethodGet, "album/lookup?"+url.Values{"term": {term}}.Encode(), nil, &wire); err != nil {
		return nil, err
	}
	var out []AlbumHit
	for _, w := range wire {
		if h, ok := w.hit(); ok {
			out = append(out, h)
		}
	}
	return out, nil
}

// SortAlbums: albums, EPs, singles, then live/compilations; newest first.
func SortAlbums(albums []Album) {
	rank := func(a Album) int {
		r := map[string]int{"Album": 0, "EP": 1, "Single": 2}[a.Type]
		if a.Type != "Album" && a.Type != "EP" && a.Type != "Single" {
			r = 3
		}
		if len(a.Secondary) > 0 {
			r += 4
		}
		return r
	}
	sort.SliceStable(albums, func(i, j int) bool {
		if ri, rj := rank(albums[i]), rank(albums[j]); ri != rj {
			return ri < rj
		}
		return albums[i].Year > albums[j].Year
	})
}

// Defaults reads the profiles and root folder most artists use.
func (c *Client) Defaults(ctx context.Context) (Defaults, error) {
	var artists []wireArtist
	if err := c.do(ctx, http.MethodGet, "artist", nil, &artists); err != nil {
		return Defaults{}, err
	}
	var roots []struct {
		Path string `json:"path"`
	}
	if err := c.do(ctx, http.MethodGet, "rootfolder", nil, &roots); err != nil {
		return Defaults{}, err
	}
	var profiles []struct {
		ID int `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "qualityprofile", nil, &profiles); err != nil {
		return Defaults{}, err
	}
	var meta []struct {
		ID int `json:"id"`
	}
	if err := c.do(ctx, http.MethodGet, "metadataprofile", nil, &meta); err != nil {
		return Defaults{}, err
	}
	if len(roots) == 0 || len(profiles) == 0 || len(meta) == 0 {
		return Defaults{}, errors.New("Lidarr has no root folder or profile")
	}
	d := Defaults{QualityProfileID: profiles[0].ID, MetadataProfileID: meta[0].ID, RootFolder: roots[0].Path}
	q, mcount, rcount := map[int]int{}, map[int]int{}, map[string]int{}
	for _, a := range artists {
		q[a.QualityProfileID]++
		mcount[a.MetadataProfileID]++
		rcount[a.RootFolderPath]++
	}
	d.QualityProfileID = mostUsed(q, d.QualityProfileID, func(id int) bool { return containsID(profiles, id) })
	d.MetadataProfileID = mostUsed(mcount, d.MetadataProfileID, func(id int) bool { return containsID(meta, id) })
	best := 0
	for _, r := range roots {
		if rcount[r.Path] > best {
			best, d.RootFolder = rcount[r.Path], r.Path
		}
	}
	return d, nil
}

func containsID(list []struct {
	ID int `json:"id"`
}, id int) bool {
	for _, x := range list {
		if x.ID == id {
			return true
		}
	}
	return false
}

func mostUsed(counts map[int]int, fallback int, valid func(int) bool) int {
	best, n := fallback, 0
	for id, c := range counts {
		if valid(id) && (c > n || (c == n && id < best)) {
			best, n = id, c
		}
	}
	return best
}

// AddArtist adds an artist without monitoring or searching any album (its
// discography is fetched by Lidarr in the background). Returns the new ID.
func (c *Client) AddArtist(ctx context.Context, a Artist, d Defaults) (int, error) {
	body := map[string]any{}
	if len(a.raw) > 0 {
		if err := json.Unmarshal(a.raw, &body); err != nil {
			return 0, err
		}
	}
	body["artistName"], body["foreignArtistId"] = a.Name, a.ForeignID
	body["qualityProfileId"], body["metadataProfileId"], body["rootFolderPath"] = d.QualityProfileID, d.MetadataProfileID, d.RootFolder
	if a.Path != "" {
		body["path"] = a.Path
	}
	body["monitored"], body["monitorNewItems"] = true, "none"
	body["addOptions"] = map[string]any{"monitor": "none", "searchForMissingAlbums": false}
	delete(body, "id")
	var created wireArtist
	if err := c.do(ctx, http.MethodPost, "artist", body, &created); err != nil {
		return 0, err
	}
	if created.ID <= 0 {
		return 0, errors.New("Lidarr returned no artist ID")
	}
	return created.ID, nil
}

// WaitArtistRefresh waits until Lidarr has no queued or running refresh/scan
// for the artist, so its track files are known (after AddArtist).
func (c *Client) WaitArtistRefresh(ctx context.Context, artistID int, step time.Duration) error {
	for {
		var cmds []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Body   struct {
				ArtistID  int   `json:"artistId"`
				ArtistIDs []int `json:"artistIds"`
			} `json:"body"`
		}
		if err := c.do(ctx, http.MethodGet, "command", nil, &cmds); err != nil {
			return err
		}
		busy := false
		for _, cmd := range cmds {
			if cmd.Status != "queued" && cmd.Status != "started" {
				continue
			}
			if cmd.Name != "RefreshArtist" && cmd.Name != "RescanArtist" && cmd.Name != "BulkRefreshArtist" {
				continue
			}
			if cmd.Body.ArtistID == artistID || slicesContains(cmd.Body.ArtistIDs, artistID) {
				busy = true
			}
		}
		if !busy {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(step):
		}
	}
}

func slicesContains(list []int, x int) bool {
	for _, v := range list {
		if v == x {
			return true
		}
	}
	return false
}

// RequestAlbum monitors the album and starts a search for it.
func (c *Client) RequestAlbum(ctx context.Context, albumID int) error {
	if err := c.do(ctx, http.MethodPut, "album/monitor", map[string]any{"albumIds": []int{albumID}, "monitored": true}, nil); err != nil {
		return err
	}
	return c.do(ctx, http.MethodPost, "command", map[string]any{"name": "AlbumSearch", "albumIds": []int{albumID}}, nil)
}

// Queue lists the current downloads.
func (c *Client) Queue(ctx context.Context) ([]QueueItem, error) {
	var page struct {
		Records []struct {
			ID                    int     `json:"id"`
			AlbumID               int     `json:"albumId"`
			Title                 string  `json:"title"`
			Status                string  `json:"status"`
			TrackedDownloadState  string  `json:"trackedDownloadState"`
			TrackedDownloadStatus string  `json:"trackedDownloadStatus"`
			Size                  float64 `json:"size"`
			SizeLeft              float64 `json:"sizeleft"`
			TimeLeft              string  `json:"timeleft"`
			ErrorMessage          string  `json:"errorMessage"`
			StatusMessages        []struct {
				Messages []string `json:"messages"`
			} `json:"statusMessages"`
			Artist *struct {
				ArtistName string `json:"artistName"`
			} `json:"artist"`
			Album *struct {
				Title string `json:"title"`
			} `json:"album"`
		} `json:"records"`
	}
	if err := c.do(ctx, http.MethodGet, "queue?pageSize=200&includeArtist=true&includeAlbum=true", nil, &page); err != nil {
		return nil, err
	}
	out := make([]QueueItem, 0, len(page.Records))
	for _, r := range page.Records {
		q := QueueItem{ID: r.ID, AlbumID: r.AlbumID, Album: r.Title, Status: r.Status, State: r.TrackedDownloadState,
			Size: r.Size, SizeLeft: r.SizeLeft, TimeLeft: r.TimeLeft, Problem: r.ErrorMessage}
		if r.Album != nil && r.Album.Title != "" {
			q.Album = r.Album.Title
		}
		if r.Artist != nil {
			q.Artist = r.Artist.ArtistName
		}
		if q.Problem == "" && r.TrackedDownloadStatus == "warning" {
			for _, sm := range r.StatusMessages {
				if len(sm.Messages) > 0 {
					q.Problem = sm.Messages[0]
					break
				}
			}
		}
		out = append(out, q)
	}
	return out, nil
}

// Imported is one track file Lidarr imported (history).
type Imported struct {
	AlbumID int
	Path    string // Lidarr's path, e.g. /music/Artist/Album/01 - x.flac
	Quality string
	At      time.Time
}

// ImportedSince lists track files imported after t.
func (c *Client) ImportedSince(ctx context.Context, t time.Time) ([]Imported, error) {
	var records []struct {
		AlbumID int       `json:"albumId"`
		Date    time.Time `json:"date"`
		Quality struct {
			Quality struct {
				Name string `json:"name"`
			} `json:"quality"`
		} `json:"quality"`
		Data struct {
			ImportedPath string `json:"importedPath"`
		} `json:"data"`
	}
	q := url.Values{"date": {t.UTC().Format(time.RFC3339)}, "eventType": {"trackFileImported"}}
	if err := c.do(ctx, http.MethodGet, "history/since?"+q.Encode(), nil, &records); err != nil {
		return nil, err
	}
	out := make([]Imported, 0, len(records))
	for _, r := range records {
		if r.Data.ImportedPath != "" {
			out = append(out, Imported{AlbumID: r.AlbumID, Path: r.Data.ImportedPath, Quality: r.Quality.Quality.Name, At: r.Date})
		}
	}
	return out, nil
}

// RootFolders are the library folders as Lidarr sees them.
func (c *Client) RootFolders(ctx context.Context) ([]string, error) {
	var roots []struct {
		Path string `json:"path"`
	}
	if err := c.do(ctx, http.MethodGet, "rootfolder", nil, &roots); err != nil {
		return nil, err
	}
	out := make([]string, 0, len(roots))
	for _, r := range roots {
		out = append(out, r.Path)
	}
	return out, nil
}

// RescanFolder makes Lidarr read a folder again (after files changed).
func (c *Client) RescanFolder(ctx context.Context, folder string) error {
	return c.do(ctx, http.MethodPost, "command", map[string]any{"name": "RescanFolders", "folders": []string{folder}}, nil)
}

// HTTPError is a non-2xx answer; Message is Lidarr's own (short) reason.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	switch {
	case e.Status == http.StatusUnauthorized:
		return "Lidarr rejects the API key (401)"
	case e.Message != "":
		return fmt.Sprintf("Lidarr %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Lidarr responded %d", e.Status)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		data, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+"/api/v1/"+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("X-Api-Key", c.key)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			return fmt.Errorf("Lidarr unreachable (%s)", c.base.Host)
		}
		return err
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 16<<20)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return &HTTPError{Status: resp.StatusCode, Message: errorMessage(limited)}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		return errors.New("got a web page instead of Lidarr's API; use just the server address, e.g. http://host:8686")
	}
	return json.NewDecoder(limited).Decode(out)
}

// errorMessage pulls the reason out of Lidarr's validation errors.
func errorMessage(r io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(r, 64<<10))
	var list []struct {
		ErrorMessage string `json:"errorMessage"`
	}
	var one struct {
		Message string `json:"message"`
	}
	msg := ""
	if json.Unmarshal(data, &list) == nil && len(list) > 0 {
		msg = list[0].ErrorMessage
	} else if json.Unmarshal(data, &one) == nil {
		msg = one.Message
	}
	msg = strings.Join(strings.Fields(msg), " ")
	if len([]rune(msg)) > 120 {
		msg = string([]rune(msg)[:119]) + "…"
	}
	return msg
}

// Ping checks the URL and API key (setup wizard).
func (c *Client) Ping(ctx context.Context) error {
	var status struct {
		Version string `json:"version"`
	}
	return c.do(ctx, http.MethodGet, "system/status", nil, &status)
}
