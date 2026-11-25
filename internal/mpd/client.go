// Package mpd is a small client for the MPD text protocol (nml fork).
// It covers what the Music view needs: library browsing, queue and transport.
package mpd

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const dialTimeout = 3 * time.Second

// Song is one library or queue entry.
type Song struct {
	File, Title, Artist, AlbumArtist, Album, Date, Genre string
	Track, Disc                                          int
	Duration                                             float64
	Pos, ID                                              int
}

// Status mirrors the fields of MPD's status command that the UI uses.
type Status struct {
	State          string // play, pause, stop
	Elapsed        float64
	Duration       float64
	Volume         int
	SongPos        int
	PlaylistLength int
	NextSong       int
	// Random, Repeat, Single: MPD playback modes (shuffle, repeat queue,
	// repeat/stop after one song).
	Random, Repeat, Single bool
}

// Client keeps one command connection and reconnects transparently.
type Client struct {
	addr string
	mu   sync.Mutex
	conn net.Conn
	rd   *bufio.Reader
}

func New(addr string) *Client { return &Client{addr: addr} }

func (c *Client) Addr() string { return c.addr }

func dial(ctx context.Context, addr string) (net.Conn, *bufio.Reader, error) {
	d := net.Dialer{Timeout: dialTimeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, nil, err
	}
	rd := bufio.NewReader(conn)
	conn.SetReadDeadline(time.Now().Add(dialTimeout))
	greeting, err := rd.ReadString('\n')
	conn.SetReadDeadline(time.Time{})
	if err != nil || !strings.HasPrefix(greeting, "OK MPD") {
		conn.Close()
		if err == nil {
			err = fmt.Errorf("mpd: unexpected greeting")
		}
		return nil, nil, err
	}
	return conn, rd, nil
}

// Quote escapes one argument for the MPD protocol.
func Quote(arg string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(arg) + `"`
}

type pair struct{ key, value string }

func readResponse(rd *bufio.Reader) ([]pair, error) {
	var out []pair
	for {
		line, err := rd.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\n")
		if line == "OK" {
			return out, nil
		}
		if strings.HasPrefix(line, "ACK ") {
			return nil, &ProtocolError{Message: line}
		}
		if k, v, ok := strings.Cut(line, ": "); ok {
			out = append(out, pair{k, v})
		}
	}
}

// ProtocolError is an ACK from MPD (the connection itself is still fine).
type ProtocolError struct{ Message string }

func (e *ProtocolError) Error() string { return "mpd: " + e.Message }

// Run sends one command line (already quoted) and returns its key/value lines.
func (c *Client) run(ctx context.Context, command string) ([]pair, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for attempt := 0; attempt < 2; attempt++ {
		if c.conn == nil {
			conn, rd, err := dial(ctx, c.addr)
			if err != nil {
				return nil, err
			}
			c.conn, c.rd = conn, rd
		}
		deadline := time.Now().Add(10 * time.Second)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		c.conn.SetDeadline(deadline)
		_, err := fmt.Fprintf(c.conn, "%s\n", command)
		var resp []pair
		if err == nil {
			resp, err = readResponse(c.rd)
		}
		c.conn.SetDeadline(time.Time{})
		var proto *ProtocolError
		if err == nil || errors.As(err, &proto) {
			return resp, err
		}
		// Stale connection (MPD closes idle clients): reconnect once.
		c.conn.Close()
		c.conn, c.rd = nil, nil
		if attempt == 1 {
			return nil, err
		}
	}
	return nil, fmt.Errorf("mpd: unreachable")
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func (c *Client) simple(ctx context.Context, command string) error {
	_, err := c.run(ctx, command)
	return err
}

// List returns all distinct values of a tag (e.g. albumartist), sorted.
func (c *Client) List(ctx context.Context, tag string) ([]string, error) {
	resp, err := c.run(ctx, "list "+tag)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, p := range resp {
		if strings.TrimSpace(p.value) != "" {
			out = append(out, p.value)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i]) < strings.ToLower(out[j]) })
	return out, nil
}

func atoiPrefix(value string) int {
	value, _, _ = strings.Cut(value, "/")
	n, _ := strconv.Atoi(strings.TrimSpace(value))
	return n
}

func parseSongs(resp []pair) []Song {
	var songs []Song
	var cur *Song
	for _, p := range resp {
		if p.key == "file" {
			songs = append(songs, Song{File: p.value, Pos: -1, ID: -1})
			cur = &songs[len(songs)-1]
			continue
		}
		if cur == nil {
			continue
		}
		switch p.key {
		case "Title":
			cur.Title = p.value
		case "Artist":
			if cur.Artist == "" {
				cur.Artist = p.value
			}
		case "AlbumArtist":
			if cur.AlbumArtist == "" {
				cur.AlbumArtist = p.value
			}
		case "Album":
			cur.Album = p.value
		case "Date":
			cur.Date = p.value
		case "Genre":
			if cur.Genre == "" {
				cur.Genre = p.value
			}
		case "Track":
			cur.Track = atoiPrefix(p.value)
		case "Disc":
			cur.Disc = atoiPrefix(p.value)
		case "duration":
			cur.Duration, _ = strconv.ParseFloat(p.value, 64)
		case "Time":
			if cur.Duration == 0 {
				cur.Duration, _ = strconv.ParseFloat(p.value, 64)
			}
		case "Pos":
			cur.Pos, _ = strconv.Atoi(p.value)
		case "Id":
			cur.ID, _ = strconv.Atoi(p.value)
		}
	}
	return songs
}

// Find returns songs with an exact tag match, sorted by album, disc, track.
func (c *Client) Find(ctx context.Context, tag, value string) ([]Song, error) {
	resp, err := c.run(ctx, "find "+tag+" "+Quote(value))
	if err != nil {
		return nil, err
	}
	songs := parseSongs(resp)
	sort.SliceStable(songs, func(i, j int) bool {
		a, b := songs[i], songs[j]
		if ya, yb := year(a.Date), year(b.Date); a.Album != b.Album && ya != yb {
			return ya < yb
		}
		if a.Album != b.Album {
			return strings.ToLower(a.Album) < strings.ToLower(b.Album)
		}
		if a.Disc != b.Disc {
			return a.Disc < b.Disc
		}
		if a.Track != b.Track {
			return a.Track < b.Track
		}
		return a.File < b.File
	})
	return songs, nil
}

func year(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return date
}

func (c *Client) Queue(ctx context.Context) ([]Song, error) {
	resp, err := c.run(ctx, "playlistinfo")
	if err != nil {
		return nil, err
	}
	return parseSongs(resp), nil
}

func (c *Client) CurrentSong(ctx context.Context) (Song, bool, error) {
	resp, err := c.run(ctx, "currentsong")
	if err != nil {
		return Song{}, false, err
	}
	songs := parseSongs(resp)
	if len(songs) == 0 {
		return Song{}, false, nil
	}
	return songs[0], true, nil
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	resp, err := c.run(ctx, "status")
	if err != nil {
		return Status{}, err
	}
	st := Status{SongPos: -1, NextSong: -1, Volume: -1}
	for _, p := range resp {
		switch p.key {
		case "state":
			st.State = p.value
		case "elapsed":
			st.Elapsed, _ = strconv.ParseFloat(p.value, 64)
		case "duration":
			st.Duration, _ = strconv.ParseFloat(p.value, 64)
		case "volume":
			st.Volume, _ = strconv.Atoi(p.value)
		case "song":
			st.SongPos, _ = strconv.Atoi(p.value)
		case "nextsong":
			st.NextSong, _ = strconv.Atoi(p.value)
		case "playlistlength":
			st.PlaylistLength, _ = strconv.Atoi(p.value)
		case "random":
			st.Random = p.value == "1"
		case "repeat":
			st.Repeat = p.value == "1"
		case "single":
			st.Single = p.value == "1" // "oneshot" counts as off
		}
	}
	return st, nil
}

func flag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

// SetModes sets shuffle (random), repeat and single in one command list.
func (c *Client) SetModes(ctx context.Context, random, repeat, single bool) error {
	return c.simple(ctx, "command_list_begin\nrandom "+flag(random)+"\nrepeat "+flag(repeat)+"\nsingle "+flag(single)+"\ncommand_list_end")
}

func (c *Client) Pause(ctx context.Context, paused bool) error {
	if paused {
		return c.simple(ctx, "pause 1")
	}
	return c.simple(ctx, "pause 0")
}
func (c *Client) Play(ctx context.Context) error { return c.simple(ctx, "play") }
func (c *Client) PlayPos(ctx context.Context, pos int) error {
	return c.simple(ctx, "play "+strconv.Itoa(pos))
}
func (c *Client) Stop(ctx context.Context) error     { return c.simple(ctx, "stop") }
func (c *Client) Next(ctx context.Context) error     { return c.simple(ctx, "next") }
func (c *Client) Previous(ctx context.Context) error { return c.simple(ctx, "previous") }
func (c *Client) Clear(ctx context.Context) error    { return c.simple(ctx, "clear") }
func (c *Client) Delete(ctx context.Context, pos int) error {
	return c.simple(ctx, "delete "+strconv.Itoa(pos))
}
func (c *Client) Update(ctx context.Context) error { return c.simple(ctx, "update") }

func (c *Client) SeekBy(ctx context.Context, delta float64) error {
	return c.simple(ctx, fmt.Sprintf("seekcur %+.0f", delta))
}

// SeekTo jumps to an absolute position in the current song.
func (c *Client) SeekTo(ctx context.Context, seconds float64) error {
	return c.simple(ctx, "seekcur "+strconv.FormatFloat(max(0, seconds), 'f', 2, 64))
}

func (c *Client) SetVolume(ctx context.Context, volume int) error {
	volume = max(0, min(100, volume))
	return c.simple(ctx, "setvol "+strconv.Itoa(volume))
}

// Add appends files to the queue in one command list.
func (c *Client) Add(ctx context.Context, files ...string) error {
	if len(files) == 0 {
		return nil
	}
	var b strings.Builder
	b.WriteString("command_list_begin\n")
	for _, f := range files {
		b.WriteString("add " + Quote(f) + "\n")
	}
	b.WriteString("command_list_end")
	return c.simple(ctx, b.String())
}

// ReplaceAndPlay clears the queue, adds files and starts at index start.
func (c *Client) ReplaceAndPlay(ctx context.Context, files []string, start int) error {
	var b strings.Builder
	b.WriteString("command_list_begin\nclear\n")
	for _, f := range files {
		b.WriteString("add " + Quote(f) + "\n")
	}
	b.WriteString("play " + strconv.Itoa(max(0, start)) + "\ncommand_list_end")
	return c.simple(ctx, b.String())
}

// Idle blocks on its own connection until one of the subsystems changes.
// It returns the changed subsystem names, or an error (including ctx cancel).
func Idle(ctx context.Context, addr string, subsystems ...string) ([]string, error) {
	conn, rd, err := dial(ctx, addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	if _, err := fmt.Fprintf(conn, "idle %s\n", strings.Join(subsystems, " ")); err != nil {
		return nil, err
	}
	resp, err := readResponse(rd)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	var changed []string
	for _, p := range resp {
		if p.key == "changed" {
			changed = append(changed, p.value)
		}
	}
	return changed, nil
}

// FindTags returns songs matching all tag/value pairs exactly (e.g.
// "title", "Gravity", "album", "Continuum"). Unsorted, as MPD returns them.
func (c *Client) FindTags(ctx context.Context, pairs ...string) ([]Song, error) {
	if len(pairs) == 0 || len(pairs)%2 != 0 {
		return nil, fmt.Errorf("mpd: FindTags needs tag/value pairs")
	}
	var b strings.Builder
	b.WriteString("find")
	for i := 0; i < len(pairs); i += 2 {
		b.WriteString(" " + pairs[i] + " " + Quote(pairs[i+1]))
	}
	resp, err := c.run(ctx, b.String())
	if err != nil {
		return nil, err
	}
	return parseSongs(resp), nil
}

// Search does a case-insensitive substring search on one tag ("any",
// "artist", "albumartist", "album", "title", "genre"), at most limit songs,
// sorted by artist, year, album, disc and track.
func (c *Client) Search(ctx context.Context, tag, query string, limit int) ([]Song, error) {
	cmd := "search " + tag + " " + Quote(query)
	if limit > 0 {
		cmd += " window 0:" + strconv.Itoa(limit)
	}
	resp, err := c.run(ctx, cmd)
	if err != nil {
		return nil, err
	}
	songs := parseSongs(resp)
	SortSongs(songs)
	return songs, nil
}

// SortSongs orders by (album)artist, year, album, disc, track, file.
func SortSongs(songs []Song) {
	artist := func(s Song) string {
		if s.AlbumArtist != "" {
			return strings.ToLower(s.AlbumArtist)
		}
		return strings.ToLower(s.Artist)
	}
	sort.SliceStable(songs, func(i, j int) bool {
		a, b := songs[i], songs[j]
		if x, y := artist(a), artist(b); x != y {
			return x < y
		}
		if ya, yb := year(a.Date), year(b.Date); a.Album != b.Album && ya != yb {
			return ya < yb
		}
		if a.Album != b.Album {
			return strings.ToLower(a.Album) < strings.ToLower(b.Album)
		}
		if a.Disc != b.Disc {
			return a.Disc < b.Disc
		}
		if a.Track != b.Track {
			return a.Track < b.Track
		}
		return a.File < b.File
	})
}

// AlbumRef is one album of the library with its album artist.
type AlbumRef struct{ Artist, Album string }

// ListAlbums returns every album grouped by albumartist ("list album group
// albumartist"), sorted by album name and then artist.
func (c *Client) ListAlbums(ctx context.Context) ([]AlbumRef, error) {
	resp, err := c.run(ctx, "list album group albumartist")
	if err != nil {
		return nil, err
	}
	var out []AlbumRef
	artist := ""
	for _, p := range resp {
		switch strings.ToLower(p.key) {
		case "albumartist":
			artist = p.value
		case "album":
			if strings.TrimSpace(p.value) != "" {
				out = append(out, AlbumRef{Artist: artist, Album: p.value})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := strings.ToLower(out[i].Album), strings.ToLower(out[j].Album)
		if a != b {
			return a < b
		}
		return strings.ToLower(out[i].Artist) < strings.ToLower(out[j].Artist)
	})
	return out, nil
}

// Picture returns the cover for a song: the embedded picture (readpicture)
// or else cover.jpg/png in its folder (albumart). nil, nil = none. It uses
// its own short connection so a large image never blocks playback commands.
func (c *Client) Picture(ctx context.Context, file string) ([]byte, error) {
	conn, rd, err := dial(ctx, c.addr)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	conn.SetDeadline(deadline)
	for _, command := range []string{"readpicture", "albumart"} {
		data, err := readBinary(conn, rd, command, file)
		var proto *ProtocolError
		if err != nil && !errors.As(err, &proto) {
			return nil, err
		}
		if len(data) > 0 {
			return data, nil
		}
	}
	return nil, nil
}

const maxPicture = 16 << 20

// readBinary fetches a chunked binary response ("size", "binary: n" + bytes).
func readBinary(conn net.Conn, rd *bufio.Reader, command, file string) ([]byte, error) {
	var out []byte
	for {
		if _, err := fmt.Fprintf(conn, "%s %s %d\n", command, Quote(file), len(out)); err != nil {
			return nil, err
		}
		size, chunk := -1, -1
		for chunk < 0 {
			line, err := rd.ReadString('\n')
			if err != nil {
				return nil, err
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case line == "OK":
				return out, nil // no picture
			case strings.HasPrefix(line, "ACK "):
				return nil, &ProtocolError{Message: line}
			case strings.HasPrefix(line, "size: "):
				size, _ = strconv.Atoi(strings.TrimPrefix(line, "size: "))
			case strings.HasPrefix(line, "binary: "):
				chunk, _ = strconv.Atoi(strings.TrimPrefix(line, "binary: "))
			}
		}
		if size > maxPicture || chunk > maxPicture {
			return nil, fmt.Errorf("mpd: picture too large")
		}
		buf := make([]byte, chunk+1) // data + "\n"
		if _, err := io.ReadFull(rd, buf); err != nil {
			return nil, err
		}
		out = append(out, buf[:chunk]...)
		if line, err := rd.ReadString('\n'); err != nil || strings.TrimSpace(line) != "OK" {
			return nil, fmt.Errorf("mpd: bad binary response")
		}
		if chunk == 0 || size <= 0 || len(out) >= size {
			return out, nil
		}
	}
}
