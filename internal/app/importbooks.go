package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
)

// importState follows files being added, dropped on the window or
// chosen with Add books, across runs: a drop during a run waits in queue
// and joins it. Books the library has already are held for the review
// sheet instead of being added again.
type importState struct {
	queue   [][]string
	files   int      // e-books found since the import began
	skipped []string // dropped files that aren't e-books, by name
	added   []int64
	failed  []string // "name: reason"
	warned  []error  // file type plugins that failed on a file
	held    []*heldBook

	review   bool // the review sheet is open
	applying bool
}

// heldBook is a dropped file matching a book in the library, with what
// to do with it.
type heldBook struct {
	p      calibre.Prepared
	dup    *calibre.Duplicate
	action dupAction
	done   func() // removes what file type plugins made of the file
}

// releaseHeld removes what plugins made of held books' files.
func releaseHeld(held []*heldBook) {
	for _, h := range held {
		if h.done != nil {
			h.done()
		}
	}
}

type dupAction int

const (
	dupSkip dupAction = iota
	dupAddFormat
	dupReplace
	dupAddNew
)

func newHeld(p calibre.Prepared, dup *calibre.Duplicate) *heldBook {
	h := &heldBook{p: p, dup: dup}
	if !dup.HasFormat {
		h.action = dupAddFormat
	}
	return h
}

// actions are the choices the file has: adding its format only when the
// book lacks it, replacing only a file that differs.
func (h *heldBook) actions() []dupAction {
	switch {
	case !h.dup.HasFormat:
		return []dupAction{dupAddFormat, dupAddNew, dupSkip}
	case h.dup.SameFile:
		return []dupAction{dupSkip, dupAddNew}
	}
	return []dupAction{dupReplace, dupAddNew, dupSkip}
}

func (h *heldBook) label(act dupAction) string {
	switch act {
	case dupAddFormat:
		return "Add " + h.p.Format + " to this book"
	case dupReplace:
		return "Replace the " + h.p.Format + " file"
	case dupAddNew:
		return "Add as a new book"
	}
	return "Skip"
}

func (h *heldBook) detail() string {
	switch {
	case h.dup.SameFile:
		return "Same file as the one you have"
	case h.dup.HasFormat:
		return "You have this " + h.p.Format + " · this file is a different one"
	case len(h.dup.Book.Formats) == 0:
		return "You have no files of it · this file is " + h.p.Format
	}
	return "You have " + strings.Join(h.dup.Book.Formats, ", ") + " · this file is " + h.p.Format
}

// importFiles adds files, and the e-books inside folders, as new books.
// During a run, more files wait for it and are added after.
func (a *App) importFiles(paths []string) {
	lib := a.lib
	if lib == nil || len(paths) == 0 {
		return
	}
	if a.busy != "" {
		a.imp.queue = append(a.imp.queue, paths)
		return
	}
	a.busy, a.busyDone, a.busyTotal = "Adding books", 0, 0
	hooks, after := a.importPlugins(), a.hookPlugins()
	a.run(func() {
		// Folders are walked here, off the main thread: they may be large.
		var files, skipped []string
		for _, p := range paths {
			filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
				switch {
				case err != nil || d.IsDir():
				case isEbook(path):
					files = append(files, path)
				case path == p: // a file inside a folder is skipped silently
					skipped = append(skipped, filepath.Base(path))
				}
				return nil
			})
		}
		a.post(func() { a.busyTotal = len(files) })
		var added []int64
		var held []*heldBook
		var failed []string
		var warned []error
		dups, _ := lib.Duplicates() // without it, every file is added
		for i, f := range files {
			file, done, errs := processImport(context.Background(), hooks, f)
			warned = append(warned, errs...)
			p := calibre.Prepare(file, calibre.Metadata{}, nil)
			var dup *calibre.Duplicate
			if dups != nil {
				dup, _ = dups.Find(p)
			}
			if dup != nil {
				h := newHeld(p, dup)
				h.done = done // the file waits for the review
				held = append(held, h)
				continue
			}
			if id, err := lib.Add(p); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", filepath.Base(f), err))
			} else {
				added = append(added, id)
				if dups != nil {
					dups.Added(id, p.Metadata)
				}
				afterAdd(context.Background(), lib, after, id, p.Format, true)
			}
			done()
			a.post(func() { a.busyDone = i + 1 })
		}
		a.post(func() {
			a.busy = ""
			if lib != a.lib {
				releaseHeld(held)
				releaseHeld(a.imp.held)
				a.imp = importState{}
				return
			}
			im := &a.imp
			im.files += len(files)
			im.skipped = append(im.skipped, skipped...)
			im.added = append(im.added, added...)
			im.failed = append(im.failed, failed...)
			im.warned = append(im.warned, warned...)
			im.held = append(im.held, held...)
			if len(im.queue) > 0 {
				next := im.queue[0]
				im.queue = im.queue[1:]
				a.importFiles(next)
				return
			}
			a.finishImport()
		})
	})
}

// finishImport shows what an import did: the review sheet when books
// were held, else a toast.
func (a *App) finishImport() {
	im := &a.imp
	added := im.added
	a.reload(func() {
		if len(added) > 0 {
			a.query = catalog.Query{Sort: catalog.SortDateAdded, Descending: true}
			a.chosen.Clear()
			a.curID = added[len(added)-1]
		}
		for _, e := range im.warned {
			a.toastError(e)
		}
		im.warned = nil
		if len(im.held) > 0 {
			im.review = true
			return
		}
		switch {
		case im.files == 0 && len(im.skipped) > 0:
			a.toast("None of these are e-books. Dalibre adds %s, or folders that hold them.", supportedFormats)
		case im.files == 0:
			a.toast("No e-books found to add")
		case len(added) > 0:
			msg := fmt.Sprintf("Added %d %s", len(added), plural(len(added), "book", "books"))
			if n := len(im.skipped); n > 0 {
				msg += fmt.Sprintf(" · skipped %d %s that %s e-books", n, plural(n, "file", "files"), plural(n, "isn’t an", "aren’t"))
			}
			a.toast("%s", msg)
		}
		for _, e := range im.failed {
			a.toastError(errors.New(e))
		}
		*im = importState{}
	})
}

// applyReview does what the review sheet chose for each held book.
func (a *App) applyReview() {
	lib := a.lib
	im := &a.imp
	if lib == nil || im.applying {
		return
	}
	held := im.held
	im.applying = true
	after := a.hookPlugins()
	a.run(func() {
		var newBooks, updated int
		var last int64
		var errs []error
		for _, h := range held {
			var err error
			id := h.dup.Book.ID
			switch h.action {
			case dupAddFormat, dupReplace:
				if err = lib.AddFormat(id, h.p.File); err == nil {
					updated++
					afterAdd(context.Background(), lib, after, id, h.p.Format, false)
				}
			case dupAddNew:
				if id, err = lib.Add(h.p); err == nil {
					newBooks++
					afterAdd(context.Background(), lib, after, id, h.p.Format, true)
				}
			default:
				continue
			}
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", filepath.Base(h.p.File), err))
				continue
			}
			last = id
		}
		releaseHeld(held)
		a.post(func() {
			queue := a.imp.queue
			a.imp = importState{queue: queue}
			a.reload(func() {
				if last != 0 {
					a.curID = last
				}
				var parts []string
				if newBooks > 0 {
					parts = append(parts, fmt.Sprintf("Added %d %s", newBooks, plural(newBooks, "book", "books")))
				}
				if updated > 0 {
					parts = append(parts, fmt.Sprintf("updated %d", updated))
				}
				if len(parts) > 0 {
					a.toast("%s", strings.Join(parts, ", "))
				}
				for _, e := range errs {
					a.toastError(e)
				}
			})
		})
	})
}

// skipReview closes the review sheet, leaving the held books out.
func (a *App) skipReview() {
	if a.imp.applying {
		return
	}
	releaseHeld(a.imp.held)
	a.imp = importState{queue: a.imp.queue}
}

const supportedFormats = "EPUB, PDF, MOBI, AZW3, AZW, FB2, CBZ, DJVU, DOCX, RTF, PRC and TXT"

func isEbook(path string) bool {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".epub", ".pdf", ".mobi", ".azw3", ".azw", ".fb2", ".txt", ".cbz", ".djvu", ".docx", ".rtf", ".prc":
		return true
	}
	return false
}
