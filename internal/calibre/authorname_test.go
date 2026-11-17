package calibre

import (
	"os"
	"path/filepath"
	"testing"
)

// An author with a comma is kept as calibre keeps it, "|" for the comma,
// and read back with the comma.
func TestAuthorWithComma(t *testing.T) {
	lib, err := Create(filepath.Join(t.TempDir(), "Library"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	f := filepath.Join(t.TempDir(), "book.epub")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := lib.AddBook(NewBook{File: f, Metadata: Metadata{Title: "Dune", Authors: []string{"Herbert, Frank"}}}); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := lib.db.QueryRow(`SELECT name FROM authors`).Scan(&stored); err != nil || stored != "Herbert| Frank" {
		t.Fatalf("stored %q, %v", stored, err)
	}
	books, err := lib.Books()
	if err != nil || len(books) != 1 || len(books[0].Authors) != 1 || books[0].Authors[0] != "Herbert, Frank" {
		t.Fatalf("read %+v, %v", books, err)
	}
}

// A library made before calibre 8 has no books_pages_link; removing a
// book from it works.
func TestRemoveFromOlderLibrary(t *testing.T) {
	lib, err := Create(filepath.Join(t.TempDir(), "Library"))
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	for _, q := range []string{`DROP TRIGGER books_pages_link_create_trigger`, `DROP TABLE books_pages_link`} {
		if _, err := lib.db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	f := filepath.Join(t.TempDir(), "book.epub")
	os.WriteFile(f, []byte("x"), 0o644)
	id, err := lib.AddBook(NewBook{File: f, Metadata: Metadata{Title: "Dune", Authors: []string{"Frank Herbert"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := lib.RemoveBooks([]int64{id}, nil); err != nil {
		t.Fatal(err)
	}
	if books, _ := lib.Books(); len(books) != 0 {
		t.Fatalf("left %d books", len(books))
	}
}
