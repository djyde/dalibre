package usbms

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dalibre/internal/device"
)

// The paths calibre gave books on a Kindle, with the metadata it had.
func TestUploadPathAsCalibre(t *testing.T) {
	cases := []struct {
		title      string
		authors    []string
		authorSort string
		ext        string
		want       string
	}{
		{"Mathematics Puzzles from Around The World", []string{"Martin Gardner"}, "Gardner, Martin", ".pdf",
			"documents/Gardner, Martin/Mathematics Puzzles from Around The World - Martin Gardner.pdf"},
		{"Radio Free Albemuth", []string{"Philip K. Dick"}, "Dick, Philip K.", ".mobi",
			"documents/Dick, Philip K_/Radio Free Albemuth - Philip K. Dick.mobi"},
		{"La Guerre Éternelle", []string{"Haldeman, Joe"}, "Haldeman, Joe", ".mobi",
			"documents/Haldeman, Joe/La Guerre Eternelle - Haldeman, Joe.mobi"},
		{"L'épée du Licteur", []string{"Wolfe,Gene"}, "Wolfe,Gene", ".mobi",
			"documents/Wolfe,Gene/L'epee du Licteur - Wolfe,Gene.mobi"},
		{"L'Écume des jours", []string{"Boris Vian"}, "Vian, Boris", ".MOBI",
			"documents/Vian, Boris/L'Ecume des jours - Boris Vian.mobi"},
	}
	for _, c := range cases {
		b := device.Book{Title: c.title, Authors: c.authors, AuthorSort: c.authorSort}
		got := uploadPath(DefaultTemplate, "documents", true, b, c.ext, len("/run/media/laurent/Kindle/"))
		if got != c.want {
			t.Errorf("uploadPath(%q) = %q, want %q", c.title, got, c.want)
		}
	}
}

func TestUploadPathShortened(t *testing.T) {
	b := device.Book{Title: strings.Repeat("Long title ", 30), Authors: []string{strings.Repeat("Name ", 20)}}
	got := uploadPath(DefaultTemplate, "documents", true, b, ".azw3", 26)
	if len(got) > maxPathLen-26 {
		t.Errorf("path is %d long, over %d: %q", len(got), maxPathLen-26, got)
	}
	if !strings.HasSuffix(got, ".azw3") || !strings.HasPrefix(got, "documents/") {
		t.Errorf("shortened path %q lost its folder or extension", got)
	}
}

// A metadata.calibre entry goes through reading and writing with the
// fields Dalibre doesn't know as they were.
func TestEntryKeepsUnknownFields(t *testing.T) {
	raw := `{"title": "Fondation", "authors": ["Isaac Asimov"], "author_sort": "Asimov, Isaac",
		"lpath": "documents/Asimov, Isaac/Fondation - Isaac Asimov.mobi", "uuid": "u1", "size": 10,
		"pubdate": "None", "timestamp": "2015-08-22T11:34:00+00:00", "series_index": null, "series": null,
		"user_metadata": {"#read": {"datatype": "bool", "#value#": true}}, "rating": 8,
		"thumbnail": null, "identifiers": {"isbn": "123"}, "tags": ["sf"], "mime": "application/x-mobipocket-ebook"}`
	var e entry
	if err := json.Unmarshal([]byte(raw), &e); err != nil {
		t.Fatal(err)
	}
	b := e.book()
	if b.Title != "Fondation" || b.AuthorSort != "Asimov, Isaac" || b.Rating != 8 || b.Identifiers["isbn"] != "123" {
		t.Fatalf("book = %+v", b)
	}
	if !b.Pubdate.IsZero() || b.Timestamp.Year() != 2015 {
		t.Errorf("dates = %v, %v", b.Pubdate, b.Timestamp)
	}
	b.Title = "Fondation (new)"
	e.update(b)
	if !strings.Contains(string(e["user_metadata"]), "#read") {
		t.Errorf("user_metadata lost: %s", e["user_metadata"])
	}
	if e.str("title") != "Fondation (new)" || e.str("pubdate") != "None" || e.str("timestamp") != "2015-08-22T11:34:00+00:00" {
		t.Errorf("entry = title %q pubdate %q timestamp %q", e.str("title"), e.str("pubdate"), e.str("timestamp"))
	}
}

// newEntry writes every field calibre's codec writes.
func TestNewEntryHasCalibreFields(t *testing.T) {
	e := newEntry(device.Book{Title: "T", Authors: []string{"A B"}, AuthorSort: "B, A", LPath: "documents/x.azw3", UUID: "u"})
	for _, k := range []string{"comments", "publication_type", "last_modified", "publisher", "cover", "uuid", "pubdate",
		"thumbnail", "title_sort", "languages", "rating", "link_maps", "db_id", "author_sort_map", "application_id",
		"authors", "tags", "title", "timestamp", "mime", "user_categories", "series_index", "user_metadata", "rights",
		"author_sort", "lpath", "size", "series", "identifiers", "book_producer"} {
		if _, ok := e[k]; !ok {
			t.Errorf("new entry has no %q", k)
		}
	}
	if string(e["author_sort_map"]) != `{"A B":"B, A"}` {
		t.Errorf("author_sort_map = %s", e["author_sort_map"])
	}
}

func newVolume(t *testing.T) (*device.Volume, string) {
	dir := t.TempDir()
	return device.NewVolume(dir, "", "Kindle", device.USBInfo{}), dir
}

func write(t *testing.T, root, name, data string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

var testConfig = Config{
	Info:     device.Info{Kind: "test", Name: "Reader"},
	EbookDir: "documents",
	SubDirs:  true,
	Formats:  []string{"AZW3", "MOBI", "PDF", "TXT"},
	Sidecars: func(lp string) []string { return []string{strings.TrimSuffix(lp, filepath.Ext(lp)) + ".sdr"} },
}

func TestOpenScanSendDelete(t *testing.T) {
	v, root := newVolume(t)
	// A book calibre sent, one it hasn't seen, one gone, and a file of a
	// format the reader doesn't take.
	write(t, root, "documents/Asimov, Isaac/Fondation - Isaac Asimov.mobi", "mobi!")
	write(t, root, "documents/Notes - Me.txt", "hello")
	write(t, root, "documents/other.epub", "epub")
	write(t, root, "documents/x.sdr/x.mbp", "notes")
	cache := `[
	  {"title": "Fondation", "authors": ["Isaac Asimov"], "author_sort": "Asimov, Isaac", "uuid": "u1",
	   "lpath": "documents/Asimov, Isaac/Fondation - Isaac Asimov.mobi", "size": 5, "custom": 1},
	  {"title": "Gone", "authors": ["X"], "lpath": "documents/gone.mobi", "uuid": "u2", "size": 1}
	]`
	write(t, root, "metadata.calibre", cache)
	write(t, root, "driveinfo.calibre", `{"device_store_uuid": "store-1", "calibre_version": "8.7.0", "last_library_uuid": null}`)

	cfg := testConfig
	cfg.LibraryUUID = func() string { return "lib-1" }
	d, err := Open(context.Background(), v, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if d.Info().ID != "store-1" {
		t.Errorf("ID = %q, want driveinfo's store-1", d.Info().ID)
	}
	books := d.Books()
	var paths []string
	for _, b := range books {
		paths = append(paths, b.LPath)
	}
	want := []string{"documents/Asimov, Isaac/Fondation - Isaac Asimov.mobi", "documents/Notes - Me.txt"}
	if !slices.Equal(paths, want) {
		t.Fatalf("books = %q, want %q", paths, want)
	}
	if books[1].Title != "Notes" || books[1].Authors[0] != "Me" || books[1].UUID == "" {
		t.Errorf("new book read as %+v", books[1])
	}
	data, _ := os.ReadFile(filepath.Join(root, "metadata.calibre"))
	if !strings.Contains(string(data), `"custom": 1`) || strings.Contains(string(data), "gone.mobi") {
		t.Errorf("metadata.calibre not rewritten as calibre would:\n%s", data)
	}
	di, _ := os.ReadFile(filepath.Join(root, "driveinfo.calibre"))
	if !strings.Contains(string(di), `"calibre_version":"8.7.0"`) || !strings.Contains(string(di), "date_last_connected") ||
		!strings.Contains(string(di), `"prefix":"`+filepath.ToSlash(root)+`/"`) {
		t.Errorf("driveinfo.calibre = %s", di)
	}

	// Send a book.
	src := filepath.Join(t.TempDir(), "book.azw3")
	os.WriteFile(src, []byte("azw3 data"), 0o644)
	ts := time.Date(2024, 3, 1, 10, 0, 0, 0, time.UTC)
	sent, err := d.Send(context.Background(), []device.Upload{{File: src, Book: device.Book{
		Title: "Dune", Authors: []string{"Frank Herbert"}, UUID: "lib-uuid", Timestamp: ts, Tags: []string{"sf"},
	}}}, nil)
	if err != nil || len(sent) != 1 {
		t.Fatalf("Send = %v, %v", sent, err)
	}
	lp := "documents/Herbert, Frank/Dune - Frank Herbert.azw3"
	if sent[0].LPath != lp || sent[0].Size != 9 || sent[0].Mime != "application/x-mobi8-ebook" {
		t.Errorf("sent = %+v", sent[0])
	}
	if got, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(lp))); string(got) != "azw3 data" {
		t.Errorf("file on the reader = %q", got)
	}
	es, _ := readCache(v)
	if len(es) != 3 || es[2].str("uuid") != "lib-uuid" || es[2].str("timestamp") != "2024-03-01T10:00:00+00:00" {
		t.Errorf("cache after send: %d entries, last %v", len(es), es[len(es)-1])
	}
	di, _ = os.ReadFile(filepath.Join(root, "driveinfo.calibre"))
	if !strings.Contains(string(di), `"last_library_uuid":"lib-1"`) {
		t.Errorf("driveinfo after send = %s", di)
	}

	// A format the reader doesn't take is refused.
	epub := filepath.Join(t.TempDir(), "b.epub")
	os.WriteFile(epub, []byte("e"), 0o644)
	if _, err := d.Send(context.Background(), []device.Upload{{File: epub, Book: device.Book{Title: "E"}}}, nil); err == nil {
		t.Error("an EPUB was sent to a reader that doesn't take it")
	}

	// Delete the sent book: its file, its author's folder left empty, its
	// sidecar, its entry.
	write(t, root, "documents/Herbert, Frank/Dune - Frank Herbert.sdr/a.mbp", "n")
	if err := d.Delete(context.Background(), []string{lp}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "documents", "Herbert, Frank")); !os.IsNotExist(err) {
		t.Errorf("author folder still there: %v", err)
	}
	if len(d.Books()) != 2 {
		t.Errorf("books after delete = %d", len(d.Books()))
	}
	// A path out of the reader is never touched.
	if err := d.Delete(context.Background(), []string{"../outside.mobi"}); err != nil {
		t.Errorf("Delete(../) = %v", err)
	}
}

func TestOpenWithoutCalibre(t *testing.T) {
	v, root := newVolume(t)
	write(t, root, "documents/Book - Author.pdf", "%PDF")
	d, err := Open(context.Background(), v, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Books()) != 1 || d.Info().ID == "" {
		t.Fatalf("books = %v, id %q", d.Books(), d.Info().ID)
	}
	if _, err := os.Stat(filepath.Join(root, "metadata.calibre")); err != nil {
		t.Errorf("no metadata.calibre written: %v", err)
	}
}
