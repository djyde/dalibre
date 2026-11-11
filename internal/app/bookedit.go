package app

// The book editor (Figma, Screens › 14 · Edit book): a window per book
// that shows its chapters as they read (Glance) and edits their source
// beside the rendered page (Edit). The page is the system's web view,
// served the book's files straight from memory by a custom scheme, so the
// view shows the edits before they are saved.

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"net/url"
	"path"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/epub"
)

// editorFormats are the formats the book editor opens, in the order
// Edit book picks among a book's files.
var editorFormats = []string{"EPUB", "KEPUB", "TXT", "AZW3"}

// editorFormat is the format Edit book opens for b, or "".
func editorFormat(b *calibre.Book) string {
	if b == nil {
		return ""
	}
	for _, f := range editorFormats {
		if slices.Contains(b.Formats, f) {
			return f
		}
	}
	return ""
}

func isEditorFormat(f string) bool { return slices.Contains(editorFormats, strings.ToUpper(f)) }

type editorMode int

const (
	modeGlance editorMode = iota
	modeEdit
)

// bookEditor is an open book editor window.
type bookEditor struct {
	a      *App
	book   *calibre.Book
	format string
	bk     *epub.Book
	win    *mygo.Window
	web    *mygo.WebView // nil where the system has no web view
	token  string        // the host the web view loads the book from

	mode      editorMode
	files     bool   // the sidebar shows Files rather than Contents
	cur       string // the file shown
	lastPage  string // the last page shown, which Edit shows beside a stylesheet
	collapsed map[string]bool
	tree      []*treeNode

	mu   sync.Mutex // guards bufs, which the web view's requests read
	bufs map[string]*editor.State

	loaded     string // the URL the web view was last told to load
	viewVer    uint64 // the version of the buffers the view shows
	reloadWait bool
	words      map[string]wordCount // live counts of edited chapters
	closing    bool
	pane       ui.Handle // the editor pane, measured to fit its find bar
	// savingSettings: the sidebar's new width is due to be saved.
	savingSettings bool
}

type wordCount struct {
	ver uint64
	n   int
}

type treeNode struct {
	name     string // its last element
	path     string // in the book; a folder's has no trailing slash
	dir      bool
	children []*treeNode
	files    int // a folder's files, all levels down
	pages    bool
}

// ---- The scheme serving the books open in editors

const bookScheme = "dalibre-book"

var bookPages = struct {
	sync.Mutex
	m    map[string]*bookEditor
	once sync.Once
}{m: map[string]*bookEditor{}}

// registerBookScheme serves the editors' books to their web views; it
// must run before any window that loads them is made.
func registerBookScheme() {
	bookPages.once.Do(func() { _ = mygo.Protocol.HandleFunc(bookScheme, serveBookFile) })
}

// bookCSS goes into every page the editor shows, under the book's own
// styles (:where weighs nothing): a page that sets nothing reads as a page.
const bookCSS = `<style>
:where(html){background:#fff;color:#1d1c19;color-scheme:light}
:where(body){max-width:38em;margin:0 auto;padding:40px 48px 64px;line-height:1.6;font-family:Charter,"Iowan Old Style",Georgia,"Songti SC","Noto Serif CJK SC",serif;font-size:17px}
:where(img,svg,video){max-width:100%;height:auto}
</style>`

// bookCSP lets a page load only its own book. 'self' is needed beside the
// scheme: WebView2 serves dalibre-book://<token>/ from
// http://<token>.dalibre-book.localhost/, which the scheme alone doesn't match.
const bookCSP = "default-src 'self' " + bookScheme + ": data:; script-src 'none'; object-src 'none'; style-src 'self' " + bookScheme + ": 'unsafe-inline'; img-src 'self' " + bookScheme + ": data:; font-src 'self' " + bookScheme + ": data:"

func serveBookFile(w http.ResponseWriter, r *http.Request) {
	bookPages.Lock()
	ed := bookPages.m[r.URL.Host]
	bookPages.Unlock()
	if ed == nil {
		http.NotFound(w, r)
		return
	}
	name := strings.TrimPrefix(r.URL.Path, "/")
	data, media, ok := ed.content(name)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if epub.IsMarkup(media) {
		data = injectCSS(data)
	}
	h := w.Header()
	ct := media
	if strings.HasPrefix(media, "text/") || strings.HasSuffix(media, "xml") {
		ct += "; charset=utf-8"
	}
	h.Set("Content-Type", ct)
	h.Set("Content-Security-Policy", bookCSP)
	h.Set("Cache-Control", "no-store")
	h.Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}

// injectCSS puts bookCSS at the start of the page's head.
func injectCSS(page []byte) []byte {
	s := string(page)
	lower := strings.ToLower(s)
	at := 0
	if i := strings.Index(lower, "<head"); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j >= 0 {
			at = i + j + 1
		}
	} else if i := strings.Index(lower, "<body"); i >= 0 {
		at = i
	}
	return []byte(s[:at] + bookCSS + s[at:])
}

// content is a file as the editor has it: the buffer's text when it is
// being edited, else the book's.
func (ed *bookEditor) content(name string) ([]byte, string, bool) {
	if !ed.bk.Has(name) {
		return nil, "", false
	}
	media := ed.bk.Type(name)
	if !ed.bk.Packaged() {
		media = "text/plain"
	}
	ed.mu.Lock()
	st := ed.bufs[name]
	ed.mu.Unlock()
	if st != nil {
		return []byte(st.Buffer.String()), media, true
	}
	data, err := ed.bk.Read(name)
	if err != nil {
		return nil, "", false
	}
	return data, media, true
}

func (ed *bookEditor) pageURL(name string) string {
	segs := strings.Split(name, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return bookScheme + "://" + ed.token + "/" + strings.Join(segs, "/")
}

// ---- Opening

// editBook opens the editor on b's file of format, or the first of its
// editable files when format is "". An editor already open on the book
// comes to the front.
func (a *App) editBook(b *calibre.Book, format string) {
	if b == nil || a.lib == nil {
		return
	}
	if ed := a.editors[b.ID]; ed != nil {
		ed.win.Show()
		ed.win.Focus()
		return
	}
	if format == "" {
		format = editorFormat(b)
	}
	if format == "" {
		return
	}
	lib := a.lib
	a.run(func() {
		d, err := lib.Details(b)
		var bk *epub.Book
		if err == nil {
			err = errNoFile
			for _, f := range d.Files {
				if f.Format == format {
					bk, err = epub.Open(f.Path)
					break
				}
			}
		}
		a.post(func() {
			if err != nil {
				a.toastError(err)
				return
			}
			if lib != a.lib || a.editors[b.ID] != nil {
				return
			}
			a.openEditorWindow(b, format, bk)
		})
	})
}

type editorError string

func (e editorError) Error() string { return string(e) }

const errNoFile = editorError("the book's file is missing")

func (a *App) openEditorWindow(b *calibre.Book, format string, bk *epub.Book) {
	ed := a.newBookEditor(b, format, bk)
	ed.win = mygo.NewWindow(mygo.WindowOptions{
		Title:           b.Title,
		Width:           1280,
		Height:          840,
		MinWidth:        800,
		MinHeight:       500,
		StateKey:        "book-editor",
		TitleBarStyle:   mygo.TitleBarHiddenInset,
		TitleBarHeight:  56,
		BackgroundColor: "light-dark(#f9f9f8, #111110)",
		Content:         ui.View(ed.view),
	})
	if mygo.WebViewAvailable() {
		if web, err := ed.win.NewWebView(mygo.WebViewOptions{
			Page:            mygo.PageOptions{Untrusted: true},
			BackgroundColor: "#ffffff",
		}); err == nil {
			ed.web = web
			web.Page().OnWillNavigate(ed.willNavigate)
		}
	}
	ed.win.OnClose(func(e *mygo.CloseEvent) {
		if ed.closing || !ed.dirty() {
			return
		}
		e.PreventDefault()
		ed.askToClose()
	})
	ed.win.OnClosed(ed.closed)
	ed.win.Invalidate()
}

// closed forgets an editor whose window closed.
func (ed *bookEditor) closed() {
	bookPages.Lock()
	delete(bookPages.m, ed.token)
	bookPages.Unlock()
	if ed.a.editors[ed.book.ID] == ed {
		delete(ed.a.editors, ed.book.ID)
	}
}

// newBookEditor makes the editor of a book, serving its files to web
// views, without its window.
func (a *App) newBookEditor(b *calibre.Book, format string, bk *epub.Book) *bookEditor {
	tok := make([]byte, 8)
	rand.Read(tok)
	ed := &bookEditor{
		a: a, book: b, format: format, bk: bk, token: hex.EncodeToString(tok),
		bufs: map[string]*editor.State{}, collapsed: map[string]bool{}, words: map[string]wordCount{},
	}
	if sp := bk.Spine(); len(sp) > 0 {
		ed.cur = sp[0].Name
	} else if fs := bk.Files(); len(fs) > 0 {
		ed.cur = fs[0].Name
	}
	ed.buildTree()
	if a.editors == nil {
		a.editors = map[int64]*bookEditor{}
	}
	a.editors[b.ID] = ed
	bookPages.Lock()
	bookPages.m[ed.token] = ed
	bookPages.Unlock()
	return ed
}

// askToClose asks whether to save the edits of a window being closed.
func (ed *bookEditor) askToClose() {
	go func() {
		res, err := mygo.Dialog.Message(mygo.MessageOptions{
			Parent:             ed.win,
			Type:               mygo.MessageQuestion,
			Message:            locales.EditorSaveChanges(ed.book.Title),
			Buttons:            []string{locales.CommonSave(), locales.EditorDontSave(), locales.CommonCancel()},
			DefaultButton:      0,
			CancelButton:       2,
			DestructiveButtons: []int{1},
		})
		if err != nil {
			return
		}
		ed.win.Update(func() {
			switch res.Button {
			case 0:
				if ed.save() {
					ed.closing = true
					ed.win.Close()
				}
			case 1:
				ed.closing = true
				ed.win.Close()
			}
		})
	}()
}

// willNavigate keeps the web view on the book: a link to another file of
// it shows that file in the editor, a link out opens in the browser.
func (ed *bookEditor) willNavigate(e *mygo.NavigateEvent) {
	u, err := url.Parse(e.URL)
	if err != nil {
		e.PreventDefault()
		return
	}
	if u.Scheme == bookScheme && u.Host == ed.token {
		name := strings.TrimPrefix(u.Path, "/")
		if name == ed.shownPage() {
			return // a link within the page
		}
		e.PreventDefault()
		ed.win.Update(func() {
			if ed.bk.Has(name) {
				ed.show(name)
				if u.Fragment != "" && ed.web != nil {
					ed.loaded = ed.pageURL(name) + "#" + u.Fragment
					ed.web.Page().LoadURL(ed.loaded)
				}
			}
		})
		return
	}
	e.PreventDefault()
	if e.UserInitiated && (u.Scheme == "http" || u.Scheme == "https" || u.Scheme == "mailto") {
		go mygo.Shell.OpenExternal(e.URL)
	}
}

// ---- State

// show makes name the file shown.
func (ed *bookEditor) show(name string) {
	ed.cur = name
	for dir := path.Dir(name); dir != "." && dir != "/"; dir = path.Dir(dir) {
		delete(ed.collapsed, dir)
	}
}

// state is the editor state of a text file, made on first use.
func (ed *bookEditor) state(name string) *editor.State {
	ed.mu.Lock()
	st := ed.bufs[name]
	ed.mu.Unlock()
	if st != nil {
		return st
	}
	data, err := ed.bk.Read(name)
	if err != nil {
		return nil
	}
	buf := editor.NewBuffer(string(data))
	buf.SetLanguage(languageOf(name, ed.bk.Type(name)))
	st = &editor.State{Buffer: buf, SoftWrap: true, LineHeight: 1.65, NoFolding: false, Label: name}
	ed.mu.Lock()
	ed.bufs[name] = st
	ed.mu.Unlock()
	return st
}

func languageOf(name, media string) *editor.Language {
	switch {
	case epub.IsMarkup(media):
		return editor.LanguageNamed("HTML")
	case media == "text/css":
		return editor.LanguageNamed("CSS")
	case media == "image/svg+xml", strings.HasSuffix(media, "+xml"), strings.HasSuffix(media, "/xml"):
		return editor.LanguageNamed("XML")
	}
	return editor.LanguageFor(name)
}

// modified reports whether a file has edits not saved.
func (ed *bookEditor) modified(name string) bool {
	ed.mu.Lock()
	st := ed.bufs[name]
	ed.mu.Unlock()
	return st != nil && st.Buffer.Modified()
}

func (ed *bookEditor) dirty() bool {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	for _, st := range ed.bufs {
		if st.Buffer.Modified() {
			return true
		}
	}
	return false
}

// bufVersion sums the buffers' versions: it changes with every edit.
func (ed *bookEditor) bufVersion() uint64 {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	var v uint64
	for _, st := range ed.bufs {
		v += st.Buffer.Version()
	}
	return v
}

// save writes the edits into the book's file and tells the library.
func (ed *bookEditor) save() bool {
	ed.mu.Lock()
	var saved []*editor.State
	for name, st := range ed.bufs {
		if st.Buffer.Modified() {
			if err := ed.bk.Set(name, []byte(st.Buffer.String())); err != nil {
				ed.mu.Unlock()
				ed.toast(err.Error(), true)
				return false
			}
			saved = append(saved, st)
		}
	}
	ed.mu.Unlock()
	if len(saved) == 0 {
		return true
	}
	size, err := ed.bk.Save()
	if err != nil {
		ed.toast(err.Error(), true)
		return false
	}
	for _, st := range saved {
		st.Buffer.MarkSaved()
	}
	ed.toast(locales.EditorSaved(ed.book.Title), false)
	a, lib, id, format := ed.a, ed.a.lib, ed.book.ID, ed.format
	if lib != nil {
		a.run(func() {
			err := lib.FormatRewritten(id, format, size)
			a.post(func() {
				if err != nil {
					a.toastError(err)
					return
				}
				a.reload(nil)
			})
		})
	}
	return true
}

// toast shows a message in the editor's window.
func (ed *bookEditor) toast(text string, isErr bool) {
	a := ed.a
	if a.editorToasts == nil {
		a.editorToasts = map[int64][]toastMsg{}
	}
	a.editorToasts[ed.book.ID] = append(a.editorToasts[ed.book.ID], toastMsg{text: text, error: isErr})
	if ed.win != nil {
		ed.win.Invalidate()
	}
}

// revert drops the edits not saved.
func (ed *bookEditor) revert() {
	ed.mu.Lock()
	defer ed.mu.Unlock()
	for name, st := range ed.bufs {
		if st.Buffer.Modified() {
			if data, err := ed.bk.Read(name); err == nil {
				st.Buffer.Reset(string(data))
			}
		}
	}
}

// shownPage is the page the web view shows: the file shown when it is a
// page, else the last page shown, beside a stylesheet being edited.
func (ed *bookEditor) shownPage() string {
	if epub.IsPage(ed.bk.Type(ed.cur)) || !ed.bk.Packaged() {
		return ed.cur
	}
	return ed.lastPage
}

// scheduleReload reloads the web view a moment after the edits stop.
func (ed *bookEditor) scheduleReload() {
	if ed.reloadWait || ed.web == nil || ed.win == nil {
		return
	}
	ed.reloadWait = true
	time.AfterFunc(350*time.Millisecond, func() {
		ed.win.Update(func() {
			ed.reloadWait = false
			v := ed.bufVersion()
			if v == ed.viewVer {
				return
			}
			ed.viewVer = v
			if ed.loaded != "" {
				ed.web.Page().Reload()
			}
		})
	})
}

// wordsOf counts a chapter's words, from its buffer while it is edited.
func (ed *bookEditor) wordsOf(it epub.Item) int {
	ed.mu.Lock()
	st := ed.bufs[it.Name]
	ed.mu.Unlock()
	if st == nil || !st.Buffer.Modified() {
		return it.Words
	}
	v := st.Buffer.Version()
	if w, ok := ed.words[it.Name]; ok && w.ver == v {
		return w.n
	}
	n := epub.PageWords([]byte(st.Buffer.String()))
	if !ed.bk.Packaged() {
		n = epub.CountWords(st.Buffer.String())
	}
	ed.words[it.Name] = wordCount{v, n}
	return n
}

// buildTree arranges the book's files as folders.
func (ed *bookEditor) buildTree() {
	root := &treeNode{dir: true}
	dirs := map[string]*treeNode{"": root}
	var dirOf func(p string) *treeNode
	dirOf = func(p string) *treeNode {
		if n := dirs[p]; n != nil {
			return n
		}
		parent := dirOf(parentDir(p))
		n := &treeNode{name: path.Base(p), path: p, dir: true}
		parent.children = append(parent.children, n)
		dirs[p] = n
		return n
	}
	for _, f := range ed.bk.Files() {
		d := dirOf(parentDir(f.Name))
		d.children = append(d.children, &treeNode{name: path.Base(f.Name), path: f.Name, pages: epub.IsMarkup(f.Type)})
	}
	var count func(n *treeNode)
	count = func(n *treeNode) {
		slices.SortStableFunc(n.children, func(x, y *treeNode) int {
			if x.dir != y.dir {
				if x.dir {
					return -1
				}
				return 1
			}
			return strings.Compare(strings.ToLower(x.name), strings.ToLower(y.name))
		})
		for _, c := range n.children {
			if c.dir {
				count(c)
				n.files += c.files
			} else {
				n.files++
			}
			n.pages = n.pages || c.pages
		}
	}
	count(root)
	ed.tree = root.children
	// Folders without pages start closed, as Styles and Images; the
	// others, and the top level, open.
	var walk func(ns []*treeNode, depth int)
	walk = func(ns []*treeNode, depth int) {
		for _, n := range ns {
			if n.dir {
				if depth > 0 && !n.pages {
					ed.collapsed[n.path] = true
				}
				walk(n.children, depth+1)
			}
		}
	}
	walk(ed.tree, 0)
}

func parentDir(p string) string {
	d := path.Dir(p)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// webEngine names the system web view Glance needs, for the empty state.
func webEngine() string {
	switch runtime.GOOS {
	case "windows":
		return "Microsoft Edge WebView2"
	case "linux":
		return "WebKitGTK"
	}
	return "WebKit"
}
