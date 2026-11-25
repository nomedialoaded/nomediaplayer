package app

// nml: follow wallpaper changes live while theme = "wallpaper".

import (
	"context"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/theme"
)

const wallpaperPoll = 3 * time.Second

type wallpaperTick struct{ mod time.Time }

func wallpaperMod() time.Time {
	info, err := os.Stat(theme.WallpaperColorsPath())
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func (m libraryShell) watchWallpaper() tea.Cmd {
	ctx := m.s.ctx
	return func() tea.Msg {
		select {
		case <-time.After(wallpaperPoll):
			return wallpaperTick{mod: wallpaperMod()}
		case <-ctx.Done():
			return nil
		}
	}
}

func (m libraryShell) updateWallpaper(msg tea.Msg) (libraryShell, tea.Cmd, bool) {
	tick, ok := msg.(wallpaperTick)
	if !ok {
		return m, nil, false
	}
	changed := !tick.mod.Equal(m.s.wallpaperMod)
	m.s.wallpaperMod = tick.mod
	if !changed || m.s.config.Theme != theme.Wallpaper {
		return m, m.watchWallpaper(), true
	}
	paths, ctx := m.s.paths, m.s.ctx
	return m, tea.Batch(m.watchWallpaper(), work(ctx, "Theme follows the wallpaper…", func(context.Context) tea.Msg {
		pal, path := theme.Load(paths.ConfigDir(), theme.Wallpaper)
		return themeReloadMsg{palette: pal, themeName: theme.Wallpaper, themePath: path, reload: true}
	})), true
}
