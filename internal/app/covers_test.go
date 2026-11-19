package app

import (
	"image"
	"image/jpeg"
	"os"
	"testing"
	"time"
)

// A cover stays decoded while builds draw it and for coverKeep after,
// then goes, so scrolling through a large library doesn't keep every
// cover it passed (#25).
func TestCoverCacheDropsUndrawnCovers(t *testing.T) {
	a := newTestApp(t)
	b := a.books[0]
	f, err := os.Create(a.lib.CoverPath(b))
	if err != nil {
		t.Fatal(err)
	}
	jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 60, 90)), nil)
	f.Close()
	b.HasCover = true

	cc := a.covers
	now := time.Now()
	a.cover(b, 80) // loads
	cc.sweep(now)
	if a.cover(b, 80) == nil {
		t.Fatal("cover didn't load")
	}
	// Drawn in this build, then not in the next ones.
	cc.sweep(now.Add(time.Second))
	if undrawn := cc.sweep(now.Add(time.Second + coverKeep - time.Millisecond)); !undrawn || len(cc.bitmaps) != 1 {
		t.Fatalf("dropped before coverKeep: undrawn %v, %d cached", undrawn, len(cc.bitmaps))
	}
	if undrawn := cc.sweep(now.Add(time.Second + coverKeep)); undrawn || len(cc.bitmaps) != 0 {
		t.Fatalf("kept after coverKeep: undrawn %v, %d cached", undrawn, len(cc.bitmaps))
	}
	// One drawn in every build stays however long it shows.
	a.cover(b, 80)
	for i := range 5 {
		a.cover(b, 80)
		cc.sweep(now.Add(time.Duration(i) * coverKeep))
	}
	if len(cc.bitmaps) != 1 {
		t.Fatalf("a cover drawn in every build went")
	}
}
