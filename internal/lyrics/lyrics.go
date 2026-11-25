// Package lyrics finds song texts for the Music view (nml fork): a .lrc file
// next to the song (Deemix writes these), else lrclib.net, cached on disk.
package lyrics

import (
	"bufio"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Line is one lyric line; At is seconds into the song (synced lyrics only).
type Line struct {
	At   float64
	Text string
}

type Lyrics struct {
	Lines  []Line
	Synced bool
	// Source: "lrc" (file next to the song), "lrclib" or "" (none).
	Source       string
	Instrumental bool
}

func (l Lyrics) Empty() bool { return len(l.Lines) == 0 && !l.Instrumental }

// LineAt returns the index of the line sung at elapsed seconds (-1 before
// the first line or for unsynced lyrics).
func (l Lyrics) LineAt(elapsed float64) int {
	if !l.Synced {
		return -1
	}
	i := sort.Search(len(l.Lines), func(i int) bool { return l.Lines[i].At > elapsed })
	return i - 1
}

// Parse reads LRC ("[mm:ss.xx]text", several stamps per line allowed,
// metadata tags skipped) or plain text.
func Parse(text string) Lyrics {
	var out Lyrics
	var plain []Line
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		var stamps []float64
		rest := line
		for strings.HasPrefix(rest, "[") {
			end := strings.IndexByte(rest, ']')
			if end < 0 {
				break
			}
			at, ok := parseStamp(rest[1:end])
			if !ok {
				break // metadata like [ar:…] or text in brackets
			}
			stamps = append(stamps, at)
			rest = rest[end+1:]
		}
		if len(stamps) == 0 {
			if isTag(line) {
				continue
			}
			plain = append(plain, Line{Text: strings.TrimSpace(line)})
			continue
		}
		for _, at := range stamps {
			out.Lines = append(out.Lines, Line{At: at, Text: strings.TrimSpace(rest)})
		}
	}
	if len(out.Lines) > 0 {
		out.Synced = true
		sort.SliceStable(out.Lines, func(i, j int) bool { return out.Lines[i].At < out.Lines[j].At })
		return out
	}
	// Plain text: trim blank lines at both ends.
	for len(plain) > 0 && plain[0].Text == "" {
		plain = plain[1:]
	}
	for len(plain) > 0 && plain[len(plain)-1].Text == "" {
		plain = plain[:len(plain)-1]
	}
	out.Lines = plain
	return out
}

func parseStamp(s string) (float64, bool) {
	min, sec, ok := strings.Cut(s, ":")
	if !ok {
		return 0, false
	}
	m, err1 := strconv.Atoi(min)
	f, err2 := strconv.ParseFloat(strings.Replace(sec, ":", ".", 1), 64)
	if err1 != nil || err2 != nil || m < 0 || f < 0 {
		return 0, false
	}
	return float64(m)*60 + f, true
}

func isTag(line string) bool {
	if !strings.HasPrefix(line, "[") || !strings.HasSuffix(line, "]") {
		return false
	}
	key, _, ok := strings.Cut(line[1:len(line)-1], ":")
	return ok && len(key) <= 8 && !strings.ContainsAny(key, " ")
}

// Song identifies what to look up.
type Song struct {
	File, Artist, Title, Album string
	Duration                   float64
}

// Finder looks lyrics up; safe for use from one goroutine at a time per call.
type Finder struct {
	MusicDir string // MPD music_directory
	CacheDir string // <state>/lyrics
	Online   bool
	HTTP     *http.Client
	// UserAgent identifies the client to lrclib.net.
	UserAgent string
}

const missRetry = 7 * 24 * time.Hour

// Find returns the lyrics; Empty() when none were found.
func (f *Finder) Find(ctx context.Context, s Song) (Lyrics, error) {
	if f.MusicDir != "" && s.File != "" {
		base := strings.TrimSuffix(s.File, filepath.Ext(s.File))
		for _, ext := range []string{".lrc", ".LRC", ".txt"} {
			if data, err := os.ReadFile(filepath.Join(f.MusicDir, base+ext)); err == nil {
				l := Parse(string(data))
				if !l.Empty() {
					l.Source = "lrc"
					return l, nil
				}
			}
		}
	}
	if !f.Online || s.Title == "" || s.Artist == "" {
		return Lyrics{}, nil
	}
	key := cacheKey(s)
	if f.CacheDir != "" {
		if data, err := os.ReadFile(filepath.Join(f.CacheDir, key+".lrc")); err == nil {
			l := Parse(string(data))
			l.Source = "lrclib"
			l.Instrumental = strings.TrimSpace(string(data)) == instrumentalMark
			if l.Instrumental {
				l.Lines = nil
			}
			return l, nil
		}
		if st, err := os.Stat(filepath.Join(f.CacheDir, key+".none")); err == nil && time.Since(st.ModTime()) < missRetry {
			return Lyrics{}, nil
		}
	}
	text, instrumental, err := f.lrclib(ctx, s)
	if err != nil {
		return Lyrics{}, err // network trouble: do not remember a miss
	}
	if f.CacheDir != "" {
		_ = os.MkdirAll(f.CacheDir, 0o700)
		switch {
		case instrumental:
			_ = os.WriteFile(filepath.Join(f.CacheDir, key+".lrc"), []byte(instrumentalMark), 0o600)
		case text != "":
			_ = os.WriteFile(filepath.Join(f.CacheDir, key+".lrc"), []byte(text), 0o600)
		default:
			_ = os.WriteFile(filepath.Join(f.CacheDir, key+".none"), nil, 0o600)
		}
	}
	if instrumental {
		return Lyrics{Source: "lrclib", Instrumental: true}, nil
	}
	l := Parse(text)
	if !l.Empty() {
		l.Source = "lrclib"
	}
	return l, nil
}

const instrumentalMark = "[instrumental]"

func cacheKey(s Song) string {
	sum := sha1.Sum([]byte(strings.ToLower(s.Artist + "\x00" + s.Title + "\x00" + s.Album)))
	return hex.EncodeToString(sum[:10])
}

type lrclibTrack struct {
	SyncedLyrics string `json:"syncedLyrics"`
	PlainLyrics  string `json:"plainLyrics"`
	Instrumental bool   `json:"instrumental"`
}

var errStatus = errors.New("lrclib: unexpected status")

// lrclib tries the exact match (artist, title, album, duration) and then a
// search on artist + title. Empty text and no error = not found.
func (f *Finder) lrclib(ctx context.Context, s Song) (string, bool, error) {
	q := url.Values{"artist_name": {s.Artist}, "track_name": {s.Title}}
	if s.Album != "" {
		q.Set("album_name", s.Album)
	}
	if s.Duration > 0 {
		q.Set("duration", strconv.Itoa(int(s.Duration+0.5)))
	}
	var t lrclibTrack
	found, err := f.getJSON(ctx, "https://lrclib.net/api/get?"+q.Encode(), &t)
	if err != nil {
		return "", false, err
	}
	if !found || (t.SyncedLyrics == "" && t.PlainLyrics == "" && !t.Instrumental) {
		var list []lrclibTrack
		sq := url.Values{"artist_name": {s.Artist}, "track_name": {s.Title}}
		if _, err := f.getJSON(ctx, "https://lrclib.net/api/search?"+sq.Encode(), &list); err != nil {
			return "", false, err
		}
		t = lrclibTrack{}
		for _, c := range list { // prefer synced
			if c.SyncedLyrics != "" {
				t = c
				break
			}
			if t.PlainLyrics == "" && !t.Instrumental {
				t = c
			}
		}
	}
	if t.SyncedLyrics != "" {
		return t.SyncedLyrics, false, nil
	}
	return t.PlainLyrics, t.Instrumental && t.PlainLyrics == "", nil
}

func (f *Finder) getJSON(ctx context.Context, address string, into any) (bool, error) {
	client := f.HTTP
	if client == nil {
		client = &http.Client{Timeout: 8 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, err
	}
	if f.UserAgent != "" {
		req.Header.Set("User-Agent", f.UserAgent)
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return false, errStatus
	}
	return true, json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 2<<20)).Decode(into)
}

// MusicDirFromMPD reads music_directory from an mpd.conf ("~" expanded).
func MusicDirFromMPD(path, home string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "music_directory") {
			continue
		}
		value := strings.Trim(strings.TrimSpace(strings.TrimPrefix(line, "music_directory")), `"`)
		if strings.HasPrefix(value, "~/") {
			value = filepath.Join(home, value[2:])
		}
		return value
	}
	return ""
}
