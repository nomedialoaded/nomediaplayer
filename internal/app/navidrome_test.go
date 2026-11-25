package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/store"
	"github.com/nomedialoaded/nomediaplayer/internal/subsonic"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

func ndTestShell() libraryShell {
	ms := newMusic("", 0)
	ms.active = true
	ms.nd = newND("http://127.0.0.1:1", "u", "p")
	ms.nd.loaded = true
	ms.nd.playlists = []subsonic.Playlist{{ID: "p1", Name: "Lofi", SongCount: 2}}
	ms.nd.albums = []subsonic.Album{{ID: "a1", Name: "Continuum", Artist: "John Mayer"}}
	ms.nd.starred = []subsonic.Song{{ID: "s1", Title: "Gravity", Album: "Continuum"}}
	ms.nd.starKeys = map[string]bool{songKey("Gravity", "Continuum"): true}
	ms.artists = []string{"Adele"}
	return libraryShell{shell: shell{s: &session{}}, music: ms}
}

func TestNavidromeListsInArtistColumn(t *testing.T) {
	m := ndTestShell()
	m.renderMusic()
	var ids []string
	for _, it := range m.Model.Visible(ui.Podcasts) {
		ids = append(ids, it.ID)
	}
	if strings.Join(ids, ",") != "artist:Adele" {
		t.Fatalf("Artiesten view = %v", ids)
	}
	for view, want := range map[int]string{
		viewPlaylists: "nd:pl:p1",
		viewFavorites: ndStarredID + ",nd:album:a1",
	} {
		m.music.libView = view
		m.renderMusic()
		ids = nil
		for _, it := range m.Model.Visible(ui.Podcasts) {
			ids = append(ids, it.ID)
		}
		if strings.Join(ids, ",") != want {
			t.Fatalf("view %d = %v", view, ids)
		}
	}
	m.music.libView = viewArtists
	// Tracks: starred mark, unresolved mark, playlist plays as a whole.
	m.music.source = "nd:pl:p1"
	m.music.tracks = []mpd.Song{{File: "x/g.mp3", Title: "Gravity", Album: "Continuum"}, {Title: "Ghost", Album: "Nope"}}
	m.music.trackND = []subsonic.Song{{ID: "s1"}, {ID: "s2"}}
	m.renderMusic()
	items := m.Model.Visible(ui.Episodes)
	if len(items) != 2 || !strings.HasPrefix(items[0].Title, "★ ") || !strings.HasPrefix(items[1].Title, "⚠ ") {
		t.Fatal("track marks", items)
	}
	if m.music.sourceLabel() != "≡ Lofi" {
		t.Fatal("label", m.music.sourceLabel())
	}
}

func TestNavidromePlaylistPicker(t *testing.T) {
	m := ndTestShell()
	m.music.source = "nd:pl:p1"
	m.music.tracks = []mpd.Song{{File: "x/g.mp3", Title: "Gravity", Album: "Continuum"}}
	m.music.trackND = []subsonic.Song{{ID: "s1"}}
	m.renderMusic()
	m.Model = m.Model.WithFocus(ui.Episodes)
	m, _, handled := m.ndKey("p")
	if !handled || m.music.picker == nil || len(m.music.picker.options) != 2 || m.music.picker.known.ID != "s1" {
		t.Fatal("picker", m.music.picker)
	}
	m, _ = m.musicPickerKey(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if m.music.picker.index != 1 {
		t.Fatal("j does not move")
	}
	m, _ = m.musicPickerKey(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m, _ = m.musicPickerKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !m.music.picker.naming {
		t.Fatal("new playlist option must ask for a name")
	}
	for _, r := range "Mix" {
		m, _ = m.musicPickerKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.music.picker.input != "Mix" {
		t.Fatal("input", m.music.picker.input)
	}
	m, _ = m.musicPickerKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.music.picker != nil {
		t.Fatal("esc must close the picker")
	}
	// x only inside a playlist.
	m.music.source = "artist:Adele"
	m, cmd, _ := m.ndKey("x")
	if cmd != nil || !strings.Contains(m.notice, "playlist") {
		t.Fatal("x outside playlist", m.notice)
	}
}

func TestNavidromeOffWithoutURL(t *testing.T) {
	if newND("", "u", "p") != nil {
		t.Fatal("empty url must disable Navidrome")
	}
	m := ndTestShell()
	m.music.nd = nil
	m, cmd, handled := m.ndKey("p")
	if !handled || cmd != nil || !strings.Contains(m.notice, "navidrome_url") {
		t.Fatal("unconfigured hint", m.notice)
	}
}

// Regression: in Muziek the queue cursor was clamped to the podcast queue's
// length (2 podcasts queued → only the first 2 songs reachable).
func TestMusicQueueNavigationNotClampedByPodcastQueue(t *testing.T) {
	m := ndTestShell()
	m.state.Queue = []store.Episode{{ID: 1, Title: "Pod 1"}, {ID: 2, Title: "Pod 2"}}
	for i := 0; i < 10; i++ {
		m.music.queue = append(m.music.queue, mpd.Song{File: fmt.Sprintf("s%d.mp3", i), Title: fmt.Sprintf("Song %d", i), Pos: i})
	}
	m.renderMusic()
	m.Model = m.Model.WithFocus(ui.Queue)
	for i := 0; i < 5; i++ {
		updated, _ := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		m = updated.(libraryShell)
	}
	item, ok := m.Model.Selected(ui.Queue)
	if !ok || item.ID != "mpdq:5" {
		t.Fatalf("queue cursor = %q, want mpdq:5", item.ID)
	}
}

func TestPreviousKeyRoutesToMPD(t *testing.T) {
	m := ndTestShell()
	m.music.routeMPD = true
	if m.music.transport(m.s, ui.Action{Kind: ui.ActionPrevious}) == nil {
		t.Fatal("v (previous) must go to MPD when music is routed")
	}
}
