# Changelog — nomediaplayer

nomediaplayer is a fork of [podmutt](https://github.com/susamn/podmutt)
(v1.4.0, MIT) that adds a music player (MPD) next to podcasts. In the app:
`?` → Changelog.

## v1.3 — 2026-11-26

### Media keys
- **Music in the media controls**: the desktop media keys and "now playing"
  (MPRIS on Linux, Now Playing on macOS) now follow music as well as
  podcasts, whichever is audible, like the player bar; a paused song keeps the
  keys. Title, artist, length and position; previous/next for music (previous
  only on Linux for now). No separate mpd-mpris needed.
- Next only works when there is a next song in the MPD queue (at the last
  song, playerctl says "No player could handle this command").

### Music
- **Album covers load much faster** (on average 0.4 s instead of 3–7 s per
  cover from a network share), so the cover no longer stays empty when
  loading took longer than the 10 s limit. nomediaplayer asks MPD for the
  picture in one piece (`binarylimit`) instead of 8 KiB chunks.

## v1.2 — 2026-11-19

### Setup and privacy
- **Setup wizard** on the first start: MPD address and music folder,
  Navidrome, Lidarr, speakers (Home Assistant + Music Assistant), podcasts
  and online lyrics. Every connection is tested right away; if a test fails
  you can edit, keep or skip. Nothing is written until the last step, and
  `Esc` cancels. Replaces podmutt's podcast welcome screen.
- **Setup page in the help** (`?` → `4`): shows what is on and lets you set
  up or change each service again (`m`, `v`, `r`, `s`, `c`, `o`, or `a` for
  everything). Changes apply right away, except a new MPD address, which
  needs a restart.
- **Integrations are off until set up**: views (`t`), the Lidarr search tab
  and key hints of services you don't use are hidden.
- **Opt-in online lookups**: the Apple Podcasts directory
  (`podcast_directory`) and lrclib.net lyrics (`lyrics_online`) are asked
  about in the setup and stay off when you skip it. With the directory off,
  nothing is sent to Apple: no charts at startup, no search, no ratings. Add
  shows by RSS URL (`U`) or OPML instead.
- `podcasts = false` switches the whole podcast side off.
- `config.toml` is written with mode `0600` in a `0700` folder; an existing
  config readable by others is made private at startup.
- **Environment variables**: every key can also be set as
  `NOMEDIAPLAYER_<KEY>`, or read from a file with `NOMEDIAPLAYER_<KEY>_FILE`
  (for tokens). The environment wins over `config.toml`.
- **English** throughout the app.
- **Wallpaper theme for any desktop**: `wallpaper_colors` points at a JSON
  file with Material colour roles (from matugen or a desktop shell; camelCase
  or snake_case keys, with or without `#`). Set it in the setup (`?` → `4` →
  `w`); the file is checked first. Light or dark follows the surface colour.
  Without the setting the `wallpaper` theme is not offered.
- **FLAC → MP3 in the setup**: the Lidarr step asks about `flac_to_mp3` and
  checks ffmpeg/ffprobe, gio or trash-put and the music folder.
- `~/` works in `music_dir` and `wallpaper_colors` in `config.toml` too.
- **Same search and filter keys everywhere**: in the podcast view `/` now
  searches podcasts and `f` filters the column, like in the music player
  (podmutt had them the other way round).
- **Podcasts open in Discover until you subscribe**: without subscriptions
  the podcast side starts in Discover (the chart you used last), also when
  episodes you played from a preview are in the library; those stay under
  `b` → All episodes.
- **Podcast Index trending in Discover**: with a Podcast Index key, `t` in
  Discover (panel 1) switches between Apple's top 100 and Podcast Index's
  trending list (title shows `‹t›`). With the Apple directory off, Discover
  uses Podcast Index only.
- **Podcast Index in the setup**: `?` → `4` → `c` asks for your own key and
  secret (masked, checked with one test search) and stores them as
  `podcast_index_key` / `podcast_index_secret`. The environment variables
  keep working and win over the file.
- **Ready to share**: podmutt's changelog, release notes, design documents,
  screenshots and GitLab CI removed; this file is now `CHANGELOG.md`. The
  GitHub workflow builds `nomediaplayer`, and `make check` works again
  (coverage gate 80%). Podcast Index keys are now
  `NOMEDIAPLAYER_PODCAST_INDEX_KEY`/`_SECRET` (the podmutt names still work).
  README: Spotify links in the privacy list, Go version, Podcast Index,
  development tools.
- Wallpaper colour files nested under `colors`/`colours` (with optional
  `dark`/`light` schemes) are read too; README explains what the colour file
  is, how to find or make it (shell or matugen) and what it must contain.
- README: which address and token for each service, and why Music Assistant
  is port 8095 and not Home Assistant's 8123. The setup's Music Assistant
  step says so too.
- `./install-nml.sh --clean`: clean install; removes settings and local data
  (podcasts, history, recently played) first. Servers are not touched.
- **Pasted browser URLs work in the setup**: `http://host:4533/app/#/album/…`
  becomes `http://host:4533` (query, `#…` and the web app's path are dropped).
  A server that answers with a web page instead of its API now says so,
  instead of `invalid character '<'`.
- **First start stays offline until the wizard has asked**: no Apple chart
  request and no lrclib lookup while the setup is still open.

### Packages
- `.deb` and `.rpm` packages (`make package`, nfpm), with a desktop entry,
  an icon and the licence. They depend on mpv and recommend mpd.
  `make package-test` installs them in Debian and Fedora distrobox
  containers.

### Music
- **Search results per album** (`w`): after `/`, `w` switches column 2
  between tracks and the albums they are on. `Enter`/`o` plays the whole
  album, `a` queues it, `A` queues all the tracks that were found.
- The player bar says **No current track** on the music side when nothing
  is playing (podcasts keep "No current episode").
- An **empty MPD library** now says so (put music files in MPD's
  `music_directory` and press `R`) instead of staying on "Loading library…".

### macOS (testing)
- `install-nml.sh` works on macOS: builds with Now Playing / media keys when
  the Xcode Command Line Tools are there, no launcher entry, `--clean` uses
  `~/Library/Application Support/nomediaplayer`, and it tells you when
  `~/.local/bin` is not on your `PATH`.
- The setup wizard's MPD hint fits the system (`brew install mpd` on macOS).
- No more `ld: warning: ... built for newer 'macOS' version` lines when
  installing on macOS: the cgo part is built for macOS 13+, like the Go part.
- Media keys and Now Playing on macOS: `install-nml.sh` now installs the
  program inside a small app bundle (`~/.local/share/nomediaplayer/`), because
  macOS ignores them for a bare program. Before, the keys did nothing.
- README: a macOS section for testers, with MPD via `brew services` (the
  config links it reads) and how to check the library with `mpc stats`.

### Lidarr
- Search finds albums as well as artists, in Lidarr's relevance order.
  `Enter` on an album requests it (and first adds the artist if needed).
- Details shows the tracklist of the album under the cursor, including
  albums you don't have yet.
- Album art of the selected album, fetched after 150 ms on an album and kept
  in a small cache.

## v1.1 — 2026-11-05

### Lidarr
- **Lidarr** view (`t`) and search tab (`/` → Tab): add artists without
  downloading their whole discography, request single albums, and follow
  the download queue. Config: `lidarr_url`, `lidarr_api_key`.
- **FLAC → MP3** (`flac_to_mp3 = true`): FLAC imported by Lidarr is
  converted locally to MP3 320 with ffmpeg (tags and art kept).
- New artists reuse an existing artist folder whose name only differs in
  dashes, quotes or case, so Lidarr sees what you already have.

### Spotify links
- Paste a shared Spotify track or album link: play it if it is in your
  library, or request the single or album through Lidarr. Only Spotify's
  public link preview is read; nothing is played from Spotify.

### Speakers (Music Assistant)
- **Speakers** view (`t`): your Music Assistant players with what they play,
  plus pause, next/previous and volume.
- `>` hands the playing track and up to 500 queued tracks to a speaker;
  `<` takes a speaker's queue back to MPD at the same position.
  Config: `ha_url`, `ha_token`, `ma_url`, `ma_token`.

### Music
- **Lyrics** (`L`) from `.lrc` files next to the music, or lrclib.net;
  synced lyrics follow the song, and `Enter` jumps to a line.
- Clear the recently played list with `D` twice.
- Fix: the progress bar stopped after 2 s when playing music.
- Faster drawing (about 2.5× less work per frame).

## v1.0 — 2026-11-04

- **Music view** (`M`): artists, albums, genres, Navidrome playlists and
  favourites (`t`); tracks; the MPD queue; album art; search (`/`) and
  filter (`f`); play the whole album (`o`); recently played list.
- **Navidrome**: favourites (`b`/`B`) and playlists (`p`, `P`, `x`) are
  read and written live.
- One player bar for music and podcasts; shuffle (`z`) and repeat (`r`);
  starting one pauses the other.
- **wallpaper** theme that follows the desktop colours.
- Help in pages (`?`), including this changelog.
- Config and history move once from `~/.config/podmutt` to
  `~/.config/nomediaplayer`.
