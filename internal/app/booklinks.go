package app

import (
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
)

// Book links, dalibre://book/{library}/{book}, bring Dalibre forward with
// a book chosen, from notes and other apps. {library} is the library's id
// (calibre's library_id uuid), so a link works wherever its folder is;
// {book} is calibre's book id, which renaming a book doesn't change.

// LinkScheme is the URL scheme of book links.
const LinkScheme = "dalibre"

// bookRef is the book a link names; the zero value names none.
type bookRef struct {
	library string
	book    int64
}

func (r bookRef) String() string {
	return LinkScheme + "://book/" + url.PathEscape(r.library) + "/" + strconv.FormatInt(r.book, 10)
}

// parseBookLink reads a book link.
func parseBookLink(raw string) (bookRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Scheme, LinkScheme) || !strings.EqualFold(u.Host, "book") {
		return bookRef{}, false
	}
	// Some launchers add a slash at the end.
	lib, id, ok := strings.Cut(strings.Trim(u.Path, "/"), "/")
	n, err := strconv.ParseInt(id, 10, 64)
	if !ok || lib == "" || err != nil || n <= 0 {
		return bookRef{}, false
	}
	return bookRef{library: lib, book: n}, true
}

// OpenLink opens a book link the system handed the app (main thread): it
// brings the window forward and chooses the book, opening its library
// first when another one is open. Links that come before the window wait
// for it.
func (a *App) OpenLink(raw string) {
	if a.win == nil {
		a.links = append(a.links, raw)
		return
	}
	a.post(func() {
		if a.win.IsMinimized() {
			a.win.Restore()
		}
		a.win.Show()
		a.win.Focus()
		a.openLink(raw)
	})
}

func (a *App) openLink(raw string) {
	ref, ok := parseBookLink(raw)
	if !ok {
		a.toasts = append(a.toasts, toastMsg{text: locales.LinkNotBook(raw), error: true})
		return
	}
	if a.lib != nil && !a.loading && a.libID == ref.library {
		a.showLinked(ref.book)
		return
	}
	root, known := a.settings.LibraryIDs[ref.library]
	if !known {
		a.linkNoLibrary(ref)
		return
	}
	a.linkWait = ref
	if !a.loading || a.loadingRoot != root {
		a.openLibrary(root)
	}
}

// linkedLibraryOpened chooses the book of the link waiting for the
// library just opened, whose id is id.
func (a *App) linkedLibraryOpened(id string) {
	ref := a.linkWait
	a.linkWait = bookRef{}
	switch ref.library {
	case "":
	case id:
		a.showLinked(ref.book)
	default:
		a.linkNoLibrary(ref)
	}
}

// showLinked chooses the open library's book id, as Show does.
func (a *App) showLinked(id int64) {
	if a.byID[id] == nil {
		a.toasts = append(a.toasts, toastMsg{text: locales.LinkNoBook(filepath.Base(a.lib.Root), strconv.FormatInt(id, 10)), error: true})
		return
	}
	a.showBook(id)
}

// linkNoLibrary says ref's library isn't one Dalibre has opened, offering
// to pick its folder and try the link again.
func (a *App) linkNoLibrary(ref bookRef) {
	a.toasts = append(a.toasts, toastMsg{text: locales.LinkNoLibrary(), error: true,
		action: locales.LinkOpenLibrary(), onAction: func() { a.chooseLibraryFor(ref) }})
}

// copyText puts text on the clipboard; tests replace it.
var copyText = func(text string) { mygo.Clipboard.Write(transfer.TextData(text)) }

// copyLink puts b's link on the clipboard.
func (a *App) copyLink(b *calibre.Book) {
	copyText(bookRef{library: a.libID, book: b.ID}.String())
	a.toasts = append(a.toasts, toastMsg{text: locales.LinkCopied(b.Title)})
}
