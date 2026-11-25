package ui

// nml: key overview (? or F1), in the style of youtube-cli: grouped sections,
// several pages (h/l), two columns on a wide terminal, j/k scroll when it
// still does not fit. Opens on the Music page inside the Music view.

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

type helpSection struct {
	title string
	rows  [][2]string
}

type helpPage struct {
	name     string
	sections []helpSection
}

const (
	helpMusicPage     = 1
	helpSetupPage     = 3
	helpChangelogPage = 4
)

var helpPages = []helpPage{
	{"General", []helpSection{
		{"Moving", [][2]string{
			{"j k  ↑ ↓", "Up / down in the list"},
			{"g G", "First / last item"},
			{"h l  ← →", "Previous / next column"},
			{"Tab", "Next column · Shift+Tab back"},
			{"1 … 6", "Go to column 1–6 (6 = player)"},
			{"Ctrl+d/u", "Half page (Details, popups)"},
		}},
		{"Playback  (music and podcasts)", [][2]string{
			{"Space", "Pause / resume"},
			{", .", "15 s back / forward"},
			{"= -", "Volume up / down"},
			{"s", "Stop"},
			{"n", "Next"},
			{"v", "Previous track (music)"},
			{"z", "Shuffle on / off (music)"},
			{"r", "Repeat: queue → track → off"},
			{"", "Controls what the player bar shows"},
		}},
		{"View", [][2]string{
			{"M", "Music ⇄ Podcasts"},
			{"/", "Search (music library or podcasts)"},
			{"f", "Filter the column"},
			{"Ctrl+U", "Clear filter or input"},
			{"Esc", "Back / close"},
		}},
		{"Other", [][2]string{
			{"? F1", "This overview · page 4 = setup, 5 = changelog"},
			{"i", "Changelog (in Music)"},
			{"e", "Settings · switch theme live"},
			{"!", "Full error message · Esc closes"},
			{"q  Ctrl+C", "Quit"},
		}},
	}},
	{"Music", []helpSection{
		{"Column 1", [][2]string{
			{"t  T", "Next / previous view:"},
			{"", "Artists · Albums · Genres ·"},
			{"", "Playlists ≡ · Favourites ★"},
			{"⌕", "Last search, always on top"},
			{"Enter", "Show tracks"},
			{"R", "Update library + Navidrome"},
		}},
		{"Tracks  (column 2)", [][2]string{
			{"Enter", "Play the album from this track"},
			{"", "★/≡/genre: whole list from here"},
			{"", "⌕: only this track, appended"},
			{"o", "Play whole album (replaces queue)"},
			{"a", "Append to the queue"},
			{"A", "All visible tracks to the queue"},
			{"⚠", "Not found in the MPD library"},
		}},
		{"Search & filter", [][2]string{
			{"/", "Search the whole library"},
			{"Tab", "All · Artist · Album · Track · Genre · Lidarr"},
			{"Enter", "Search · Esc cancel"},
			{"f", "Filter the column, also in results"},
			{"w", "Results per track or per album"},
			{"", "Album: Enter plays the whole album"},
			{"Esc", "Clear filter (also with / or t)"},
		}},
		{"Lidarr  (t → Lidarr)", [][2]string{
			{"/ Tab", "Lidarr: search an artist or album"},
			{"Enter +", "Add the artist (downloads nothing)"},
			{"Enter", "Search and download the album (asks first)"},
			{"⬇", "Downloads: progress, done = MPD updated"},
			{"", "✓ in library · ◐ partly · ⬇ requested"},
		}},
		{"Spotify link  (paste in Music or /)", [][2]string{
			{"paste", "In library: ▶ play · otherwise ⬇ Lidarr (single/album)"},
		}},
		{"Speakers  (t → Speakers, Music Assistant)", [][2]string{
			{">", "Current track + queue to a speaker, MPD pauses"},
			{"<", "Continue a speaker here (track, position, queue)"},
			{"Space n v", "Speaker: pause · next · previous (=/- volume)"},
			{"w", "Queue to another speaker"},
		}},
		{"Column 5  (L switches)", [][2]string{
			{"L", "Recently played ⇄ Lyrics"},
			{"Enter", "Recent: add the track and play"},
			{"a o d", "Recent: queue · album · remove"},
			{"D D", "Recent: clear the list (press twice)"},
			{"Enter", "Lyrics: jump to this line"},
			{"j k", "Lyrics: scroll (stops following)"},
			{"", "Lyrics from .lrc, otherwise lrclib.net"},
		}},
		{"Queue  (column 3)", [][2]string{
			{"Enter", "Play this track"},
			{"d  Del", "Remove from the queue"},
			{"D", "Clear the queue"},
		}},
		{"Navidrome  (changes go to the server)", [][2]string{
			{"p", "Add to a playlist · or a new one"},
			{"P", "Save the queue as a playlist"},
			{"x", "Remove from the open playlist"},
			{"b", "Track favourite on / off"},
			{"B", "Album favourite on / off"},
			{"", "On the chosen track, else what plays"},
		}},
	}},
	{"Podcasts", []helpSection{
		{"Shows  (column 1)", [][2]string{
			{"Enter", "Show episodes"},
			{"u", "Subscribe / unsubscribe"},
			{"N", "Add a feed URL"},
			{"o", "Change sort order"},
			{"R", "Refresh feeds"},
		}},
		{"Episodes  (column 2)", [][2]string{
			{"Enter", "Play / resume"},
			{"i", "Episode details"},
			{"a", "Add to queue"},
			{"A", "All visible to the queue"},
			{"c", "Played / unplayed"},
		}},
		{"Queue & favourites  (3 and 5)", [][2]string{
			{"m", "Actions (make favourite)"},
			{"d  Del", "Remove · u undo"},
			{"D", "Clear the queue"},
			{"o", "Reverse queue order"},
			{"Enter", "Play favourite (column 5)"},
		}},
		{"Discover", [][2]string{
			{"b", "My subscriptions ⇄ Discover"},
			{"/", "Search · Tab switches source"},
			{"t", "Charts: Apple Top 100 ⇄ Podcast Index trending"},
			{"U", "RSS or website preview"},
		}},
		{"Now playing", [][2]string{
			{"C", "Chapters"},
			{"I", "Episode URLs"},
		}},
	}},
	{"Setup", nil},     // nml: rendered from the app's setup status
	{"Changelog", nil}, // rendered from Changelog
}

// SetupRequest asks the app to open the setup wizard (from the help's Setup
// page); Section "" is the whole setup.
type SetupRequest struct{ Section string }

// setupKeys maps the Setup page keys to wizard sections (n/p/h/l/j/k page
// and scroll, so they are not used here).
var setupKeys = map[string]string{"a": "", "m": "library", "v": "navidrome", "r": "lidarr", "s": "speakers", "c": "podcasts", "w": "theme", "o": "privacy"}

// WithHiddenHints hides music key hints (by key text) of services that are
// switched off, e.g. ">/<" without speakers or "M" without podcasts.
func (m Model) WithHiddenHints(keys ...string) Model {
	m.hiddenHints = map[string]bool{}
	for _, k := range keys {
		m.hiddenHints[k] = true
	}
	return m
}

// WithSetupRows sets the status lines of the help's Setup page.
func (m Model) WithSetupRows(rows [][2]string) Model { m.setupRows = rows; return m }

func (m Model) setupLines(width int) []string {
	sec := helpSection{"Services  (press the key to set one up)", m.setupRows}
	lines := m.helpSectionLines(sec, width)
	pal := m.Theme()
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(string(pal.Muted)))
	lines = append(lines, "", muted.Render("  Settings stay on this computer, in config.toml (only you can read it)."),
		muted.Render("  A service you switch off is hidden; nothing is sent to it."))
	return lines
}

// helpSetupCmd: a Setup page key closes the help and opens the wizard.
func (m *Model) helpSetupCmd(key string) tea.Cmd {
	section, ok := setupKeys[key]
	if !ok || m.helpPage != helpSetupPage {
		return nil
	}
	m.help = false
	return func() tea.Msg { return SetupRequest{Section: section} }
}

const helpKeyWidth = 11

func (m Model) helpSectionLines(sec helpSection, width int) []string {
	pal := m.Theme()
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(string(pal.Muted)))
	lines := []string{popupSection(sec.title, pal)}
	for _, row := range sec.rows {
		key := row[0]
		var line string
		if key == "" {
			line = "  " + strings.Repeat(" ", helpKeyWidth) + muted.Render(row[1])
		} else {
			pad := max(1, helpKeyWidth-ansi.StringWidth(key))
			line = "  " + popupLabel(key, pal) + strings.Repeat(" ", pad) + row[1]
		}
		if ansi.StringWidth(line) > width {
			line = ansi.Truncate(line, width, "…")
		}
		lines = append(lines, line)
	}
	return lines
}

// helpLines lays a page out in one or two columns of the given width.
func (m Model) helpLines(width, page int) []string {
	if page == helpChangelogPage {
		return m.changelogLines(width)
	}
	if page == helpSetupPage {
		return m.setupLines(width)
	}
	const gap = 3
	columns := 1
	if width >= 2*40+gap {
		columns = 2
	}
	colWidth := (width - gap*(columns-1)) / columns
	var blocks [][]string
	for _, sec := range helpPages[page].sections {
		blocks = append(blocks, m.helpSectionLines(sec, colWidth))
	}
	stack := func(part [][]string) []string {
		var out []string
		for i, block := range part {
			if i > 0 {
				out = append(out, "")
			}
			out = append(out, block...)
		}
		return out
	}
	if columns == 1 || len(blocks) < 2 {
		return stack(blocks)
	}
	// Keep sections whole and in order; split where the taller column is shortest.
	split, best := 1, 1<<30
	for i := 1; i < len(blocks); i++ {
		if h := max(len(stack(blocks[:i])), len(stack(blocks[i:]))); h < best {
			split, best = i, h
		}
	}
	left, right := stack(blocks[:split]), stack(blocks[split:])
	out := make([]string, max(len(left), len(right)))
	for i := range out {
		l, r := "", ""
		if i < len(left) {
			l = left[i]
		}
		if i < len(right) {
			r = right[i]
		}
		out[i] = l + strings.Repeat(" ", max(0, colWidth-ansi.StringWidth(l))+gap) + r
	}
	return out
}

func (m *Model) openHelp() {
	m.help, m.helpScroll, m.helpPage = true, 0, 0
	if m.data.Music {
		m.helpPage = helpMusicPage
	}
}

// helpKey handles keys while the overview is open.
func (m *Model) helpKey(key string) {
	switch key {
	case "esc", "?", "f1", "q":
		m.help = false
	case "l", "right", "tab", "n":
		m.helpPage, m.helpScroll = (m.helpPage+1)%len(helpPages), 0
	case "h", "left", "shift+tab", "p":
		m.helpPage, m.helpScroll = (m.helpPage+len(helpPages)-1)%len(helpPages), 0
	case "1", "2", "3", "4", "5":
		if i := int(key[0] - '1'); i < len(helpPages) {
			m.helpPage, m.helpScroll = i, 0
		}
	case "j", "down":
		m.helpScroll++
	case "k", "up":
		m.helpScroll = max(0, m.helpScroll-1)
	case "ctrl+d", "pgdown":
		m.helpScroll += 8
	case "ctrl+u", "pgup":
		m.helpScroll = max(0, m.helpScroll-8)
	case "g", "home":
		m.helpScroll = 0
	case "G", "end":
		m.helpScroll = 1 << 20 // clamped when rendering
	}
}

// helpSize fits the box to the widest page so it does not jump when paging.
func (m Model) helpSize(available int) (int, int) {
	width := max(20, min(120, m.width-4))
	tallest := 0
	for p := range helpPages {
		if p == helpChangelogPage {
			continue // long; it scrolls instead of stretching the box
		}
		tallest = max(tallest, len(m.helpLines(width-4, p)))
	}
	return width, max(6, min(available-2, tallest+6))
}

func (m Model) renderHelp(available int) string {
	width, height := m.helpSize(available)
	inner := width - 4 // border + one space padding each side

	pal := m.Theme()
	muted := lipgloss.NewStyle().Foreground(lipgloss.Color(string(pal.Muted)))
	tabs := make([]string, len(helpPages))
	for i, p := range helpPages {
		label := strconv.Itoa(i+1) + " " + p.name
		if i == m.helpPage {
			tabs[i] = popupSection(label, pal)
		} else {
			tabs[i] = muted.Render(label)
		}
	}
	header := strings.Join(tabs, muted.Render("  ·  "))

	body := m.helpLines(inner, m.helpPage)
	room := max(1, height-6) // border 2; header, blank, blank, footer
	scrolls := len(body) > room
	if scrolls {
		scroll := min(m.helpScroll, len(body)-room)
		body = body[scroll : scroll+room]
	}
	hint := "h/l page · 1-5 choose · Esc closes"
	if scrolls {
		hint = "j/k scroll · " + hint
	}
	rows := []string{" " + header, ""}
	for _, line := range body {
		rows = append(rows, " "+line)
	}
	for len(rows) < height-4 {
		rows = append(rows, "")
	}
	rows = append(rows, "", " "+popupHint(hint, pal))
	title := "Keys · nomediaplayer " + m.Version() + " · page " + strconv.Itoa(m.helpPage+1) + "/" + strconv.Itoa(len(helpPages))
	return m.box(title, rows, width, height, true, "", "")
}
