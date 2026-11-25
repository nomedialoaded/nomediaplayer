#!/usr/bin/env bash
# nml: build the fork and install it as ~/.local/bin/nomediaplayer
# for the current user, with launcher entry and icon. Usage:
#   ./install-nml.sh           install or update, settings stay
#   ./install-nml.sh --clean   clean install: settings and local data removed first
# --clean: first remove all settings and local data (config, podcasts,
# history, recently played, runtime files). Servers (MPD, Navidrome, Lidarr)
# are not touched. Files go to the trash when gio or trash-put is available.
set -euo pipefail
cd "$(dirname "$0")"
if [ "${1:-}" = "--clean" ]; then
	if pgrep -x nomediaplayer >/dev/null; then
		echo "nomediaplayer is running; quit it first (q)" >&2
		exit 1
	fi
	for d in "${XDG_CONFIG_HOME:-$HOME/.config}/nomediaplayer" \
		"${XDG_STATE_HOME:-$HOME/.local/state}/nomediaplayer"; do
		[ -e "$d" ] || continue
		if command -v gio >/dev/null && gio trash "$d" 2>/dev/null; then :
		elif command -v trash-put >/dev/null && trash-put "$d" 2>/dev/null; then :
		else command rm -rf "$d"; fi
		echo "removed $d"
	done
	# lock and socket only (tmpfs, no trash)
	[ -n "${XDG_RUNTIME_DIR:-}" ] && command rm -rf "$XDG_RUNTIME_DIR/nomediaplayer"
fi
VERSION="${VERSION:-1.2.0}"
BIN="$HOME/.local/bin/nomediaplayer"
APPS="$HOME/.local/share/applications"
ICONS="$HOME/.local/share/icons/hicolor/scalable/apps"
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o bin/nomediaplayer ./cmd/podmutt
install -Dm755 bin/nomediaplayer "$BIN"
install -Dm644 packaging/nml/nomediaplayer.svg "$ICONS/nomediaplayer.svg"
sed "s|@BIN@|$BIN|g" packaging/nml/nomediaplayer.desktop > "$APPS/nomediaplayer.desktop"
command -v update-desktop-database >/dev/null && update-desktop-database "$APPS" || true
echo "nomediaplayer $VERSION -> $BIN"
