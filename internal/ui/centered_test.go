package ui

import (
	"fmt"
	"strings"
	"testing"
)

func TestFavoritesCenteredKeepsSelectionInMiddle(t *testing.T) {
	d := Data{Music: true, FavoritesHeader: [3]string{"Tekst", "", "Tijd"}, FavoritesCentered: true}
	for i := 0; i < 40; i++ {
		text := fmt.Sprintf("regel-%02d", i)
		d.Favorites = append(d.Favorites, Item{ID: fmt.Sprintf("lyr:%d", i), Title: text, PlainTitle: text, Index: i})
	}
	m := resize(New(d), 170, 45).Select(Favorites, "lyr:20")
	rows := m.renderList(Favorites, m.items(Favorites), 40, 11)
	at := -1
	for i, r := range rows {
		if strings.Contains(r, "regel-20") {
			at = i
		}
	}
	if len(rows) != 11 || at != 5 || !strings.Contains(rows[6], "regel-21") {
		t.Fatalf("sung line in the middle with the next lines below, at=%d rows=%q", at, rows)
	}
	// Near the end the list stays full instead of scrolling past the last line.
	m = m.Select(Favorites, "lyr:38")
	rows = m.renderList(Favorites, m.items(Favorites), 40, 11)
	if !strings.Contains(rows[10], "regel-39") || !strings.Contains(rows[9], "regel-38") {
		t.Fatalf("end of the text, rows=%q", rows)
	}
	// Without the flag (Recent gespeeld) the old behaviour stays.
	m.data.FavoritesCentered = false
	m = m.Select(Favorites, "lyr:20")
	rows = m.renderList(Favorites, m.items(Favorites), 40, 11)
	if !strings.Contains(rows[10], "regel-20") {
		t.Fatalf("uncentered list keeps the selection at the bottom, rows=%q", rows)
	}
}
