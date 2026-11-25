package ui

import (
	"fmt"
	"testing"

	"charm.land/lipgloss/v2"
)

// lipglossWide is the original wide layout, kept as the reference.
func (m Model) lipglossWide(available int) string {
	g := m.wideLayout()
	middle := lipgloss.JoinVertical(lipgloss.Left, m.pane(Episodes, g.episodeWidth, g.episodes), m.pane(Details, g.episodeWidth, g.details))
	right := lipgloss.JoinVertical(lipgloss.Left, m.pane(Queue, g.queueWidth, g.queue), m.pane(Favorites, g.queueWidth, g.favorites))
	upper := lipgloss.JoinHorizontal(lipgloss.Top, middle, right)
	body := lipgloss.JoinVertical(lipgloss.Left, upper, m.playerPane(g.playerWidth, g.player))
	return lipgloss.JoinHorizontal(lipgloss.Top, m.pane(Podcasts, g.libraryWidth, available), body)
}

func TestWideJoinMatchesLipgloss(t *testing.T) {
	for name, data := range map[string]Data{"podcasts": fixture(), "muziek": musicBenchData()} {
		for _, size := range [][2]int{{100, 24}, {140, 40}, {170, 45}, {230, 60}} {
			t.Run(fmt.Sprintf("%s-%dx%d", name, size[0], size[1]), func(t *testing.T) {
				m := resize(New(data), size[0], size[1])
				if m.compact() {
					t.Skip("compact layout")
				}
				available := m.height - statusRows
				if got, want := m.wideContent(available), m.lipglossWide(available); got != want {
					t.Fatalf("fast join differs from lipgloss\n--- got\n%s\n--- want\n%s", got, want)
				}
			})
		}
	}
}

func TestItemsWithoutFilterDoNotLeak(t *testing.T) {
	m := resize(New(musicBenchData()), 170, 45)
	v := m.Visible(Podcasts)
	v[0].Title = "changed"
	if m.items(Podcasts)[0].Title == "changed" {
		t.Fatal("Visible must return a copy")
	}
	if it := m.items(Podcasts); cap(it) != len(it) {
		t.Fatal("items without filter must be capped so appends cannot write into the data")
	}
}
