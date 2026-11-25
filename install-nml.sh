#!/usr/bin/env bash
# nml: build the fork and install it as ~/.local/bin/nomediaplayer
# for the current user, with launcher entry and icon (Linux). Works on
# macOS too (no launcher entry; Now Playing needs the Xcode Command Line
# Tools). Usage:
#   ./install-nml.sh           install or update, settings stay
#   ./install-nml.sh --clean   clean install: settings and local data removed first
# --clean: first remove all settings and local data (config, podcasts,
# history, recently played, runtime files). Servers (MPD, Navidrome, Lidarr)
# are not touched. Files go to the trash when gio or trash-put is available.
set -euo pipefail
cd "$(dirname "$0")"
OS="$(uname -s)"
if [ "$OS" = Darwin ]; then
	PROFILE_DIRS=("$HOME/Library/Application Support/nomediaplayer")
else
	PROFILE_DIRS=("${XDG_CONFIG_HOME:-$HOME/.config}/nomediaplayer"
		"${XDG_STATE_HOME:-$HOME/.local/state}/nomediaplayer")
fi
if [ "${1:-}" = "--clean" ]; then
	if pgrep -x nomediaplayer >/dev/null; then
		echo "nomediaplayer is running; quit it first (q)" >&2
		exit 1
	fi
	for d in "${PROFILE_DIRS[@]}"; do
		[ -e "$d" ] || continue
		if command -v gio >/dev/null && gio trash "$d" 2>/dev/null; then :
		elif command -v trash-put >/dev/null && trash-put "$d" 2>/dev/null; then :
		elif command -v trash >/dev/null && trash "$d" 2>/dev/null; then : # macOS 15+
		else command rm -rf "$d"; fi
		echo "removed $d"
	done
	# lock and socket only (tmpfs, no trash)
	[ -n "${XDG_RUNTIME_DIR:-}" ] && command rm -rf "$XDG_RUNTIME_DIR/nomediaplayer"
fi
VERSION="${VERSION:-1.2.0}"
BIN="$HOME/.local/bin/nomediaplayer"
command -v go >/dev/null || { echo "Go is not installed (Linux: your package manager; macOS: brew install go)" >&2; exit 1; }
CGO=0
if [ "$OS" = Darwin ]; then
	# cgo builds the native Now Playing / media-key bridge (internal/macmedia)
	if xcrun --show-sdk-path >/dev/null 2>&1; then CGO=1
	else echo "note: no Xcode Command Line Tools (xcode-select --install); building without Now Playing / media keys"; fi
fi
CGO_ENABLED=$CGO go build -trimpath -ldflags="-s -w -X main.version=$VERSION" -o bin/nomediaplayer ./cmd/podmutt
mkdir -p "$(dirname "$BIN")"
install -m755 bin/nomediaplayer "$BIN"
if [ "$OS" != Darwin ]; then
	APPS="$HOME/.local/share/applications"
	ICONS="$HOME/.local/share/icons/hicolor/scalable/apps"
	mkdir -p "$APPS" "$ICONS"
	install -m644 packaging/nml/nomediaplayer.svg "$ICONS/nomediaplayer.svg"
	sed "s|@BIN@|$BIN|g" packaging/nml/nomediaplayer.desktop > "$APPS/nomediaplayer.desktop"
	command -v update-desktop-database >/dev/null && update-desktop-database "$APPS" || true
fi
echo "nomediaplayer $VERSION -> $BIN"
case ":$PATH:" in
*":$HOME/.local/bin:"*) ;;
*) echo "note: ~/.local/bin is not on your PATH; add this line to ~/.zshrc (macOS) or ~/.bashrc:"
	echo '  export PATH="$HOME/.local/bin:$PATH"' ;;
esac
