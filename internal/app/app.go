// Package app owns the foreground session and releases profile ownership after
// the terminal program exits, including initialization and rendering failures.
package app

import (
	"errors"
	"io"
	"os"
	"runtime"

	"github.com/nomedialoaded/nomediaplayer/internal/feedback"
	"github.com/nomedialoaded/nomediaplayer/internal/profile"
	"github.com/nomedialoaded/nomediaplayer/internal/theme"
)

var Version = "dev"

func SetVersion(v string) {
	if v != "" {
		Version = v
	}
}

func Run(input io.Reader, output io.Writer) error {
	return withProfile(func(cfg profile.Config, paths profile.Paths) error {
		return withSession(cfg, paths, func(s *session) error {
			return runLibrary(s, input, output)
		})
	})
}

func withProfile(run func(profile.Config, profile.Paths) error) (err error) {
	// nml: move a podmutt-named profile to nomediaplayer once (TUI only).
	if home, herr := os.UserHomeDir(); herr == nil {
		if err := profile.MigrateLegacy(runtime.GOOS, home, os.Getenv); err != nil {
			return err
		}
	}
	paths, err := userPaths()
	if err != nil {
		return err
	}
	cfg, err := profile.Load(paths.Config)
	if err != nil {
		return err
	}
	if err := paths.Prepare(); err != nil {
		return feedback.Wrap(feedback.Profile, err)
	}
	owner, err := paths.Acquire()
	if err != nil {
		if errors.Is(err, profile.ErrAlreadyRunning) {
			return err
		}
		return feedback.Wrap(feedback.Profile, err)
	}
	defer func() { err = errors.Join(err, owner.Close()) }()
	_ = theme.EnsureFiles(paths.ConfigDir())
	return run(cfg, paths)
}
