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
	if got, want := BookPath(12, "The Three-Body Problem", "Liu Cixin"), "Liu Cixin/The Three-Body Problem (12)"; got != want {
		t.Errorf("BookPath = %q, want %q", got, want)
	}
	// Chinese stays Chinese, not pinyin ("Liu Ci Xin/San Ti (3)").
	if got := BookPath(3, "三体", "刘慈欣"); got != "刘慈欣/三体 (3)" {
		t.Errorf("CJK BookPath = %q", got)
	}
	if got := BookFileName("三体", "刘慈欣"); got != "三体 - 刘慈欣" {
		t.Errorf("CJK BookFileName = %q", got)
	}
	// Long CJK names are cut at whole characters and fit in 255 bytes.
	longCJK := strings.Repeat("地球往事", 40)
	p := BookPath(7, longCJK, longCJK)
	for _, part := range strings.Split(p, "/") {
		if len(part) > 255 || !utf8.ValidString(part) {
			t.Errorf("CJK path part %d bytes, valid %v", len(part), utf8.ValidString(part))
		}
	}
	if n := BookFileName(longCJK, longCJK); len(n)+len(".epub") > 255 || !utf8.ValidString(n) {
		t.Errorf("CJK file name %d bytes", len(n))
	}
	if got := BookPath(1, "What? Why: Now", "CON"); got != "CONw/What_ Why_ Now (1)" {
		t.Errorf("sanitized BookPath = %q", got)
	}
	long := strings.Repeat("Very Long Title ", 20)
	p = BookPath(99, long, "Author")
	// The limit is calibre's, on the title before " (id)" is appended.
	if title := strings.TrimSuffix(strings.SplitN(p, "/", 2)[1], " (99)"); len(title) > pathLimit()-len(" (99)")/2-2 {
		t.Errorf("title component too long: %d %q", len(title), title)
	}
	if runtime.GOOS == "windows" {
		if got := BookFileName("The Three-Body Problem", "Liu Cixin"); got != "The Three-Body Problem - Liu Cixin" {
			t.Errorf("BookFileName = %q", got)
		}
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
	if b2.Path != BookPath(id, "Three Body", "Cixin Liu") {
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

// A book calibre added has pinyin names; RenameBooks gives it back its
// Chinese ones.
func TestRenamePinyinBooks(t *testing.T) {
	lib := newTestLibrary(t)
	id, err := lib.AddBook(NewBook{File: writeTemp(t, "a.epub", "x"), Metadata: Metadata{Title: "三体", Authors: []string{"刘慈欣"}}})
	if err != nil {
		t.Fatal(err)
	}
	other, err := lib.AddBook(NewBook{File: writeTemp(t, "b.epub", "y"), Metadata: Metadata{Title: "Dune", Authors: []string{"Frank Herbert"}}})
	if err != nil {
		t.Fatal(err)
	}
	// Make it look like calibre wrote it.
	b, _ := lib.Book(id)
	pinyinPath := fmt.Sprintf("Liu Ci Xin/San Ti (%d)", id)
	pinyinDir := filepath.Join(lib.Root, filepath.FromSlash(pinyinPath))
	if err := os.MkdirAll(filepath.Dir(pinyinDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(lib.Dir(b), pinyinDir); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Dir(lib.Dir(b)))
	if err := os.Rename(filepath.Join(pinyinDir, "三体 - 刘慈欣.epub"), filepath.Join(pinyinDir, "San Ti - Liu Ci Xin.epub")); err != nil {
		t.Fatal(err)
	}
	lib.db.Exec(`UPDATE books SET path = ? WHERE id = ?`, pinyinPath, id)
	lib.db.Exec(`UPDATE data SET name = 'San Ti - Liu Ci Xin' WHERE book = ?`, id)

	misnamed, err := lib.MisnamedBooks()
	if err != nil {
		t.Fatal(err)
	}
	if len(misnamed) != 1 || misnamed[0].ID != id {
		t.Fatalf("misnamed = %v, want only book %d (not %d)", misnamed, id, other)
	}
	n, err := lib.RenameBooks([]int64{id}, nil)
	if err != nil || n != 1 {
		t.Fatalf("RenameBooks = %d, %v", n, err)
	}
	b, _ = lib.Book(id)
	if want := fmt.Sprintf("刘慈欣/三体 (%d)", id); b.Path != want {
		t.Errorf("path = %q, want %q", b.Path, want)
	}
	d, _ := lib.Details(b)
	if _, err := os.Stat(d.Files[0].Path); err != nil || filepath.Base(d.Files[0].Path) != "三体 - 刘慈欣.epub" {
		t.Errorf("file %s: %v", d.Files[0].Path, err)
	}
	if _, err := os.Stat(filepath.Join(lib.Root, "Liu Ci Xin")); !os.IsNotExist(err) {
		t.Errorf("empty pinyin author folder left behind")
	}
	if misnamed, _ := lib.MisnamedBooks(); len(misnamed) != 0 {
		t.Errorf("still misnamed: %d", len(misnamed))
	}
}
