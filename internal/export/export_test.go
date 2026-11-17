package export

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"dalibre/internal/calibre"
)

func book(id int64, title string, authors []string, series string, index float64, files ...calibre.FormatFile) Book {
	return Book{
		Book:    &calibre.Book{ID: id, Title: title, Authors: authors, Series: series, SeriesIndex: index},
		Details: &calibre.Details{Files: files, Identifiers: map[string]string{"isbn": "9780765382030"}},
	}
}

func TestPath(t *testing.T) {
	tbp := book(1, "The Three-Body Problem", []string{"Liu Cixin"}, "Remembrance of Earth’s Past", 1)
	loose := book(2, "三体: 地球往事", []string{"刘慈欣"}, "", 0)
	const issue = "{authors}/{series}/{series_index:|| - }{title}/{title} - {authors}"
	for _, c := range []struct {
		template string
		b        Book
		ascii    bool
		want     string
	}{
		{DefaultTemplate, tbp, false, "Cixin, Liu/The Three-Body Problem/The Three-Body Problem - Liu Cixin"},
		{issue, tbp, false, "Liu Cixin/Remembrance of Earth’s Past/1 - The Three-Body Problem/The Three-Body Problem - Liu Cixin"},
		// No series: its folder is left out and the index's " - " too.
		{issue, loose, false, "刘慈欣/三体_ 地球往事/三体_ 地球往事 - 刘慈欣"},
		{issue, loose, true, "Liu Ci Xin/San Ti _ Di Qiu Wang Shi/San Ti _ Di Qiu Wang Shi - Liu Ci Xin"},
		{"{series:||/}{title} [{isbn}] {format}", loose, false, "三体_ 地球往事 [9780765382030] EPUB"},
		{"{series:||/}{title}", tbp, false, "Remembrance of Earth’s Past/The Three-Body Problem"},
		// A slash in a value is not a folder.
		{"{authors}/{title}", book(5, "AC/DC", []string{"A/B"}, "", 0), false, "A_B/AC_DC"},
		// Nothing left of the name: Unknown. Leading slash and dots stay inside.
		{"/../{publisher}", tbp, false, "_/Unknown"},
		{"{authors}/{title}", book(3, "x", []string{"CON"}, "", 0), false, "_CON/x"},
	} {
		if got := Path(c.template, c.b, "EPUB", c.ascii); got != c.want {
			t.Errorf("Path(%q, %q, ascii %v) = %q, want %q", c.template, c.b.Title, c.ascii, got, c.want)
		}
	}
	long := book(4, strings.Repeat("长", 120), []string{"A"}, "", 0)
	got := Path("{title}", long, "EPUB", false)
	if len(got) > maxName || !utf8.ValidString(got) || !strings.HasPrefix(got, "长") {
		t.Errorf("long title cut to %d bytes, valid %v", len(got), utf8.ValidString(got))
	}
}

func TestPlanAndRun(t *testing.T) {
	src := t.TempDir()
	file := func(name, data string) calibre.FormatFile {
		p := filepath.Join(src, name)
		os.WriteFile(p, []byte(data), 0o644)
		return calibre.FormatFile{Format: strings.ToUpper(strings.TrimPrefix(filepath.Ext(name), ".")), Path: p}
	}
	// Two different books that the template names alike, one with a PDF
	// only, and a file already in the folder at the first one's name.
	books := []Book{
		book(1, "Dune", []string{"Frank Herbert"}, "", 0, file("a.epub", "dune 1"), file("a.mobi", "dune mobi")),
		book(2, "Dune", []string{"Frank Herbert"}, "", 0, file("b.epub", "dune 2")),
		book(3, "Notes", []string{"Me"}, "", 0, file("c.pdf", "pdf")),
	}
	books[0].Cover = file("cover.jpg", "jpg").Path
	books[0].OPF = func() []byte { return []byte("<opf/>") }

	for _, c := range []struct {
		policy Policy
		paths  []string
		notes  []Note
		dune   string // what Dune - Frank Herbert.epub holds after
	}{
		{KeepBoth, []string{"Dune - Frank Herbert (2).epub", "Dune - Frank Herbert.mobi", "Dune - Frank Herbert (3).epub"},
			[]Note{Numbered, New, Numbered}, "theirs"},
		{Replace, []string{"Dune - Frank Herbert.epub", "Dune - Frank Herbert.mobi", "Dune - Frank Herbert (2).epub"},
			[]Note{Replaces, New, Numbered}, "dune 1"},
		{Skip, []string{"Dune - Frank Herbert.epub", "Dune - Frank Herbert.mobi", "Dune - Frank Herbert (2).epub"},
			[]Note{Skipped, New, Numbered}, "theirs"},
	} {
		dir := t.TempDir()
		os.WriteFile(filepath.Join(dir, "Dune - Frank Herbert.epub"), []byte("theirs"), 0o644)
		o := Options{Template: "{title} - {authors}", Formats: []string{"EPUB", "MOBI"}, Policy: c.policy}
		items, missing := Plan(dir, books, o)
		if len(missing) != 1 || missing[0] != "Notes" {
			t.Errorf("%v: missing %v, want [Notes]", c.policy, missing)
		}
		if len(items) != len(c.paths) {
			t.Fatalf("%v: %d items, want %d: %+v", c.policy, len(items), len(c.paths), items)
		}
		for i, it := range items {
			if it.Path != c.paths[i] || it.Note != c.notes[i] {
				t.Errorf("%v: item %d = %q note %d, want %q note %d", c.policy, i, it.Path, it.Note, c.paths[i], c.notes[i])
			}
		}
		n, err := Run(context.Background(), dir, items, nil)
		wantN := 3
		if c.policy == Skip {
			wantN = 2
		}
		if err != nil || n != wantN {
			t.Errorf("%v: Run = %d, %v; want %d", c.policy, n, err, wantN)
		}
		if b, _ := os.ReadFile(filepath.Join(dir, "Dune - Frank Herbert.epub")); string(b) != c.dune {
			t.Errorf("%v: existing file holds %q, want %q", c.policy, b, c.dune)
		}
		entries, _ := os.ReadDir(dir)
		for _, e := range entries {
			if strings.HasSuffix(e.Name(), ".part") {
				t.Errorf("%v: left %s", c.policy, e.Name())
			}
		}
	}

	// Extras are named after the book's first file, even when it was
	// numbered.
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Dune.epub"), nil, 0o644)
	items, _ := Plan(dir, books[:1], Options{Template: "{title}", Formats: []string{"EPUB"}, Extras: true})
	var got []string
	for _, it := range items {
		got = append(got, it.Path)
	}
	if want := "Dune (2).epub Dune (2).jpg Dune (2).opf"; strings.Join(got, " ") != want {
		t.Errorf("with extras: %v, want %s", got, want)
	}
	if _, err := Run(context.Background(), dir, items, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "Dune (2).opf")); string(b) != "<opf/>" {
		t.Errorf("opf = %q", b)
	}

	// Stopped: nothing more is written.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir = t.TempDir()
	items, _ = Plan(dir, books, Options{Template: "{title}", Formats: []string{"EPUB"}})
	if n, err := Run(ctx, dir, items, nil); n != 0 || err == nil {
		t.Errorf("stopped Run = %d, %v", n, err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("stopped Run wrote %d files", len(entries))
	}
}
