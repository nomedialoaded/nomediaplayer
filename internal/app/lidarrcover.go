package app

// nml: album covers for the Lidarr view. The Details cover follows the album
// under the cursor (not the song that plays). Images come from Lidarr's own
// copy on the LAN, are scaled down once to at most lidarrCoverPx and kept in
// a small LRU that is emptied as soon as the Lidarr view is left, so
// browsing a long discography costs no memory afterwards. A missing cover is
// remembered (nil) so scrolling back and forth does not ask again.

import (
	"container/list"
	"context"
	"image"
	"image/color"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nomedialoaded/nomediaplayer/internal/artwork"
	"github.com/nomedialoaded/nomediaplayer/internal/lidarr"
	"github.com/nomedialoaded/nomediaplayer/internal/ui"
)

const (
	lidarrCoverPrefix = "lidarr-cover:"
	lidarrCoverCache  = 24  // decoded covers kept while in the Lidarr view
	lidarrCoverPx     = 320 // longest side after scaling (~400 kB each)
	// lidarrCoverSettle: a cover is only fetched once the cursor rests on an
	// album this long; scrolling through a discography fetches nothing.
	lidarrCoverSettle = 150 * time.Millisecond
)

type lidarrCovers struct {
	client *lidarr.Client
	mu     sync.Mutex
	order  *list.List // keys, most recent first
	byID   map[string]*list.Element
	images map[string]image.Image // nil = no cover
}

func newLidarrCovers(client *lidarr.Client) *lidarrCovers {
	return &lidarrCovers{client: client, order: list.New(), byID: map[string]*list.Element{}, images: map[string]image.Image{}}
}

// Image implements imageFetcher: key "lidarr-cover:<albumID>" (album in
// Lidarr) or "lidarr-cover:mb:<release group>" (search result, remote
// thumbnail only); remote = the album's remote cover URL (may be empty).
func (c *lidarrCovers) Image(ctx context.Context, key, remote string) (image.Image, error) {
	rest := strings.TrimPrefix(key, lidarrCoverPrefix)
	id := 0
	if !strings.HasPrefix(rest, "mb:") {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return nil, lidarr.ErrNoCover
		}
		id = n
	}
	if c.client == nil {
		return nil, lidarr.ErrNoCover
	}
	c.mu.Lock()
	if el, ok := c.byID[key]; ok {
		c.order.MoveToFront(el)
		img := c.images[key]
		c.mu.Unlock()
		if img == nil {
			return nil, lidarr.ErrNoCover
		}
		return img, nil
	}
	c.mu.Unlock()
	select {
	case <-ctx.Done(): // cursor moved on (the load is cancelled)
		return nil, ctx.Err()
	case <-time.After(lidarrCoverSettle):
	}
	data, err := c.client.AlbumCover(ctx, id, remote, artwork.MaxBytes)
	if ctx.Err() != nil {
		return nil, ctx.Err() // cancelled by scrolling on: do not remember
	}
	var img image.Image
	if err == nil {
		if decoded, derr := artwork.Decode(data); derr == nil {
			img = shrink(decoded, lidarrCoverPx)
		}
	}
	c.mu.Lock()
	if _, ok := c.byID[key]; !ok {
		c.byID[key] = c.order.PushFront(key)
	}
	c.images[key] = img
	for c.order.Len() > lidarrCoverCache {
		old := c.order.Back()
		c.order.Remove(old)
		delete(c.byID, old.Value.(string))
		delete(c.images, old.Value.(string))
	}
	c.mu.Unlock()
	if img == nil {
		return nil, lidarr.ErrNoCover
	}
	return img, nil
}

// clear drops every cached cover (leaving the Lidarr view).
func (c *lidarrCovers) clear() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.order.Init()
	clear(c.byID)
	clear(c.images)
	c.mu.Unlock()
}

func (c *lidarrCovers) len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.order.Len()
}

// shrink scales img down so its longest side is at most max. It averages up
// to 3×3 samples per output pixel straight from the decoded image, so no
// full-size copy is made (a 1200 px cover would be another 5.8 MB).
func shrink(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= max && h <= max {
		return img
	}
	nw, nh := max, h*max/w
	if h > w {
		nw, nh = w*max/h, max
	}
	nw, nh = maxInt(nw, 1), maxInt(nh, 1)
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	ycc, _ := img.(*image.YCbCr)
	for y := 0; y < nh; y++ {
		y0, y1 := y*h/nh, maxInt((y+1)*h/nh, y*h/nh+1)
		sy := maxInt((y1-y0+1)/2, 1)
		for x := 0; x < nw; x++ {
			x0, x1 := x*w/nw, maxInt((x+1)*w/nw, x*w/nw+1)
			sx := maxInt((x1-x0+1)/2, 1)
			var r, g, bl, a, n uint32
			for py := y0; py < y1; py += sy {
				for px := x0; px < x1; px += sx {
					if ycc != nil { // JPEG: read the planes directly, no allocation
						yi, ci := ycc.YOffset(b.Min.X+px, b.Min.Y+py), ycc.COffset(b.Min.X+px, b.Min.Y+py)
						cr, cg, cb := color.YCbCrToRGB(ycc.Y[yi], ycc.Cb[ci], ycc.Cr[ci])
						r, g, bl, a, n = r+uint32(cr)<<8, g+uint32(cg)<<8, bl+uint32(cb)<<8, a+0xffff, n+1
						continue
					}
					cr, cg, cb, ca := img.At(b.Min.X+px, b.Min.Y+py).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
			}
			o := dst.Pix[y*dst.Stride+x*4:]
			o[0], o[1], o[2], o[3] = uint8(r/n>>8), uint8(g/n>>8), uint8(bl/n>>8), uint8(a/n>>8)
		}
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// lidarrArtURLs is the cover for the Lidarr view: the album under the cursor
// in column 2, or the album of the selected download. ok=false: not in the
// Lidarr view (normal song cover).
func (m libraryShell) lidarrArtURLs() (key, remote string, ok bool) {
	ms := m.music
	if ms == nil || ms.lidarr == nil || !isLidarrSource(ms.source) {
		return "", "", false
	}
	hitSource := strings.HasPrefix(ms.source, lidarrHitPfx)
	if f := m.Model.Focus(); f != ui.Episodes && f != ui.Details && !hitSource {
		return "", "", true // help text in Details: logo
	}
	item, found := m.Model.Selected(ui.Episodes)
	if !found {
		return "", "", true
	}
	switch {
	case strings.HasPrefix(item.ID, lidarrHitPfx):
		if h, ok := ms.lidarr.hit(item.ID); ok && h.Album.Cover != "" {
			return lidarrCoverPrefix + "mb:" + h.Album.ForeignID, h.Album.Cover, true
		}
	case strings.HasPrefix(item.ID, lidarrAlbumPfx):
		if al, ok := ms.lidarr.album(ms.source, item.ID); ok {
			return lidarrCoverPrefix + strconv.Itoa(al.ID), al.Cover, true
		}
	case strings.HasPrefix(item.ID, lidarrDLPfx):
		for _, q := range ms.lidarr.queue {
			if lidarrDLPfx+strconv.Itoa(q.ID) == item.ID && q.AlbumID > 0 {
				return lidarrCoverPrefix + strconv.Itoa(q.AlbumID), "", true
			}
		}
	}
	return "", "", true
}
