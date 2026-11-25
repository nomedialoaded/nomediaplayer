// Package homeassistant talks to Home Assistant's REST API for the Music
// Assistant speakers (nml fork): list them, control them, find a song in
// Music Assistant and hand playback over. The token only travels in the
// Authorization header and never appears in errors.
package homeassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

type Client struct {
	base  *url.URL
	token string
	HTTP  *http.Client
	entry string // Music Assistant config entry (for search)
}

// New checks the address; empty URL or token means off (nil).
func New(address, token string) (*Client, error) {
	if strings.TrimSpace(address) == "" || strings.TrimSpace(token) == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(address), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("ha_url must be http(s)://host:port")
	}
	return &Client{base: u, token: strings.TrimSpace(token), HTTP: &http.Client{Timeout: 15 * time.Second}}, nil
}

// Speaker is a Music Assistant player as Home Assistant shows it.
type Speaker struct {
	EntityID string
	Name     string
	State    string // playing, paused, idle, off, unavailable
	Title    string
	Artist   string
	Album    string
	MediaID  string // e.g. library://track/145
	Queue    string // Music Assistant queue ID (active_queue)
	Volume   float64
	Muted    bool
	Position float64
	Duration float64
	At       time.Time // when Position was measured
	Shuffle  bool
	Repeat   string
}

// Playing position now (HA only updates it on changes).
func (s Speaker) Elapsed(now time.Time) float64 {
	p := s.Position
	if s.State == "playing" && !s.At.IsZero() {
		p += now.Sub(s.At).Seconds()
	}
	if s.Duration > 0 {
		p = min(p, s.Duration)
	}
	return p
}

func (s Speaker) Available() bool { return s.State != "unavailable" && s.State != "" }

type wireState struct {
	EntityID   string `json:"entity_id"`
	State      string `json:"state"`
	Attributes struct {
		FriendlyName  string   `json:"friendly_name"`
		MassType      string   `json:"mass_player_type"`
		Title         string   `json:"media_title"`
		Artist        string   `json:"media_artist"`
		Album         string   `json:"media_album_name"`
		ContentID     string   `json:"media_content_id"`
		ActiveQueue   string   `json:"active_queue"`
		Volume        *float64 `json:"volume_level"`
		Muted         bool     `json:"is_volume_muted"`
		Position      float64  `json:"media_position"`
		Duration      float64  `json:"media_duration"`
		PositionAt    string   `json:"media_position_updated_at"`
		Shuffle       bool     `json:"shuffle"`
		Repeat        string   `json:"repeat"`
		GroupMembers  []string `json:"group_members"`
		SourceListRaw any      `json:"source_list"`
	} `json:"attributes"`
}

// Speakers lists the Music Assistant players, sorted by name.
func (c *Client) Speakers(ctx context.Context) ([]Speaker, error) {
	var states []wireState
	if err := c.do(ctx, http.MethodGet, "/api/states", nil, &states); err != nil {
		return nil, err
	}
	var out []Speaker
	for _, st := range states {
		if !strings.HasPrefix(st.EntityID, "media_player.") || st.Attributes.MassType == "" {
			continue
		}
		a := st.Attributes
		s := Speaker{EntityID: st.EntityID, Name: a.FriendlyName, State: st.State, Title: a.Title, Artist: a.Artist,
			Album: a.Album, MediaID: a.ContentID, Queue: a.ActiveQueue, Muted: a.Muted, Position: a.Position, Duration: a.Duration, Shuffle: a.Shuffle, Repeat: a.Repeat}
		if a.Volume != nil {
			s.Volume = *a.Volume
		}
		if t, err := time.Parse(time.RFC3339Nano, a.PositionAt); err == nil {
			s.At = t
		}
		if s.Name == "" {
			s.Name = st.EntityID
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// Media sends a media_player service (media_play_pause, media_next_track,
// media_previous_track, media_stop, volume_set, media_seek, …).
func (c *Client) Media(ctx context.Context, service, entity string, data map[string]any) error {
	body := map[string]any{"entity_id": entity}
	for k, v := range data {
		body[k] = v
	}
	return c.do(ctx, http.MethodPost, "/api/services/media_player/"+url.PathEscape(service), body, nil)
}

// Song is what to look up in Music Assistant.
type Song struct{ Title, Artist, Album string }

// Find returns the Music Assistant URI of a song from its library
// ("library://track/…"); empty when not found.
func (c *Client) Find(ctx context.Context, s Song) (string, error) {
	entry, err := c.massEntry(ctx)
	if err != nil {
		return "", err
	}
	body := map[string]any{"config_entry_id": entry, "name": s.Title, "media_type": []string{"track"}, "limit": 10}
	if s.Artist != "" {
		body["artist"] = s.Artist
	}
	var resp struct {
		ServiceResponse struct {
			Tracks []struct {
				URI     string `json:"uri"`
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				Album *struct {
					Name string `json:"name"`
				} `json:"album"`
			} `json:"tracks"`
		} `json:"service_response"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/services/music_assistant/search?return_response", body, &resp); err != nil {
		return "", err
	}
	best, bestScore := "", -1
	for _, t := range resp.ServiceResponse.Tracks {
		score := 0
		if fold(t.Name) == fold(s.Title) {
			score += 4
		}
		for _, a := range t.Artists {
			if fold(a.Name) == fold(s.Artist) {
				score += 2
				break
			}
		}
		if t.Album != nil && s.Album != "" && fold(t.Album.Name) == fold(s.Album) {
			score += 1
		}
		if score > bestScore && score >= 4 { // the title must match
			best, bestScore = t.URI, score
		}
	}
	return best, nil
}

func fold(s string) string {
	r := strings.NewReplacer("’", "'", "‘", "'", "“", "\"", "”", "\"")
	return strings.ToLower(strings.TrimSpace(r.Replace(s)))
}

// Play starts uris on the speaker; enqueue is "replace" (new queue) or "add".
func (c *Client) Play(ctx context.Context, entity string, uris []string, enqueue string) error {
	if len(uris) == 0 {
		return nil
	}
	var media any = uris
	if len(uris) == 1 {
		media = uris[0]
	}
	return c.do(ctx, http.MethodPost, "/api/services/music_assistant/play_media",
		map[string]any{"entity_id": entity, "media_id": media, "media_type": "track", "enqueue": enqueue}, nil)
}

// Transfer moves the queue of one speaker to another.
func (c *Client) Transfer(ctx context.Context, from, to string) error {
	return c.do(ctx, http.MethodPost, "/api/services/music_assistant/transfer_queue",
		map[string]any{"entity_id": to, "source_player": from, "auto_play": true}, nil)
}

// QueueItem is one entry of a speaker's Music Assistant queue.
type QueueItem struct {
	Name     string
	Duration float64
}

// Queue returns the current and next item of a speaker.
func (c *Client) Queue(ctx context.Context, entity string) (current, next *QueueItem, items int, err error) {
	var resp struct {
		ServiceResponse map[string]struct {
			Items       int `json:"items"`
			CurrentItem *struct {
				Name     string  `json:"name"`
				Duration float64 `json:"duration"`
			} `json:"current_item"`
			NextItem *struct {
				Name     string  `json:"name"`
				Duration float64 `json:"duration"`
			} `json:"next_item"`
		} `json:"service_response"`
	}
	if err = c.do(ctx, http.MethodPost, "/api/services/music_assistant/get_queue?return_response", map[string]any{"entity_id": entity}, &resp); err != nil {
		return
	}
	q, ok := resp.ServiceResponse[entity]
	if !ok {
		return
	}
	if q.CurrentItem != nil {
		current = &QueueItem{Name: q.CurrentItem.Name, Duration: q.CurrentItem.Duration}
	}
	if q.NextItem != nil {
		next = &QueueItem{Name: q.NextItem.Name, Duration: q.NextItem.Duration}
	}
	return current, next, q.Items, nil
}

func (c *Client) massEntry(ctx context.Context) (string, error) {
	if c.entry != "" {
		return c.entry, nil
	}
	var entries []struct {
		EntryID string `json:"entry_id"`
		State   string `json:"state"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/config/config_entries/entry?domain=music_assistant", nil, &entries); err != nil {
		return "", err
	}
	for _, e := range entries {
		if e.State == "loaded" {
			c.entry = e.EntryID
			return c.entry, nil
		}
	}
	return "", errors.New("Music Assistant is not loaded in Home Assistant")
}

// HTTPError is a non-2xx answer.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "Home Assistant rejects the token (401)"
	}
	if e.Message != "" {
		return fmt.Sprintf("Home Assistant %d: %s", e.Status, e.Message)
	}
	return fmt.Sprintf("Home Assistant responded %d", e.Status)
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
	req, err := http.NewRequestWithContext(ctx, method, c.base.String()+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Home Assistant unreachable (%s)", c.base.Host)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 32<<20)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		data, _ := io.ReadAll(io.LimitReader(limited, 4<<10))
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &m)
		msg := strings.Join(strings.Fields(m.Message), " ")
		if len([]rune(msg)) > 120 {
			msg = string([]rune(msg)[:119]) + "…"
		}
		return &HTTPError{Status: resp.StatusCode, Message: msg}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, limited)
		return nil
	}
	return json.NewDecoder(limited).Decode(out)
}

// Ping checks the URL and token (setup wizard).
func (c *Client) Ping(ctx context.Context) error {
	var out struct {
		Message string `json:"message"`
	}
	return c.do(ctx, http.MethodGet, "/api/", nil, &out)
}
