package calibre

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTitleSort(t *testing.T) {
	for in, want := range map[string]string{
		"The Three-Body Problem": "Three-Body Problem, The",
		"A Tale of Two Cities":   "Tale of Two Cities, A",
		"An Unquiet Mind":        "Unquiet Mind, An",
		"Theory of Everything":   "Theory of Everything",
		"“Quoted” Title":         "Quoted” Title",
		"三体":                     "三体",
	} {
		if got := TitleSort(in); got != want {
			t.Errorf("TitleSort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAuthorSort(t *testing.T) {
	for in, want := range map[string]string{
		"Liu Cixin":             "Cixin, Liu",
		"Ursula K. Le Guin":     "Guin, Ursula K. Le",
		"Martin Luther King Jr": "King, Martin Luther Jr",
		"Dr. John Smith":        "Smith, John",
		"Plato":                 "Plato",
		"Asimov, Isaac":         "Asimov, Isaac",
		"Acme Software":         "Acme Software",
		"刘慈欣":                   "刘慈欣",
	} {
		if got := AuthorSort(in); got != want {
			t.Errorf("AuthorSort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBookPath(t *testing.T) {
	if got, want := BookPath(12, "The Three-Body Problem", "Liu Cixin", false), "Liu Cixin/The Three-Body Problem (12)"; got != want {
		t.Errorf("BookPath = %q, want %q", got, want)
	}
	if got := BookPath(3, "三体", "刘慈欣", false); got != "Liu Ci Xin/San Ti (3)" {
		t.Errorf("CJK BookPath = %q", got)
	}
	if got := BookPath(1, "What? Why: Now", "CON", false); got != "CONw/What_ Why_ Now (1)" {
		t.Errorf("sanitized BookPath = %q", got)
	}
	long := strings.Repeat("Very Long Title ", 20)
	p := BookPath(99, long, "Author", false)
	// The limit is calibre's, on the title before " (id)" is appended.
	if title := strings.TrimSuffix(strings.SplitN(p, "/", 2)[1], " (99)"); len(title) > pathLimit()-len(" (99)")/2-2 {
		t.Errorf("title component too long: %d %q", len(title), title)
	}
	if runtime.GOOS == "windows" {
		if got := BookFileName("The Three-Body Problem", "Liu Cixin", false); got != "The Three-Body Problem - Liu Cixin" {
			t.Errorf("BookFileName = %q", got)
		}
	}
}

func TestBookPathKeepNames(t *testing.T) {
	if got := BookPath(3, "三体", "刘慈欣", true); got != "刘慈欣/三体 (3)" {
		t.Errorf("BookPath = %q", got)
	}
	if got := BookPath(1, "黑暗？森林: 第二部", "CON", true); got != "CONw/黑暗？森林_ 第二部 (1)" {
		t.Errorf("sanitized BookPath = %q", got)
	}
	if got := BookFileName("三体", "刘慈欣", true); got != "三体 - 刘慈欣" {
		t.Errorf("BookFileName = %q", got)
	}
	// Long names are cut at whole characters, within calibre's limit in
	// characters and the 255 bytes a file name can take.
	long := strings.Repeat("三体𝄞", 60) // 3- and 4-byte characters
	parts := strings.Split(BookPath(99, long, long, true), "/")
	limit := pathLimit() - len(" (99)")/2 - 2
	title := strings.TrimSuffix(parts[1], " (99)")
	for _, s := range []string{parts[0], title} {
		if !utf8.ValidString(s) || utf8.RuneCountInString(s) > limit || utf8.RuneCountInString(s) < limit/2 {
			t.Errorf("component %d chars, valid %v: %q", utf8.RuneCountInString(s), utf8.ValidString(s), s)
		}
	}
	for _, s := range []string{parts[0], parts[1], BookFileName(long, long, true) + ".original_epub"} {
		if len(s) > 255 || !utf8.ValidString(s) {
			t.Errorf("name of %d bytes, valid %v: %q", len(s), utf8.ValidString(s), s)
		}
	}
}

func TestKeepNames(t *testing.T) {
	lib := newTestLibrary(t)
	lib.SetKeepNames(true)
	id, err := lib.AddBook(NewBook{File: writeTemp(t, "in.epub", "epub"), Metadata: Metadata{Title: "三体", Authors: []string{"刘慈欣"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.AddFormat(id, writeTemp(t, "x.pdf", "pdf")); err != nil {
		t.Fatal(err)
	}
	b, _ := lib.Book(id)
	if want := fmt.Sprintf("刘慈欣/三体 (%d)", id); b.Path != want {
		t.Errorf("path = %q, want %q", b.Path, want)
	}
	for _, f := range []string{"三体 - 刘慈欣.epub", "三体 - 刘慈欣.pdf"} {
		if _, err := os.Stat(filepath.Join(lib.Dir(b), f)); err != nil {
			t.Error(err)
		}
	}

	// Turned off, a rename goes back to calibre's ASCII.
	lib.SetKeepNames(false)
	d, _ := lib.Details(b)
	m := b.Metadata(d)
	m.Title = "三体II"
	if err := lib.UpdateMetadata(id, m); err != nil {
		t.Fatal(err)
	}
	b, _ = lib.Book(id)
	if want := fmt.Sprintf("Liu Ci Xin/San Ti II (%d)", id); b.Path != want {
		t.Errorf("path = %q, want %q", b.Path, want)
	}
	if _, err := os.Stat(filepath.Join(lib.Dir(b), "San Ti II - Liu Ci Xin.epub")); err != nil {
		t.Error(err)
	}
}

func newTestLibrary(t *testing.T) *Library {
	t.Helper()
	lib, err := Create(filepath.Join(t.TempDir(), "Library"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	return lib
}

func writeTemp(t *testing.T, name, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreateSchema(t *testing.T) {
	lib := newTestLibrary(t)
	v, err := lib.UserVersion()
	if err != nil || v != SchemaVersion {
		t.Fatalf("user_version = %d, %v", v, err)
	}
	// Reopening must work, as calibre would.
	lib.Close()
	lib2, err := Open(lib.Root)
	if err != nil {
		t.Fatal(err)
	}
	lib2.Close()
	if _, err := Open(t.TempDir()); err != ErrNotLibrary {
		t.Fatalf("Open(empty) = %v", err)
	}
}

func TestBookLifecycle(t *testing.T) {
	lib := newTestLibrary(t)
	file := writeTemp(t, "in.epub", "fake epub bytes")
	pub := time.Date(2014, 11, 11, 0, 0, 0, 0, time.UTC)
	id, err := lib.AddBook(NewBook{File: file, Metadata: Metadata{
		Title: "The Three-Body Problem", Authors: []string{"Liu Cixin"},
		Series: "Remembrance of Earth’s Past", SeriesIndex: 1,
		Tags: []string{"Science Fiction", "Hugo Award", "science fiction"}, Publisher: "Tor Books",
		Rating: 8, Pubdate: pub, Languages: []string{"eng"},
		Identifiers: map[string]string{"isbn": "9780765382030"}, Comments: "<p>Aliens.</p>",
	}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lib.Book(id)
	if err != nil {
		t.Fatal(err)
	}
	// Triggers ran through our SQL functions.
	if b.Sort != "Three-Body Problem, The" || len(b.UUID) != 36 {
		t.Errorf("sort %q uuid %q", b.Sort, b.UUID)
	}
	if b.AuthorSort != "Cixin, Liu" || b.Series != "Remembrance of Earth’s Past" || b.Rating != 8 || b.Publisher != "Tor Books" {
		t.Errorf("book = %+v", b)
	}
	if len(b.Tags) != 2 {
		t.Errorf("tags deduplicated case-insensitively: %v", b.Tags)
	}
	if !b.Pubdate.Equal(pub) {
		t.Errorf("pubdate %v", b.Pubdate)
	}
	if strings.Join(b.Formats, ",") != "EPUB" || b.Size != int64(len("fake epub bytes")) {
		t.Errorf("formats %v size %d", b.Formats, b.Size)
	}
	d, err := lib.Details(b)
	if err != nil {
		t.Fatal(err)
	}
	if d.Comments != "<p>Aliens.</p>" || d.Identifiers["isbn"] != "9780765382030" || len(d.Files) != 1 {
		t.Errorf("details %+v", d)
	}
	if _, err := os.Stat(d.Files[0].Path); err != nil {
		t.Errorf("format file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(lib.Dir(b), "metadata.opf")); err != nil {
		t.Errorf("metadata.opf missing: %v", err)
	}

	// Renaming moves the folder and the files.
	m := b.Metadata(d)
	m.Title, m.Authors, m.AuthorSort = "Three Body", []string{"Cixin Liu", "Ken Liu"}, ""
	m.Tags, m.Rating, m.Series = []string{"SF"}, 0, ""
	if err := lib.UpdateMetadata(id, m); err != nil {
		t.Fatal(err)
	}
	b2, _ := lib.Book(id)
	if b2.Path != BookPath(id, "Three Body", "Cixin Liu", false) {
		t.Errorf("path = %q", b2.Path)
	}
	if b2.Sort != "Three Body" || b2.AuthorSort != "Liu, Cixin & Liu, Ken" || b2.Rating != 0 || b2.Series != "" {
		t.Errorf("updated = %+v", b2)
	}
	if strings.Join(b2.Authors, "|") != "Cixin Liu|Ken Liu" {
		t.Errorf("author order = %v", b2.Authors)
	}
	d2, _ := lib.Details(b2)
	if _, err := os.Stat(d2.Files[0].Path); err != nil || !strings.HasPrefix(filepath.Base(d2.Files[0].Path), "Three Body - Cixin Liu") {
		t.Errorf("renamed file: %v %s", err, d2.Files[0].Path)
	}
	if _, err := os.Stat(lib.Dir(b)); !os.IsNotExist(err) {
		t.Errorf("old folder still there: %v", err)
	}
	if _, err := os.Stat(filepath.Dir(lib.Dir(b))); !os.IsNotExist(err) {
		t.Errorf("empty author folder left behind")
	}
	var unused int
	lib.db.QueryRow(`SELECT count(*) FROM tags WHERE name IN ('Hugo Award','Science Fiction')`).Scan(&unused)
	if unused != 0 {
		t.Errorf("unused tags kept: %d", unused)
	}
	var dirty int
	lib.db.QueryRow(`SELECT count(*) FROM metadata_dirtied WHERE book = ?`, id).Scan(&dirty)
	if dirty != 1 {
		t.Errorf("book not marked dirty for calibre")
	}

	// Formats.
	if err := lib.AddFormat(id, writeTemp(t, "x.pdf", "pdf!")); err != nil {
		t.Fatal(err)
	}
	b3, _ := lib.Book(id)
	if strings.Join(b3.Formats, ",") != "EPUB,PDF" {
		t.Errorf("formats after add = %v", b3.Formats)
	}
	if err := lib.RemoveFormat(id, "PDF"); err != nil {
		t.Fatal(err)
	}

	// Removal goes to the trash folder.
	if err := lib.RemoveBooks([]int64{id}, nil); err != nil {
		t.Fatal(err)
	}
	books, _ := lib.Books()
	if len(books) != 0 {
		t.Errorf("books left: %d", len(books))
	}
	entries, _ := os.ReadDir(filepath.Join(lib.Root, TrashDir))
	if len(entries) != 1 {
		t.Errorf("trash entries = %d", len(entries))
	}
	var authors int
	lib.db.QueryRow(`SELECT count(*) FROM authors`).Scan(&authors)
	if authors != 0 {
		t.Errorf("authors left: %d", authors)
	}
}

func TestSetCoverReencodes(t *testing.T) {
	lib := newTestLibrary(t)
	id, err := lib.AddBook(NewBook{File: writeTemp(t, "a.epub", "x"), Metadata: Metadata{Title: "A", Authors: []string{"B"}}})
	if err != nil {
		t.Fatal(err)
	}
	// 1×1 PNG
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
	if err := lib.SetCover(id, png); err != nil {
		t.Fatal(err)
	}
	b, _ := lib.Book(id)
	data, err := os.ReadFile(lib.CoverPath(b))
	if err != nil || !b.HasCover || data[0] != 0xff || data[1] != 0xd8 {
		t.Errorf("cover not a JPEG: has=%v err=%v", b.HasCover, err)
	}
}
