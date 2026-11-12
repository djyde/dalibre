package calibre

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

func readFormatRow(t *testing.T, l *Library) map[string]any {
	t.Helper()
	var raw string
	if err := l.db.QueryRow(`SELECT val FROM preferences WHERE key = ?`, FormatKey).Scan(&raw); err != nil {
		t.Fatalf("format row: %v", err)
	}
	m := map[string]any{}
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("format row %q: %v", raw, err)
	}
	return m
}

func TestCreateWritesFormat(t *testing.T) {
	lib := newTestLibrary(t)
	row := readFormatRow(t, lib)
	if row["version"] != float64(FormatVersion) || row["file_names"] != "original" {
		t.Errorf("row = %v", row)
	}
	if f := lib.Format(); f.Version != FormatVersion || f.FileNames != FileNamesOriginal {
		t.Errorf("format = %+v", f)
	}
}

func TestFormatOfCalibreLibrary(t *testing.T) {
	lib := newTestLibrary(t)
	lib.db.Exec(`DELETE FROM preferences WHERE key = ?`, FormatKey)
	lib.loadFormat()
	if f := lib.Format(); f.Version != 0 || f.FileNames != FileNamesOriginal {
		t.Errorf("format of a calibre library = %+v", f)
	}
}

func TestFileNamesASCII(t *testing.T) {
	lib := newTestLibrary(t)
	if err := lib.SetFileNames(FileNamesASCII); err != nil {
		t.Fatal(err)
	}
	// The setting lives in the library: reopening keeps it.
	root := lib.Root
	lib.Close()
	lib, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer lib.Close()
	if lib.Format().FileNames != FileNamesASCII {
		t.Fatalf("reopened format = %+v", lib.Format())
	}
	id, err := lib.AddBook(NewBook{File: writeTemp(t, "a.epub", "x"), Metadata: Metadata{Title: "三体", Authors: []string{"刘慈欣"}}})
	if err != nil {
		t.Fatal(err)
	}
	b, _ := lib.Book(id)
	if want := "Liu Ci Xin/San Ti (" + itoa(id) + ")"; b.Path != want {
		t.Errorf("path = %q, want %q", b.Path, want)
	}
	// Switching back makes the book misnamed, and renaming fixes it.
	lib.SetFileNames(FileNamesOriginal)
	if m, _ := lib.MisnamedBooks(); len(m) != 1 {
		t.Fatalf("misnamed = %d", len(m))
	}
	if _, err := lib.RenameBooks([]int64{id}, nil); err != nil {
		t.Fatal(err)
	}
	b, _ = lib.Book(id)
	if filepath.ToSlash(b.Path) != "刘慈欣/三体 ("+itoa(id)+")" {
		t.Errorf("renamed path = %q", b.Path)
	}
}

func TestSaveFormatKeepsUnknownFields(t *testing.T) {
	lib := newTestLibrary(t)
	lib.db.Exec(`UPDATE preferences SET val = '{"version":7,"file_names":"original","later":{"x":1}}' WHERE key = ?`, FormatKey)
	lib.loadFormat()
	if err := lib.SetFileNames(FileNamesASCII); err != nil {
		t.Fatal(err)
	}
	row := readFormatRow(t, lib)
	if row["version"] != float64(7) || row["file_names"] != "ascii" || row["later"] == nil {
		t.Errorf("row = %v", row)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }
