package theme

// nml: "wallpaper" theme. Reads a JSON file with Material colour roles that a
// wallpaper tool (matugen, a desktop shell, …) generates from the current
// wallpaper, so the player follows the desktop colours. The file is set with
// wallpaper_colors; keys may be camelCase (onSurface) or snake_case
// (on_surface), values "#rrggbb" or "rrggbb".

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const Wallpaper = "wallpaper"

var wallpaperFile string

// SetWallpaperFile sets the colour file (wallpaper_colors); "~/" is expanded.
func SetWallpaperFile(path string) { wallpaperFile = ExpandHome(path) }

// ExpandHome turns a leading "~/" into the home directory.
func ExpandHome(path string) string {
	path = strings.TrimSpace(path)
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

// WallpaperColorsPath is the configured colour file ("" = none).
func WallpaperColorsPath() string { return wallpaperFile }

// WallpaperPalette maps the Material scheme onto a Palette. ok is false when
// no file is set or it is missing or unreadable.
func WallpaperPalette() (Palette, bool) {
	return ReadWallpaperPalette(wallpaperFile)
}

// ReadWallpaperPalette reads one colour file (also used by the setup check).
func ReadWallpaperPalette(path string) (Palette, bool) {
	if path == "" {
		return Palette{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Palette{}, false
	}
	var raw map[string]any
	if json.Unmarshal(data, &raw) != nil {
		return Palette{}, false
	}
	raw = colourRoles(raw)
	c := map[string]string{}
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if !strings.HasPrefix(s, "#") {
			s = "#" + s
		}
		if (len(s) != 7 && len(s) != 9) || !isHex(s[1:]) {
			continue
		}
		c[camel(k)] = s[:7]
	}
	if c["primary"] == "" || c["surface"] == "" {
		return Palette{}, false
	}
	get := func(key, fallback string) Color {
		if v := strings.TrimSpace(c[key]); strings.HasPrefix(v, "#") {
			return Color(v)
		}
		return Color(c[fallback])
	}
	mode := "dark"
	if luminance(c["surface"]) > 0.5 {
		mode = "light"
	}
	primary, secondary, tertiary := get("primary", "primary"), get("secondary", "primary"), get("tertiary", "secondary")
	return Palette{
		Mode:              mode,
		Accent:            primary,
		Selection:         get("surfaceContainerHighest", "surface"),
		Muted:             get("outline", "onSurfaceVariant"),
		Background:        get("surface", "surface"),
		DarkBackground:    get("surfaceContainer", "surface"),
		DarkerBackground:  get("surface", "surface"),
		LighterBackground: get("surfaceContainerHigh", "surface"),
		Foreground:        get("onSurface", "onSurface"),
		DarkForeground:    get("onSurfaceVariant", "onSurface"),
		LightForeground:   get("onSurface", "onSurface"),
		BrightForeground:  get("onSurface", "onSurface"),
		Red:               get("error", "primary"),
		Yellow:            tertiary,
		Orange:            tertiary,
		Green:             primary,
		Cyan:              secondary,
		Blue:              get("onPrimaryContainer", "primary"),
		Magenta:           primary,
		Brown:             get("onSecondaryContainer", "secondary"),
		BrightRed:         get("error", "primary"),
		BrightYellow:      tertiary,
		BrightGreen:       primary,
		BrightCyan:        secondary,
		BrightBlue:        get("onPrimaryContainer", "primary"),
		BrightMagenta:     primary,
	}, true
}

// colourRoles finds the object with the colour roles: the file itself, or
// one level down under "colors"/"colours" (several desktop shells write
// {"name": …, "colours": {…}}), and when that holds "dark" and "light"
// schemes, the one named by "mode" (default dark).
func colourRoles(raw map[string]any) map[string]any {
	if _, ok := raw["primary"].(string); ok {
		return raw
	}
	for _, key := range []string{"colors", "colours"} {
		inner, ok := raw[key].(map[string]any)
		if !ok {
			continue
		}
		if _, ok := inner["primary"].(string); ok {
			return inner
		}
		mode, _ := raw["mode"].(string)
		if mode != "light" {
			mode = "dark"
		}
		if scheme, ok := inner[mode].(map[string]any); ok {
			return scheme
		}
	}
	return raw
}

func isHex(s string) bool {
	for _, r := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", r) {
			return false
		}
	}
	return s != ""
}

// camel turns on_surface into onSurface.
func camel(key string) string {
	parts := strings.Split(key, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] != "" {
			parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
		}
	}
	return strings.Join(parts, "")
}

// luminance of "#rrggbb", 0 (black) to 1 (white).
func luminance(hex string) float64 {
	if len(hex) < 7 {
		return 0
	}
	ch := func(i int) float64 {
		v, _ := strconv.ParseUint(hex[i:i+2], 16, 8)
		return float64(v) / 255
	}
	return 0.2126*ch(1) + 0.7152*ch(3) + 0.0722*ch(5)
}
