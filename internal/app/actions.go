package app

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
	"dalibre/internal/plugin"
)

// Plugin actions (Figma, 17 · Plugin actions): what a plugin runs on the
// books chosen, from the book menu, or on the whole library, from the
// sidebar's Tools. It runs in the background with the sidebar's progress,
// may ask the user a form on the way, and returns what it found and what
// it wants changed: Dalibre shows the books it found as a view of the
// list, and applies or asks about the changes.

// actionState is the actions' part of the app's state.
type actionState struct {
	// cancel stops the action running; nil when none is.
	cancel context.CancelFunc
	// form is the form the action running asks, review the changes one
	// returned that wait for the user, results the books one found.
	form    *formAsk
	review  *changeReview
	results *actionResults
	// answer answers forms in place of the dialog, as tests do.
	answer func(plugin.Form) (map[string]any, error)
}

// pluginAction is an action with the plugin whose it is.
type pluginAction struct {
	p   *plugin.Plugin
	act *plugin.Action
}

// toolActions are the actions that run on the whole library, for the
// sidebar's Tools. Main thread.
func (a *App) toolActions() []pluginAction {
	var out []pluginAction
	for _, p := range a.plugins {
		for i := range p.Manifest.Capabilities.Actions {
			if act := &p.Manifest.Capabilities.Actions[i]; act.Scope == plugin.ScopeLibrary {
				out = append(out, pluginAction{p, act})
			}
		}
	}
	return out
}

// bookActions are the actions that run on books: first those of the
// plugin whose results are showing that are for its results, then the
// others. Main thread.
func (a *App) bookActions() (results, others []pluginAction) {
	showing := ""
	if r := a.act.results; r != nil && a.page == pageLibrary {
		showing = r.plugin
	}
	for _, p := range a.plugins {
		for i := range p.Manifest.Capabilities.Actions {
			act := &p.Manifest.Capabilities.Actions[i]
			switch {
			case act.Scope != plugin.ScopeBooks:
			case act.In == plugin.InResults && p.Manifest.ID == showing:
				results = append(results, pluginAction{p, act})
			case act.In == "":
				others = append(others, pluginAction{p, act})
			}
		}
	}
	return results, others
}

// takes reports whether act can run on books ids.
func (a *App) takes(act *plugin.Action, ids []int64) bool {
	formats := make([][]string, 0, len(ids))
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			formats = append(formats, b.Formats)
		}
	}
	return act.Takes(formats)
}

// actionIcon is the icon an action picked from Dalibre's set.
func actionIcon(act *plugin.Action) *ui.SVG {
	if i, ok := actionIcons[act.Icon]; ok {
		return i
	}
	return icons.Puzzle
}

var actionIcons = map[string]*ui.SVG{
	"puzzle": icons.Puzzle, "sparkle": icons.Sparkle, "copy": icons.Copy, "check": icons.Check, "check-circle": icons.CheckCircle,
	"x": icons.X, "layers": icons.Layers, "file": icons.File, "pencil": icons.Pencil, "tag": icons.Tag, "search": icons.Search,
	"trash": icons.Trash, "refresh": icons.Refresh, "replace": icons.Replace, "bookmark": icons.Bookmark,
	"book-open": icons.BookOpen, "library": icons.Library, "database": icons.Database, "globe": icons.Globe, "external": icons.External,
}

// actionFile is the file of b an action is given: of the first of the
// action's formats the book has, else the one a file type plugin would
// run on.
func actionFile(act *plugin.Action, b *calibre.Book, d *calibre.Details) string {
	format := runFormat(b)
	for _, f := range act.Formats {
		if slices.Contains(b.Formats, f) {
			format = f
			break
		}
	}
	for _, f := range d.Files {
		if f.Format == format {
			return f.Path
		}
	}
	return ""
}

// libraryPager gives an action the library's books a page at a time, in
// id order, as they were when it began. Any goroutine.
// With comments, each book's comments are read from lib as well.
func libraryPager(lib *calibre.Library, books []*calibre.Book, index map[string]int64) func(after int64, limit int, comments bool) ([]plugin.BookInfo, error) {
	sorted := slices.Clone(books)
	slices.SortFunc(sorted, func(x, y *calibre.Book) int { return cmp.Compare(x.ID, y.ID) })
	idents := map[int64]map[string]string{}
	for k, id := range index {
		if typ, v, ok := strings.Cut(k, ":"); ok {
			if idents[id] == nil {
				idents[id] = map[string]string{}
			}
			idents[id][typ] = v
		}
	}
	return func(after int64, limit int, comments bool) ([]plugin.BookInfo, error) {
		i, _ := slices.BinarySearchFunc(sorted, after+1, func(b *calibre.Book, id int64) int { return cmp.Compare(b.ID, id) })
		out := []plugin.BookInfo{}
		for ; i < len(sorted) && len(out) < limit; i++ {
			b := sorted[i]
			d := &calibre.Details{Identifiers: idents[b.ID]}
			if comments && lib != nil {
				full, err := lib.Details(b)
				if err != nil {
					return nil, err
				}
				d.Comments = full.Comments
			}
			out = append(out, bookInfo(b, d))
		}
		return out, nil
	}
}

// runAction runs act of plugin p in the background: on books ids, or on
// the library.
func (a *App) runAction(p *plugin.Plugin, act *plugin.Action, ids []int64) {
	lib := a.lib
	if lib == nil || a.busy != "" {
		return
	}
	var books []*calibre.Book
	if act.Scope == plugin.ScopeBooks {
		for _, id := range ids {
			if b := a.byID[id]; b != nil {
				books = append(books, b)
			}
		}
		if len(books) == 0 {
			return
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.busyDone, a.busyTotal = act.Label(), 0, 0
	a.act.cancel = cancel
	host := &plugin.ActionHost{
		Covers: actionCovers(lib, act, books),
		Form:   a.formAsker(p, act),
		Books:  libraryPager(lib, a.books, a.idIndex),
		Progress: func(done, total int) {
			a.post(func() {
				if a.act.cancel != nil {
					a.busyDone, a.busyTotal = done, total
				}
			})
		},
	}
	hooks, after := a.importPlugins(), a.hookPlugins()
	write := p.Manifest.Permissions.Library == plugin.LibraryWrite
	a.run(func() {
		var out actionOutcome
		infos, files, err := actionBooks(lib, act, books)
		if err == nil {
			var res *plugin.ActionResult
			if res, err = p.RunAction(ctx, act.ID, infos, files, host); err == nil {
				out = applyAction(ctx, lib, res, write, hooks, after)
				res.Done()
			}
		}
		cancel()
		a.post(func() {
			a.busy, a.act.cancel, a.act.form = "", nil, nil
			a.reload(func() { a.actionDone(p, act, ids, out, err) })
		})
	})
}

// actionBooks is what an action is given of books: their details, and a
// file of each when it takes files. Any goroutine.
func actionBooks(lib *calibre.Library, act *plugin.Action, books []*calibre.Book) ([]plugin.BookInfo, map[int64]string, error) {
	var infos []plugin.BookInfo
	files := map[int64]string{}
	for _, b := range books {
		d, err := lib.Details(b)
		if err != nil {
			return nil, nil, err
		}
		infos = append(infos, bookInfo(b, d))
		if act.Files {
			if f := actionFile(act, b, d); f != "" {
				files[b.ID] = f
			}
		}
	}
	return infos, files, nil
}

// actionCovers are the covers of books, by id, for an action that takes
// them.
func actionCovers(lib *calibre.Library, act *plugin.Action, books []*calibre.Book) map[int64]string {
	if !act.Covers {
		return nil
	}
	out := map[int64]string{}
	for _, b := range books {
		if b.HasCover {
			out[b.ID] = lib.CoverPath(b)
		}
	}
	return out
}

// metaUndo puts a book's details back as they were.
type metaUndo struct {
	id  int64
	old calibre.Metadata
}

// actionOutcome is what an action came to, once what it returned was
// written into the library.
type actionOutcome struct {
	message string
	groups  []plugin.Group
	open    string
	changed int     // books whose details or files changed
	touched []int64 // those books
	added   []int64 // books added
	// review are changes waiting for the user, from a plugin that may not
	// change books without asking.
	review []*reviewItem
	failed []error
	undo   []metaUndo
	files  runOutcome // the formats written, to undo
}

// applyAction writes what an action returned into lib: the files it
// made, and the changes it asks for when write, which otherwise wait for
// review. Any goroutine.
func applyAction(ctx context.Context, lib *calibre.Library, res *plugin.ActionResult, write bool, hooks, after []*plugin.Plugin) actionOutcome {
	return applyActionTo(ctx, lib, res, write, true, hooks, after)
}

// applyActionTo is applyAction, leaving the files unwritten without
// files: what an agent is shown before it says to apply.
func applyActionTo(ctx context.Context, lib *calibre.Library, res *plugin.ActionResult, write, files bool, hooks, after []*plugin.Plugin) actionOutcome {
	out := actionOutcome{message: res.Message, groups: res.Groups, open: res.Open}
	touched := map[int64]bool{}
	if !files {
		res = &plugin.ActionResult{Changes: res.Changes}
	}
	for _, f := range res.Formats {
		err := putFormat(lib, f, &out.files)
		if err != nil {
			out.failed = append(out.failed, err)
			continue
		}
		touched[f.Book] = true
	}
	for _, nb := range res.Add {
		file, done, warned := processImport(ctx, hooks, nb.File)
		out.failed = append(out.failed, warned...)
		var over calibre.Metadata
		if nb.Metadata != nil {
			over = applyChanges(calibre.Metadata{}, nb.Metadata)
		}
		id, err := lib.ImportWith(file, over, nil)
		done()
		if err != nil {
			out.failed = append(out.failed, err)
			continue
		}
		afterAdd(ctx, lib, after, id, strings.ToUpper(strings.TrimPrefix(filepath.Ext(file), ".")), true)
		out.added = append(out.added, id)
	}
	for _, c := range res.Changes {
		b, err := lib.Book(c.Book)
		if err != nil {
			continue // the book went while the action ran
		}
		d, err := lib.Details(b)
		if err != nil {
			out.failed = append(out.failed, err)
			continue
		}
		old := b.Metadata(d)
		now := b.Metadata(d)
		now = applyChanges(now, c.Metadata)
		lines := metadataDiff(old, now)
		if len(lines) == 0 {
			continue
		}
		if !write {
			out.review = append(out.review, &reviewItem{id: b.ID, title: b.Title, on: true, lines: lines, old: old, now: now})
			continue
		}
		if err := lib.UpdateMetadata(b.ID, now); err != nil {
			out.failed = append(out.failed, fmt.Errorf("%s: %w", b.Title, err))
			continue
		}
		out.undo = append(out.undo, metaUndo{b.ID, old})
		touched[b.ID] = true
	}
	out.changed, out.touched = len(touched), slices.Sorted(maps.Keys(touched))
	return out
}

// putFormat writes the file an action made into its book: in place of a
// file of the same format, kept as ORIGINAL_<format> unless the book has
// one, or as a new format.
func putFormat(lib *calibre.Library, f plugin.NewFormat, out *runOutcome) error {
	b, err := lib.Book(f.Book)
	if err != nil {
		return err
	}
	d, err := lib.Details(b)
	if err != nil {
		return err
	}
	if out.undoDir == "" {
		if out.undoDir, err = os.MkdirTemp("", "dalibre-undo-"); err != nil {
			return err
		}
	}
	step := undoStep{id: b.ID, format: f.Format}
	for _, have := range d.Files {
		if have.Format != f.Format {
			continue
		}
		step.prev = filepath.Join(out.undoDir, fmt.Sprintf("%d.%s", b.ID, strings.ToLower(f.Format)))
		if err := copyFileTo(have.Path, step.prev); err != nil {
			return err
		}
		if !slices.Contains(b.Formats, "ORIGINAL_"+f.Format) {
			orig := filepath.Join(out.undoDir, fmt.Sprintf("%d.original_%s", b.ID, strings.ToLower(f.Format)))
			if err := copyFileTo(have.Path, orig); err != nil {
				return err
			}
			if err := lib.AddFormat(b.ID, orig); err != nil {
				return err
			}
			step.original = "ORIGINAL_" + f.Format
		}
	}
	// The file goes in under its format's name; the library names it.
	named := filepath.Join(filepath.Dir(f.File), "book."+strings.ToLower(f.Format))
	if err := os.Rename(f.File, named); err != nil {
		return err
	}
	if err := lib.AddFormat(b.ID, named); err != nil {
		return fmt.Errorf("%s: %w", b.Title, err)
	}
	out.undo = append(out.undo, step)
	return nil
}

// changeLine is one detail of a book an action changes.
type changeLine struct {
	name     string // as plugins and agents call the detail: "title"
	field    func() string
	old, now string
}

// metadataDiff is what differs from old to now, a line for each detail.
func metadataDiff(old, now calibre.Metadata) []changeLine {
	var out []changeLine
	add := func(name string, field func() string, a, b string) {
		if a != b {
			out = append(out, changeLine{name, field, a, b})
		}
	}
	list := func(l []string) string { return strings.Join(l, ", ") }
	series := func(m calibre.Metadata) string {
		if m.Series == "" {
			return ""
		}
		return locales.BooksSeriesIndex(m.Series, seriesIndex(m.SeriesIndex))
	}
	date := func(m calibre.Metadata) string {
		if m.Pubdate.IsZero() {
			return ""
		}
		return m.Pubdate.Format("2006-01-02")
	}
	idents := func(m calibre.Metadata) string {
		var parts []string
		for _, k := range slices.Sorted(maps.Keys(m.Identifiers)) {
			parts = append(parts, k+":"+m.Identifiers[k])
		}
		return list(parts)
	}
	rating := func(m calibre.Metadata) string {
		if m.Rating == 0 {
			return ""
		}
		return strings.Repeat("★", (m.Rating+1)/2)
	}
	text := func(m calibre.Metadata) string {
		t := strings.Join(strings.Fields(htmlToText(m.Comments)), " ")
		if r := []rune(t); len(r) > 80 {
			t = string(r[:79]) + "…"
		}
		return t
	}
	add("title", locales.CommonTitle, old.Title, now.Title)
	add("authors", locales.CommonAuthors, list(old.Authors), list(now.Authors))
	add("series", locales.CommonSeries, series(old), series(now))
	add("tags", locales.CommonTags, list(old.Tags), list(now.Tags))
	add("publisher", locales.CommonPublisher, old.Publisher, now.Publisher)
	add("pubdate", locales.CommonPublished, date(old), date(now))
	add("languages", locales.CommonLanguage, list(old.Languages), list(now.Languages))
	add("identifiers", locales.CommonIdentifiers, idents(old), idents(now))
	add("rating", locales.CommonRating, rating(old), rating(now))
	if old.Comments != now.Comments {
		out = append(out, changeLine{"comments", locales.CommonDescription, text(old), text(now)})
	}
	return out
}

// actionDone tells how an action went, and shows what it found. Main
// thread, the library read again.
func (a *App) actionDone(p *plugin.Plugin, act *plugin.Action, ids []int64, out actionOutcome, err error) {
	title := act.Label()
	switch k := errKind(err); {
	case err == nil:
	case k == plugin.KindCancelled || isCanceled(err):
		return // the user's own doing
	case k == plugin.KindAuth || k == plugin.KindSettings:
		reason := errText(err)
		if k == plugin.KindSettings {
			reason = locales.RunPluginFillIn(strings.Join(p.MissingSettings(), ", "))
		}
		id := p.Manifest.ID
		a.toasts = append(a.toasts, toastMsg{text: locales.RunPluginNeedsSetup(p.Manifest.Name, reason), error: true,
			action: locales.GetBooksOpenPluginSettings(), onAction: func() { a.openPlugins(id) }})
		return
	default:
		a.toasts = append(a.toasts, toastMsg{text: locales.ActionFailed(title, errText(err)), error: true})
		return
	}
	for _, e := range out.failed {
		a.toastError(e)
	}
	said := len(out.failed) > 0
	if len(out.groups) > 0 {
		a.showResults(p, act, ids, out)
		said = true
	} else if r := a.act.results; r != nil && r.plugin == p.Manifest.ID && r.action == act.ID {
		a.act.results = nil // run again, and nothing is left to show
	}
	if out.open != "" {
		openURL(out.open)
		said = true
	}
	if len(out.review) > 0 {
		a.act.review = &changeReview{title: title, items: out.review}
		said = true
	}
	if out.changed > 0 {
		lib, undo, files := a.lib, out.undo, out.files
		a.toasts = append(a.toasts, toastMsg{text: locales.RunPluginChanged(title, out.changed),
			action: locales.CommonUndo(), onAction: func() { a.undoAction(lib, undo, files) },
			onClose: func() { go os.RemoveAll(files.undoDir) }})
		said = true
	} else if out.files.undoDir != "" {
		go os.RemoveAll(out.files.undoDir)
	}
	if n := len(out.added); n > 0 {
		first := out.added[0]
		a.toasts = append(a.toasts, toastMsg{text: locales.ActionAdded(title, n), action: locales.ActionShow(), onAction: func() { a.showBook(first) }})
		said = true
	}
	switch {
	case out.message != "" && len(out.groups) == 0:
		a.toast("%s · %s", title, out.message)
	case !said:
		a.toast("%s", locales.RunPluginNothing(title))
	}
}

// showBook shows book id in the library, chosen.
func (a *App) showBook(id int64) {
	if a.byID[id] == nil {
		return
	}
	a.query.Search = ""
	a.setQuery(catalog.AllBooks, catalog.Filter{})
	a.curID = id
}

// undoAction puts back what an action changed: the books' details and
// the files it replaced.
func (a *App) undoAction(lib *calibre.Library, undo []metaUndo, files runOutcome) {
	a.run(func() {
		var errs []error
		for _, u := range undo {
			if err := lib.UpdateMetadata(u.id, u.old); err != nil {
				errs = append(errs, err)
			}
		}
		a.post(func() {
			for _, e := range errs {
				a.toastError(e)
			}
			if len(files.undo) > 0 {
				a.undoRun(lib, files.undo, files.undoDir) // reloads
				return
			}
			a.reload(nil)
		})
	})
}

// ---- Forms

// formAsk is a form an action asks the user, in a dialog.
type formAsk struct {
	p     *plugin.Plugin
	act   *plugin.Action
	form  plugin.Form
	texts []string // a text field's text, a select's chosen title
	bools []bool   // a toggle
	sets  [][]bool // a multiselect's options
	reply chan formReply
}

type formReply struct {
	answers map[string]any
	err     error
}

func newFormAsk(p *plugin.Plugin, act *plugin.Action, f plugin.Form, reply chan formReply) *formAsk {
	fa := &formAsk{p: p, act: act, form: f, reply: reply,
		texts: make([]string, len(f.Fields)), bools: make([]bool, len(f.Fields)), sets: make([][]bool, len(f.Fields))}
	for i, fd := range f.Fields {
		switch v := fd.Value.(type) {
		case string:
			fa.texts[i] = v
			if fd.Type == plugin.FieldSelect {
				for _, o := range fd.Options {
					if o.Value == v {
						fa.texts[i] = o.Title
					}
				}
			}
		case bool:
			fa.bools[i] = v
		case []string:
			fa.sets[i] = make([]bool, len(fd.Options))
			for j, o := range fd.Options {
				fa.sets[i][j] = slices.Contains(v, o.Value)
			}
		}
	}
	return fa
}

// answers are what the dialog holds, by field key.
func (fa *formAsk) answers() map[string]any {
	out := map[string]any{}
	for i, fd := range fa.form.Fields {
		switch fd.Type {
		case plugin.FieldText:
			out[fd.Key] = fa.texts[i]
		case plugin.FieldSelect:
			out[fd.Key] = fd.Options[0].Value
			for _, o := range fd.Options {
				if o.Title == fa.texts[i] {
					out[fd.Key] = o.Value
					break
				}
			}
		case plugin.FieldToggle:
			out[fd.Key] = fa.bools[i]
		case plugin.FieldMultiselect:
			vals := []string{}
			for j, o := range fd.Options {
				if fa.sets[i][j] {
					vals = append(vals, o.Value)
				}
			}
			out[fd.Key] = vals
		}
	}
	return out
}

// formAsker is how the action running asks the user a form: in a dialog,
// waited for on the action's goroutine.
func (a *App) formAsker(p *plugin.Plugin, act *plugin.Action) func(context.Context, plugin.Form) (map[string]any, error) {
	return func(ctx context.Context, f plugin.Form) (map[string]any, error) {
		if a.act.answer != nil {
			return a.act.answer(f)
		}
		reply := make(chan formReply, 1)
		a.post(func() {
			if f.Remember {
				if st := a.settings.Plugins[p.Manifest.ID]; st != nil {
					f.Prefill(st.Answers[act.ID])
				}
			}
			a.act.form = newFormAsk(p, act, f, reply)
		})
		select {
		case r := <-reply:
			return r.answers, r.err
		case <-ctx.Done():
			a.post(func() {
				if fa := a.act.form; fa != nil && fa.reply == reply {
					a.act.form = nil
				}
			})
			return nil, ctx.Err()
		}
	}
}

// answerForm closes the form with the user's answers, or cancelled.
func (a *App) answerForm(submit bool) {
	fa := a.act.form
	if fa == nil {
		return
	}
	a.act.form = nil
	if submit {
		answers := fa.answers()
		if fa.form.Remember {
			st := a.settings.Plugin(fa.p.Manifest.ID)
			if st.Answers == nil {
				st.Answers = map[string]map[string]any{}
			}
			// As JSON would have them, so they read back the same.
			saved := map[string]any{}
			for k, v := range answers {
				if list, ok := v.([]string); ok {
					items := make([]any, len(list))
					for i, s := range list {
						items[i] = s
					}
					v = items
				}
				saved[k] = v
			}
			st.Answers[fa.act.ID] = saved
			a.saveSettings()
		}
		fa.reply <- formReply{answers: answers}
	} else {
		fa.reply <- formReply{err: plugin.ErrCancelled}
	}
}

// ---- Changes to review

// changeReview is the changes an action asks for, shown before they are
// made: its plugin may not change books without asking.
type changeReview struct {
	title string
	items []*reviewItem
}

// reviewItem is the changes to one book.
type reviewItem struct {
	id       int64
	title    string
	on       bool
	lines    []changeLine
	old, now calibre.Metadata
}

// applyChangeReview makes the changes the user left on.
func (a *App) applyChangeReview() {
	rv, lib := a.act.review, a.lib
	a.act.review = nil
	if rv == nil || lib == nil {
		return
	}
	a.run(func() {
		var undo []metaUndo
		var errs []error
		for _, it := range rv.items {
			if !it.on {
				continue
			}
			if err := lib.UpdateMetadata(it.id, it.now); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", it.title, err))
				continue
			}
			undo = append(undo, metaUndo{it.id, it.old})
		}
		a.post(func() {
			a.reload(func() {
				for _, e := range errs {
					a.toastError(e)
				}
				if len(undo) > 0 {
					a.toasts = append(a.toasts, toastMsg{text: locales.RunPluginChanged(rv.title, len(undo)),
						action: locales.CommonUndo(), onAction: func() { a.undoAction(lib, undo, runOutcome{}) }})
				}
			})
		})
	})
}

// ---- Results

// actionResults is the books an action found, shown as a view of the
// list, in the action's groups.
type actionResults struct {
	plugin, action string
	title, message string
	ids            []int64 // the books it ran on, to run it again
	groups         []plugin.Group

	version int // of the books rows was made for
	rows    []resultRow
	shown   []*calibre.Book
	cur     int // the table's chosen row
	table   ui.ListState
}

// resultRow is a row of the results: a group's header, or a book.
type resultRow struct {
	header string // a group's title
	count  int    // the group's books
	book   *calibre.Book
	note   string
}

func (a *App) showResults(p *plugin.Plugin, act *plugin.Action, ids []int64, out actionOutcome) {
	a.act.results = &actionResults{plugin: p.Manifest.ID, action: act.ID, title: act.Label(), message: out.message,
		ids: ids, groups: out.groups, version: -1, cur: -1}
	a.page = pageLibrary
	a.curID = 0
	a.chosen.Clear()
}

// closeResults leaves the results for the library.
func (a *App) closeResults() { a.act.results = nil }

// build makes the rows of the books still in the library. A book shows
// once, under the first group that has it; what later groups say of it
// joins its note.
func (r *actionResults) build(byID map[int64]*calibre.Book, version int) {
	if r.version == version && r.rows != nil {
		return
	}
	r.version, r.rows, r.shown = version, []resultRow{}, []*calibre.Book{}
	at := map[int64]int{}
	for _, g := range r.groups {
		head := -1
		for _, gb := range g.Books {
			b := byID[gb.ID]
			if b == nil {
				continue
			}
			if i, ok := at[b.ID]; ok {
				more := g.Title
				if gb.Note != "" {
					more = gb.Note
				}
				r.rows[i].note = strings.TrimPrefix(r.rows[i].note+" · "+more, " · ")
				continue
			}
			if head < 0 {
				head = len(r.rows)
				r.rows = append(r.rows, resultRow{header: g.Title})
			}
			r.rows[head].count++
			at[b.ID] = len(r.rows)
			r.rows = append(r.rows, resultRow{book: b, note: gb.Note})
			r.shown = append(r.shown, b)
		}
	}
}

// groupCount is how many groups still have books.
func (r *actionResults) groupCount() int {
	n := 0
	for _, row := range r.rows {
		if row.book == nil {
			n++
		}
	}
	return n
}

// rerunResults runs the action whose results show again.
func (a *App) rerunResults() {
	r := a.act.results
	if r == nil {
		return
	}
	for _, p := range a.plugins {
		if p.Manifest.ID == r.plugin {
			if act := p.Manifest.Action(r.action); act != nil {
				a.runAction(p, act, r.ids)
			}
		}
	}
}
