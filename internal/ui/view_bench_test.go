package ui

import (
	"fmt"
	"testing"
)

// musicBenchData is a Muziek screen at a real library size.
func musicBenchData() Data {
	d := Data{Music: true, Titles: [5]string{"Artiesten ‹t›", "Nummers", "Wachtrij", "Details", "Recent gespeeld ‹L›"}}
	for i := 0; i < 600; i++ {
		d.Podcasts = append(d.Podcasts, Item{ID: fmt.Sprintf("artist:%d", i), Title: fmt.Sprintf("Artiest nummer %d", i), Channel: "x"})
	}
	for i := 0; i < 40; i++ {
		t := fmt.Sprintf("Nummer %d — Een Artiest", i)
		d.Episodes = append(d.Episodes, Item{ID: fmt.Sprintf("track:%d", i), Title: t, PlainTitle: t, Channel: "Album", Date: "2006", Duration: "3:45", Index: i})
		d.Queue = append(d.Queue, Item{ID: fmt.Sprintf("mpdq:%d", i), Title: t, PlainTitle: t, Channel: "Album", Duration: "3:45"})
		d.Favorites = append(d.Favorites, Item{ID: fmt.Sprintf("recent:%d", i), Title: t, PlainTitle: t, Channel: "Album", Date: "21:30"})
	}
	d.Details = "Gravity\n\nArtiest   John Mayer\nAlbum     Continuum\nJaar      2006\n"
	d.Player = Player{Status: "playing", Title: "♪ Gravity", Channel: "John Mayer · Continuum", Position: "1:02", Duration: "4:05", Progress: 0.25, HasCurrent: true, HasVolume: true, Volume: 80}
	return d
}

func BenchmarkMusicView(b *testing.B) {
	m := resize(New(musicBenchData()), 170, 45)
	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

func BenchmarkMusicPanes(b *testing.B) {
	m := resize(New(musicBenchData()), 170, 45)
	g := m.wideLayout()
	cases := []struct {
		name string
		f    func()
	}{
		{"library", func() { _ = m.pane(Podcasts, g.libraryWidth, m.height-statusRows) }},
		{"episodes", func() { _ = m.pane(Episodes, g.episodeWidth, g.episodes) }},
		{"details", func() { _ = m.pane(Details, g.episodeWidth, g.details) }},
		{"queue", func() { _ = m.pane(Queue, g.queueWidth, g.queue) }},
		{"favorites", func() { _ = m.pane(Favorites, g.queueWidth, g.favorites) }},
		{"player", func() { _ = m.playerPane(g.playerWidth, g.player) }},
		{"hints", func() { _ = m.bottomHints() }},
		{"status", func() { _ = m.statusLine() }},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				c.f()
			}
		})
	}
}
