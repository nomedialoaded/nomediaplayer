#!/usr/bin/env bash
# Side-by-side test build: ~/.local/bin/nomediaplayer-dev with its own
# config (~/.config/nomediaplayer-dev), state (~/.local/state/nomediaplayer-dev),
# control socket, MPRIS name and env prefix (NOMEDIAPLAYER_DEV_*).
# It never reads or migrates the regular nomediaplayer/podmutt profile.
set -euo pipefail
cd "$(dirname "$0")"
NAME="${NAME:-nomediaplayer-dev}"
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
BIN="$HOME/.local/bin/$NAME"
mkdir -p bin "$(dirname "$BIN")"
CGO_ENABLED=0 go build -trimpath \
	-ldflags="-s -w -X main.version=$VERSION -X github.com/nomedialoaded/nomediaplayer/internal/profile.AppName=$NAME" \
	-o "bin/$NAME" ./cmd/podmutt
install -m755 "bin/$NAME" "$BIN"
echo "$NAME $VERSION -> $BIN (profile: ~/.config/$NAME, ~/.local/state/$NAME)"
