// Package podmutt holds files from the repository root that the program
// embeds (nml fork, nomediaplayer).
package podmutt

import _ "embed"

// Changelog is CHANGELOG.md, shown in the help (page 4, or i in the Music view)
// and by --changelog.
//
//go:embed CHANGELOG.md
var Changelog string
