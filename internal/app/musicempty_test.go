package app

import "testing"

func TestEmptyMPDLibraryHint(t *testing.T) {
	ms := &musicState{libView: viewArtists}
	if got := ms.libraryEmpty(); got != "" {
		t.Fatalf("before MPD answers the list is still loading, got %q", got)
	}
	ms.artistsLoaded = true
	if got := ms.libraryEmpty(); got != emptyMPDLibrary {
		t.Fatalf("an empty MPD database must explain itself, got %q", got)
	}
	ms.artists = []string{"Muse"}
	if got := ms.libraryEmpty(); got != "" {
		t.Fatalf("a filled library needs no hint, got %q", got)
	}
}
