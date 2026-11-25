package homeassistant

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testToken = "secret-token-123"

func fakeHA(t *testing.T, handle func(w http.ResponseWriter, r *http.Request, body map[string]any)) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]any
		data, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(data, &body)
		handle(w, r, body)
	}))
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, testToken)
	if err != nil || c == nil {
		t.Fatal(err)
	}
	return c
}

func TestNewValidates(t *testing.T) {
	if c, err := New("", "x"); c != nil || err != nil {
		t.Fatal("empty = off")
	}
	for _, bad := range []string{"ftp://ha:8123", "http://user:pw@ha:8123", "ha:8123"} {
		if _, err := New(bad, "x"); err == nil {
			t.Fatal("accepted", bad)
		}
	}
}

func TestSpeakersOnlyMusicAssistant(t *testing.T) {
	c := fakeHA(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		io.WriteString(w, `[
		 {"entity_id":"media_player.woonkamer","state":"playing","attributes":{"friendly_name":"Woonkamer","mass_player_type":"player",
		  "media_title":"Gravity","media_artist":"John Mayer","volume_level":0.4,"media_position":10,"media_duration":240,
		  "media_position_updated_at":"2026-11-05T10:00:00+00:00"}},
		 {"entity_id":"media_player.tv","state":"off","attributes":{"friendly_name":"TV"}},
		 {"entity_id":"media_player.bureau","state":"idle","attributes":{"friendly_name":"Bureau","mass_player_type":"player"}},
		 {"entity_id":"light.x","state":"on","attributes":{"mass_player_type":"player"}}]`)
	})
	list, err := c.Speakers(context.Background())
	if err != nil || len(list) != 2 || list[0].Name != "Bureau" || list[1].Volume != 0.4 {
		t.Fatalf("%v %+v", err, list)
	}
	at := time.Date(2026, 11, 5, 10, 0, 30, 0, time.UTC)
	if got := list[1].Elapsed(at); got != 40 {
		t.Fatal("elapsed", got)
	}
}

func TestFindPicksTitleArtistAlbum(t *testing.T) {
	var got map[string]any
	c := fakeHA(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		switch r.URL.Path {
		case "/api/config/config_entries/entry":
			io.WriteString(w, `[{"entry_id":"old","state":"not_loaded"},{"entry_id":"E1","state":"loaded"}]`)
		case "/api/services/music_assistant/search":
			got = body
			io.WriteString(w, `{"service_response":{"tracks":[
			 {"uri":"library://track/1","name":"Gravity (Live)","artists":[{"name":"John Mayer"}]},
			 {"uri":"library://track/2","name":"Gravity","artists":[{"name":"John Mayer"}],"album":{"name":"Where the Light Is"}},
			 {"uri":"library://track/3","name":"Gravity","artists":[{"name":"John Mayer"}],"album":{"name":"Continuum"}}]}}`)
		}
	})
	uri, err := c.Find(context.Background(), Song{Title: "Gravity", Artist: "John Mayer", Album: "Continuum"})
	if err != nil || uri != "library://track/3" {
		t.Fatal(uri, err)
	}
	if got["config_entry_id"] != "E1" || got["artist"] != "John Mayer" || got["album"] != nil || got["library_only"] != nil {
		t.Fatal("search body", got)
	}
	if uri, _ := c.Find(context.Background(), Song{Title: "Something Else"}); uri != "" {
		t.Fatal("title must match", uri)
	}
}

func TestErrorsHideToken(t *testing.T) {
	c := fakeHA(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {})
	c.token = "wrong"
	_, err := c.Speakers(context.Background())
	if err == nil || strings.Contains(err.Error(), "wrong") || !strings.Contains(err.Error(), "401") {
		t.Fatal(err)
	}
}

func TestPlayAndTransferBodies(t *testing.T) {
	var bodies []map[string]any
	var paths []string
	c := fakeHA(t, func(w http.ResponseWriter, r *http.Request, body map[string]any) {
		paths, bodies = append(paths, r.URL.Path), append(bodies, body)
	})
	ctx := context.Background()
	_ = c.Play(ctx, "media_player.a", []string{"u1"}, "replace")
	_ = c.Play(ctx, "media_player.a", []string{"u1", "u2"}, "add")
	_ = c.Transfer(ctx, "media_player.a", "media_player.b")
	_ = c.Media(ctx, "volume_set", "media_player.a", map[string]any{"volume_level": 0.3})
	if paths[0] != "/api/services/music_assistant/play_media" || bodies[0]["media_id"] != "u1" || bodies[0]["enqueue"] != "replace" {
		t.Fatal(paths, bodies)
	}
	if ids, ok := bodies[1]["media_id"].([]any); !ok || len(ids) != 2 {
		t.Fatal("list", bodies[1])
	}
	if bodies[2]["source_player"] != "media_player.a" || bodies[2]["entity_id"] != "media_player.b" {
		t.Fatal("transfer", bodies[2])
	}
	if paths[3] != "/api/services/media_player/volume_set" || bodies[3]["volume_level"] != 0.3 {
		t.Fatal("volume", paths[3], bodies[3])
	}
}
