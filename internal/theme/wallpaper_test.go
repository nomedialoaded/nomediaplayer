package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWallpaperPalette(t *testing.T) {
	t.Cleanup(func() { SetWallpaperFile("") })
	SetWallpaperFile("")
	if _, ok := WallpaperPalette(); ok {
		t.Fatal("no wallpaper_colors must not give a palette")
	}
	for _, name := range Available("") {
		if name == Wallpaper {
			t.Fatal("wallpaper theme is only offered with wallpaper_colors set")
		}
	}
	file := filepath.Join(t.TempDir(), "colors.json")
	SetWallpaperFile(file)
	if p, _ := Load("", Wallpaper); p.Accent != Catppuccin().Accent {
		t.Fatal("missing colour file falls back to catppuccin")
	}
	os.WriteFile(file, []byte(`{"primary":"#ffb2b9","secondary":"#e5bdbf","tertiary":"#e8c08e","surface":"#1a1112","onSurface":"#f0dedf","outline":"#9f8c8d","error":"#ffb4ab"}`), 0o644)
	p, path := Load("", Wallpaper)
	if path != file || p.Accent != "#ffb2b9" || p.Background != "#1a1112" || p.Muted != "#9f8c8d" || p.Red != "#ffb4ab" || p.Mode != "dark" {
		t.Fatalf("palette %+v", p)
	}
	// Missing roles fall back to a related colour, never empty.
	if p.Selection != "#1a1112" || p.Blue != "#ffb2b9" {
		t.Fatalf("fallbacks %+v", p)
	}
	offered := false
	for _, name := range Available("") {
		offered = offered || name == Wallpaper
	}
	if !offered {
		t.Fatal("wallpaper theme must be offered with wallpaper_colors set")
	}
}

func TestWallpaperPaletteSnakeCaseAndLight(t *testing.T) {
	file := filepath.Join(t.TempDir(), "scheme.json")
	os.WriteFile(file, []byte(`{"primary":"6750a4","surface":"fef7ff","on_surface":"1d1b20","surface_container_highest":"e6e0e9","name":"x","n":3}`), 0o644)
	p, ok := ReadWallpaperPalette(file)
	if !ok || p.Accent != "#6750a4" || p.Foreground != "#1d1b20" || p.Selection != "#e6e0e9" || p.Mode != "light" {
		t.Fatalf("palette %+v ok=%v", p, ok)
	}
	os.WriteFile(file, []byte(`{"surface":"#000000"}`), 0o644)
	if _, ok := ReadWallpaperPalette(file); ok {
		t.Fatal("a file without primary is not a colour scheme")
	}
}

func TestExpandHome(t *testing.T) {
	home, _ := os.UserHomeDir()
	if got := ExpandHome("~/x/colors.json"); got != filepath.Join(home, "x", "colors.json") {
		t.Fatal(got)
	}
	if got := ExpandHome(" /abs "); got != "/abs" {
		t.Fatal(got)
	}
}

func TestWallpaperPaletteNested(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"colours": `{"name":"dynamic","mode":"dark","colours":{"primary":"ffb2b9","surface":"1a1112","onSurface":"f0dedf"}}`,
		"colors":  `{"colors":{"primary":"#ffb2b9","surface":"#1a1112"}}`,
		"modes":   `{"mode":"light","colors":{"dark":{"primary":"#000001","surface":"#000000"},"light":{"primary":"#ffb2b9","surface":"#1a1112"}}}`,
	}
	for name, body := range cases {
		file := filepath.Join(dir, name+".json")
		os.WriteFile(file, []byte(body), 0o644)
		p, ok := ReadWallpaperPalette(file)
		if !ok || p.Accent != "#ffb2b9" || p.Background != "#1a1112" {
			t.Errorf("%s: %+v ok=%v", name, p, ok)
		}
	}
	file := filepath.Join(dir, "bad.json")
	os.WriteFile(file, []byte(`{"primary":"zzzzzz","surface":"#1a1112"}`), 0o644)
	if _, ok := ReadWallpaperPalette(file); ok {
		t.Error("non-hex values are not colours")
	}
}
