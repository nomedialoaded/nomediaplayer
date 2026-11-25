package discovery

import (
	"context"
	"crypto/sha1" // Podcast Index's documented authentication protocol, not password hashing.
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nomedialoaded/nomediaplayer/internal/activity"
	"github.com/nomedialoaded/nomediaplayer/internal/feedback"
)

// Searcher is the small shared provider contract. Charts remain Apple-specific.
type Searcher interface {
	Search(context.Context, string) ([]Show, error)
}

const IndexKeyEnv = "NOMEDIAPLAYER_PODCAST_INDEX_KEY"
const IndexSecretEnv = "NOMEDIAPLAYER_PODCAST_INDEX_SECRET"

var ErrIndexCredentials = feedback.New(feedback.Credentials, "Podcast Index needs your own free API credentials: ? → Setup → c (Podcasts); Apple and RSS need none")

// Index keeps no API response cache or durable directory records. The only
// persistent content comes from separately fetching a user-selected public RSS.
type Index struct {
	transport             *Apple
	key, secret, endpoint string
	now                   func() time.Time
}

func NewIndex(key, secret string, client *http.Client) *Index {
	transport := NewApple(client)
	// Never forward signed credentials, even to a same-host redirect target.
	transport.client.CheckRedirect = func(*http.Request, []*http.Request) error { return ErrUnavailable }
	return &Index{transport: transport, key: key, secret: secret, endpoint: "https://api.podcastindex.org/api/1.0/search/byterm", now: time.Now}
}
func (i *Index) String() string   { return "Podcast Index client (credentials redacted)" }
func (i *Index) GoString() string { return i.String() }
func (i *Index) Search(ctx context.Context, query string) ([]Show, error) {
	ctx, finish := activity.Begin(ctx, "Searching Podcast Index…", activity.Foreground)
	defer finish()
	query = strings.Join(strings.Fields(query), " ")
	if size := len([]rune(query)); size == 0 || size > 128 {
		return nil, ErrQuery
	}
	if i.key == "" || i.secret == "" {
		return nil, ErrIndexCredentials
	}
	return i.fetch(ctx, i.endpoint, url.Values{"q": {query}}, maxResults)
}

// Trending is Podcast Index's list of podcasts that are popular right now
// (the chart for Podcast Index, like Apple's top 100).
func (i *Index) Trending(ctx context.Context) ([]Show, error) {
	ctx, finish := activity.Begin(ctx, "Loading Podcast Index trending…", activity.Foreground)
	defer finish()
	if i.key == "" || i.secret == "" {
		return nil, ErrIndexCredentials
	}
	return i.fetch(ctx, strings.Replace(i.endpoint, "/search/byterm", "/podcasts/trending", 1), url.Values{}, maxTopResults)
}

// fetch runs one signed request and maps the feeds to shows.
func (i *Index) fetch(ctx context.Context, address string, query url.Values, limit int) ([]Show, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	select {
	case i.transport.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-i.transport.gate }()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(address)
	if err != nil {
		return nil, ErrUnavailable
	}
	params := endpoint.Query()
	for k, v := range query {
		params[k] = v
	}
	params.Set("max", strconv.Itoa(limit))
	endpoint.RawQuery = params.Encode()
	stamp := strconv.FormatInt(i.now().Unix(), 10)
	sum := sha1.Sum([]byte(i.key + i.secret + stamp))
	headers := http.Header{"X-Auth-Key": {i.key}, "X-Auth-Date": {stamp}, "Authorization": {hex.EncodeToString(sum[:])}, "User-Agent": {"nomediaplayer/1"}}
	var response struct {
		Status json.RawMessage
		Feeds  []struct {
			ID         int64
			Title, URL string
		}
	}
	if err := i.transport.readHeaders(ctx, endpoint.String(), &response, headers); err != nil {
		return nil, err
	}
	if string(response.Status) != "true" && string(response.Status) != `"true"` {
		return nil, feedback.New(feedback.Authentication, "Podcast Index rejected the request; check your API key, secret and system clock")
	}
	records := make([]appleRecord, 0, min(len(response.Feeds), limit))
	for _, record := range response.Feeds {
		source := "https://podcastindex.org"
		if record.ID > 0 {
			source += "/podcast/" + strconv.FormatInt(record.ID, 10)
		}
		records = append(records, appleRecord{CollectionName: record.Title, FeedURL: record.URL, CollectionViewURL: source})
	}
	return normalize(records, limit), nil
}

// IndexCredentials picks the Podcast Index key and secret: the configured
// ones (podcast_index_key/_secret, which NOMEDIAPLAYER_PODCAST_INDEX_* already
// override), else the environment, else podmutt's PODMUTT_PODCAST_INDEX_*.
func IndexCredentials(cfgKey, cfgSecret string, getenv func(string) string) (key, secret string) {
	if cfgKey != "" || cfgSecret != "" {
		return cfgKey, cfgSecret
	}
	key, secret = getenv(IndexKeyEnv), getenv(IndexSecretEnv)
	if key == "" && secret == "" {
		key, secret = getenv("PODMUTT_PODCAST_INDEX_KEY"), getenv("PODMUTT_PODCAST_INDEX_SECRET")
	}
	return key, secret
}
