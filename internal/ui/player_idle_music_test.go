package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// nml: the idle player line names a track on the music side and an
// episode on the podcast side.
func TestIdlePlayerLineMusicVsPodcasts(t *testing.T) {
	m := New(Data{})
	if line := ansi.Strip(m.renderPlayerTopLine(Player{}, 100)); !strings.Contains(line, "No current episode") {
		t.Fatalf("podcasts: %q", line)
	}
	m.data.Music = true
	if line := ansi.Strip(m.renderPlayerTopLine(Player{}, 100)); !strings.Contains(line, "No current track") {
		t.Fatalf("music: %q", line)
	}
}
