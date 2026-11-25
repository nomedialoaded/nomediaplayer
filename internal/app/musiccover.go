package app

// nml: album covers for the Music view, read from MPD (embedded picture or
// cover file in the album folder). Songs of one folder share a cover.

import (
	"container/list"
	"context"
	"errors"
	"image"
	"path"
	"strings"
	"sync"

	"github.com/nomedialoaded/nomediaplayer/internal/artwork"
	"github.com/nomedialoaded/nomediaplayer/internal/mpd"
)

const (
	musicArtPrefix = "mpd:"
	coverCacheSize = 48
)

var errNoCover = errors.New("no album art")

type musicCovers struct {
	client *mpd.Client
	mu     sync.Mutex
	order  *list.List // of string keys, most recent first
	byKey  map[string]*list.Element
	images map[string]image.Image // nil = known to have no cover
}

func newMusicCovers(client *mpd.Client) *musicCovers {
	return &musicCovers{client: client, order: list.New(), byKey: map[string]*list.Element{}, images: map[string]image.Image{}}
}

// Image implements imageFetcher for "mpd:<file>" keys; a missing cover is an
// error so the caller falls back to the logo.
func (c *musicCovers) Image(ctx context.Context, key, _ string) (image.Image, error) {
	file := strings.TrimPrefix(key, musicArtPrefix)
	dir := path.Dir(file)
	c.mu.Lock()
	if el, ok := c.byKey[dir]; ok {
		c.order.MoveToFront(el)
		img := c.images[dir]
		c.mu.Unlock()
		if img == nil {
			return nil, errNoCover
		}
		return img, nil
	}
	c.mu.Unlock()
	data, err := c.client.Picture(ctx, file)
	if err != nil {
		return nil, err
	}
	var img image.Image
	if len(data) > 0 {
		if img, err = artwork.Decode(data); err != nil {
			img = nil
		}
	}
	c.mu.Lock()
	c.byKey[dir] = c.order.PushFront(dir)
	c.images[dir] = img
	for c.order.Len() > coverCacheSize {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.byKey, old.Value.(string))
		delete(c.images, old.Value.(string))
	}
	c.mu.Unlock()
	if img == nil {
		return nil, errNoCover
	}
	return img, nil
}
