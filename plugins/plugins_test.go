package plugins

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"dalibre/internal/plugin"
)

// serve answers every host's requests with h, as if the plugin reached
// the real ones.
type serve struct{ h http.Handler }

func (s serve) RoundTrip(r *http.Request) (*http.Response, error) {
	rec := &recorder{header: http.Header{}, code: 200}
	s.h.ServeHTTP(rec, r)
	return &http.Response{StatusCode: rec.code, Status: http.StatusText(rec.code), Header: rec.header,
		Body: nopCloser{strings.NewReader(rec.body.String())}, Request: r, ContentLength: int64(rec.body.Len())}, nil
}

type recorder struct {
	header http.Header
	body   strings.Builder
	code   int
}

func (r *recorder) Header() http.Header         { return r.header }
func (r *recorder) Write(b []byte) (int, error) { return r.body.Write(b) }
func (r *recorder) WriteHeader(code int)        { r.code = code }

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

const searchFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<link rel="self" href="/ebooks/search.opds/?query=frankenstein"/>
<link rel="next" title="Next Page" href="/ebooks/search.opds/?query=frankenstein&amp;start_index=26"/>
<entry><id>https://www.gutenberg.org/ebooks/authors/search.opds/?query=frankenstein</id><title>Authors</title>
<content type="text">2 author names match your search.</content></entry>
<entry><id>https://www.gutenberg.org/ebooks/84.opds</id><title>Frankenstein; Or, The Modern Prometheus</title>
<content type="text">Mary Wollstonecraft Shelley</content></entry>
<entry><id>https://www.gutenberg.org/ebooks/67976.opds</id><title>故事新讀本 (Chinese)</title>
<content type="text">May 2, 2022</content></entry>
</feed>`

const bookFeed = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dcterms="http://purl.org/dc/terms/">
<entry><title>Frankenstein</title>
<link type="application/epub+zip" rel="http://opds-spec.org/acquisition" length="294175" href="https://www.gutenberg.org/ebooks/84.epub.noimages"/>
</entry>
<entry><title>Frankenstein; or, The modern Prometheus</title>
<content type="xhtml"><div xmlns="http://www.w3.org/1999/xhtml">
<p>Summary:
A scientist builds a creature &amp; abandons it. (This is an automatically generated summary.)
</p>
<p>Downloads: 204693</p>
</div></content>
<published>1993-10-01T00:00:00+00:00</published>
<rights>Public domain in the USA.</rights>
<author><name>Shelley, Mary Wollstonecraft</name></author>
<category scheme="http://purl.org/dc/terms/LCSH" term="Monsters -- Fiction"/>
<category scheme="http://purl.org/dc/terms/LCSH" term="Gothic fiction"/>
<dcterms:language>en</dcterms:language>
<link type="application/epub+zip" rel="http://opds-spec.org/acquisition" length="440131" href="https://www.gutenberg.org/ebooks/84.epub3.images"/>
<link type="application/x-mobipocket-ebook" rel="http://opds-spec.org/acquisition" length="547590" href="https://www.gutenberg.org/ebooks/84.kf8.images"/>
<link type="application/x-mobipocket-ebook" rel="http://opds-spec.org/acquisition" length="519544" href="https://www.gutenberg.org/ebooks/84.kindle.images"/>
</entry>
</feed>`

func TestGutenberg(t *testing.T) {
	var gotQuery string
	mux := http.NewServeMux()
	mux.HandleFunc("www.gutenberg.org/ebooks/search.opds/", func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(searchFeed))
	})
	mux.HandleFunc("www.gutenberg.org/ebooks/84.opds", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(bookFeed)) })
	mux.HandleFunc("www.gutenberg.org/ebooks/84.epub3.images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Write([]byte("PK epub"))
	})
	p, err := plugin.LoadBuiltin(builtinFS(t, "gutenberg"), plugin.Options{Transport: serve{mux}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Manifest.ID != "dalibre.gutenberg" || !p.Manifest.HasCollections() {
		t.Fatalf("manifest %+v", p.Manifest)
	}
	ctx := context.Background()

	page, err := p.Search(ctx, plugin.Query{Text: "frankenstein", Page: 2, Filters: map[string]string{"language": "zh", "sort": "new"}})
	if err != nil {
		t.Fatal(err)
	}
	if gotQuery != "sort_order=release_date&start_index=26&query=frankenstein%20l.zh" {
		t.Errorf("query %s", gotQuery)
	}
	if len(page.Items) != 2 || !page.HasMore || len(page.Warnings) != 0 {
		t.Fatalf("page %+v", page)
	}
	r := page.Items[0]
	if r.ID != "84" || r.Authors[0] != "Mary Wollstonecraft Shelley" || len(r.Formats) != 3 || r.Identifiers["gutenberg"] != "84" ||
		r.URL != "https://www.gutenberg.org/ebooks/84" || !strings.HasSuffix(r.Cover, "/pg84.cover.medium.jpg") {
		t.Fatalf("result %+v", r)
	}
	if other := page.Items[1]; other.Title != "故事新讀本" || len(other.Authors) != 0 {
		t.Errorf("titled %q by %q", other.Title, other.Authors)
	}

	if _, err := p.List(ctx, plugin.Query{Collection: "popular"}); err != nil || gotQuery != "sort_order=downloads&start_index=1" {
		t.Errorf("list: %v, query %s", err, gotQuery)
	}

	full, err := p.Get(ctx, r)
	if err != nil {
		t.Fatal(err)
	}
	if full.Title != "Frankenstein; or, The modern Prometheus" || full.Language != "en" || full.Description != "A scientist builds a creature & abandons it." ||
		len(full.Tags) != 2 || full.Tags[0] != "Monsters — Fiction" || full.Authors[0] != "Mary Wollstonecraft Shelley" {
		t.Fatalf("get %+v", full)
	}
	if len(full.Formats) != 3 || full.Formats[0].Format != "EPUB" || full.Formats[0].Size != 440131 || full.Formats[1].Format != "AZW3" {
		t.Fatalf("formats %+v", full.Formats)
	}
	if len(full.Fields) != 3 || full.Fields[1].Value != "204,693" || full.Fields[2].Value != "Public domain in the USA" {
		t.Fatalf("fields %+v", full.Fields)
	}

	path, err := p.Download(ctx, full, "EPUB", t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Ext(path) != ".epub" {
		t.Fatalf("path %s", path)
	}
}

// builtinFS is the built-in plugin in folder name.
func builtinFS(t *testing.T, name string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(files, name)
	if err != nil {
		t.Fatal(err)
	}
	if len(Builtin()) != 3 {
		t.Fatalf("%d built-in plugins", len(Builtin()))
	}
	return sub
}

const neodbThreeBody = `{"uuid":"7OGq","category":"book","title":"三体","orig_title":"三体","author":["Liu Cixin"],
	"language":["zh"],"pub_house":"重庆出版社","pub_year":2008,"pub_month":1,"series":"中国科幻基石丛书","isbn":"9787536692930",
	"tags":["科幻","小说"],"rating":8.6,"rating_count":1548,"pages":"302",
	"description":"文化大革命如火如荼进行的同时。\r\n\u3000\u3000四光年外……",
	"cover_image_url":"https://neodb.social/m/item/a.jpg",
	"external_resources":[{"url":"https://book.douban.com/subject/2567698/"},{"url":"https://www.goodreads.com/book/show/9574008"}]}`

const neodbSequel = `{"uuid":"4nl4","category":"book","title":"三体3","orig_title":"三体","author":["刘慈欣"],
	"cover_image_url":"https://neodb.social/s/img/missing.png"}`

func TestNeoDB(t *testing.T) {
	var queries []string
	mux := http.NewServeMux()
	mux.HandleFunc("neodb.social/api/catalog/search", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("query")
		queries = append(queries, q)
		if r.URL.Query().Get("category") != "book" {
			t.Errorf("category %q", r.URL.Query().Get("category"))
		}
		switch q {
		case "9787536692930":
			w.Write([]byte(`{"data":[` + neodbThreeBody + `],"pages":1,"count":1}`))
		case "三体 刘慈欣":
			w.Write([]byte(`{"data":[` + neodbSequel + `],"pages":1,"count":1}`))
		case "三体":
			w.Write([]byte(`{"data":[` + neodbSequel + `,` + neodbThreeBody + `,{"uuid":"x","category":"movie"}],"pages":1,"count":3}`))
		case "busy":
			w.WriteHeader(429)
		default:
			w.Write([]byte(`{"data":[],"pages":0,"count":0}`))
		}
	})
	mux.HandleFunc("neodb.social/api/book/7OGq", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(neodbThreeBody)) })
	mux.HandleFunc("neodb.social/api/book/gone000000", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	p, err := plugin.LoadBuiltin(builtinFS(t, "neodb"), plugin.Options{Transport: serve{mux}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if p.Manifest.ID != "dalibre.neodb" || !p.Manifest.IsMetadataSource() || p.Manifest.IsBookSource() {
		t.Fatalf("manifest %+v", p.Manifest)
	}
	ctx := context.Background()

	// By title and author: the exact title first, though its author is written differently.
	got, err := p.Identify(ctx, plugin.Book{Title: "三体", Authors: []string{"刘慈欣"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "7OGq" || got[1].ID != "4nl4" {
		t.Fatalf("candidates %+v", got)
	}
	if strings.Join(queries, "|") != "三体 刘慈欣|三体" {
		t.Errorf("queries %q", queries)
	}
	c := got[0]
	if c.Title != "三体" || c.Authors[0] != "Liu Cixin" || c.Publisher != "重庆出版社" || c.Pubdate != "2008-01" || c.Language != "zh" ||
		c.Series != "中国科幻基石丛书" || len(c.Tags) != 2 || c.Cover != "https://neodb.social/m/item/a.jpg" || c.URL != "https://neodb.social/book/7OGq" {
		t.Fatalf("candidate %+v", c)
	}
	if c.Identifiers["isbn"] != "9787536692930" || c.Identifiers["douban"] != "2567698" || c.Identifiers["goodreads"] != "9574008" || c.Identifiers["neodb"] != "7OGq" {
		t.Errorf("identifiers %v", c.Identifiers)
	}
	if c.Description != "文化大革命如火如荼进行的同时。\n四光年外……" {
		t.Errorf("description %q", c.Description)
	}
	if got[1].Cover != "" {
		t.Errorf("missing cover kept: %q", got[1].Cover)
	}

	// By ISBN: one search, the edition only.
	queries = nil
	got, err = p.Identify(ctx, plugin.Book{Title: "whatever", Identifiers: map[string]string{"isbn": "978-7-5366-9293-0"}})
	if err != nil || len(got) != 1 || got[0].ID != "7OGq" || len(queries) != 1 {
		t.Fatalf("by isbn: %v %+v %q", err, got, queries)
	}

	// By NeoDB id, and falling back when it is gone.
	got, err = p.Identify(ctx, plugin.Book{Identifiers: map[string]string{"neodb": "7OGq000000"}})
	if err != nil || len(got) != 0 {
		t.Errorf("unknown id: %v %+v", err, got)
	}
	queries = nil
	got, err = p.Identify(ctx, plugin.Book{Title: "nothing", Identifiers: map[string]string{"neodb": "gone000000"}})
	if err != nil || len(got) != 0 || len(queries) != 1 {
		t.Errorf("gone id: %v %+v %q", err, got, queries)
	}

	var pe *plugin.Error
	if _, err := p.Identify(ctx, plugin.Book{Title: "busy"}); !errors.As(err, &pe) || pe.Kind != plugin.KindRateLimited {
		t.Errorf("rate limited: %v", err)
	}
}

const googleDune = `{"id":"B1hSG45JCX4C","volumeInfo":{"title":"Dune","authors":["Frank Herbert"],"publisher":"Penguin",
	"publishedDate":"2005-08-02","description":"<p>Set on the desert planet Arrakis…</p>",
	"industryIdentifiers":[{"type":"ISBN_10","identifier":"0441172717"},{"type":"ISBN_13","identifier":"9780441172719"}],
	"pageCount":896,"categories":["Fiction / Science Fiction / General"],"averageRating":4.5,"ratingsCount":120,
	"imageLinks":{"thumbnail":"http://books.google.com/books/content?id=B1hSG45JCX4C&zoom=1"},"language":"en"}}`

const googleDunePT = `{"id":"DunePT0001","volumeInfo":{"title":"Dune (Edição em Português)","authors":["Frank Herbert"],"publisher":"Aleph","publishedDate":"2017","language":"pt"}}`

const googleMessiah = `{"id":"Messiah0001","volumeInfo":{"title":"Dune Messiah","authors":["Frank Herbert"],"publishedDate":"1969","language":"en"}}`

func TestGoogleBooks(t *testing.T) {
	var queries, keys, langRestricts []string
	mux := http.NewServeMux()
	mux.HandleFunc("www.googleapis.com/books/v1/volumes", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		lr := r.URL.Query().Get("langRestrict")
		queries = append(queries, q)
		langRestricts = append(langRestricts, lr)
		keys = append(keys, r.Header.Get("X-Goog-Api-Key"))
		switch q {
		case "intitle:Dune inauthor:Frank inauthor:Herbert":
			if lr == "pt" {
				w.Write([]byte(`{"totalItems":1,"items":[` + googleDunePT + `]}`))
				return
			}
			w.Write([]byte(`{"totalItems":2,"items":[` + googleMessiah + `,` + googleDune + `]}`))
		case "isbn:9780441172719":
			w.Write([]byte(`{"totalItems":1,"items":[` + googleDune + `]}`))
		case "intitle:quota":
			w.WriteHeader(429)
			w.Write([]byte(`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","errors":[{"reason":"rateLimitExceeded"}]}}`))
		case "intitle:badkey":
			w.WriteHeader(400)
			w.Write([]byte(`{"error":{"code":400,"status":"INVALID_ARGUMENT","errors":[{"reason":"badRequest"}],"details":[{"reason":"API_KEY_INVALID"}]}}`))
		default:
			w.Write([]byte(`{"totalItems":0}`))
		}
	})
	mux.HandleFunc("www.googleapis.com/books/v1/volumes/B1hSG45JCX4C", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(googleDune)) })
	mux.HandleFunc("www.googleapis.com/books/v1/volumes/gone000000", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(404) })
	load := func(key string) *plugin.Plugin {
		p, err := plugin.LoadBuiltin(builtinFS(t, "googlebooks"), plugin.Options{Transport: serve{mux}, Settings: map[string]string{"apiKey": key}})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	p := load("k123")
	if p.Manifest.ID != "dalibre.googlebooks" || !p.Manifest.IsMetadataSource() || len(p.MissingSettings()) != 0 {
		t.Fatalf("manifest %+v", p.Manifest)
	}
	ctx := context.Background()

	// By title and author: the exact title first.
	got, err := p.Identify(ctx, plugin.Book{Title: "Dune", Authors: []string{"Frank Herbert"}, Language: "eng"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "B1hSG45JCX4C" || got[1].ID != "Messiah0001" {
		t.Fatalf("candidates %+v", got)
	}
	if len(queries) != 1 || keys[0] != "k123" {
		t.Errorf("queries %q, keys %q", queries, keys)
	}
	c := got[0]
	if c.Title != "Dune" || c.Authors[0] != "Frank Herbert" || c.Publisher != "Penguin" || c.Pubdate != "2005-08-02" || c.Language != "en" ||
		strings.Join(c.Tags, "|") != "Fiction|Science Fiction" || c.Description != "<p>Set on the desert planet Arrakis…</p>" ||
		c.Cover != "https://books.google.com/books/content?id=B1hSG45JCX4C&printsec=frontcover&img=1" ||
		c.URL != "https://books.google.com/books?id=B1hSG45JCX4C" {
		t.Fatalf("candidate %+v", c)
	}
	if c.Identifiers["isbn"] != "9780441172719" || c.Identifiers["google"] != "B1hSG45JCX4C" {
		t.Errorf("identifiers %v", c.Identifiers)
	}
	if got[1].Cover != "" || got[1].Pubdate != "1969" {
		t.Errorf("second %+v", got[1])
	}

	// By ISBN, and by Google id, falling back when it is gone.
	queries = nil
	got, err = p.Identify(ctx, plugin.Book{Title: "whatever", Identifiers: map[string]string{"isbn": "978-0-441-17271-9"}})
	if err != nil || len(got) != 1 || len(queries) != 1 {
		t.Fatalf("by isbn: %v %+v %q", err, got, queries)
	}
	queries = nil
	got, err = p.Identify(ctx, plugin.Book{Identifiers: map[string]string{"google": "B1hSG45JCX4C"}})
	if err != nil || len(got) != 1 || got[0].ID != "B1hSG45JCX4C" || len(queries) != 0 {
		t.Fatalf("by id: %v %+v %q", err, got, queries)
	}
	got, err = p.Identify(ctx, plugin.Book{Title: "nothing", Identifiers: map[string]string{"google": "gone000000"}})
	if err != nil || len(got) != 0 {
		t.Errorf("gone id: %v %+v", err, got)
	}

	// Errors: a refused key, and the quota with a key and without one.
	var pe *plugin.Error
	if _, err := p.Identify(ctx, plugin.Book{Title: "badkey"}); !errors.As(err, &pe) || pe.Kind != plugin.KindAuth {
		t.Errorf("bad key: %v", err)
	}
	if _, err := p.Identify(ctx, plugin.Book{Title: "quota"}); !errors.As(err, &pe) || pe.Kind != plugin.KindRateLimited {
		t.Errorf("quota with key: %v", err)
	}
	keys = nil
	if _, err := load("").Identify(ctx, plugin.Book{Title: "quota"}); !errors.As(err, &pe) || pe.Kind != plugin.KindAuth || keys[0] != "" {
		t.Errorf("quota without key: %v %q", err, keys)
	}

	// Non-English library language searches with langRestrict first and deduplicates fallback results.
	queries, langRestricts = nil, nil
	got, err = p.Identify(ctx, plugin.Book{Title: "Dune", Authors: []string{"Frank Herbert"}, Language: "por"})
	if err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 || langRestricts[0] != "pt" || langRestricts[1] != "" {
		t.Fatalf("localized search: queries=%q langRestricts=%q", queries, langRestricts)
	}
	if len(got) != 3 || got[0].ID != "B1hSG45JCX4C" || got[1].ID != "DunePT0001" {
		t.Fatalf("localized candidates: %+v", got)
	}
}
