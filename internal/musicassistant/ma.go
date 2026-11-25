// Package musicassistant reads player queues straight from the Music
// Assistant server API (nml fork): Home Assistant only exposes the current
// and next item, the server has the whole queue. The token only travels in
// the Authorization header and never appears in errors.
package musicassistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base  *url.URL
	token string
	HTTP  *http.Client
}

// New checks the address; empty URL or token means off (nil).
func New(address, token string) (*Client, error) {
	if strings.TrimSpace(address) == "" || strings.TrimSpace(token) == "" {
		return nil, nil
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(address), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return nil, errors.New("ma_url must be http(s)://host:port")
	}
	return &Client{base: u, token: strings.TrimSpace(token), HTTP: &http.Client{Timeout: 20 * time.Second}}, nil
}

// Queue is the state of one player queue.
type Queue struct {
	ID           string  `json:"queue_id"`
	Name         string  `json:"display_name"`
	State        string  `json:"state"` // playing, paused, idle
	Items        int     `json:"items"`
	CurrentIndex *int    `json:"current_index"`
	Elapsed      float64 `json:"elapsed_time"`
	ElapsedAt    float64 `json:"elapsed_time_last_updated"` // unix seconds
}

// Position in the current item now.
func (q Queue) Position(now time.Time) float64 {
	p := q.Elapsed
	if q.State == "playing" && q.ElapsedAt > 0 {
		at := time.Unix(0, int64(q.ElapsedAt*1e9))
		if d := now.Sub(at).Seconds(); d > 0 && d < 24*3600 {
			p += d
		}
	}
	return math.Max(0, p)
}

// Item is one track in a queue.
type Item struct {
	Name     string
	Duration float64
	Title    string
	Artists  []string
	Album    string
	URI      string
	// SubsonicID: the song ID in Navidrome when the track comes from an
	// OpenSubsonic provider (exact match to the MPD file).
	SubsonicID string
}

type rawItem struct {
	Name      string  `json:"name"`
	Duration  float64 `json:"duration"`
	MediaItem *struct {
		URI     string `json:"uri"`
		Name    string `json:"name"`
		Artists []struct {
			Name string `json:"name"`
		} `json:"artists"`
		Album *struct {
			Name string `json:"name"`
		} `json:"album"`
		Mappings []struct {
			ItemID string `json:"item_id"`
			Domain string `json:"provider_domain"`
		} `json:"provider_mappings"`
	} `json:"media_item"`
}

func (r rawItem) item() Item {
	it := Item{Name: r.Name, Duration: r.Duration}
	if mi := r.MediaItem; mi != nil {
		it.Title, it.URI = mi.Name, mi.URI
		for _, a := range mi.Artists {
			it.Artists = append(it.Artists, a.Name)
		}
		if mi.Album != nil {
			it.Album = mi.Album.Name
		}
		for _, m := range mi.Mappings {
			if m.Domain == "opensubsonic" || m.Domain == "subsonic" {
				it.SubsonicID = m.ItemID
				break
			}
		}
	}
	return it
}

// Queue returns one queue.
func (c *Client) Queue(ctx context.Context, id string) (Queue, error) {
	var q Queue
	err := c.call(ctx, "player_queues/get", map[string]any{"queue_id": id}, &q)
	return q, err
}

// Items returns up to limit items from offset on.
func (c *Client) Items(ctx context.Context, id string, offset, limit int) ([]Item, error) {
	var out []Item
	for len(out) < limit {
		page := min(200, limit-len(out))
		var raw []rawItem
		if err := c.call(ctx, "player_queues/items", map[string]any{"queue_id": id, "offset": offset + len(out), "limit": page}, &raw); err != nil {
			return nil, err
		}
		for _, r := range raw {
			out = append(out, r.item())
		}
		if len(raw) < page {
			break
		}
	}
	return out, nil
}

// HTTPError is a non-2xx answer.
type HTTPError struct{ Status int }

func (e *HTTPError) Error() string {
	if e.Status == http.StatusUnauthorized {
		return "Music Assistant rejects the token (401)"
	}
	return fmt.Sprintf("Music Assistant responded %d", e.Status)
}

func (c *Client) call(ctx context.Context, command string, args map[string]any, out any) error {
	data, err := json.Marshal(map[string]any{"command": command, "args": args})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base.String()+"/api", bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("Music Assistant unreachable (%s)", c.base.Host)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 32<<20)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_, _ = io.Copy(io.Discard, io.LimitReader(limited, 4<<10))
		return &HTTPError{Status: resp.StatusCode}
	}
	if strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "html") {
		return errors.New("got a web page instead of the Music Assistant API; use just the server address, e.g. http://host:8095")
	}
	body, err := io.ReadAll(limited)
	if err != nil {
		return err
	}
	// Errors come back as {"error_code":…, "details":…} with status 200 or 4xx.
	var e struct {
		Code    any    `json:"error_code"`
		Details string `json:"details"`
	}
	if json.Unmarshal(body, &e) == nil && e.Code != nil {
		msg := strings.Join(strings.Fields(e.Details), " ")
		if len([]rune(msg)) > 120 {
			msg = string([]rune(msg)[:119]) + "…"
		}
		return fmt.Errorf("Music Assistant: %s", msg)
	}
	return json.Unmarshal(body, out)
}

// Ping checks the URL and token (setup wizard).
func (c *Client) Ping(ctx context.Context) error {
	var out json.RawMessage
	return c.call(ctx, "players/all", nil, &out)
}
