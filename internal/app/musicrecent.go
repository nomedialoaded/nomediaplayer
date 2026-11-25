package app

// nml: "Recently played" in panel 5 of the Music view. Every song MPD starts
// playing (also from rmpc or media keys while nomediaplayer runs) goes on top;
// kept in <state>/recent-music.json.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	recentPrefix = "recent:"
	recentMax    = 200
	recentFile   = "recent-music.json"
)

type recentSong struct {
	Song     mpd.Song  `json:"song"`
	PlayedAt time.Time `json:"played_at"`
}

type musicRecent struct {
	path    string
	entries []recentSong
	last    string // file last recorded, so pause/resume does not count twice
}

func loadRecent(stateDir string) *musicRecent {
	r := &musicRecent{}
	if stateDir == "" {
		return r
	}
	r.path = filepath.Join(stateDir, recentFile)
	if data, err := os.ReadFile(r.path); err == nil {
		_ = json.Unmarshal(data, &r.entries)
	}
	if len(r.entries) > 0 {
		r.last = r.entries[0].Song.File
	}
	return r
}

func (r *musicRecent) save() {
	if r.path == "" {
		return
	}
	data, err := json.Marshal(r.entries)
	if err != nil {
		return
	}
	tmp := r.path + ".tmp"
	if os.WriteFile(tmp, data, 0o600) == nil {
		_ = os.Rename(tmp, r.path)
	}
}

// record puts a song on top (once per new song); reports a change.
func (r *musicRecent) record(s mpd.Song, at time.Time) bool {
	if r == nil || s.File == "" || s.File == r.last {
		return false
	}
	r.last = s.File
	s.Pos, s.ID = 0, 0
	out := []recentSong{{Song: s, PlayedAt: at}}
	for _, e := range r.entries {
		if e.Song.File != s.File && len(out) < recentMax {
			out = append(out, e)
		}
	}
	r.entries = out
	r.save()
	return true
}

func (r *musicRecent) remove(i int) bool {
	if r == nil || i < 0 || i >= len(r.entries) {
		return false
	}
	r.entries = append(r.entries[:i:i], r.entries[i+1:]...)
	r.save()
	return true
}

// clear empties the list. The song that plays now is not recorded again
// until the next one starts.
func (r *musicRecent) clear() {
	if r == nil {
		return
	}
	r.entries = nil
	r.save()
}

func (r *musicRecent) song(id string) (mpd.Song, bool) {
	i, err := strconv.Atoi(strings.TrimPrefix(id, recentPrefix))
	if r == nil || !strings.HasPrefix(id, recentPrefix) || err != nil || i < 0 || i >= len(r.entries) {
		return mpd.Song{}, false
	}
	return r.entries[i].Song, true
}

// playedLabel: "14:05" today, "gisteren", weekday this week, else 02-11.
func playedLabel(t, now time.Time) string {
	day := func(x time.Time) time.Time { y, m, d := x.Date(); return time.Date(y, m, d, 0, 0, 0, 0, x.Location()) }
	days := int(day(now).Sub(day(t)).Hours() / 24)
	switch {
	case days <= 0:
		return t.Format("15:04")
	case days == 1:
		return "gisteren"
	case days < 7:
		return []string{"zo", "ma", "di", "wo", "do", "vr", "za"}[t.Weekday()] + " " + t.Format("15:04")
	}
	return t.Format("02-01")
}

func (r *musicRecent) items(now time.Time, current string) []ui.Item {
	if r == nil {
		return nil
	}
	items := make([]ui.Item, 0, len(r.entries))
	for i, e := range r.entries {
		title := songTitle(e.Song)
		if a := songArtist(e.Song); a != "" {
			title += " — " + a
		}
		items = append(items, ui.Item{ID: fmt.Sprintf("%s%d", recentPrefix, i), Title: title, PlainTitle: title,
			Channel: e.Song.Album, Date: playedLabel(e.PlayedAt, now), Current: current != "" && e.Song.File == current, Index: i})
	}
	return items
}
