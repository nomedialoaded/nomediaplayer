package ui

// nml: fast joins for the main layout. lipgloss.Join* re-measures every line
// of every nested block; our panes already have a known width and height, so
// each line is measured once and only padded when it falls short.

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

type block struct {
	text  string
	width int
}

// joinRows places blocks side by side (top-aligned), each padded to its width.
func joinRows(blocks ...block) string {
	split := make([][]string, len(blocks))
	height := 0
	for i, b := range blocks {
		split[i] = strings.Split(b.text, "\n")
		height = max(height, len(split[i]))
	}
	var sb strings.Builder
	for row := 0; row < height; row++ {
		if row > 0 {
			sb.WriteByte('\n')
		}
		for i, b := range blocks {
			line := ""
			if row < len(split[i]) {
				line = split[i][row]
			}
			sb.WriteString(line)
			if i < len(blocks)-1 { // the last block needs no right padding
				if w := ansi.StringWidth(line); w < b.width {
					sb.WriteString(strings.Repeat(" ", b.width-w))
				}
			}
		}
	}
	return sb.String()
}

// stack puts blocks under each other (left-aligned, like JoinVertical).
func stack(parts ...string) string { return strings.Join(parts, "\n") }
