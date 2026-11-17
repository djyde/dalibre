package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

// Running a file type plugin on books from the book menu (Figma, 16 · Run
// plugin on books). Choosing the action is the whole decision: it runs in
// the background with the sidebar's progress, keeps each old file as
// ORIGINAL_<format>, and a toast says how it went, with Undo.

// maxMenuActions is how many file type plugins' actions the book menu
// shows itself; more go into a Plugins submenu.
const maxMenuActions = 2

// pluginActions adds the file type plugins' actions to the book menu, to
// run on books ids, and reports whether there were any.
func (a *App) pluginActions(m *ui.Menu, ids []int64) bool {
	ps := a.fileTypePlugins()
	results, others := a.bookActions()
	// What the plugin whose results show does with them comes first.
	for _, pa := range results {
		a.actionItem(m, pa, ids)
	}
	add := func(m *ui.Menu) {
		for _, p := range ps {
			if m.Item(p.Manifest.ActionTitle()).ID("run:" + p.Manifest.ID).Icon(icons.Menu(icons.Sparkle)).Disabled(a.busy != "").Chosen() {
				a.runPlugin(p, ids)
			}
		}
		for _, pa := range others {
			a.actionItem(m, pa, ids)
		}
	}
	switch n := len(ps) + len(others); {
	case n == 0:
	case n <= maxMenuActions:
		add(m)
	default:
		m.Submenu(locales.BooksMenuPlugins(), add).Icon(icons.Menu(icons.Puzzle))
	}
	return len(results)+len(ps)+len(others) > 0
}

// actionItem is a plugin's action in a menu, to run on books ids: greyed
// when it doesn't take them.
func (a *App) actionItem(m *ui.Menu, pa pluginAction, ids []int64) {
	if m.Item(pa.act.Title).ID("act:" + pa.p.Manifest.ID + ":" + pa.act.ID).Icon(icons.Menu(actionIcon(pa.act))).
		Disabled(a.busy != "" || !a.takes(pa.act, ids)).Chosen() {
		a.runAction(pa.p, pa.act, ids)
	}
}

// formatOrder is which of a book's formats a plugin is run on: the first
// it has.
var formatOrder = []string{"EPUB", "KEPUB", "AZW3", "MOBI", "AZW", "KFX", "PDF", "FB2", "DOCX", "TXT"}

// runFormat is the format of b a plugin is run on.
func runFormat(b *calibre.Book) string {
	best, rank := "", len(formatOrder)+1
	for _, f := range b.Formats {
		if strings.HasPrefix(f, "ORIGINAL_") {
			continue
		}
		r := slices.Index(formatOrder, f)
		if r < 0 {
			r = len(formatOrder)
		}
		if r < rank {
			best, rank = f, r
		}
	}
	return best
}

// undoStep puts one book's file back as it was before a run.
type undoStep struct {
	id       int64
	format   string // the format the run wrote
	prev     string // a copy of the file it replaced; "" when the book had none
	original string // the ORIGINAL_ format the run added, to remove; ""
}

// runOutcome is what a run did.
type runOutcome struct {
	changed, unchanged int
	failed             []error
	setup              error // the plugin needs setting up: the run stopped
	undo               []undoStep
	undoDir            string
}

// runPlugin runs file type plugin p on books ids in the background, one
// after the other, and writes what it makes into the library.
func (a *App) runPlugin(p *plugin.Plugin, ids []int64) {
	lib := a.lib
	if lib == nil || a.busy != "" {
		return
	}
	var books []*calibre.Book
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			books = append(books, b)
		}
	}
	title := p.Manifest.ActionTitle()
	a.busy, a.busyDone, a.busyTotal = title, 0, len(books)
	a.run(func() {
		out := runBooks(context.Background(), lib, p, books, func(done int) { a.post(func() { a.busyDone = done }) })
		a.post(func() {
			a.busy = ""
			a.reload(func() { a.runDone(p, title, out) })
		})
	})
}

// runBooks runs p on books and writes what it makes into lib.
func runBooks(ctx context.Context, lib *calibre.Library, p *plugin.Plugin, books []*calibre.Book, progress func(int)) runOutcome {
	var out runOutcome
	for i, b := range books {
		progress(i)
		err := runBook(ctx, lib, p, b, &out)
		if k := errKind(err); k == plugin.KindAuth || k == plugin.KindSettings {
			out.setup = err // every other book would fail as this one
			break
		}
		if err != nil {
			out.failed = append(out.failed, err)
		}
	}
	progress(len(books))
	return out
}

// runBook runs p on b's file and, when it makes a new one, writes it into
// the book: in place of the old file, kept as ORIGINAL_<format> unless the
// book has one, or as a format of its own when the plugin made another.
func runBook(ctx context.Context, lib *calibre.Library, p *plugin.Plugin, b *calibre.Book, out *runOutcome) error {
	format := runFormat(b)
	if format == "" {
		out.unchanged++
		return nil
	}
	d, err := lib.Details(b)
	if err != nil {
		return err
	}
	file := ""
	for _, f := range d.Files {
		if f.Format == format {
			file = f.Path
		}
	}
	if file == "" {
		out.unchanged++
		return nil
	}
	res, err := p.Process(ctx, file, plugin.ReasonManual, &plugin.Book{Title: b.Title, Authors: b.Authors, Identifiers: d.Identifiers})
	if err != nil {
		return err
	}
	defer res.Done()
	if res.File == "" {
		out.unchanged++
		return nil
	}
	if out.undoDir == "" {
		if out.undoDir, err = os.MkdirTemp("", "dalibre-undo-"); err != nil {
			return err
		}
	}
	step := undoStep{id: b.ID, format: res.Format}
	for _, f := range d.Files {
		if f.Format == res.Format { // the file the new one replaces, for Undo
			step.prev = filepath.Join(out.undoDir, fmt.Sprintf("%d.%s", b.ID, strings.ToLower(f.Format)))
			if err := copyFileTo(f.Path, step.prev); err != nil {
				return err
			}
		}
	}
	if res.Format == format && !slices.Contains(b.Formats, "ORIGINAL_"+format) {
		orig := filepath.Join(out.undoDir, fmt.Sprintf("%d.original_%s", b.ID, strings.ToLower(format)))
		if err := copyFileTo(file, orig); err != nil {
			return err
		}
		if err := lib.AddFormat(b.ID, orig); err != nil {
			return err
		}
		step.original = "ORIGINAL_" + format
	}
	if err := lib.AddFormat(b.ID, res.File); err != nil {
		return fmt.Errorf("%s: %w", b.Title, err)
	}
	out.changed++
	out.undo = append(out.undo, step)
	return nil
}

// runDone tells how a run went, in one toast.
func (a *App) runDone(p *plugin.Plugin, title string, out runOutcome) {
	switch {
	case out.setup != nil:
		reason := errText(out.setup)
		if errKind(out.setup) == plugin.KindSettings {
			reason = locales.RunPluginFillIn(strings.Join(p.MissingSettings(), ", "))
		}
		id := p.Manifest.ID
		a.toasts = append(a.toasts, toastMsg{text: locales.RunPluginNeedsSetup(p.Manifest.Name, reason), error: true,
			action: locales.GetBooksOpenPluginSettings(), onAction: func() { a.openPlugins(id) }})
	case len(out.failed) > 0:
		a.toasts = append(a.toasts, toastMsg{text: locales.RunPluginFailedOn(title, len(out.failed), errText(out.failed[0])), error: true})
	}
	switch {
	case out.changed > 0:
		lib, undo, dir := a.lib, out.undo, out.undoDir
		a.toasts = append(a.toasts, toastMsg{text: locales.RunPluginChanged(title, out.changed),
			action: locales.CommonUndo(), onAction: func() { a.undoRun(lib, undo, dir) },
			onClose: func() { go os.RemoveAll(dir) }})
	case out.setup == nil && len(out.failed) == 0:
		a.toast("%s", locales.RunPluginNothing(title))
	}
}

// undoRun puts back the files a run replaced.
func (a *App) undoRun(lib *calibre.Library, steps []undoStep, dir string) {
	a.run(func() {
		var errs []error
		for _, s := range steps {
			var err error
			if s.prev != "" {
				err = lib.AddFormat(s.id, s.prev)
			} else {
				err = lib.RemoveFormat(s.id, s.format)
			}
			if err == nil && s.original != "" {
				err = lib.RemoveFormat(s.id, s.original)
			}
			if err != nil {
				errs = append(errs, err)
			}
		}
		os.RemoveAll(dir)
		a.post(func() {
			a.reload(func() {
				for _, e := range errs {
					a.toastError(e)
				}
			})
		})
	})
}

func copyFileTo(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
