package app

// nml: the library's own folder names win (Deemix folders, files checked
// with Picard). When nomediaplayer adds an artist to Lidarr it points the
// artist at the existing folder whose name only differs in dashes, quotes,
// replaced characters or case (library "G-Unit" vs MusicBrainz "G‐Unit"),
// so Lidarr sees what is already there and imports into that folder. A new
// artist gets a folder in the same style: plain ASCII dashes and quotes,
// "_" for characters a path cannot hold.

import (
	"context"
	"os"
	"strings"

	"github.com/nomedialoaded/nomediaplayer/internal/flacconv"
	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
)

// folderKey compares artist folder names loosely: fold() plus the
// characters Deemix and Lidarr each replace differently (AC_DC / AC+DC).
func folderKey(name string) string {
	r := strings.NewReplacer("_", "-", "+", "-", "*", "-", "/", "-", "\\", "-", ":", "-", "‑", "-", "—", "-", "‒", "-")
	return fold(r.Replace(fold(name)))
}

// libraryFolderName is an artist name in the library's style.
func libraryFolderName(name string) string {
	r := strings.NewReplacer(
		"‐", "-", "‑", "-", "‒", "-", "–", "-", "—", "-",
		"’", "'", "‘", "'", "“", "\"", "”", "\"",
		"/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_")
	return strings.TrimRight(strings.TrimSpace(r.Replace(name)), ".")
}

// existingArtistFolder finds the one library folder that looks like the
// artist's ("" when none or when several do: then nothing is guessed).
// Names come from the listing, not os.Stat: the SMB share matches
// case-insensitively.
func existingArtistFolder(musicDir, lidarrRoot string, a lidarr.Artist) string {
	if musicDir == "" || lidarrRoot == "" {
		return ""
	}
	dir, ok := flacconv.LocalDir(strings.TrimRight(lidarrRoot, "/")+"/x", lidarrRoot, musicDir)
	if !ok {
		return ""
	}
	entries, err := os.ReadDir(strings.TrimSuffix(dir, "/x"))
	if err != nil {
		return ""
	}
	keys := map[string]bool{}
	for _, n := range []string{a.Folder, a.Name, libraryFolderName(a.Name)} {
		if n != "" {
			keys[folderKey(n)] = true
		}
	}
	var match string
	for _, e := range entries {
		if !e.IsDir() || !keys[folderKey(e.Name())] {
			continue
		}
		if e.Name() == a.Folder || e.Name() == libraryFolderName(a.Name) {
			return e.Name() // exact: no doubt
		}
		if match != "" {
			return ""
		}
		match = e.Name()
	}
	return match
}

// artistFolder picks the folder for a new Lidarr artist; reused is the
// existing library folder it found ("" for a new one).
func artistFolder(musicDir, lidarrRoot string, a lidarr.Artist) (folder, reused string) {
	if f := existingArtistFolder(musicDir, lidarrRoot, a); f != "" {
		return f, f
	}
	if f := libraryFolderName(a.Name); f != "" {
		return f, ""
	}
	return a.Folder, ""
}

// addArtistAdopting adds the artist to Lidarr with the library's folder.
// reused is the existing folder it now uses, when its name differs from
// what Lidarr would have made.
func addArtistAdopting(ctx context.Context, c *lidarr.Client, d lidarr.Defaults, a lidarr.Artist, musicDir string) (id int, reused string, err error) {
	folder, existing := artistFolder(musicDir, d.RootFolder, a)
	if folder != "" && d.RootFolder != "" {
		a.Path = strings.TrimRight(d.RootFolder, "/") + "/" + folder
	}
	if existing == a.Folder {
		existing = ""
	}
	id, err = c.AddArtist(ctx, a, d)
	if err != nil {
		return 0, "", err
	}
	return id, existing, nil
}

func (ms *musicState) musicDir() string {
	if ms != nil && ms.lyrics != nil && ms.lyrics.finder != nil {
		return ms.lyrics.finder.MusicDir
	}
	return ""
}
