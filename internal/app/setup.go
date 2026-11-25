package app

// nml: setup wizard. Runs on first start (no config.toml and no
// NOMEDIAPLAYER_* environment) and later from the help (? → Setup) for the
// whole setup or one integration. Answers are tested right away and written
// to config.toml (0600) only at the end; Esc leaves everything as it was.
// Integrations answered with "no" are switched off (their keys cleared).

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/discovery"
	"github.com/nomedialoaded/nomediaplayer/internal/flacconv"
	"github.com/nomedialoaded/nomediaplayer/internal/homeassistant"
	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
	"github.com/nomedialoaded/nomediaplayer/internal/lyrics"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
	"github.com/nomedialoaded/nomediaplayer/internal/musicassistant"
	"github.com/nomedialoaded/nomediaplayer/internal/profile"
	"github.com/nomedialoaded/nomediaplayer/internal/subsonic"
	"github.com/nomedialoaded/nomediaplayer/internal/theme"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

// Setup sections, also the keys on the help's Setup page.
const (
	setupAll       = ""
	setupLibrary   = "library"
	setupNavidrome = "navidrome"
	setupLidarr    = "lidarr"
	setupSpeakers  = "speakers"
	setupPodcasts  = "podcasts"
	setupTheme     = "theme"
	setupPrivacy   = "privacy"
)

type setupKind int

const (
	stepInfo  setupKind = iota // text, Enter continues
	stepAsk                    // yes / no
	stepInput                  // one value
	stepTest                   // runs a check, then continues
)

type setupStep struct {
	section string
	kind    setupKind
	key     string // config key (ask, input)
	title   string
	body    string
	secret  bool
	// optional: an empty answer is fine and skips the rest of the section.
	optional bool
	test     func(context.Context, profile.Config) error
	// gate: an ask whose "no" switches the section off.
	gate bool
}

type setupWizard struct {
	steps    []setupStep
	i        int
	input    string
	cfg      profile.Config // working copy, written on save
	off      map[string]bool
	touched  map[string]bool // keys to write
	testing  bool
	testErr  string
	firstRun bool
}

type setupTested struct {
	step int
	err  error
}

func setupSteps(section string) []setupStep {
	intro := setupStep{section: setupAll, kind: stepInfo, title: "Welcome to nomediaplayer",
		body: "A terminal player for your own music (MPD) and podcasts.\n\n" +
			"This setup asks where your music is and which services you use.\n" +
			"Everything you enter stays on this computer, in\n" +
			"~/.config/" + profile.AppName + "/config.toml (readable only by you).\n" +
			"Services you skip stay off; turn them on later with ? → Setup.\n\n" +
			"Nothing is sent anywhere unless you switch it on here."}
	library := []setupStep{
		{section: setupLibrary, kind: stepInput, key: "mpd_address", title: "Music library · MPD",
			body: "nomediaplayer plays your music through MPD (Music Player Daemon).\nAddress of MPD (host:port):"},
		{section: setupLibrary, kind: stepTest, title: "Music library · MPD", body: "Connecting to MPD…", test: testMPD},
		{section: setupLibrary, kind: stepInput, key: "music_dir", title: "Music library · folder", optional: true,
			body: "Folder with your music (MPD's music_directory), used for\nlyrics (.lrc) and FLAC conversion. Empty = skip."},
		{section: setupLibrary, kind: stepTest, title: "Music library · folder", body: "Checking the folder…", test: testMusicDir},
	}
	navidrome := []setupStep{
		{section: setupNavidrome, kind: stepAsk, key: "navidrome", gate: true, title: "Navidrome",
			body: "Do you use Navidrome (or another Subsonic server)?\nThen playlists and favourites come from and go to that server."},
		{section: setupNavidrome, kind: stepInput, key: "navidrome_url", title: "Navidrome", body: "Server URL (e.g. http://navidrome.lan:4533):"},
		{section: setupNavidrome, kind: stepInput, key: "navidrome_user", title: "Navidrome", body: "User name:"},
		{section: setupNavidrome, kind: stepInput, key: "navidrome_password", secret: true, title: "Navidrome", body: "Password:"},
		{section: setupNavidrome, kind: stepTest, title: "Navidrome", body: "Connecting to Navidrome…", test: testNavidrome},
	}
	lidarrSteps := []setupStep{
		{section: setupLidarr, kind: stepAsk, key: "lidarr", gate: true, title: "Lidarr",
			body: "Do you use Lidarr?\nThen you can search artists and albums and request them from here."},
		{section: setupLidarr, kind: stepInput, key: "lidarr_url", title: "Lidarr", body: "Lidarr URL (e.g. http://lidarr.lan:8686):"},
		{section: setupLidarr, kind: stepInput, key: "lidarr_api_key", secret: true, title: "Lidarr",
			body: "API key (Lidarr › Settings › General › Security):"},
		{section: setupLidarr, kind: stepTest, title: "Lidarr", body: "Connecting to Lidarr…", test: testLidarr},
		{section: setupLidarr, kind: stepAsk, key: "flac_to_mp3", title: "Lidarr · FLAC → MP3",
			body: "Convert FLAC albums that Lidarr imports to MP3 320?\n" +
				"ffmpeg on this computer writes the MP3 next to the FLAC,\n" +
				"checks the length and moves the FLAC to the trash.\n\n" +
				"Needs: the music folder (Music library step) with write\n" +
				"access, ffmpeg + ffprobe, and gio or trash-put. Lidarr's\n" +
				"root folder must be the same library as that music folder."},
		{section: setupLidarr, kind: stepTest, title: "Lidarr · FLAC → MP3", body: "Checking ffmpeg and the music folder…", test: testFlac},
	}
	speakers := []setupStep{
		{section: setupSpeakers, kind: stepAsk, key: "speakers", gate: true, title: "Speakers · Music Assistant",
			body: "Do you use Music Assistant in Home Assistant?\nThen you can hand music to speakers in the house (> and <)."},
		{section: setupSpeakers, kind: stepInput, key: "ha_url", title: "Speakers · Home Assistant",
			body: "Home Assistant URL (e.g. http://homeassistant.lan:8123):"},
		{section: setupSpeakers, kind: stepInput, key: "ha_token", secret: true, title: "Speakers · Home Assistant",
			body: "Long-lived access token (your HA profile › Security):"},
		{section: setupSpeakers, kind: stepTest, title: "Speakers · Home Assistant", body: "Connecting to Home Assistant…", test: testHA},
		{section: setupSpeakers, kind: stepInput, key: "ma_url", optional: true, title: "Speakers · Music Assistant server",
			body: "Optional: Music Assistant server URL, needed to take a whole\n" +
				"speaker queue back (<). Empty = skip.\n\n" +
				"This is port 8095, not Home Assistant's 8123, also when Music\n" +
				"Assistant runs as a Home Assistant add-on\n" +
				"(e.g. http://homeassistant.lan:8095)."},
		{section: setupSpeakers, kind: stepInput, key: "ma_token", secret: true, title: "Speakers · Music Assistant server",
			body: "Music Assistant token (open http://<host>:8095 › Settings ›\nProfile; not the Home Assistant token):"},
		{section: setupSpeakers, kind: stepTest, title: "Speakers · Music Assistant server", body: "Connecting to Music Assistant…", test: testMA},
	}
	podcasts := []setupStep{
		{section: setupPodcasts, kind: stepAsk, key: "podcasts", gate: true, title: "Podcasts",
			body: "Do you want the podcast player too (M switches)?\nSubscriptions are stored locally; no account is needed."},
		{section: setupPodcasts, kind: stepAsk, key: "podcast_directory", title: "Podcasts · directory",
			body: "Use the Apple Podcasts directory to search and browse shows?\n" +
				"Search terms and your country go to Apple; no account is used.\n" +
				"No = add shows by RSS URL (U) or OPML import only."},
		{section: setupPodcasts, kind: stepAsk, key: "podcast_index", title: "Podcasts · Podcast Index",
			body: "Also search Podcast Index (podcastindex.org), an open podcast\n" +
				"directory? It needs your own free API key and secret:\n" +
				"sign up with your e-mail at https://api.podcastindex.org/\n" +
				"(both arrive by e-mail). Search terms go to Podcast Index.\n" +
				"In the podcast search (/), Tab switches to it."},
		{section: setupPodcasts, kind: stepInput, key: "podcast_index_key", secret: true, title: "Podcasts · Podcast Index",
			body: "API Key:"},
		{section: setupPodcasts, kind: stepInput, key: "podcast_index_secret", secret: true, title: "Podcasts · Podcast Index",
			body: "API Secret:"},
		{section: setupPodcasts, kind: stepTest, title: "Podcasts · Podcast Index", body: "Searching Podcast Index…", test: testPodcastIndex},
	}
	themeSteps := []setupStep{
		{section: setupTheme, kind: stepAsk, key: "wallpaper", gate: true, title: "Theme · wallpaper colours",
			body: "Should the colours follow your wallpaper?\n" +
				"This needs a colour file: JSON with Material colour roles\n" +
				"(primary, surface, …) that your desktop shell or matugen\n" +
				"writes when the wallpaper changes. See the README, section\n" +
				"\"Colours that follow your wallpaper\".\n" +
				"No = keep the chosen theme (change it in Settings)."},
		{section: setupTheme, kind: stepInput, key: "wallpaper_colors", title: "Theme · wallpaper colours",
			body: "Path of the colour file, e.g.\n" +
				"~/.cache/matugen/nomediaplayer.json (matugen) or\n" +
				"~/.local/state/<shell>/colors.json (a desktop shell).\n" +
				"Not sure? The README (\"Colours that follow your wallpaper\")\n" +
				"has a command that finds candidate files."},
		{section: setupTheme, kind: stepTest, title: "Theme · wallpaper colours", body: "Reading the colour file…", test: testWallpaper},
	}
	privacy := []setupStep{
		{section: setupPrivacy, kind: stepAsk, key: "lyrics_online", title: "Lyrics",
			body: "Look up missing lyrics on lrclib.net?\nArtist, title and album of the playing song are sent to lrclib.net.\nNo = only .lrc files next to your music."},
	}
	done := setupStep{section: setupAll, kind: stepInfo, key: "done", title: "Ready"}
	var steps []setupStep
	switch section {
	case setupAll:
		steps = append(steps, intro)
		steps = append(steps, library...)
		steps = append(steps, navidrome...)
		steps = append(steps, lidarrSteps...)
		steps = append(steps, speakers...)
		steps = append(steps, podcasts...)
		steps = append(steps, themeSteps...)
		steps = append(steps, privacy...)
	case setupLibrary:
		steps = library
	case setupNavidrome:
		steps = navidrome
	case setupLidarr:
		steps = lidarrSteps
	case setupSpeakers:
		steps = speakers
	case setupPodcasts:
		steps = podcasts
	case setupTheme:
		steps = themeSteps
	case setupPrivacy:
		steps = privacy
	}
	return append(steps, done)
}

// startSetup opens the wizard for all sections or one.
func (m libraryShell) startSetup(section string, firstRun bool) libraryShell {
	m.setup = &setupWizard{steps: setupSteps(section), cfg: m.s.config, off: map[string]bool{},
		touched: map[string]bool{}, firstRun: firstRun}
	if firstRun {
		// privacy first: online services start as "no" for a new user.
		m.setup.cfg.LyricsOnline, m.setup.cfg.PodcastDirectory = false, false
	}
	m.setup.enter()
	m.renderSetup()
	return m
}

// enter prepares the current step's input with the current value.
func (w *setupWizard) enter() {
	w.testErr, w.input = "", ""
	st := w.steps[w.i]
	if st.kind == stepInput {
		w.input = w.value(st.key)
	}
}

// value is the current value of a wizard key as text.
func (w *setupWizard) value(key string) string {
	switch key {
	case "mpd_address":
		return net.JoinHostPort(w.cfg.MPDHost, strconv.Itoa(w.cfg.MPDPort))
	case "music_dir":
		if w.cfg.MusicDir != "" {
			return w.cfg.MusicDir
		}
		if home, err := os.UserHomeDir(); err == nil {
			return lyrics.MusicDirFromMPD(filepath.Join(home, ".config", "mpd", "mpd.conf"), home)
		}
		return ""
	}
	if v, ok := configField(&w.cfg, key); ok {
		return fmt.Sprint(v.Interface())
	}
	return ""
}

// yes is the current answer of an ask step.
func (w *setupWizard) yes(key string) bool {
	switch key {
	case "navidrome":
		return w.cfg.NavidromeURL != ""
	case "lidarr":
		return w.cfg.LidarrURL != ""
	case "speakers":
		return w.cfg.HAURL != ""
	case "wallpaper":
		return w.cfg.Theme == theme.Wallpaper && w.cfg.WallpaperColors != ""
	case "podcast_index":
		return w.cfg.PodcastIndexKey != ""
	}
	if v, ok := configField(&w.cfg, key); ok && v.Kind() == reflect.Bool {
		return v.Bool()
	}
	return false
}

// set stores one answer in the working config and marks it for saving.
func (w *setupWizard) set(key, value string) error {
	value = strings.TrimSpace(value)
	switch key {
	case "mpd_address":
		host, port, err := net.SplitHostPort(value)
		if err != nil {
			host, port = value, "6600"
		}
		p, err := strconv.Atoi(port)
		if host == "" || err != nil || p < 1 || p > 65535 {
			return errors.New("use host:port, e.g. 127.0.0.1:6600")
		}
		w.cfg.MPDHost, w.cfg.MPDPort = host, p
		w.touched["mpd_host"], w.touched["mpd_port"] = true, true
		return nil
	case "navidrome_url", "lidarr_url", "ha_url", "ma_url":
		value = cleanServerURL(key, value)
	case "music_dir", "wallpaper_colors":
		value = theme.ExpandHome(value)
	}
	v, ok := configField(&w.cfg, key)
	if !ok {
		return fmt.Errorf("unknown setting %s", key)
	}
	switch v.Kind() {
	case reflect.Bool:
		v.SetBool(value == "true")
	default:
		v.SetString(value)
	}
	w.touched[key] = true
	return nil
}

// switchOff clears a section's keys (answered "no").
func (w *setupWizard) switchOff(section string) {
	keys := map[string][]string{
		setupNavidrome: {"navidrome_url", "navidrome_user", "navidrome_password"},
		setupLidarr:    {"lidarr_url", "lidarr_api_key", "flac_to_mp3"},
		setupSpeakers:  {"ha_url", "ha_token", "ma_url", "ma_token"},
	}[section]
	for _, k := range keys {
		_ = w.set(k, "")
	}
	if section == setupTheme {
		_ = w.set("wallpaper_colors", "")
		if w.cfg.Theme == theme.Wallpaper {
			_ = w.set("theme", "colors")
		}
	}
	if section == setupPodcasts {
		_ = w.set("podcasts", "false")
		_ = w.set("podcast_directory", "false")
		_ = w.set("podcast_index_key", "")
		_ = w.set("podcast_index_secret", "")
	}
	w.off[section] = true
}

// skipSection moves past the remaining steps of the current section.
func (w *setupWizard) skipSection() {
	sec := w.steps[w.i].section
	for w.i < len(w.steps)-1 && w.steps[w.i].section == sec {
		w.i++
	}
	w.enter()
}

func (w *setupWizard) next() {
	if w.i < len(w.steps)-1 {
		w.i++
	}
	w.enter()
}

func configField(cfg *profile.Config, key string) (reflect.Value, bool) {
	v := reflect.ValueOf(cfg).Elem()
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		if t.Field(i).Tag.Get("toml") == key {
			return v.Field(i), true
		}
	}
	return reflect.Value{}, false
}

func (m *libraryShell) renderSetup() {
	w := m.setup
	if w == nil {
		m.Model = m.Model.WithDialog(nil)
		return
	}
	st := w.steps[w.i]
	d := &ui.Dialog{Title: st.title + w.progress(), Body: st.body}
	switch st.kind {
	case stepInfo:
		if st.key == "done" {
			d.Body = w.summary()
			d.Hint = "Enter save · Esc cancel (nothing is saved)"
		} else {
			d.Hint = "Enter continue · Esc skip setup · q quit"
		}
	case stepAsk:
		def := "y/N"
		if w.yes(st.key) {
			def = "Y/n"
		}
		d.Body += "\n\n[" + def + "]"
		d.Hint = "y yes · n no · Enter keep · Esc cancel"
	case stepInput:
		d.InputLabel, d.InputValue = "Value", w.input
		if st.secret {
			d.InputValue = strings.Repeat("•", len([]rune(w.input)))
		}
		d.Hint = "Enter next · Esc cancel · Ctrl+U clear · paste works"
	case stepTest:
		switch {
		case w.testing:
			d.Hint = "Testing…"
		case w.testErr != "":
			d.Body = "✗ " + w.testErr
			d.Hint = "e edit again · k keep anyway · s skip this service · Esc cancel"
		}
	}
	m.Model = m.Model.WithDialog(d)
}

// progress is "step 3 of 7", counted per section (welcome and the
// summary are steps of their own).
func (w *setupWizard) progress() string {
	group, current := 0, 0
	for i, st := range w.steps {
		if i == 0 || st.section != w.steps[i-1].section || i == len(w.steps)-1 {
			group++
		}
		if i == w.i {
			current = group
		}
	}
	if group <= 1 {
		return ""
	}
	return fmt.Sprintf(" · step %d of %d", current, group)
}

// summary lists what will be saved.
func (w *setupWizard) summary() string {
	c := w.cfg
	state := func(on bool, detail string) string {
		if !on {
			return "off"
		}
		return "on · " + detail
	}
	var b strings.Builder
	b.WriteString("This is saved in ~/.config/" + profile.AppName + "/config.toml (only you can read it):\n\n")
	fmt.Fprintf(&b, "%-12s %s\n", "MPD", net.JoinHostPort(c.MPDHost, strconv.Itoa(c.MPDPort)))
	if c.MusicDir != "" {
		fmt.Fprintf(&b, "%-12s %s\n", "Music folder", c.MusicDir)
	}
	fmt.Fprintf(&b, "%-12s %s\n", "Navidrome", state(c.NavidromeURL != "", c.NavidromeURL))
	fmt.Fprintf(&b, "%-12s %s\n", "Lidarr", state(c.LidarrURL != "", lidarrDetail(c)))
	fmt.Fprintf(&b, "%-12s %s\n", "Speakers", state(c.HAURL != "", c.HAURL))
	fmt.Fprintf(&b, "%-12s %s\n", "Podcasts", state(c.Podcasts, podcastDetail(c)))
	fmt.Fprintf(&b, "%-12s %s\n", "Theme", themeDetail(c))
	fmt.Fprintf(&b, "%-12s %s\n", "Lyrics", map[bool]string{true: ".lrc + lrclib.net", false: ".lrc files only"}[c.LyricsOnline])
	b.WriteString("\nChange it any time: ? → Setup.")
	return b.String()
}

func (m libraryShell) setupKey(msg tea.KeyPressMsg) (libraryShell, tea.Cmd) {
	w := m.setup
	st := w.steps[w.i]
	key := msg.String()
	// q quits (nothing saved; on a first start the wizard comes back), except
	// in a text field where q is just a letter.
	if key == "ctrl+c" || (key == "q" && st.kind != stepInput) {
		return m, tea.Quit
	}
	if key == "esc" {
		return m.finishSetup(false)
	}
	switch st.kind {
	case stepInfo:
		if key == "enter" {
			if st.key == "done" {
				return m.finishSetup(true)
			}
			w.next()
		}
	case stepAsk:
		answer := w.yes(st.key)
		switch key {
		case "y", "Y":
			answer = true
		case "n", "N":
			answer = false
		case "enter":
		default:
			return m, nil
		}
		if st.gate {
			if !answer {
				w.switchOff(st.section)
				w.skipSection()
				break
			}
			if st.key == "podcasts" {
				_ = w.set("podcasts", "true")
			}
			if st.key == "wallpaper" {
				_ = w.set("theme", theme.Wallpaper)
			}
			w.next()
			break
		}
		if st.key == "podcast_index" {
			if !answer {
				_ = w.set("podcast_index_key", "")
				_ = w.set("podcast_index_secret", "")
				w.skipSection() // the Podcast Index steps end the section
				break
			}
			w.next()
			break
		}
		_ = w.set(st.key, strconv.FormatBool(answer))
		w.next()
	case stepInput:
		switch key {
		case "enter":
			if err := w.set(st.key, w.input); err != nil {
				w.testErr = err.Error()
				m.notice = err.Error()
				m.renderStatus()
				break
			}
			if st.optional && strings.TrimSpace(w.input) == "" {
				if st.key == "ma_url" {
					_ = w.set("ma_token", "")
				}
				w.skipSection()
				break
			}
			w.next()
		default:
			ti := ui.TextInput{Value: w.input, Limit: 500}
			ti.EditKey(msg)
			w.input = ti.Value
		}
	case stepTest:
		if w.testing {
			return m, nil
		}
		switch key {
		case "e":
			for w.i > 0 && w.steps[w.i].kind != stepInput {
				w.i--
			}
			// back to the first input of this check
			for w.i > 0 && w.steps[w.i-1].kind == stepInput && w.steps[w.i-1].section == st.section {
				w.i--
			}
			w.enter()
		case "k", "enter":
			w.next()
		case "s":
			if st.section == setupPodcasts {
				// skipping Podcast Index keeps the podcasts on
				_ = w.set("podcast_index_key", "")
				_ = w.set("podcast_index_secret", "")
			} else {
				w.switchOff(st.section)
			}
			w.skipSection()
		}
	}
	m.renderSetup()
	return m, m.setupRunTest()
}

func (m *libraryShell) setupPaste(text string) {
	if w := m.setup; w != nil && w.steps[w.i].kind == stepInput {
		ti := ui.TextInput{Value: w.input, Limit: 500}
		ti.Append(strings.TrimSpace(text))
		w.input = ti.Value
		m.renderSetup()
	}
}

// setupRunTest starts the check of a test step that was just reached.
func (m libraryShell) setupRunTest() tea.Cmd {
	w := m.setup
	if w == nil || w.testing || w.testErr != "" {
		return nil
	}
	st := w.steps[w.i]
	if st.kind != stepTest || st.test == nil {
		return nil
	}
	w.testing = true
	m.renderSetup()
	cfg, step := w.cfg, w.i
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(m.s.ctx, 8*time.Second)
		defer cancel()
		return setupTested{step: step, err: st.test(ctx, cfg)}
	}
}

func (m libraryShell) updateSetup(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	switch msg := msg.(type) {
	case ui.SetupRequest:
		if m.setup == nil {
			m = m.startSetup(msg.Section, false)
			return m, m.setupRunTest(), true
		}
		return m, nil, true
	case setupTested:
		w := m.setup
		if w == nil || w.i != msg.step {
			return m, nil, true
		}
		w.testing = false
		if msg.err != nil {
			w.testErr = msg.err.Error()
		} else {
			w.next()
		}
		m.renderSetup()
		return m, m.setupRunTest(), true
	case tea.KeyPressMsg:
		if m.setup == nil {
			return m, nil, false
		}
		updated, cmd := m.setupKey(msg)
		return updated, cmd, true
	case tea.PasteMsg:
		if m.setup == nil {
			return m, nil, false
		}
		m.setupPaste(msg.Content)
		return m, nil, true
	}
	return m, nil, false
}

// finishSetup saves (or drops) the answers and applies them right away.
func (m libraryShell) finishSetup(save bool) (libraryShell, tea.Cmd) {
	w := m.setup
	m.setup = nil
	m.Model = m.Model.WithDialog(nil)
	_ = m.s.paths.SetOnboarded()
	if !save {
		if w.firstRun {
			// keep a new user private: online lookups stay off until chosen.
			_ = profile.SetKeys(m.s.paths.Config, []profile.Setting{{Key: "podcast_directory", Value: false}, {Key: "lyrics_online", Value: false}})
			m.s.config.PodcastDirectory, m.s.config.LyricsOnline = false, false
			m.Model = m.Model.WithSetupRows(setupRows(m.s.config))
			m.applyHints()
			m.notice = "Setup skipped · online services stay off · ? → Setup to set things up"
			m.renderStatus()
			return m, m.applyIntegrations()
		}
		m.notice = "Setup cancelled · nothing changed · ? → Setup to run it later"
		m.renderStatus()
		return m, nil
	}
	var settings []profile.Setting
	for _, key := range profile.Keys() {
		if !w.touched[key] {
			continue
		}
		v, _ := configField(&w.cfg, key)
		settings = append(settings, profile.Setting{Key: key, Value: v.Interface()})
	}
	if err := profile.SetKeys(m.s.paths.Config, settings); err != nil {
		m.notice = "Saving the settings failed: " + err.Error()
		m.renderStatus()
		return m, nil
	}
	mpdChanged := w.cfg.MPDHost != m.s.config.MPDHost || w.cfg.MPDPort != m.s.config.MPDPort
	themeChanged := w.cfg.Theme != m.s.config.Theme || w.cfg.WallpaperColors != m.s.config.WallpaperColors
	m.s.config = w.cfg
	m.Model = m.Model.WithSetupRows(setupRows(w.cfg))
	m.applyHints()
	m.notice = "Settings saved"
	if mpdChanged {
		m.notice += " · restart nomediaplayer to use the new MPD address"
	}
	cmd := m.applyIntegrations()
	if themeChanged {
		cmd = tea.Batch(cmd, m.applyTheme())
	}
	return m, cmd
}

// applyTheme loads the configured theme (after the wizard changed it).
func (m *libraryShell) applyTheme() tea.Cmd {
	theme.SetWallpaperFile(m.s.config.WallpaperColors)
	m.s.wallpaperMod = time.Time{}
	name, dir := m.s.config.Theme, m.s.paths.ConfigDir()
	return func() tea.Msg {
		pal, path := theme.Load(dir, name)
		return themeReloadMsg{palette: pal, themeName: name, themePath: path}
	}
}

// applyIntegrations rebuilds the service clients from the current config.
func (m *libraryShell) applyIntegrations() tea.Cmd {
	cfg, ms := m.s.config, m.music
	indexKey, indexSecret := discovery.IndexCredentials(cfg.PodcastIndexKey, cfg.PodcastIndexSecret, os.Getenv)
	m.browse.indexDirectory = discovery.NewIndex(indexKey, indexSecret, nil)
	if ms == nil {
		return nil
	}
	ms.nd = newND(cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePassword)
	ms.lidarr = newLidarrState(cfg.LidarrURL, cfg.LidarrAPIKey)
	ms.speakers = newSpeakerState(cfg.HAURL, cfg.HAToken, cfg.MAURL, cfg.MAToken)
	ms.lyrics = newLyricsState(cfg.MusicDir, m.s.paths.State, cfg.LyricsOnline)
	if ms.flac == nil || !cfg.FlacToMP3 {
		ms.flac = newFlacState(cfg.FlacToMP3, m.s.paths.State)
	}
	if !ms.viewEnabled(ms.libView) {
		ms.libView = viewArtists
	}
	if !cfg.Podcasts && !ms.active {
		updated, cmd := m.toggleMusic()
		*m = updated
		m.renderMusic()
		return tea.Batch(cmd, m.ndLoadLists())
	}
	if ms.active {
		m.renderMusic()
	} else {
		m.renderLibrary()
	}
	m.renderStatus()
	return tea.Batch(m.ndLoadLists(), m.flacCheck())
}

// --- checks -----------------------------------------------------------------

func testMPD(ctx context.Context, cfg profile.Config) error {
	addr := net.JoinHostPort(cfg.MPDHost, strconv.Itoa(cfg.MPDPort))
	if _, err := mpd.New(addr).Status(ctx); err != nil {
		return fmt.Errorf("MPD at %s does not answer (%v).\nIs MPD installed and running? (systemctl --user start mpd)", addr, err)
	}
	return nil
}

func testMusicDir(_ context.Context, cfg profile.Config) error {
	if cfg.MusicDir == "" {
		return nil
	}
	st, err := os.Stat(cfg.MusicDir)
	if err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a folder on this computer", cfg.MusicDir)
	}
	return nil
}

func testFlac(_ context.Context, cfg profile.Config) error {
	if !cfg.FlacToMP3 {
		return nil
	}
	if _, err := flacconv.Find(); err != nil {
		return errors.New("ffmpeg and ffprobe are needed (install ffmpeg)")
	}
	_, gio := exec.LookPath("gio")
	_, trashPut := exec.LookPath("trash-put")
	if gio != nil && trashPut != nil {
		return errors.New("gio or trash-put is needed to move the FLAC to the trash\n(install glib2 or trash-cli)")
	}
	dir := cfg.MusicDir
	if dir == "" {
		if home, err := os.UserHomeDir(); err == nil {
			dir = lyrics.MusicDirFromMPD(filepath.Join(home, ".config", "mpd", "mpd.conf"), home)
		}
	}
	if dir == "" {
		return errors.New("the music folder is unknown: set it in the Music library step")
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("%s is not a folder on this computer", dir)
	}
	return nil
}

func testPodcastIndex(ctx context.Context, cfg profile.Config) error {
	if cfg.PodcastIndexKey == "" || cfg.PodcastIndexSecret == "" {
		return errors.New("both the API key and the API secret are needed")
	}
	if err := podcastIndexCheck(ctx, cfg.PodcastIndexKey, cfg.PodcastIndexSecret); err != nil {
		return fmt.Errorf("Podcast Index: %v", err)
	}
	return nil
}

// podcastIndexCheck runs one small search (replaced in tests).
var podcastIndexCheck = func(ctx context.Context, key, secret string) error {
	_, err := discovery.NewIndex(key, secret, nil).Search(ctx, "podcast")
	return err
}

func testWallpaper(_ context.Context, cfg profile.Config) error {
	if cfg.WallpaperColors == "" {
		return errors.New("no colour file given")
	}
	if _, err := os.Stat(cfg.WallpaperColors); err != nil {
		return fmt.Errorf("%s does not exist (yet)", cfg.WallpaperColors)
	}
	if _, ok := theme.ReadWallpaperPalette(cfg.WallpaperColors); !ok {
		return errors.New("this file has no Material colours: it needs at least\n\"primary\" and \"surface\" (pywal's colors.json is not supported)")
	}
	return nil
}

func testNavidrome(ctx context.Context, cfg profile.Config) error {
	if err := subsonic.New(cfg.NavidromeURL, cfg.NavidromeUser, cfg.NavidromePassword).Ping(ctx); err != nil {
		return fmt.Errorf("Navidrome: %v", err)
	}
	return nil
}

func testLidarr(ctx context.Context, cfg profile.Config) error {
	c, err := lidarr.New(cfg.LidarrURL, cfg.LidarrAPIKey)
	if err == nil {
		err = c.Ping(ctx)
	}
	if err != nil {
		return fmt.Errorf("Lidarr: %v", err)
	}
	return nil
}

func testHA(ctx context.Context, cfg profile.Config) error {
	c, err := homeassistant.New(cfg.HAURL, cfg.HAToken)
	if err == nil {
		err = c.Ping(ctx)
	}
	if err != nil {
		return fmt.Errorf("Home Assistant: %v", err)
	}
	return nil
}

func testMA(ctx context.Context, cfg profile.Config) error {
	if cfg.MAURL == "" {
		return nil
	}
	c, err := musicassistant.New(cfg.MAURL, cfg.MAToken)
	if err == nil {
		err = c.Ping(ctx)
	}
	if err != nil {
		return fmt.Errorf("Music Assistant: %v", err)
	}
	return nil
}

// applyHints hides the key hints of services that are off.
func (m *libraryShell) applyHints() {
	cfg := m.s.config
	var hide []string
	if cfg.HAURL == "" {
		hide = append(hide, ">/<")
	}
	if !cfg.Podcasts {
		hide = append(hide, "M")
	}
	if cfg.NavidromeURL == "" {
		hide = append(hide, "b", "x")
	}
	m.Model = m.Model.WithHiddenHints(hide...)
}

// setupRows is the status table on the help's Setup page.
func setupRows(cfg profile.Config) [][2]string {
	state := func(on bool, detail string) string {
		if !on {
			return "off"
		}
		return "on · " + detail
	}
	mpdLine := net.JoinHostPort(cfg.MPDHost, strconv.Itoa(cfg.MPDPort))
	if cfg.MusicDir != "" {
		mpdLine += " · " + cfg.MusicDir
	}
	dir := podcastDetail(cfg)
	lyr := ".lrc only"
	if cfg.LyricsOnline {
		lyr = ".lrc + lrclib.net"
	}
	return [][2]string{
		{"a", "Run the whole setup again"},
		{"m", "Music library (MPD)     " + mpdLine},
		{"v", "Navidrome               " + state(cfg.NavidromeURL != "", cfg.NavidromeURL)},
		{"r", "Lidarr                  " + state(cfg.LidarrURL != "", lidarrDetail(cfg))},
		{"s", "Speakers (HA + MA)      " + state(cfg.HAURL != "", cfg.HAURL)},
		{"c", "Podcasts                " + state(cfg.Podcasts, dir)},
		{"w", "Theme                   " + themeDetail(cfg)},
		{"o", "Online lyrics           " + lyr},
	}
}

// cleanServerURL turns what people paste (often the browser's address bar,
// e.g. http://host:4533/app/#/album/…) into the server address: http:// is
// added when missing, query and fragment are dropped, and the web app's own
// path is removed (Navidrome /app, Home Assistant /lovelace…, Lidarr pages).
func cleanServerURL(key, value string) string {
	if value == "" {
		return ""
	}
	if !strings.HasPrefix(value, "http://") && !strings.HasPrefix(value, "https://") {
		value = "http://" + value
	}
	u, err := url.Parse(value)
	if err != nil || u.Host == "" {
		return strings.TrimRight(value, "/")
	}
	u.RawQuery, u.Fragment, u.RawFragment = "", "", ""
	path := strings.TrimRight(u.Path, "/")
	switch key {
	case "navidrome_url":
		if i := strings.Index(path+"/", "/app/"); i >= 0 {
			path = path[:i]
		}
	case "ha_url", "lidarr_url":
		path = "" // both are served from the root
	}
	u.Path, u.RawPath = path, ""
	return strings.TrimRight(u.String(), "/")
}

func lidarrDetail(cfg profile.Config) string {
	if cfg.FlacToMP3 {
		return cfg.LidarrURL + " · FLAC → MP3"
	}
	return cfg.LidarrURL
}

func themeDetail(cfg profile.Config) string {
	name := cfg.Theme
	if name == "" {
		name = "colors"
	}
	if name == theme.Wallpaper {
		return "follows the wallpaper · " + cfg.WallpaperColors
	}
	return name + " (change it in Settings)"
}

func podcastDetail(cfg profile.Config) string {
	var parts []string
	if cfg.PodcastDirectory {
		parts = append(parts, "Apple directory")
	}
	if cfg.PodcastIndexKey != "" {
		parts = append(parts, "Podcast Index")
	}
	if len(parts) == 0 {
		return "RSS/OPML only"
	}
	return strings.Join(parts, " + ")
}
