package musicassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func fake(t *testing.T, items int) *Client {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			Command string         `json:"command"`
			Args    map[string]any `json:"args"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		switch req.Command {
		case "player_queues/get":
			fmt.Fprintf(w, `{"queue_id":%q,"state":"playing","items":%d,"current_index":2,"elapsed_time":100,"elapsed_time_last_updated":%f}`,
				req.Args["queue_id"], items, float64(time.Now().Add(-10*time.Second).UnixNano())/1e9)
		case "player_queues/items":
			off, lim := int(req.Args["offset"].(float64)), int(req.Args["limit"].(float64))
			var out []map[string]any
			for i := off; i < min(items, off+lim); i++ {
				out = append(out, map[string]any{"name": fmt.Sprint("A - T", i), "duration": 200, "media_item": map[string]any{
					"uri": fmt.Sprint("library://track/", i), "name": fmt.Sprint("T", i), "artists": []any{map[string]any{"name": "A"}},
					"album": map[string]any{"name": "Al"}, "provider_mappings": []any{map[string]any{"item_id": fmt.Sprint("nd", i), "provider_domain": "opensubsonic"}}}})
			}
			json.NewEncoder(w).Encode(out)
		default:
			w.Write([]byte(`{"error_code":999,"details":"Invalid command"}`))
		}
	}))
	t.Cleanup(srv.Close)
	c, _ := New(srv.URL, "tok")
	return c
}

func TestQueueAndPagedItems(t *testing.T) {
	c := fake(t, 450)
	ctx := context.Background()
	q, err := c.Queue(ctx, "q1")
	if err != nil || q.CurrentIndex == nil || *q.CurrentIndex != 2 {
		t.Fatal(q, err)
	}
	if p := q.Position(time.Now()); p < 109 || p > 112 {
		t.Fatal("position", p)
	}
	items, err := c.Items(ctx, "q1", 2, 501)
	if err != nil || len(items) != 448 || items[0].Title != "T2" || items[0].SubsonicID != "nd2" || items[0].Artists[0] != "A" || items[0].Album != "Al" {
		t.Fatal(len(items), err, items[0])
	}
	if err := c.call(ctx, "nope", nil, &q); err == nil || !strings.Contains(err.Error(), "Invalid command") {
		t.Fatal(err)
	}
	c.token = "wrong"
	if _, err := c.Queue(ctx, "q1"); err == nil || strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "401") {
		t.Fatal(err)
	}
}

// MA_URL=… MA_TOKEN=… MA_QUEUE=… go test -run Live (read-only)
func TestLiveReadOnly(t *testing.T) {
	c, _ := New(os.Getenv("MA_URL"), os.Getenv("MA_TOKEN"))
	if c == nil {
		t.Skip("MA_URL/MA_TOKEN not set")
	}
	q, err := c.Queue(context.Background(), os.Getenv("MA_QUEUE"))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%+v cur=%v pos=%.0f", q, *q.CurrentIndex, q.Position(time.Now()))
	items, err := c.Items(context.Background(), q.ID, *q.CurrentIndex, 10)
	for _, it := range items {
		t.Logf("%q %v %q nd=%s", it.Title, it.Artists, it.Album, it.SubsonicID)
	}
	if err != nil {
		t.Fatal(err)
	}
}
