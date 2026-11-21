package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/calibre"
)

// TestDropImport drops a new book, a new format of a book the library
// has, a copy of one and a picture: the new book goes in at once, the
// two others wait in the review sheet, the picture is listed as skipped.
func TestDropImport(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	dir := t.TempDir()
	write := func(name string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte("x"), 0o644)
		return p
	}
	newBook := write("Klara and the Sun - Kazuo Ishiguro.epub")
	dunePDF := write("Dune - Frank Herbert.pdf")
	piranesi := write("Piranesi - Susanna Clarke.epub") // the sample's file holds "x" too
	pic := write("cover.jpg")
	before := len(a.books)

	if !tt.DropFiles(700, 400, newBook, dunePDF, piranesi, pic) {
		t.Fatal("drop not taken")
	}
	tt.Frame()
	if len(a.books) != before+1 {
		t.Fatalf("books = %d, want %d", len(a.books), before+1)
	}
	if !a.imp.review || len(a.imp.held) != 2 {
		t.Fatalf("review %v, held %d", a.imp.review, len(a.imp.held))
	}
	for _, s := range []string{"2 books are already in your library", "Add PDF to this book", "Same file as the one you have", "cover.jpg — not an e-book"} {
		if !tt.HasText(s) {
			t.Errorf("sheet misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "import-review")

	if err := tt.Click("Apply"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.imp.review || len(a.imp.held) != 0 {
		t.Fatal("sheet still open")
	}
	if len(a.books) != before+1 {
		t.Errorf("the same file was added again: %d books", len(a.books))
	}
	for _, b := range a.books {
		if b.Title == "Dune" && !slices.Contains(b.Formats, "PDF") {
			t.Errorf("Dune formats = %v", b.Formats)
		}
	}
}

// TestOpenFileBeforeLibrary opens a file with the app before its library
// is open, as Finder does when it launches the app: the file waits, then
// is added to the library once it opens.
func TestOpenFileBeforeLibrary(t *testing.T) {
	root := newTestApp(t).loadingRoot
	f := filepath.Join(t.TempDir(), "Klara and the Sun - Kazuo Ishiguro.azw3")
	os.WriteFile(f, []byte("x"), 0o644)

	a := New("")
	a.run = func(fn func()) { fn() }
	a.trash = nil
	a.covers.thumbs = nil
	a.OpenFile(f)
	a.openLibrary(root)
	t.Cleanup(func() { a.lib.Close() })

	if len(a.opened) != 0 {
		t.Fatalf("still waiting: %q", a.opened)
	}
	i := slices.IndexFunc(a.books, func(b *calibre.Book) bool { return b.Title == "Klara and the Sun" })
	if i < 0 {
		t.Fatalf("not added; %d books", len(a.books))
	}
	if got := a.books[i].Formats; !slices.Equal(got, []string{"AZW3"}) {
		t.Errorf("formats = %v", got)
	}
}

// TestDropNothingAddable drops only a picture: nothing is added and the
// toast names what can be.
func TestDropNothingAddable(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	pic := filepath.Join(t.TempDir(), "cover.jpg")
	os.WriteFile(pic, []byte("x"), 0o644)
	before := len(a.books)
	tt.DropFiles(700, 400, pic)
	tt.Frame()
	if len(a.books) != before || a.imp.review {
		t.Fatalf("books %d review %v", len(a.books), a.imp.review)
	}
	if !tt.HasText("None of these are e-books. Dalibre adds " + supportedFormats + ", or folders that hold them.") {
		t.Errorf("no toast; texts %q", tt.Texts())
	}
}
