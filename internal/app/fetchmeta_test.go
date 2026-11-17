package app

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/http"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/plugin"
	builtin "dalibre/plugins"
)

const neodbThreeBody = `{"uuid":"0Oio","category":"book","title":"The Three-Body Problem","author":["Cixin Liu"],
	"language":["en"],"pub_house":"Tor Books","pub_year":2014,"pub_month":11,"isbn":"9780765382030",
	"tags":["science fiction","china"],"description":"The Three-Body Problem is the first chance for English-speaking readers.",
	"cover_image_url":"https://neodb.social/m/cover.png","series":"Remembrance of Earth's Past",
	"external_resources":[{"url":"https://www.goodreads.com/book/show/20518872"}]}`

const neodbSanTi = `{"uuid":"7OGq","category":"book","title":"三体","author":["刘慈欣"],"language":["zh"],
	"pub_house":"重庆出版社","pub_year":2008,"cover_image_url":"https://neodb.social/m/missing.png"}`

// neodbApp is a test app with the built-in NeoDB plugin, answered by a
// fake NeoDB.
func neodbApp(t *testing.T) (*App, *[]string) {
	a := newTestApp(t)
	var queries []string
	cover := image.NewRGBA(image.Rect(0, 0, 80, 120))
	for i := range cover.Pix {
		cover.Pix[i] = 0x30
	}
	cover.Set(1, 1, color.White)
	var buf bytes.Buffer
	png.Encode(&buf, cover)
	mux := http.NewServeMux()
	mux.HandleFunc("neodb.social/api/catalog/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		switch {
		case q == "nothing":
			w.Write([]byte(`{"data":[]}`))
		case q == "down":
			w.WriteHeader(503)
		default:
			w.Write([]byte(`{"data":[` + neodbThreeBody + `,` + neodbSanTi + `]}`))
		}
	})
	mux.HandleFunc("neodb.social/m/cover.png", func(w http.ResponseWriter, r *http.Request) { w.Write(buf.Bytes()) })
	for _, fsys := range builtin.Builtin() {
		if data, _ := fs.ReadFile(fsys, "plugin.json"); !strings.Contains(string(data), "dalibre.neodb") {
			continue
		}
		p, err := plugin.LoadBuiltin(fsys, plugin.Options{Transport: fakeWeb{mux}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		a.plugins = append(a.plugins, p)
	}
	if len(a.metadataSources()) != 1 {
		t.Fatal("no NeoDB plugin")
	}
	return a, &queries
}

func TestFetchMetadata(t *testing.T) {
	a, queries := neodbApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Click("The Three-Body Problem")
	tt.Frame()
	a.openEditor(a.current())
	a.edit.identifiers = "isbn:9780765382030, amazon:B00IQO403K"
	tt.Frame()
	if err := tt.Click("Fetch metadata"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	// The book's ISBN is looked up first.
	if len(*queries) != 1 || (*queries)[0] != "9780765382030" {
		t.Fatalf("queries %q", *queries)
	}
	cands := a.fetchCandidates()
	if len(cands) != 1 || a.fetch.sel != cands[0].key() {
		t.Fatalf("candidates %+v, selected %q", cands, a.fetch.sel)
	}
	if !tt.HasText("CANDIDATES") || !tt.HasText("Cixin Liu · 2014") || !tt.HasText("Tor Books · NeoDB") || !tt.HasText("neodb.social") {
		t.Fatalf("fetch view: %q", tt.Texts())
	}
	use := a.fetch.use
	if use["title"] || !use["authors"] || !use["tags"] || !use["publisher"] || !use["identifiers"] || !use["cover"] || !use["description"] || !use["series"] {
		t.Errorf("picked %v", use)
	}
	if rc := a.fetch.covers["https://neodb.social/m/cover.png"]; rc == nil || rc.bmp == nil || rc.size.X != 80 {
		t.Fatalf("cover %+v", rc)
	}
	shot(t, tt, "fetch-metadata")

	// Untick tags, then use the rest.
	a.fetch.use["tags"] = false
	if err := tt.Click("Use selected fields"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	e := &a.edit
	if a.fetch.open || !tt.HasText("Edit metadata") {
		t.Fatal("not back in the form")
	}
	if e.authors[0] != "Cixin Liu" || e.publisher != "Tor Books" || e.series != "Remembrance of Earth's Past" ||
		e.pubdate.Year() != 2014 || e.pubdate.Month() != 11 || e.cover == nil || e.languages[0] != "eng" {
		t.Fatalf("form %+v", e)
	}
	if e.tags[0] != "Science Fiction" || len(e.tags) != 1 {
		t.Errorf("tags taken though unticked: %v", e.tags)
	}
	if e.identifiers != "amazon:B00IQO403K, goodreads:20518872, isbn:9780765382030, neodb:0Oio" {
		t.Errorf("identifiers %q", e.identifiers)
	}
	if !strings.HasPrefix(e.comments, "The Three-Body Problem is the first") {
		t.Errorf("description %q", e.comments)
	}
	// Saving writes it.
	if err := tt.Click("Save"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	b := a.byID[e.id]
	if b.Authors[0] != "Cixin Liu" || b.Publisher != "Tor Books" {
		t.Fatalf("saved %+v", b)
	}
}

func TestFetchMetadataStates(t *testing.T) {
	a, queries := neodbApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Click("Dune")
	tt.Frame()
	a.openEditor(a.current())
	a.openFetch()
	tt.Frame()
	if (*queries)[0] != "Dune Frank Herbert" || len(a.fetchCandidates()) != 2 {
		t.Fatalf("queries %q, %d candidates", *queries, len(a.fetchCandidates()))
	}

	a.fetch.text = "nothing"
	a.fetchSearch()
	tt.Frame()
	if !tt.HasText("Nothing found on NeoDB") {
		t.Fatalf("no results: %q", tt.Texts())
	}
	a.fetch.text = "down"
	a.fetchSearch()
	tt.Frame()
	if !tt.HasText("NeoDB isn’t answering") || !tt.HasText("Retry") {
		t.Fatalf("error: %q", tt.Texts())
	}
	shot(t, tt, "fetch-metadata-error")
	a.fetch.text = "978-0-7653-8203-0"
	if b := a.fetchBook(); b.Identifiers["isbn"] != "9780765382030" || b.Title != "" {
		t.Errorf("typed isbn: %+v", b)
	}

	if err := tt.Click("Back"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.fetch.open || !a.editOpen {
		t.Fatal("Back didn't return to the form")
	}

	a.plugins = nil
	a.openFetch()
	tt.Frame()
	if !tt.HasText("No plugin fetches metadata") {
		t.Fatalf("no plugins: %q", tt.Texts())
	}
}

func TestISBNOf(t *testing.T) {
	for in, want := range map[string]string{
		"978-0-7653-8203-0": "9780765382030", "ISBN 0765382032": "0765382032", "080442957X": "080442957X",
		"Dune": "", "12345": "", "97807653820X0": "",
	} {
		if got := isbnOf(in); got != want {
			t.Errorf("isbnOf(%q) = %q, want %q", in, got, want)
		}
	}
}

// A long title and a long author stay inside their fields.
func TestEditLongNames(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Click("Dune")
	tt.Frame()
	a.openEditor(a.current())
	a.edit.title = "大模型应用开发极简入门：基于 GPT-4 和 ChatGPT"
	a.edit.authors = []string{"[比] 奥利维耶 • 卡埃朗（Olivier Caelen） [法] 玛丽–艾丽斯 • 布莱特（Marie-Alice Blete）", "何文斯"}
	a.edit.authorSort = ""
	tt.Frame()
	tt.Frame()
	shot(t, tt, "edit-long-names")
	tt.ClickAt(700, 279) // the token field's input, after the chips
	tt.Frame()
	shot(t, tt, "edit-long-names-focus")
}

// The detail panel's Look up online opens the editor on Fetch metadata, and only
// shows with a metadata plugin on.
func TestFetchFromDetail(t *testing.T) {
	a, queries := neodbApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Click("Dune")
	tt.Frame()
	shot(t, tt, "detail-fetch")
	if err := tt.Click("Look up metadata online"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.editOpen || !a.fetch.open || len(*queries) == 0 {
		t.Fatalf("edit %v, fetch %v, queries %q", a.editOpen, a.fetch.open, *queries)
	}
	a.editOpen, a.fetch.open = false, false
	a.plugins = nil
	tt.Frame()
	if _, ok := tt.Find("Look up metadata online"); ok {
		t.Fatal("Fetch shows without a metadata plugin")
	}
}

// A source that needs a key says so, and opens its settings.
func TestFetchMetadataNeedsSetup(t *testing.T) {
	a := newTestApp(t)
	mux := http.NewServeMux()
	mux.HandleFunc("www.googleapis.com/books/v1/volumes", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
		w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED"}}`))
	})
	for _, fsys := range builtin.Builtin() {
		if data, _ := fs.ReadFile(fsys, "plugin.json"); !strings.Contains(string(data), "dalibre.googlebooks") {
			continue
		}
		p, err := plugin.LoadBuiltin(fsys, plugin.Options{Transport: fakeWeb{mux}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		a.plugins = append(a.plugins, p)
		a.pluginsKnown = append(a.pluginsKnown, knownPlugin{manifest: p.Manifest, builtin: true})
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Click("Dune")
	tt.Frame()
	a.openEditor(a.current())
	a.openFetch()
	tt.Frame()
	if !tt.HasText("Google Books needs setting up") || !tt.HasText("Google’s shared quota is used up. Add your own API key.") {
		t.Fatalf("setup: %q", tt.Texts())
	}
	shot(t, tt, "fetch-metadata-setup")
	if err := tt.Click("Open plugin settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.pluginsOpen || a.plTab != pluginsTabInstalled || a.pl.sel != "dalibre.googlebooks" || !tt.HasText("API key") {
		t.Fatalf("plugins open %v on %q", a.pluginsOpen, a.pl.sel)
	}
}
