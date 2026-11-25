package app

import (
	"context"
	"errors"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/nomedialoaded/nomediaplayer/internal/activity"
	"github.com/nomedialoaded/nomediaplayer/internal/control"
	"github.com/nomedialoaded/nomediaplayer/internal/macmedia"
	"github.com/nomedialoaded/nomediaplayer/internal/mpris"
	"github.com/nomedialoaded/nomediaplayer/internal/playback"
	"github.com/nomedialoaded/nomediaplayer/internal/player"
	"github.com/nomedialoaded/nomediaplayer/internal/profile"
	"github.com/nomedialoaded/nomediaplayer/internal/store"
	"github.com/nomedialoaded/nomediaplayer/internal/theme"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

type session struct {
	ctx         context.Context
	config      profile.Config
	paths       profile.Paths
	store       *store.Store
	playback    *playback.Engine
	nativeMedia *macmedia.Service
	media       *mpris.Service
	mediaError  error
	theme       theme.Palette
	// nml: routes player actions to MPD while music is the audible source.
	musicRoute func(ui.Action) tea.Cmd
	// nml: last seen mtime of the wallpaper colour file.
	wallpaperMod time.Time
}

// withSession runs under withProfile ownership. All foreground services share
// this database, actor, and cancellation lifetime; no client creates its own.
func withSession(cfg profile.Config, paths profile.Paths, run func(*session) error) (err error) {
	ctx, cancel := context.WithCancel(activity.WithTracker(context.Background(), activity.New()))
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(paths.State, "podcasts.db"))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, db.Close()) }()
	engine, err := playback.New(ctx, db, func(ctx context.Context) (playback.Player, error) { return player.Start(ctx, cfg.MPVPath) })
	if err != nil {
		return err
	}
	defer func() { cancel(); err = errors.Join(err, engine.Close(context.Background())) }()
	server, err := control.Listen(ctx, paths, engine)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, server.Close()) }()
	media, mediaError := mpris.Start(ctx, engine)
	if media != nil {
		defer media.Close()
	}
	native, nativeError := macmedia.Start(ctx, engine)
	if native != nil {
		defer native.Close()
	}
	theme.SetWallpaperFile(cfg.WallpaperColors)
	pal, _ := theme.Load(paths.ConfigDir(), cfg.Theme)
	return run(&session{ctx: ctx, config: cfg, paths: paths, store: db, playback: engine, media: media, nativeMedia: native, mediaError: errors.Join(mediaError, nativeError), theme: pal})
}

func (s *session) settingsData() ui.SettingsData {
	_, activeThemePath := theme.Load(s.paths.ConfigDir(), s.config.Theme)
	return ui.SettingsData{
		ActiveTheme: s.config.Theme,
		Themes:      theme.Available(s.paths.ConfigDir()),
		ConfigPath:  s.paths.Config,
		ThemePath:   activeThemePath,
		MPVPath:     s.config.MPVPath,
		RefreshMin:  s.config.RefreshMinutes,
		Icons:       s.config.Icons,
		Country:     s.config.Country,
	}
}
