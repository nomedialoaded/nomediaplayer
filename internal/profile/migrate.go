//go:build linux || darwin

package profile

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/nomedialoaded/nomediaplayer/internal/feedback"
)

// ErrLegacyRunning: the old (podmutt-named) TUI still owns its profile.
var ErrLegacyRunning = feedback.New(feedback.AlreadyRunning, "nomediaplayer: an older version is still running (podmutt profile). Close it first; the new version then moves your settings and history over.")

// MigrateLegacy moves the podmutt-named config and state directories to the
// nomediaplayer names, once: only when the new directory does not exist yet
// and the old TUI is not running. It never overwrites or merges.
func MigrateLegacy(platform, home string, getenv func(string) string) error {
	if IsVariant() {
		return nil // a test build never touches the real (or podmutt) profile
	}
	oldP, err := resolveNamed(LegacyAppName, platform, home, getenv)
	if err != nil {
		return err
	}
	newP, err := resolveNamed(AppName, platform, home, getenv)
	if err != nil {
		return err
	}
	moves := [][2]string{{oldP.State, newP.State}}
	if oldP.ConfigDir() != oldP.State {
		moves = append(moves, [2]string{oldP.ConfigDir(), newP.ConfigDir()})
	}
	var pending [][2]string
	for _, mv := range moves {
		if exists(mv[0]) && !exists(mv[1]) {
			pending = append(pending, mv)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	if exists(oldP.State) && exists(oldP.Runtime) {
		lock, err := oldP.Acquire()
		if errors.Is(err, ErrAlreadyRunning) {
			return ErrLegacyRunning
		}
		if err == nil {
			defer lock.Close()
		}
	}
	for _, mv := range pending {
		if err := os.MkdirAll(filepath.Dir(mv[1]), 0o700); err != nil {
			return err
		}
		if err := os.Rename(mv[0], mv[1]); err != nil {
			return fmt.Errorf("profile: moving %s to %s: %w", mv[0], mv[1], err)
		}
	}
	return nil
}

func exists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}
