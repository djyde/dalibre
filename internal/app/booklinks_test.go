package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/calibre"
)

func TestParseBookLink(t *testing.T) {
	ref := bookRef{library: "9f3c-uuid", book: 42}
	for _, raw := range []string{ref.String(), "DALIBRE://Book/9f3c-uuid/42/"} {
		if got, ok := parseBookLink(raw); !ok || got != ref {
			t.Errorf("parseBookLink(%q) = %+v, %v", raw, got, ok)
		}
	}
	for _, raw := range []string{"dalibre://bok/9f3c-uuid/42", "calibre://book/9f3c-uuid/42", "dalibre://book/9f3c-uuid", "dalibre://book//42", "dalibre://book/9f3c-uuid/0", "dalibre://book/9f3c-uuid/x"} {
		if _, ok := parseBookLink(raw); ok {
			t.Errorf("parseBookLink(%q) took it", raw)
		}
	}
}

// A link chooses its book in its own library, opening that library when
// another is open, and says why when it can't.
func TestOpenBookLink(t *testing.T) {
	a := newTestApp(t)
	first, firstID := a.lib.Root, a.libID
	if firstID == "" {
		t.Fatal("library has no id")
	}
	dune := findBook(t, a, "Dune")

	// Another library, whose book 2 is not the first one's book 2.
	other := filepath.Join(t.TempDir(), "Other")
	lib, err := calibre.Create(other)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Solaris", "Roadside Picnic"} {
		f := filepath.Join(t.TempDir(), "book.epub")
		os.WriteFile(f, []byte("x"), 0o644)
		if _, err := lib.AddBook(calibre.NewBook{File: f, Metadata: calibre.Metadata{Title: title, Authors: []string{"A"}}}); err != nil {
			t.Fatal(err)
		}
	}
	otherID := lib.UUID()
	lib.Close()
	a.openLibrary(other)
	picnic := findBook(t, a, "Roadside Picnic")
	a.openLibrary(first)

	// The open library: the search is cleared and the book chosen.
	a.query.Search = "zzz"
	a.openLink(bookRef{library: firstID, book: dune.ID}.String())
	if a.curID != dune.ID || a.query.Search != "" || len(a.toasts) != 0 {
		t.Fatalf("same library: cur %d, search %q, toasts %v", a.curID, a.query.Search, a.toasts)
	}

	// Another library Dalibre has opened: it opens, then the book is chosen.
	a.openLink(bookRef{library: otherID, book: picnic.ID}.String())
	if a.lib == nil || a.lib.Root != other || a.byID[a.curID] == nil || a.byID[a.curID].Title != "Roadside Picnic" {
		t.Fatalf("other library: open %v, cur %d", a.lib, a.curID)
	}
	if a.linkWait != (bookRef{}) {
		t.Errorf("link still waiting: %+v", a.linkWait)
	}

	cases := []struct{ link, toast string }{
		{bookRef{library: otherID, book: 999}.String(), "Other has no book with id 999"},
		{bookRef{library: "unknown", book: 1}.String(), "isn’t one Dalibre has opened"},
		{"dalibre://bok/x/1", "dalibre://bok/x/1 isn’t a book link"},
	}
	for _, c := range cases {
		a.toasts = nil
		a.openLink(c.link)
		if len(a.toasts) != 1 || !a.toasts[0].error || !strings.Contains(a.toasts[0].text, c.toast) {
			t.Errorf("%s: toasts %+v, want %q", c.link, a.toasts, c.toast)
		}
		if a.lib.Root != other {
			t.Errorf("%s: library changed to %s", c.link, a.lib.Root)
		}
	}
	// The unknown library's toast offers to pick its folder.
	a.toasts = nil
	a.openLink(bookRef{library: "unknown", book: 1}.String())
	if a.toasts[0].onAction == nil {
		t.Error("no Open library… action")
	}
}

func findBook(t *testing.T, a *App, title string) *calibre.Book {
	t.Helper()
	for _, b := range a.books {
		if b.Title == title {
			return b
		}
	}
	t.Fatalf("no %q in %s", title, a.lib.Root)
	return nil
}

// A book's menu copies its link.
func TestCopyLinkMenu(t *testing.T) {
	a := newTestApp(t)
	var copied string
	saved := copyText
	t.Cleanup(func() { copyText = saved })
	copyText = func(s string) { copied = s }
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if r, ok := tt.Find("Copy link"); ok {
		tt.Move(r.X+10, r.Y+r.H/2)
		tt.Frame()
	}
	shot(t, tt, "book-menu-copy-link")
	if err := tt.ChooseMenuItem("Copy link"); err != nil {
		t.Fatalf("%v; menu %q", err, tt.Menu())
	}
	tt.Advance(time.Second) // the toast slides in
	shot(t, tt, "copy-link-toast")
	if !tt.HasText("Copied link to Dune") {
		t.Errorf("no toast: %q", tt.Texts())
	}
	dune := findBook(t, a, "Dune")
	if want := (bookRef{library: a.libID, book: dune.ID}).String(); copied != want {
		t.Fatalf("copied %q, want %q", copied, want)
	}
}
