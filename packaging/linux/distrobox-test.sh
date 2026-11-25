#!/usr/bin/env bash
# Install the built .deb/.rpm in throwaway Debian and Fedora distrobox
# containers and check that nomediaplayer runs. Needs podman + distrobox.
# Containers get their own home under dist/boxhome so the host profile is
# never touched (PATH is reset so the host ~/.local/bin is not used); they are removed afterwards (KEEP=1 keeps them).
#
# If podman says "'overlay' is not supported" (e.g. after a kernel update
# without a reboot), use separate vfs storage outside /tmp (nosuid breaks
# sudo in the box):
#   printf '[storage]\ndriver="vfs"\ngraphroot="%s/s"\nrunroot="%s/r"\n' ~/.cache/nmp-box ~/.cache/nmp-box > ~/.cache/nmp-box.conf
#   CONTAINERS_STORAGE_CONF=~/.cache/nmp-box.conf make package-test
set -euo pipefail
cd "$(dirname "$0")/../.."
deb=$(ls dist/nomediaplayer_*_amd64.deb | tail -1)
rpm=$(ls dist/nomediaplayer-*.x86_64.rpm | tail -1)
run() { # name image install-cmd verify-cmd
	local name=$1 image=$2 install=$3 verify=$4 home="$PWD/dist/boxhome/$1"
	mkdir -p "$home"
	distrobox create --yes --name "$name" --image "$image" --home "$home" --no-entry >/dev/null
	echo "== $name ($image)"
	distrobox enter "$name" -- bash -euc "
		$install
		export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin
		$verify
		test \"\$(readlink -f \"\$(command -v nomediaplayer)\")\" = /usr/bin/nomediaplayer
		/usr/bin/nomediaplayer --version
		HOME=\$(mktemp -d) XDG_RUNTIME_DIR=\$(mktemp -d) /usr/bin/nomediaplayer status || true
		test -f /usr/share/applications/nomediaplayer.desktop
		test -f /usr/share/icons/hicolor/scalable/apps/nomediaplayer.svg
		test -f /usr/share/doc/nomediaplayer/copyright
		grep -q 'podmutt contributors' /usr/share/doc/nomediaplayer/copyright
		command -v mpv >/dev/null
		echo OK: $name"
	[ "${KEEP:-}" = 1 ] || distrobox rm --force "$name" >/dev/null
}
run nmp-debian docker.io/library/debian:stable "sudo apt-get update -qq && sudo apt-get install -y -qq ./$deb >/dev/null" "dpkg -s nomediaplayer | grep -q '^Status: install ok installed'; dpkg -L nomediaplayer"
run nmp-fedora registry.fedoraproject.org/fedora:latest "sudo dnf install -y -q ./$rpm >/dev/null" "rpm -q nomediaplayer; rpm -ql nomediaplayer"
