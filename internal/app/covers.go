package app

import (
	"bytes"
	"hash/fnv"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/thumbs"
)

type coverKey struct {
	id     int64
	mod    int64
	height int
}

// coverKeep is how long a cover stays decoded once no build draws it:
// scrolling back within it shows the cover at once; later, it loads again
// from its thumbnail in the background.
const coverKeep = 3 * time.Second

// coverUse is when a cover was last asked for: the build and the time.
type coverUse struct {
	gen  int
	seen time.Time
}

// cachedCover is a decoded cover and when it was last drawn.
type cachedCover struct {
	bmp *ui.Bitmap
	coverUse
}

// coverCache holds decoded covers, made from small thumbnails on disk. It
// keeps only those the window drew lately, so a library of thousands of
// books holds about as many decoded covers as fit on the screen.
type coverCache struct {
	thumbs  *thumbs.Cache
	bitmaps map[coverKey]*cachedCover
	failed  map[coverKey]bool
	loading map[coverKey]*coverUse
	// gen counts the window's builds; sweeping says a later build is
	// asked for, to sweep again.
	gen      int
	sweeping bool
}

func newCoverCache() *coverCache {
	tc, _ := thumbs.New()
	return &coverCache{thumbs: tc, bitmaps: map[coverKey]*cachedCover{}, failed: map[coverKey]bool{}, loading: map[coverKey]*coverUse{}}
}

// sweep starts a build of the window: it drops the covers no build drew
// for coverKeep, and reports whether others it didn't draw remain.
func (cc *coverCache) sweep(now time.Time) (undrawn bool) {
	for k, e := range cc.bitmaps {
		switch {
		case e.gen == cc.gen:
			e.seen = now
		case now.Sub(e.seen) >= coverKeep:
			delete(cc.bitmaps, k)
		default:
			undrawn = true
		}
	}
	cc.gen++
	return undrawn
}

// sweepCovers sweeps the cover cache as a build starts and, while covers
// it didn't draw remain, has the window build again once they are due, so
// they go even when nothing else changes.
func (a *App) sweepCovers() {
	cc := a.covers
	if !cc.sweep(time.Now()) || cc.sweeping {
		return
	}
	cc.sweeping = true
	time.AfterFunc(coverKeep, func() { a.post(func() { cc.sweeping = false }) })
}

func (cc *coverCache) forget(id int64) {
	for k := range cc.bitmaps {
		if k.id == id {
			delete(cc.bitmaps, k)
		}
	}
	for k := range cc.failed {
		if k.id == id {
			delete(cc.failed, k)
		}
	}
}

// cover returns the book's cover at about height pixels, or nil while it
// loads or when the book has none.
func (a *App) cover(b *calibre.Book, height int) *ui.Bitmap {
	if !b.HasCover || a.lib == nil {
		return nil
	}
	cc := a.covers
	k := coverKey{b.ID, b.LastModified.UnixNano(), height}
	if e := cc.bitmaps[k]; e != nil {
		e.gen = cc.gen
		return e.bmp
	}
	if u := cc.loading[k]; u != nil {
		*u = coverUse{cc.gen, time.Now()}
		return nil
	}
	if cc.failed[k] {
		return nil
	}
	use := &coverUse{cc.gen, time.Now()}
	cc.loading[k] = use
	src := a.lib.CoverPath(b)
	a.run(func() {
		var bmp *ui.Bitmap
		path := src
		if cc.thumbs != nil {
			if p, err := cc.thumbs.Get(src, height); err == nil {
				path = p
			}
		}
		if data, err := os.ReadFile(path); err == nil {
			if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
				bmp = ui.NewBitmap(img)
			}
		}
		a.post(func() {
			delete(cc.loading, k)
			if bmp == nil {
				cc.failed[k] = true
				return
			}
			// Kept as last asked for: one a fling scrolled past while it
			// loaded goes with the next sweep.
			cc.bitmaps[k] = &cachedCover{bmp, *use}
		})
	})
	return nil
}

// tone is a generated cover's colors: background and ink.
type tone struct{ bg, fg ui.Color }

var tones = []tone{
	{ui.Hex("#1F2A44"), ui.Hex("#EFE6D2")}, // ink
	{ui.Hex("#B4553A"), ui.Hex("#FBEDE3")}, // clay
	{ui.Hex("#46583A"), ui.Hex("#ECEEDD")}, // moss
	{ui.Hex("#2B6474"), ui.Hex("#E3F0F1")}, // sea
	{ui.Hex("#572C4C"), ui.Hex("#F4E4EE")}, // plum
	{ui.Hex("#E6D9BF"), ui.Hex("#2A2420")}, // sand
}

func toneOf(title string) tone {
	h := fnv.New32a()
	h.Write([]byte(title))
	return tones[h.Sum32()%uint32(len(tones))]
}

// coverView draws the book's cover in a w×h box: the image when there is
// one, else a typographic cover in a tone derived from the title.
func (a *App) coverView(c *ui.Context, b *calibre.Book, w, h float32, small bool) ui.Element {
	return coverOf(c, b, a.cover(b, int(h*2)), w, h, small)
}

// coverOf draws the cover in a w×h box from bmp, or a typographic one
// when bmp is nil.
func coverOf(c *ui.Context, b *calibre.Book, bmp *ui.Bitmap, w, h float32, small bool) ui.Element {
	return coverObi(c, b, bmp, w, h, small, "")
}

// coverObi is coverOf wearing the obi of the reader the book is on;
// reader "" for none. A typographic cover's title moves up above the obi;
// an image is covered by it, as by a real one.
func coverObi(c *ui.Context, b *calibre.Book, bmp *ui.Bitmap, w, h float32, small bool, reader string) ui.Element {
	if small {
		reader = ""
	}
	box := ui.Box(c).Size(w, h).Radius(2, 4, 4, 2).Clip().Shadow(0, 1, 1, 0, ui.RGBA(26, 23, 18, 0.12)).Shadow(0, 6, 14, -4, ui.RGBA(26, 23, 18, 0.16))
	if bmp != nil {
		box.Children(func() {
			ui.Image(c, bmp).Fill().Fit(ui.Cover)
			spine(c, small)
			if reader != "" {
				widgets.ReaderObi(c, reader, w)
			}
		})
		return box
	}
	tn := toneOf(b.Title)
	box.Background(tn.bg)
	if small {
		box.Children(func() {
			ui.Box(c).Absolute().Left(w*0.2).Top(h*0.7).Size(w*0.4, 1).Background(tn.fg.Alpha(0.7))
			ui.Box(c).Absolute().Left(w*0.2).Top(h*0.78).Size(w*0.55, 1).Background(tn.fg.Alpha(0.5))
			spine(c, true)
		})
		return box
	}
	scale := w / theme.CoverW
	bottom := float32(theme.Space12)
	if reader != "" {
		bottom += widgets.ObiHeight
	}
	box.Padding(theme.Space12*scale, theme.Space12*scale, bottom*scale, theme.Space16*scale).Justify(ui.SpaceBetween).Children(func() {
		author := strings.ToUpper(strings.Join(b.Authors, ", "))
		ui.Text(c, author).Font(theme.FontMono).FontSize(8 * scale).LetterSpacing(1 * scale).TextColor(tn.fg.Alpha(0.75)).MaxLines(2)
		ui.Column(c).Gap(theme.Space8 * scale).Children(func() {
			ui.Box(c).Size(16*scale, 1.5).Background(tn.fg.Alpha(0.6))
			ui.Text(c, b.Title).FontWeight(600).FontSize(15 * scale).FixedLineHeight(19 * scale).LetterSpacing(-0.1 * scale).TextColor(tn.fg).MaxLines(5)
		})
		spine(c, false)
		if reader != "" {
			widgets.ReaderObi(c, reader, w)
		}
	})
	return box
}

// spine shades the left edge, as a book's binding.
func spine(c *ui.Context, small bool) {
	w := float32(6)
	if small {
		w = 2
	}
	ui.Box(c).Absolute().Left(0).Top(0).Bottom(0).Width(w).PassThrough().LinearGradient(ui.LinearGradient{
		Angle: 90,
		Stops: []ui.GradientStop{{Color: ui.RGBA(0, 0, 0, 0.22), At: 0}, {Color: ui.RGBA(255, 255, 255, 0.10), At: 0.6}, {Color: ui.Transparent, At: 1}},
	})
}
