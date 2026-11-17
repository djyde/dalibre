package app

import (
	"archive/zip"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/calibre"
	"dalibre/internal/epub"
)

// writeSampleEPUB writes a small EPUB of two chapters and a stylesheet.
func writeSampleEPUB(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "Pride and Prejudice.epub")
	f, err := os.Create(p)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	add := func(name, data string, method uint16) {
		w, _ := zw.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		io.WriteString(w, data)
	}
	page := func(h, p string) string {
		return `<?xml version="1.0" encoding="utf-8"?><html xmlns="http://www.w3.org/1999/xhtml"><head><title>` + h +
			`</title><link rel="stylesheet" href="../Styles/style.css"/></head><body><h2>` + h + `</h2><p>` + p + `</p></body></html>`
	}
	add("mimetype", "application/epub+zip", zip.Store)
	add("META-INF/container.xml", `<container><rootfiles><rootfile full-path="OEBPS/content.opf"/></rootfiles></container>`, zip.Deflate)
	add("OEBPS/content.opf", `<package><manifest>
<item id="c1" href="Text/chapter_001.xhtml" media-type="application/xhtml+xml"/>
<item id="c2" href="Text/chapter_002.xhtml" media-type="application/xhtml+xml"/>
<item id="css" href="Styles/style.css" media-type="text/css"/>
<item id="img" href="Images/cover.jpg" media-type="image/jpeg"/>
</manifest><spine><itemref idref="c1"/><itemref idref="c2"/></spine></package>`, zip.Deflate)
	add("OEBPS/Text/chapter_001.xhtml", page("Chapter 1", "It is a truth universally acknowledged."), zip.Deflate)
	add("OEBPS/Text/chapter_002.xhtml", page("Chapter 2", "Mr. Bennet was among the earliest."), zip.Deflate)
	add("OEBPS/Styles/style.css", "p { text-indent: 1em }", zip.Deflate)
	add("OEBPS/Images/cover.jpg", "\xff\xd8\xff", zip.Store)
	zw.Close()
	f.Close()
	return p
}

// newTestEditor opens the editor, without its window, on Dune's EPUB.
func newTestEditor(t *testing.T) (*App, *bookEditor) {
	t.Helper()
	a := newTestApp(t)
	var dune *calibre.Book
	for _, b := range a.books {
		if b.Title == "Dune" {
			dune = b
		}
	}
	if err := a.lib.AddFormat(dune.ID, writeSampleEPUB(t)); err != nil {
		t.Fatal(err)
	}
	d, err := a.lib.Details(dune)
	if err != nil {
		t.Fatal(err)
	}
	bk, err := epub.Open(d.Files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	ed := a.newBookEditor(dune, "EPUB", bk)
	t.Cleanup(ed.closed)
	return a, ed
}

func TestEditorFormat(t *testing.T) {
	for _, c := range []struct {
		formats []string
		want    string
	}{
		{[]string{"MOBI", "EPUB"}, "EPUB"},
		{[]string{"TXT", "KEPUB"}, "KEPUB"},
		{[]string{"PDF", "AZW3"}, "AZW3"},
		{[]string{"AZW3", "EPUB"}, "EPUB"},
		{[]string{"PDF", "MOBI"}, ""},
	} {
		if got := editorFormat(&calibre.Book{Formats: c.formats}); got != c.want {
			t.Errorf("editorFormat(%v) = %q, want %q", c.formats, got, c.want)
		}
	}
}

func TestBookEditorView(t *testing.T) {
	_, ed := newTestEditor(t)
	tt := ui.NewTester(ed.view, 1280, 840)
	tt.Frame()
	for _, s := range []string{"Chapter 1", "Chapter 2", "Contents", "Files", "Glance", "Edit", "1 of 2"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "editor-glance")

	if err := tt.Click("Files"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"META-INF", "OEBPS", "Text", "chapter_001.xhtml", "Styles"} {
		if !tt.HasText(s) {
			t.Errorf("tree misses %q; texts %q", s, tt.Texts())
		}
	}
	// Folders without pages start closed.
	if tt.HasText("style.css") {
		t.Error("Styles is open")
	}

	ed.mode = modeEdit
	tt.Frame()
	if !tt.HasText("Ln 1, Col 1") {
		t.Errorf("no caret position; texts %q", tt.Texts())
	}
	shot(t, tt, "editor-edit")
}

// An AZW3 opens in the editor like an EPUB, and its edits save back into
// the library's file.
func TestBookEditorAZW3(t *testing.T) {
	a := newTestApp(t)
	b := a.books[0]
	raw, err := os.ReadFile("../epub/testdata/alice.azw3")
	if err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "Alice.azw3")
	os.WriteFile(src, raw, 0o644)
	if err := a.lib.AddFormat(b.ID, src); err != nil {
		t.Fatal(err)
	}
	d, err := a.lib.Details(b)
	if err != nil {
		t.Fatal(err)
	}
	var file string
	for _, f := range d.Files {
		if f.Format == "AZW3" {
			file = f.Path
		}
	}
	bk, err := epub.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	ed := a.newBookEditor(b, "AZW3", bk)
	t.Cleanup(ed.closed)
	ed.mode = modeEdit
	ed.show("Text/part0004.xhtml")
	tt := ui.NewTester(ed.view, 1280, 840)
	tt.Frame()
	for _, s := range []string{"Contents", "CHAPTER I. Down the Rabbit-Hole", "AZW3"} {
		if !tt.HasText(s) {
			t.Errorf("missing %q; texts %q", s, tt.Texts())
		}
	}
	w := httptest.NewRecorder()
	serveBookFile(w, httptest.NewRequest("GET", ed.pageURL("Text/part0004.xhtml"), nil))
	if body := w.Body.String(); !strings.Contains(body, "Down the Rabbit-Hole") || !strings.Contains(body, "../Styles/flow0002.css") {
		t.Errorf("served page = %.300s", body)
	}
	st := ed.state(ed.cur)
	st.Buffer.Set(strings.Replace(st.Buffer.String(), "Alice was beginning", "Alice, edited, was beginning", 1))
	tt.Frame()
	if !tt.HasText("Save") || !tt.HasText("Revert") {
		t.Errorf("an edited AZW3 offers no Save; texts %q", tt.Texts())
	}
	if !ed.save() {
		t.Fatal("save failed")
	}
	again, err := epub.Open(file)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Read("Text/part0004.xhtml"); !strings.Contains(string(got), "Alice, edited, was beginning") {
		t.Error("the edit isn't in the saved file")
	}
	shot(t, tt, "editor-azw3")
}

func TestBookEditorServesEdits(t *testing.T) {
	_, ed := newTestEditor(t)
	name := "OEBPS/Text/chapter_001.xhtml"
	get := func() (string, *httptest.ResponseRecorder) {
		w := httptest.NewRecorder()
		serveBookFile(w, httptest.NewRequest("GET", ed.pageURL(name), nil))
		return w.Body.String(), w
	}
	body, w := get()
	if !strings.Contains(body, "universally acknowledged") || !strings.Contains(body, ":where(body)") {
		t.Fatalf("page = %s", body)
	}
	// 'self' lets the book's images, styles and fonts load where WebView2
	// serves the scheme from http://<token>.dalibre-book.localhost/.
	if csp := w.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'none'") || !strings.Contains(csp, "img-src 'self'") || !strings.Contains(csp, "style-src 'self'") || !strings.Contains(csp, "font-src 'self'") {
		t.Errorf("CSP = %q", csp)
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/xhtml+xml") {
		t.Errorf("Content-Type = %q", ct)
	}

	st := ed.state(name)
	st.Buffer.Set(strings.Replace(st.Buffer.String(), "universally", "generally", 1))
	if body, _ := get(); !strings.Contains(body, "generally acknowledged") {
		t.Errorf("the view doesn't see the edit: %s", body)
	}
	if !ed.dirty() || !ed.modified(name) {
		t.Fatal("not dirty after an edit")
	}

	// Another editor's token, or a file the book lacks, is not served.
	w = httptest.NewRecorder()
	serveBookFile(w, httptest.NewRequest("GET", bookScheme+"://nope/"+name, nil))
	if w.Code != 404 {
		t.Errorf("unknown token served: %d", w.Code)
	}
}

func TestBookEditorSave(t *testing.T) {
	a, ed := newTestEditor(t)
	name := "OEBPS/Text/chapter_002.xhtml"
	st := ed.state(name)
	st.Buffer.Set(strings.Replace(st.Buffer.String(), "earliest", "first", 1))
	if !ed.save() {
		t.Fatal("save failed")
	}
	if ed.dirty() {
		t.Error("still dirty after saving")
	}
	b2, err := epub.Open(ed.bk.Path())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := b2.Read(name)
	if !strings.Contains(string(data), "among the first") {
		t.Errorf("saved chapter = %s", data)
	}
	d, err := a.lib.Details(ed.book)
	if err != nil {
		t.Fatal(err)
	}
	fi, _ := os.Stat(ed.bk.Path())
	if d.Files[0].Size != fi.Size() {
		t.Errorf("library size %d, file %d", d.Files[0].Size, fi.Size())
	}

	// Revert drops edits not saved.
	st.Buffer.Set("broken")
	ed.revert()
	if ed.dirty() || !strings.Contains(st.Buffer.String(), "among the first") {
		t.Errorf("revert left %q", st.Buffer.String())
	}
}

// Hovering an EPUB row swaps its size for Edit without resizing the row.
func TestFormatRowEditKeepsSize(t *testing.T) {
	a, ed := newTestEditor(t)
	a.curID = ed.book.ID
	a.reload(nil)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Frame()
	file, ok := tt.Find("Dune - Frank Herbert.epub")
	if !ok {
		t.Fatalf("no format row; texts %q", tt.Texts())
	}
	below, ok := tt.Find("Add a format")
	if !ok {
		t.Fatalf("no Add format; texts %q", tt.Texts())
	}
	tt.Move(file.X+file.W/2, file.Y+file.H/2)
	tt.Frame()
	if tt.HasText("2 KB") {
		t.Fatalf("no Edit on hover; texts %q", tt.Texts())
	}
	after, _ := tt.Find("Add a format")
	if after.Y != below.Y {
		t.Errorf("the row resized: Add format moved from y %v to %v", below.Y, after.Y)
	}
	shot(t, tt, "format-row-hover")
	// The Edit button sits at the row's right end, where the size was.
	tt.Move(1440-48, file.Y+file.H/2)
	tt.Frame()
	shot(t, tt, "format-row-edit-hover")
}

// The find bar stays inside a narrow editor pane, off the sidebar.
func TestBookEditorFindBarFits(t *testing.T) {
	_, ed := newTestEditor(t)
	ed.mode = modeEdit
	for _, w := range []int{800, 1280} {
		tt := ui.NewTester(ed.view, w, 600)
		tt.Frame()
		ed.state(ed.cur).Find("truth")
		tt.Frame()
		tt.Frame()
		// The query field sits left of the toggles, at least 60 wide.
		r, ok := tt.Find("Aa")
		if !ok {
			t.Fatalf("no find bar at %d; texts %q", w, tt.Texts())
		}
		if r.X < 240+60 {
			t.Errorf("at width %d the find bar runs over the sidebar: Aa at x %v", w, r.X)
		}
		shot(t, tt, "editor-find-"+strconv.Itoa(w))
	}
}

// Dragging the divider resizes the sidebar, within its limits.
func TestBookEditorSidebarResizes(t *testing.T) {
	a, ed := newTestEditor(t)
	tt := ui.NewTester(ed.view, 1280, 840)
	tt.Frame()
	drag := func(from, to float32) {
		tt.Press(from, 400)
		tt.Frame()
		tt.Move(to, 400)
		tt.Frame()
		tt.Release(to, 400)
		tt.Frame()
	}
	drag(240, 320)
	if got := a.settings.EditorSidebar; got < 310 || got > 330 {
		t.Fatalf("sidebar = %v after dragging to 320", got)
	}
	drag(320, 900)
	if got := a.settings.EditorSidebar; got != editorSidebarMax {
		t.Errorf("sidebar = %v, want the max %d", got, editorSidebarMax)
	}
}
