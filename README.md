<p align="center">
  <img src="packaging/nml/nomediaplayer.svg" width="140" alt="nomediaplayer logo">
</p>

# nomediaplayer

A keyboard-driven terminal player for **your own music** (through MPD) and
**podcasts**, in one window. `M` switches between the two.

Runs on **Linux**; **macOS** is in testing (see [macOS](#macos-testing)).

nomediaplayer is a fork of [podmutt](https://github.com/susamn/podmutt) (MIT).
It adds the music side and optional integrations, and keeps podmutt's podcast
player.

## Features

**Music (MPD)**
- Browse by artist, album or genre; search everything with `/` and see the
  results per track or per album (`w`).
- Play, queue, shuffle and repeat; album art in the terminal (Kitty graphics
  or a coloured fallback).
- Synced lyrics from `.lrc` files next to your music, and optionally from
  lrclib.net.
- Recently played list, desktop media keys (MPRIS), and a small CLI
  (`nomediaplayer play|pause|next|status`).

**Optional integrations** (each one off until you set it up)
- **Navidrome** (or any Subsonic server): playlists and favourites.
- **Lidarr**: search artists and albums that aren't in your library yet and
  request them.
- **Music Assistant** (through Home Assistant): hand the music to a speaker in
  the house (`>`) and take it back (`<`).

**Podcasts** (from podmutt)
- Subscribe by RSS URL or OPML import, or (optionally) search and browse the
  Apple Podcasts directory. No account is needed.
- Queue, favourites, chapters, resume and playback speed; audio through mpv.

## Privacy

- All settings are in `~/.config/nomediaplayer/config.toml`, which is created
  with mode `0600` (only you can read it). Library, queue and history are in
  `~/.local/state/nomediaplayer/`. Nothing is stored anywhere else.
- No telemetry and no account.
- nomediaplayer only contacts the servers you set up yourself (MPD, Navidrome,
  Lidarr, Home Assistant, Music Assistant) and the feeds you subscribe to.
- Three online services are opt-in and are asked about in the setup:
  - the **Apple Podcasts directory**, which receives your search terms and
    country;
  - **Podcast Index**, which receives your search terms when you search it
    (only with your own key);
  - **lrclib.net**, which receives the artist, title and album of the playing
    song when there is no local `.lrc` file;
  - your podcast feeds, which are fetched from their own servers.
- Only when you paste a Spotify track or album link into the music view:
  the link is fetched from `open.spotify.com` (the public page Spotify serves
  for link previews) to read title, artist and album, so the song can be found
  in your library or requested from Lidarr. No account, nothing is played from
  Spotify.

## Requirements

- **Linux.** macOS is in testing (see [macOS](#macos-testing) below);
  Windows does not build.
- **MPD** (Music Player Daemon) for the music side. nomediaplayer is a client:
  it tells MPD what to play, and MPD plays it through *its own* audio output
  from *its own* `music_directory`. Run MPD on this computer to hear the
  music here; point nomediaplayer at an MPD on another machine (a server,
  a Raspberry Pi with speakers) and the music plays there.
- **mpv** for podcasts. Podcasts do not use MPD; they always play on this
  computer.
- **Go 1.27 or newer** to build it. If your distribution ships an older Go
  (1.21 or newer), `go` downloads the right toolchain by itself the first time
  you build, which needs an internet connection. Check with `go version`.
- Optional: **ffmpeg** (with ffprobe) and `gio` or `trash-put` for FLAC → MP3,
  a terminal with the Kitty graphics protocol (kitty, WezTerm, Ghostty) for
  sharp album art.

A minimal MPD setup on this computer:

```sh
sudo pacman -S mpd        # Arch;  Debian/Ubuntu: sudo apt install mpd
mkdir -p ~/.config/mpd
cat > ~/.config/mpd/mpd.conf <<'CONF'
music_directory "~/Music"
playlist_directory "~/.local/share/mpd/playlists"
db_file "~/.local/share/mpd/database"
state_file "~/.local/state/mpd/state"
audio_output {
    type "pipewire"
    name "PipeWire"
}
CONF
mkdir -p ~/.local/share/mpd/playlists ~/.local/state/mpd
systemctl --user enable --now mpd
```

nomediaplayer reads `music_directory` from `~/.config/mpd/mpd.conf` as the
default music folder. The music folder is only used on this computer for
lyrics (`.lrc` files) and FLAC → MP3; playing and browsing go through MPD.

## Install

On Linux (for macOS, see [macOS](#macos-testing) first):

```sh
git clone https://github.com/nomedialoaded/nomediaplayer.git
cd nomediaplayer
./install-nml.sh          # builds and installs for your user only
```

This puts the binary in `~/.local/bin/nomediaplayer` (make sure that is on
your `PATH`) and adds a launcher entry and icon under `~/.local/share`.
To update later: `git pull && ./install-nml.sh`.
For a clean install that starts with the setup wizard again, use
`./install-nml.sh --clean`: it first removes your settings and local data
(podcast subscriptions, history, recently played). Your servers (MPD,
Navidrome, Lidarr) are not touched.

Other ways to build:

```sh
make build                       # bin/nomediaplayer, nothing installed
sudo make install-linux          # /usr/local/bin + desktop entry + icon
make package                     # your own .deb and .rpm in dist/ (needs nfpm)
```

## macOS (testing)

nomediaplayer builds and runs on macOS 13 or newer, but has had little
testing there. Feedback is welcome as a GitHub issue.

```sh
xcode-select --install          # once; needed for Now Playing and media keys
brew install go mpv             # mpv plays podcasts
git clone https://github.com/nomedialoaded/nomediaplayer.git
cd nomediaplayer
./install-nml.sh                # binary in ~/.local/bin, no app icon
```

If the installer says `~/.local/bin` is not on your `PATH`, add the line it
prints to `~/.zshrc` and open a new terminal. Then run `nomediaplayer`.

- **Podcasts** work without anything else.
- **Music** needs MPD and your own music files: `brew install mpd`, create
  `~/.config/mpd/mpd.conf` as shown above, with
  `audio_output { type "osx" name "Mac" }` instead of the PipeWire block, and
  start it with `mpd ~/.config/mpd/mpd.conf`. Without MPD, skip the music step
  in the setup wizard.
- Now Playing in Control Center and the media keys work for podcasts; music
  played through MPD does not show up there yet.
- Settings live in `~/Library/Application Support/nomediaplayer/`.
- Update with `git pull && ./install-nml.sh`.

## First start

On the first start a setup wizard asks:

1. **Music library**: the MPD address (default `127.0.0.1:6600`) and your music
   folder. Both are tested right away.
2. **Navidrome**: yes or no. If yes, the URL, user name and password.
3. **Lidarr**: yes or no. If yes, the URL and API key, and whether FLAC
   imports should become MP3 (see below).
4. **Speakers**: yes or no. If yes, the Home Assistant URL and token, plus an
   optional Music Assistant URL and token.
5. **Podcasts**: yes or no, whether to use the Apple Podcasts directory, and
   optionally Podcast Index with your own key (see below).
6. **Theme**: whether the colours should follow your wallpaper (see below).
7. **Online lyrics**: yes or no.

Each connection is checked before you continue. If a check fails you can
edit the values, keep them anyway or skip that service. Nothing is written
until the last step, and `Esc` cancels the setup. If you skip the setup, all
online lookups stay off.

To change anything later, press `?`, go to the **Setup** page (`4`) and press
the key of the service you want to change. A service you switch off
disappears from the views (`t`), the search and the key hints.

## Which address and token for each service

The setup asks for the address of each **server**, not the page you see in
the browser. A URL copied from the address bar (with `/app/#/…` or a page
path) is cleaned up automatically, but the port must be right. If a check
says *got a web page instead of the API*, the address points at a web page:
check the port.

| Service | Address (example) | Where to get the token or key |
|---|---|---|
| MPD | `127.0.0.1:6600` (host:port, no `http://`) | none |
| Navidrome | `http://192.168.1.10:4533` | your normal user name and password |
| Lidarr | `http://192.168.1.10:8686` | Lidarr › Settings › General › Security › API Key |
| Home Assistant | `http://192.168.1.10:8123` | your HA profile › Security › Long-lived access tokens › Create token |
| Music Assistant | `http://192.168.1.10:8095` | Music Assistant › Settings › Profile › create a token |
| Podcast Index | none (online service) | free key + secret by e-mail from <https://api.podcastindex.org/> |

**Music Assistant is not Home Assistant.** Even when Music Assistant runs as
a Home Assistant add-on on the same machine, it has its own server on port
**8095**. Opening it from the Home Assistant sidebar shows an `…:8123/…`
address in the browser; that is Home Assistant passing the page through and
does not work here. Open `http://<ha-host>:8095` in the browser instead: that
is the address to enter, and its Settings › Profile page has the token.
The Home Assistant token and the Music Assistant token are two different
tokens.

Typical mistakes:

- **No port** (`http://192.168.1.10`): you reach whatever web page runs on
  port 80 on that machine (often the NAS or router page).
- **Home Assistant's port for Music Assistant** (`:8123` instead of `:8095`):
  *Music Assistant responded 404*.
- **The browser URL of the web app** (`…:4533/app/#/album/…`): cleaned up by
  the setup; in `config.toml` use just `http://host:4533`.

## Themes

Pick a theme in the app's Settings: `colors` (default), `catppuccin`,
`solarized-dark`, or your own `~/.config/nomediaplayer/themes/<name>.toml`
(copy one of the generated files as a start). `kill -USR1` on the process
reloads the theme file.

### Colours that follow your wallpaper (optional)

The `wallpaper` theme takes its colours from your current wallpaper and
changes along with it (checked every few seconds). nomediaplayer does not
read the wallpaper itself: it reads a **colour file** that another program
on your desktop writes whenever the wallpaper changes. Setup step *Theme*
(`?` → `4` → `w`) asks for the path of that file and checks it.

**What you need:** a program that turns your wallpaper into a Material You
colour scheme and saves it as JSON. Two common situations:

1. **Your desktop shell already does it.** Several Quickshell/Hyprland
   shells with "Material You" or "dynamic colours" keep the current scheme
   in a JSON file, usually under `~/.local/state/<shell>/` or
   `~/.cache/<shell>/`. To find candidates, look for JSON files that contain
   a `primary` colour:

   ```sh
   grep -rlsE --include='*.json' '"primary" *: *"#?[0-9a-fA-F]{6}' ~/.local/state ~/.cache ~/.config
   ```

   Pick the one that changes when you change the wallpaper (check its date
   with `ls -l`).

2. **You use matugen** (or want to): matugen makes the colours from an image
   and writes them through templates. Add a template for nomediaplayer once;
   after that every `matugen image <wallpaper>` keeps the file up to date:

   ```toml
   # ~/.config/matugen/config.toml
   [templates.nomediaplayer]
   input_path = "~/.config/matugen/templates/nomediaplayer.json"
   output_path = "~/.cache/matugen/nomediaplayer.json"
   ```

   ```json
   { "primary": "{{colors.primary.default.hex}}",
     "secondary": "{{colors.secondary.default.hex}}",
     "tertiary": "{{colors.tertiary.default.hex}}",
     "surface": "{{colors.surface.default.hex}}",
     "on_surface": "{{colors.on_surface.default.hex}}",
     "on_surface_variant": "{{colors.on_surface_variant.default.hex}}",
     "surface_container": "{{colors.surface_container.default.hex}}",
     "surface_container_high": "{{colors.surface_container_high.default.hex}}",
     "surface_container_highest": "{{colors.surface_container_highest.default.hex}}",
     "outline": "{{colors.outline.default.hex}}",
     "error": "{{colors.error.default.hex}}" }
   ```

   The path to enter in the setup is then `~/.cache/matugen/nomediaplayer.json`.

**What the file must look like:** JSON with Material colour roles as keys.
`primary` and `surface` are required; `secondary`, `tertiary`, `onSurface`,
`onSurfaceVariant`, `surfaceContainer…`, `outline`, `error`,
`onPrimaryContainer` and `onSecondaryContainer` are used when present
(missing ones fall back to related colours). Accepted:

- keys in camelCase (`onSurface`) or snake_case (`on_surface`);
- values `#rrggbb` or `rrggbb`;
- the roles at the top level, or one level down under `colors` or `colours`;
  if that holds a `dark` and a `light` scheme, the one named by a top-level
  `mode` is used (default dark).

```json
{ "primary": "#ffb2b9", "secondary": "#e5bdbf", "tertiary": "#e8c08e",
  "surface": "#1a1112", "onSurface": "#f0dedf", "outline": "#9f8c8d",
  "error": "#ffb4ab" }
```

Light or dark follows the brightness of `surface`.

**Not supported:** pywal's `~/.cache/wal/colors.json` (it has `color0` …
`color15`, not Material roles). Use one of the normal themes, or a theme
file in `~/.config/nomediaplayer/themes/`, instead.

If the file is missing or unreadable, the `wallpaper` theme falls back to
Catppuccin. Without `wallpaper_colors` the theme is not offered at all.

## FLAC → MP3 (with Lidarr)

Lidarr sometimes imports FLAC. With `flac_to_mp3 = true` nomediaplayer turns
those albums into MP3 320: it looks at Lidarr's import history at start, after
each finished download and every 15 minutes, writes the MP3 next to the FLAC
with ffmpeg, checks the length, and only then moves the FLAC to the trash
(never deleted outright). MPD is asked to rescan afterwards.

This only works when:

- the music folder (`music_dir`) is reachable and writable **on this
  computer** (a local folder or a mounted share);
- Lidarr's root folder is that same library. Lidarr's paths are mapped onto
  `music_dir` by the part after the root folder, so `/music/Artist/Album` in a
  Lidarr container becomes `<music_dir>/Artist/Album` here;
- `ffmpeg`, `ffprobe` and `gio` or `trash-put` are installed.

The wizard checks the tools and the folder. Several computers may run it on
the same shared folder; a lock file in the album folder keeps them from
converting the same album twice. Leave it off if you want to keep FLAC.

## Configuration

The wizard writes `config.toml` for you. You can also edit it by hand; see
[`config.example.toml`](config.example.toml) for all the keys. Every key can
also be set as an environment variable `NOMEDIAPLAYER_<KEY>`, or read from a
file with `NOMEDIAPLAYER_<KEY>_FILE`. This lets you keep tokens out of the
config file:

```sh
NOMEDIAPLAYER_LIDARR_API_KEY_FILE=~/.secrets/lidarr nomediaplayer
```

## Keys

Press `?` in the app for the full list. The most used keys:

| Key | Action |
|---|---|
| `M` | Switch between music and podcasts |
| `t` / `T` | Next or previous view (artists, albums, genres, …) |
| `/` | Search (the music library, or podcasts in the podcast view) |
| `f` | Filter the focused column |
| `w` | Show search results per track or per album |
| `Enter` | Play |
| `a` / `A` | Add to the queue / add everything |
| `Space` | Pause |
| `n` / `v` | Next or previous track |
| `L` | Lyrics |
| `>` / `<` | Send to a speaker / take it back (with Music Assistant) |
| `?` | Help, setup and changelog |
| `q` | Quit |

## Podcast Index (optional)

Podcast search uses Apple's directory (if you switched it on) or RSS/OPML. As
a third source you can search [Podcast Index](https://podcastindex.org), an
open, independent podcast directory. It needs your own free API key and
secret:

1. Sign up with your e-mail address at <https://api.podcastindex.org/>. The
   **API Key** and the **API Secret** arrive together by e-mail.
2. In nomediaplayer: `?` → `4` (Setup) → `c` (Podcasts), answer *yes* to
   Podcast Index and paste both. The setup does one test search to check them.
3. In the podcast view, `t` in Discover (panel 1) switches between Apple's
   top 100 and Podcast Index's **trending** list, the same way `t` switches
   views in the music player. With the Apple directory off, Discover shows
   only the Podcast Index list.
4. In the podcast search (`/`), `Tab` switches to Podcast Index.

The key and secret are stored in `config.toml` (`podcast_index_key`,
`podcast_index_secret`, readable only by you), or can be set as
`NOMEDIAPLAYER_PODCAST_INDEX_KEY` / `NOMEDIAPLAYER_PODCAST_INDEX_SECRET`.
They are never passed on to mpv. Podcast Index only finds shows; the
episodes always come from the show's own RSS feed.

## Development

- `make check` runs formatting, module, vet and test checks with a coverage
  gate; `make race` runs the tests with the race detector.
- `./install-dev.sh` installs a side-by-side test build called
  `nomediaplayer-dev`, with its own config, state and socket, so you can try
  changes without touching your normal setup.
- `tools/ttydrive.py` drives the app in a pseudo-terminal for screenshots and
  scripted UI checks (`uv run -q --with pyte python tools/ttydrive.py`).

Neither is needed to use the app.

## Feedback

Bugs, questions and ideas are welcome as
[GitHub issues](https://github.com/nomedialoaded/nomediaplayer/issues).
On macOS, please mention your macOS version and whether Now Playing works
for podcasts.

## Changelog

See [CHANGELOG.md](CHANGELOG.md), or press `?` in the app, or run
`nomediaplayer --changelog`.

## Credits and licence

nomediaplayer is MIT-licensed. It is based on podmutt, Copyright (c) podmutt
contributors; see [LICENSE](LICENSE). podmutt's original README is kept in
[docs/podmutt-README.md](docs/podmutt-README.md).
