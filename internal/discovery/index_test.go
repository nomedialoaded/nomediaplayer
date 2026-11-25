package discovery

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func fixtureIndex(server *httptest.Server) *Index {
	index := NewIndex("test-key", "test-secret", server.Client())
	index.endpoint = server.URL
	index.transport.interval = 0
	index.now = func() time.Time { return time.Unix(1234567890, 0) }
	return index
}
func TestIndexSignedSearchNormalizationAndNoResponseCache(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		sum := sha1.Sum([]byte("test-keytest-secret1234567890"))
		if r.Header.Get("X-Auth-Key") != "test-key" || r.Header.Get("Authorization") != hex.EncodeToString(sum[:]) || r.Header.Get("X-Auth-Date") != "1234567890" || !strings.Contains(r.UserAgent(), "nomediaplayer/") {
			t.Error("missing signed headers")
		}
		if r.URL.Query().Get("q") != "science hour" || r.URL.Query().Get("max") != "25" {
			t.Error("unbounded/invalid query")
		}
		fmt.Fprint(w, `{"status":"true","feeds":[{"id":12,"title":"Science","url":"https://example.test/rss#anchor"},{"title":"Duplicate","url":"https://example.test/rss"},{"title":"Bad","url":"https://user:password@example.test/rss"},{"title":"","url":"https://example.test/empty"},{"title":"Fallback source","url":"http://example.test/other"}]}`)
	}))
	defer server.Close()
	index := fixtureIndex(server)
	for range 2 {
		shows, err := index.Search(context.Background(), " science  hour ")
		if err != nil || len(shows) != 2 || shows[0].FeedURL != "https://example.test/rss" || shows[0].SourceURL != "https://podcastindex.org/podcast/12" || shows[1].SourceURL != "https://podcastindex.org" {
			t.Fatal(shows, err)
		}
	}
	if calls != 2 || len(index.transport.cache) != 0 {
		t.Fatal("API content cached")
	}
	if strings.Contains(fmt.Sprintf("%+v %#v", index, index), "test-secret") {
		t.Fatal("debug string exposed credentials")
	}
}
func TestIndexLimitsErrorsAndNoCredentialRedirect(t *testing.T) {
	var redirectCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirectCalls.Add(1) }))
	defer target.Close()
	for _, scenario := range []string{"status", "json", "large", "chunked-large", "unauthorized", "limited", "redirect"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "status":
					fmt.Fprint(w, `{"status":"false","description":"test-secret"}`)
				case "json":
					fmt.Fprint(w, "test-secret invalid json")
				case "large":
					w.Header().Set("Content-Length", fmt.Sprint(maxResponse+1))
					w.WriteHeader(200)
				case "chunked-large":
					w.WriteHeader(200)
					w.(http.Flusher).Flush()
					fmt.Fprint(w, strings.Repeat("a", maxResponse+1))
				case "unauthorized":
					w.WriteHeader(401)
					fmt.Fprint(w, "test-secret")
				case "limited":
					w.Header().Set("Retry-After", "9999")
					w.WriteHeader(429)
				case "redirect":
					http.Redirect(w, r, target.URL, 302)
				}
			}))
			defer server.Close()
			index := fixtureIndex(server)
			_, err := index.Search(context.Background(), "query")
			if err == nil || strings.Contains(err.Error(), "test-secret") || strings.Contains(err.Error(), "test-key") {
				t.Fatal("error missing or leaked secrets", err)
			}
			if scenario == "limited" && time.Until(index.transport.next) > 31*time.Second {
				t.Fatal("unbounded retry delay")
			}
		})
	}
	if redirectCalls.Load() != 0 {
		t.Fatal("forwarded credential request to redirect target")
	}
}
func TestIndexMissingCredentialsQueryAndCanceledQuota(t *testing.T) {
	index := NewIndex("", "", nil)
	if _, err := index.Search(context.Background(), "query"); err != ErrIndexCredentials {
		t.Fatal(err)
	}
	for _, query := range []string{"", strings.Repeat("x", 129)} {
		if _, err := index.Search(context.Background(), query); err != ErrQuery {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		<-release
		fmt.Fprint(w, `{"status":true,"feeds":[]}`)
	}))
	defer server.Close()
	index = fixtureIndex(server)
	done := make(chan error, 1)
	go func() { _, err := index.Search(context.Background(), "one"); done <- err }()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := index.Search(ctx, "two"); err == nil {
		t.Fatal("canceled quota wait succeeded")
	}
	close(release)
	if err := <-done; err != nil || calls.Load() != 1 {
		t.Fatal(err, calls.Load())
	}
	index.endpoint = ":invalid"
	if _, err := index.Search(context.Background(), "query"); err == nil {
		t.Fatal("invalid endpoint accepted")
	}
}

func TestIndexCredentialsLegacyNames(t *testing.T) {
	env := map[string]string{"PODMUTT_PODCAST_INDEX_KEY": "k", "PODMUTT_PODCAST_INDEX_SECRET": "s"}
	if k, s := IndexCredentials("", "", func(n string) string { return env[n] }); k != "k" || s != "s" {
		t.Fatal("podmutt names must still work")
	}
	env[IndexKeyEnv], env[IndexSecretEnv] = "nk", "ns"
	if k, s := IndexCredentials("", "", func(n string) string { return env[n] }); k != "nk" || s != "ns" {
		t.Fatal("new names win")
	}
}

func TestIndexCredentialsConfigWins(t *testing.T) {
	env := map[string]string{"PODMUTT_PODCAST_INDEX_KEY": "old", "PODMUTT_PODCAST_INDEX_SECRET": "old"}
	if k, s := IndexCredentials("ck", "cs", func(n string) string { return env[n] }); k != "ck" || s != "cs" {
		t.Fatal("configured key wins")
	}
}

func TestIndexTrendingSignedChart(t *testing.T) {
	var path, max string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path, max = r.URL.Path, r.URL.Query().Get("max")
		if r.Header.Get("X-Auth-Key") != "test-key" || r.Header.Get("Authorization") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":"true","feeds":[{"id":7,"title":"Trending show","url":"https://feeds.example/t.xml"}]}`))
	}))
	defer server.Close()
	index := fixtureIndex(server)
	index.endpoint = server.URL + "/api/1.0/search/byterm"
	shows, err := index.Trending(context.Background())
	if err != nil || len(shows) != 1 || shows[0].Title != "Trending show" || shows[0].FeedURL != "https://feeds.example/t.xml" {
		t.Fatalf("trending %+v %v", shows, err)
	}
	if path != "/api/1.0/podcasts/trending" || max != "100" {
		t.Fatalf("request %s max=%s", path, max)
	}
	if _, err := NewIndex("", "", nil).Trending(context.Background()); err != ErrIndexCredentials {
		t.Fatal("no credentials, no request")
	}
}
