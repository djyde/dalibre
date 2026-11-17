package plugin

import (
	"context"
	"strings"
	"testing"
)

const metaManifest = `{"id":"meta","name":"Meta","version":"1.0.0","api":1,
	"capabilities":{"metadata":{"fields":["title","authors","cover"]}},
	"permissions":{"hosts":["meta.example"],"library":"read"}}`

func TestIdentify(t *testing.T) {
	var logs []string
	dir := write(t, metaManifest, `
		export function identify(book) {
			if (book.identifiers.isbn === "1") return [];
			return [
				{ id: "a", title: book.title + "!", authors: book.authors, identifiers: { ISBN: " 978 ", "bad:key": "x" },
				  cover: "https://meta.example/a.jpg", formats: [{ format: "EPUB" }], extra: 1 },
				{ id: "a", title: "dup" },
				{ id: "b" },
				{ id: "c", title: "C", pubdate: "someday", nonsense: true },
			];
		}`)
	p, err := Load(dir, Options{Logf: func(f string, a ...any) { logs = append(logs, f) }})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	got, err := p.Identify(context.Background(), Book{Title: " Dune ", Authors: []string{"Frank Herbert", " "}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Dune!" || got[0].Authors[0] != "Frank Herbert" || got[1].ID != "c" {
		t.Fatalf("candidates %+v", got)
	}
	if got[0].Formats != nil || got[0].Extra != nil || got[0].Identifiers["isbn"] != "978" || len(got[0].Identifiers) != 1 {
		t.Errorf("candidate a %+v", got[0])
	}
	if got[1].Pubdate != "" {
		t.Errorf("bad pubdate kept: %q", got[1].Pubdate)
	}
	if len(logs) < 4 {
		t.Errorf("warnings %v", logs)
	}
	none, err := p.Identify(context.Background(), Book{Identifiers: map[string]string{"isbn": "1"}})
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("no candidates: %v, %v", none, err)
	}
	if _, err := p.Identify(context.Background(), Book{}); kind(err) != KindInvalid {
		t.Errorf("empty book: %v", err)
	}
}

func TestMetadataManifest(t *testing.T) {
	for _, tc := range []struct{ manifest, want string }{
		{`{"id":"m","name":"M","version":"1.0.0","api":1,"capabilities":{"metadata":{}},"permissions":{"hosts":["a.example"]}}`, `permissions.library "read"`},
		{`{"id":"m","name":"M","version":"1.0.0","api":1,"capabilities":{"metadata":{}},"permissions":{"hosts":[],"library":"read"}}`, "must list the hosts"},
		{`{"id":"m","name":"M","version":"1.0.0","api":1,"capabilities":{"metadata":{"fields":["rating"]}},"permissions":{"hosts":["a.example"],"library":"read"}}`, `"rating" isn’t one of`},
		{`{"id":"m","name":"M","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.example"],"library":"admin"}}`, `must be "read" or "write"`},
	} {
		_, err := ParseManifest([]byte(tc.manifest))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: got %v, want %q", tc.manifest, err, tc.want)
		}
	}
	m, err := ParseManifest([]byte(`{"id":"m","name":"M","version":"1.0.0","api":1,"capabilities":["metadata"],"permissions":{"hosts":["a.example"],"library":"read"}}`))
	if err != nil || !m.IsMetadataSource() || m.IsBookSource() {
		t.Fatalf("array form: %v", err)
	}
	if (Permissions{Hosts: []string{"a.example"}}).Covers(m.Permissions) {
		t.Error("a grant without library covers a plugin asking for it")
	}
	if !(Permissions{Hosts: []string{"a.example"}, Library: "read"}).Covers(m.Permissions) {
		t.Error("a full grant doesn't cover")
	}
	if d := m.Permissions.Describe(); len(d) != 2 || !strings.Contains(d[1], "books you look up") {
		t.Errorf("describe %v", d)
	}
	dir := write(t, metaManifest, `export function search() { return { items: [] } }`)
	if _, err := Load(dir, Options{}); err == nil || !strings.Contains(err.Error(), "must export identify") {
		t.Errorf("no identify: %v", err)
	}
}
