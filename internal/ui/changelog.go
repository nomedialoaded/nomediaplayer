package ui

// nml: the changelog as the last help page (Markdown → wrapped lines).

import (
	"regexp"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// Changelog is set by main (embedded CHANGELOG.md).
var Changelog string

var mdLink = regexp.MustCompile(`\[([^\]]+)\]\([^)]+\)`)

func cleanMarkdown(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	return strings.NewReplacer("**", "", "`", "").Replace(s)
}

func wrapText(s string, width int) []string {
	if width < 10 {
		width = 10
	}
	return strings.Split(ansi.Wordwrap(s, width, ""), "\n")
}

// changelogLines renders the changelog for a width: versions as section
// titles, ### as labels, bullets wrapped with a hanging indent.
func (m Model) changelogLines(width int) []string {
	pal := m.Theme()
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(string(pal.Muted)))
	text := Changelog
	if strings.TrimSpace(text) == "" {
		return []string{muted.Render("No changelog in this build.")}
	}
	type block struct{ kind, text string }
	var blocks []block
	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "# "):
			// the document title is the help box title already
		case strings.HasPrefix(line, "## "):
			blocks = append(blocks, block{"h2", line[3:]})
		case strings.HasPrefix(line, "### "):
			blocks = append(blocks, block{"h3", line[4:]})
		case strings.HasPrefix(line, "- "):
			blocks = append(blocks, block{"item", line[2:]})
		case line == "":
			blocks = append(blocks, block{"gap", ""})
		default:
			last := len(blocks) - 1
			if last >= 0 && (blocks[last].kind == "item" || blocks[last].kind == "text") {
				blocks[last].text += " " + line
			} else {
				blocks = append(blocks, block{"text", line})
			}
		}
	}
	var out []string
	gap := func() {
		if len(out) > 0 && out[len(out)-1] != "" {
			out = append(out, "")
		}
	}
	for _, b := range blocks {
		switch b.kind {
		case "gap":
			gap()
		case "h2":
			gap()
			out = append(out, popupSection(cleanMarkdown(b.text), pal))
		case "h3":
			out = append(out, "  "+popupLabel(cleanMarkdown(b.text), pal))
		case "item":
			for i, l := range wrapText(cleanMarkdown(b.text), width-6) {
				prefix := "    "
				if i == 0 {
					prefix = "  • "
				}
				out = append(out, prefix+l)
			}
		case "text":
			for _, l := range wrapText(cleanMarkdown(b.text), width-2) {
				out = append(out, muted.Render(l))
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

// OpenChangelog opens the help on its changelog page.
func (m Model) OpenChangelog() Model {
	m.help, m.helpScroll, m.helpPage = true, 0, helpChangelogPage
	return m
}
