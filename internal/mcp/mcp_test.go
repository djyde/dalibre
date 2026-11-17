package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

// testHost is a library with the window left out.
type testHost struct {
	lib     *calibre.Library
	mu      sync.Mutex
	changed int
	sources []*plugin.Plugin
	added   map[string]*plugin.Plugin // by id, as add_plugin loaded them
	off     map[string]bool
}

// The host's plugins: add_plugin loads a folder at once, with no user to
// ask.
func (h *testHost) Plugins(context.Context) PluginList {
	var out PluginList
	for _, id := range slices.Sorted(maps.Keys(h.added)) {
		p := h.added[id]
		info := PluginInfo{ID: id, Name: p.Manifest.Name, Version: p.Manifest.Version, Capabilities: p.Manifest.Capabilities.Names(), State: PluginLoaded, Folder: p.Dir}
		if h.off[id] {
			info.State = PluginDisabled
		}
		out.Plugins = append(out.Plugins, info)
	}
	return out
}

func (h *testHost) AddPlugin(ctx context.Context, folder string) (PluginInfo, error) {
	p, err := plugin.Load(folder, plugin.Options{DataDir: filepath.Join(folder, ".data"), Logf: func(string, ...any) {}})
	if err != nil {
		return PluginInfo{}, err
	}
	if h.added == nil {
		h.added, h.off = map[string]*plugin.Plugin{}, map[string]bool{}
	}
	if old := h.added[p.Manifest.ID]; old != nil {
		old.Close()
	}
	h.added[p.Manifest.ID] = p
	return h.Plugins(ctx).Plugins[slices.Index(slices.Sorted(maps.Keys(h.added)), p.Manifest.ID)], nil
}

func (h *testHost) ReloadPlugins(ctx context.Context) {
	for _, p := range h.added {
		h.AddPlugin(ctx, p.Dir)
	}
}

func (h *testHost) SetPluginEnabled(_ context.Context, id string, on bool) error {
	if h.added[id] == nil {
		return errors.New("no plugin " + id)
	}
	h.off[id] = !on
	return nil
}

func (h *testHost) RemovePlugin(_ context.Context, id string) error {
	if h.added[id] == nil {
		return errors.New("no plugin " + id)
	}
	h.added[id].Close()
	delete(h.added, id)
	return nil
}

func (h *testHost) Plugin(_ context.Context, id string) *plugin.Plugin {
	if h.off[id] {
		return nil
	}
	return h.added[id]
}

func (h *testHost) Library() *calibre.Library { return h.lib }
func (h *testHost) Changed(...int64) {
	h.mu.Lock()
	h.changed++
	h.mu.Unlock()
}
func (h *testHost) Trash() func(string) error { return nil }

func (h *testHost) BookSources(context.Context) []*plugin.Plugin     { return h.sources }
func (h *testHost) MetadataSources(context.Context) []*plugin.Plugin { return nil }
func (h *testHost) Download(ctx context.Context, p *plugin.Plugin, r plugin.Result, format string) (int64, error) {
	dir := os.TempDir()
	file, err := p.Download(ctx, r, format, dir, nil)
	if err != nil {
		return 0, err
	}
	defer os.Remove(file)
	return h.lib.ImportWith(file, calibre.Metadata{Title: r.Title, Authors: r.Authors}, nil)
}

func testLibrary(t *testing.T) *calibre.Library {
	t.Helper()
	lib, err := calibre.Create(filepath.Join(t.TempDir(), "Library"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	for _, b := range []struct {
		title, author, series string
		tags                  []string
	}{
		{"The Left Hand of Darkness", "Ursula K. Le Guin", "Hainish Cycle", []string{"Science fiction"}},
		{"A Wizard of Earthsea", "Ursula K. Le Guin", "Earthsea", []string{"Fantasy"}},
		{"Emma", "Jane Austen", "", nil},
	} {
		f := filepath.Join(t.TempDir(), "in.epub")
		os.WriteFile(f, []byte("epub of "+b.title), 0o644)
		if _, err := lib.AddBook(calibre.NewBook{File: f, Metadata: calibre.Metadata{Title: b.title, Authors: []string{b.author},
			Series: b.series, Tags: b.tags, Rating: 8, Languages: []string{"eng"}, Comments: "<p>About <b>" + b.title + "</b></p>"}}); err != nil {
			t.Fatal(err)
		}
	}
	return lib
}

// connect runs s and returns a client of it.
func connect(t *testing.T, s *Server) *sdk.ClientSession {
	t.Helper()
	st, ct := sdk.NewInMemoryTransports()
	ctx := context.Background()
	if _, err := s.MCP().Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := sdk.NewClient(&sdk.Implementation{Name: "test-agent"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	return cs
}

// call calls a tool and decodes its structured result into out; it
// returns the error text when the tool failed.
func call(t *testing.T, cs *sdk.ClientSession, name string, args any, out any) string {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if res.IsError {
		return res.Content[0].(*sdk.TextContent).Text
	}
	text := res.Content[0].(*sdk.TextContent).Text
	data, _ := json.Marshal(res.StructuredContent)
	if string(data) != text {
		t.Errorf("%s: text %s isn't the structured content %s", name, text, data)
	}
	if out != nil {
		if err := json.Unmarshal(data, out); err != nil {
			t.Fatalf("%s: %v in %s", name, err, data)
		}
	}
	return ""
}

func toolNames(t *testing.T, cs *sdk.ClientSession) []string {
	var names []string
	for tool, err := range cs.Tools(context.Background(), nil) {
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, tool.Name)
	}
	slices.Sort(names)
	return names
}

func TestReadTools(t *testing.T) {
	lib := testLibrary(t)
	s := New(&testHost{lib: lib}, "test", false)
	cs := connect(t, s)

	if got := strings.Join(toolNames(t, cs), " "); got != "get_book list_facets search_books" {
		t.Fatalf("read-only tools: %s", got)
	}
	for tool, _ := range cs.Tools(context.Background(), nil) {
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s isn't read-only", tool.Name)
		}
	}

	var sr searchOut
	call(t, cs, "search_books", map[string]any{"author": "Ursula K. Le Guin", "sort": "title", "desc": false}, &sr)
	if sr.Total != 2 || sr.Books[0].Title != "The Left Hand of Darkness" || sr.Books[1].Title != "A Wizard of Earthsea" {
		t.Fatalf("by author: %+v", sr)
	}
	call(t, cs, "search_books", map[string]any{"query": "earthsea", "language": "en"}, &sr)
	if sr.Total != 1 || sr.Books[0].Series != "Earthsea" || sr.Books[0].Rating != 4 || sr.Books[0].Formats[0] != "EPUB" {
		t.Fatalf("search: %+v", sr)
	}
	call(t, cs, "search_books", map[string]any{"limit": 1, "offset": 1}, &sr)
	if sr.Total != 3 || len(sr.Books) != 1 {
		t.Fatalf("page: %+v", sr)
	}
	if msg := call(t, cs, "search_books", map[string]any{"sort": "colour"}, nil); !strings.Contains(msg, "unknown sort") {
		t.Errorf("bad sort: %q", msg)
	}

	var b Book
	call(t, cs, "get_book", map[string]any{"id": sr.Books[0].ID}, &b)
	if b.Comments == "" || strings.Contains(b.Comments, "<") || len(b.Files) != 1 || b.Files[0].Size == 0 || !filepath.IsAbs(b.Files[0].Path) {
		t.Fatalf("book: %+v", b)
	}
	if msg := call(t, cs, "get_book", map[string]any{"id": 999}, nil); msg != "no book 999" {
		t.Errorf("missing book: %q", msg)
	}

	var f facetsOut
	call(t, cs, "list_facets", map[string]any{"kind": "authors"}, &f)
	if len(f.Values) != 2 || f.Values[1] != (Facet{"Ursula K. Le Guin", 2}) {
		t.Fatalf("authors: %+v", f)
	}

	// Write tools appear when changes are allowed.
	s.SetAllowChanges(true)
	if n := len(toolNames(t, cs)); n != 21 {
		t.Errorf("with changes: %d tools", n)
	}
	s.SetAllowChanges(false)
	if n := len(toolNames(t, cs)); n != 3 {
		t.Errorf("read-only again: %d tools", n)
	}
}

func TestNoLibrary(t *testing.T) {
	cs := connect(t, New(&testHost{}, "test", true))
	if msg := call(t, cs, "search_books", map[string]any{}, nil); msg != errNoLibrary.Error() {
		t.Errorf("no library: %q", msg)
	}
}

func TestWriteTools(t *testing.T) {
	lib := testLibrary(t)
	h := &testHost{lib: lib}
	s := New(h, "test", true)
	cs := connect(t, s)
	for tool := range cs.Tools(context.Background(), nil) {
		a := tool.Annotations
		destructive := a.DestructiveHint == nil || *a.DestructiveHint
		if want := tool.Name == "remove_books" || tool.Name == "remove_format"; want && (!destructive || a.ReadOnlyHint) {
			t.Errorf("%s isn't destructive", tool.Name)
		}
	}
	var sr searchOut
	call(t, cs, "search_books", map[string]any{"author": "Ursula K. Le Guin"}, &sr)
	ids := []int64{sr.Books[0].ID, sr.Books[1].ID}

	var up updateOut
	call(t, cs, "update_books", map[string]any{"ids": ids, "patch": map[string]any{"add_tags": []string{"Classics"}, "remove_tags": []string{"fantasy"}, "rating": 5}}, &up)
	if len(up.Updated) != 2 || len(up.Failed) != 0 {
		t.Fatalf("update: %+v", up)
	}
	for _, id := range ids {
		b, _ := lib.Book(id)
		if !slices.Contains(b.Tags, "Classics") || slices.Contains(b.Tags, "Fantasy") || b.Rating != 10 || b.Authors[0] != "Ursula K. Le Guin" {
			t.Errorf("book %d: %+v", id, b)
		}
	}
	call(t, cs, "update_books", map[string]any{"ids": []int64{ids[0], 999}, "patch": map[string]any{"title": "Left Hand", "comments": "One.\n\nTwo & three.", "identifiers": map[string]string{"isbn": "9780441478125"}}}, &up)
	if len(up.Updated) != 1 || len(up.Failed) != 1 || up.Failed[0].ID != 999 {
		t.Fatalf("partial update: %+v", up)
	}
	var b Book
	call(t, cs, "get_book", map[string]any{"id": ids[0]}, &b)
	if b.Title != "Left Hand" || b.Comments != "One.\n\nTwo & three." || b.Identifiers["isbn"] != "9780441478125" || b.Series != "Earthsea" {
		t.Fatalf("after update: %+v", b)
	}

	// add_format: a converted file outside the library.
	conv := filepath.Join(t.TempDir(), "converted.mobi")
	os.WriteFile(conv, []byte("mobi"), 0o644)
	var fo formatOut
	if msg := call(t, cs, "add_format", map[string]any{"id": ids[0], "path": conv}, &fo); msg != "" {
		t.Fatal(msg)
	}
	if fo.Format != "MOBI" || !slices.Equal(fo.Formats, []string{"EPUB", "MOBI"}) || !strings.HasPrefix(fo.Path, lib.Root) {
		t.Fatalf("add format: %+v", fo)
	}
	call(t, cs, "remove_format", map[string]any{"id": ids[0], "format": "epub"}, &fo)
	if !slices.Equal(fo.Formats, []string{"MOBI"}) {
		t.Fatalf("remove format: %+v", fo)
	}
	if _, err := os.Stat(b.Files[0].Path); !os.IsNotExist(err) {
		t.Errorf("EPUB still there: %v", err)
	}
	if msg := call(t, cs, "remove_format", map[string]any{"id": ids[0], "format": "PDF"}, nil); !strings.Contains(msg, "has no PDF") {
		t.Errorf("missing format: %q", msg)
	}

	// add_books: one new, one already there.
	dir := t.TempDir()
	newBook := filepath.Join(dir, "Persuasion - Jane Austen.epub")
	os.WriteFile(newBook, []byte("x"), 0o644)
	again := filepath.Join(dir, "Emma - Jane Austen.epub")
	os.WriteFile(again, []byte("y"), 0o644)
	notes := filepath.Join(dir, "notes.md")
	os.WriteFile(notes, []byte("z"), 0o644)
	var ab addBooksOut
	call(t, cs, "add_books", map[string]any{"paths": []string{newBook, again, notes}}, &ab)
	if len(ab.Added) != 1 || ab.Added[0].Title != "Persuasion" || len(ab.Duplicates) != 1 || ab.Duplicates[0].Title != "Emma" ||
		!ab.Duplicates[0].HasFormat || ab.Duplicates[0].SameFile || len(ab.Failed) != 1 || ab.Failed[0].File != notes {
		t.Fatalf("add books: %+v", ab)
	}

	// set_cover from a file.
	cover := filepath.Join(t.TempDir(), "cover.jpg")
	os.WriteFile(cover, []byte{0xff, 0xd8, 0xff, 0xe0, 0, 0}, 0o644)
	var co coverOut
	call(t, cs, "set_cover", map[string]any{"id": ab.Added[0].ID, "path": cover}, &co)
	if _, err := os.Stat(co.Cover); err != nil {
		t.Fatalf("cover: %v", err)
	}

	var ro removeOut
	call(t, cs, "remove_books", map[string]any{"ids": []int64{ab.Added[0].ID}}, &ro)
	if _, err := lib.Book(ab.Added[0].ID); err == nil {
		t.Error("book not removed")
	}
	if entries, _ := os.ReadDir(filepath.Join(lib.Root, calibre.TrashDir)); len(entries) != 1 {
		t.Errorf("trash: %v", entries)
	}
	if h.changed < 6 {
		t.Errorf("window told of %d changes", h.changed)
	}

	// Read only again: the tools are gone, and a call that raced it fails.
	s.SetAllowChanges(false)
	if _, err := s.changeable(); err != errReadOnly {
		t.Errorf("changeable while read-only: %v", err)
	}
}

func TestCheckFile(t *testing.T) {
	lib := testLibrary(t)
	dir := t.TempDir()
	write := func(name string, size int) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, make([]byte, size), 0o644)
		return p
	}
	books, _ := lib.Books()
	d, _ := lib.Details(books[0])
	inside := d.Files[0].Path
	link := filepath.Join(dir, "link.epub")
	os.Symlink(inside, link)
	for _, c := range []struct{ path, want string }{
		{write("ok.epub", 10), ""},
		{"relative.epub", "isn't an absolute path"},
		{filepath.Join(dir, "missing.epub"), "no such file"},
		{dir, "isn't a file"},
		{write("notes.md", 10), "isn't an e-book"},
		{write("empty.epub", 0), "is empty"},
		{inside, "inside the library folder"},
		{link, "inside the library folder"},
	} {
		err := checkFile(lib, c.path)
		switch {
		case c.want == "" && err != nil:
			t.Errorf("%s: %v", c.path, err)
		case c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)):
			t.Errorf("%s: %v, want %q", c.path, err, c.want)
		}
	}
	big := filepath.Join(dir, "big.epub")
	f, _ := os.Create(big)
	f.Truncate(MaxFileSize + 1) // sparse
	f.Close()
	if err := checkFile(lib, big); err == nil || !strings.Contains(err.Error(), "over") {
		t.Errorf("big file: %v", err)
	}
}

// oneBook is a book source of one book, whose file is at url.
type oneBook struct{ url string }

func (o oneBook) Search(ctx context.Context, get plugin.Getter, q plugin.Query) (plugin.Page, error) {
	return plugin.Page{Items: []plugin.Result{{ID: "1", Title: "Middlemarch", Authors: []string{"George Eliot"},
		Formats: []plugin.Format{{Format: "EPUB", URL: o.url}}}}}, nil
}
func (oneBook) Collections(context.Context, plugin.Getter) ([]plugin.Collection, error) {
	return nil, nil
}
func (oneBook) List(context.Context, plugin.Getter, plugin.Query) (plugin.Page, error) {
	return plugin.Page{}, nil
}

func TestSourceTools(t *testing.T) {
	files := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Write([]byte("an epub"))
	}))
	defer files.Close()
	m := &plugin.Manifest{ID: "test.books", Name: "Books", Version: "1.0.0", API: plugin.APIVersion,
		Capabilities: plugin.Capabilities{BookSource: &plugin.BookSource{}},
		Permissions:  plugin.Permissions{Hosts: []string{"*"}, LocalNetwork: true}}
	lib := testLibrary(t)
	h := &testHost{lib: lib, sources: []*plugin.Plugin{plugin.NewNative(m, oneBook{files.URL + "/m.epub"}, plugin.Options{})}}
	cs := connect(t, New(h, "test", true))

	if msg := call(t, cs, "download_book", map[string]any{"source": "test.books", "id": "1"}, nil); !strings.Contains(msg, "find_books first") {
		t.Errorf("before finding: %q", msg)
	}
	var fo findOut
	call(t, cs, "find_books", map[string]any{"query": "middlemarch"}, &fo)
	if len(fo.Results) != 1 || fo.Results[0].Source != "test.books" || fo.Results[0].Formats[0] != "EPUB" {
		t.Fatalf("find: %+v", fo)
	}
	var do downloadOut
	if msg := call(t, cs, "download_book", map[string]any{"source": "test.books", "id": "1"}, &do); msg != "" {
		t.Fatal(msg)
	}
	b, err := lib.Book(do.ID)
	if err != nil || b.Title != "Middlemarch" || do.Format != "EPUB" {
		t.Fatalf("downloaded %+v: %v", b, err)
	}
	if msg := call(t, cs, "find_books", map[string]any{"query": "x", "source": "nope"}, nil); !strings.Contains(msg, `no source "nope"`) {
		t.Errorf("unknown source: %q", msg)
	}
}

func TestHTTP(t *testing.T) {
	lib := testLibrary(t)
	s := New(&testHost{lib: lib}, "test", false)
	s.SetToken("secret")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	init := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}`
	post := func(mod func(*http.Request)) int {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+Path, strings.NewReader(init))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", "Bearer secret")
		mod(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		return resp.StatusCode
	}
	port := srv.URL[strings.LastIndex(srv.URL, ":")+1:]
	for _, c := range []struct {
		name string
		mod  func(*http.Request)
		want int
	}{
		{"ok", func(*http.Request) {}, 200},
		{"localhost", func(r *http.Request) { r.Host = "localhost:" + port }, 200},
		{"same origin", func(r *http.Request) { r.Header.Set("Origin", "http://127.0.0.1:"+port) }, 200},
		{"no token", func(r *http.Request) { r.Header.Del("Authorization") }, 401},
		{"wrong token", func(r *http.Request) { r.Header.Set("Authorization", "Bearer guess") }, 401},
		{"rebound host", func(r *http.Request) { r.Host = "evil.example:" + port }, 403},
		{"other port", func(r *http.Request) { r.Host = "127.0.0.1:1" }, 403},
		{"web page", func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }, 403},
		{"other local origin", func(r *http.Request) { r.Header.Set("Origin", "http://localhost:3000") }, 403},
	} {
		if got := post(c.mod); got != c.want {
			t.Errorf("%s: %d, want %d", c.name, got, c.want)
		}
	}
	// A new token works at once; the old one doesn't.
	s.SetToken("fresh")
	if got := post(func(*http.Request) {}); got != 401 {
		t.Errorf("old token: %d", got)
	}
	if got := post(func(r *http.Request) { r.Header.Set("Authorization", "Bearer fresh") }); got != 200 {
		t.Errorf("new token: %d", got)
	}
}

func TestBridge(t *testing.T) {
	lib := testLibrary(t)
	s := New(&testHost{lib: lib}, "test", true)
	s.SetToken("secret")
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()

	bridge := func(endpoint, token, root string) *sdk.ClientSession {
		inR, inW := io.Pipe()
		outR, outW := io.Pipe()
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			Bridge(ctx, endpoint, token, root, "test", inR, outW)
			close(done)
		}()
		cs, err := sdk.NewClient(&sdk.Implementation{Name: "claude-code"}, nil).Connect(ctx, &sdk.IOTransport{Reader: outR, Writer: inW}, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			cs.Close()
			cancel()
			inW.Close()
			<-done
		})
		return cs
	}

	// The app runs: every tool, and the app sees who called.
	cs := bridge(srv.URL+Path, "secret", "")
	if n := len(toolNames(t, cs)); n != 21 {
		t.Errorf("through the app: %d tools", n)
	}
	var sr searchOut
	call(t, cs, "search_books", map[string]any{"query": "emma"}, &sr)
	if sr.Total != 1 {
		t.Fatalf("search through the app: %+v", sr)
	}
	if name, when := s.LastUse(); name != "claude-code" || when.IsZero() {
		t.Errorf("last use: %q %v", name, when)
	}

	// The app doesn't run: the last library, read only.
	srv.CloseClientConnections()
	srv.Close()
	cs = bridge(srv.URL+Path, "secret", lib.Root)
	if got := strings.Join(toolNames(t, cs), " "); got != "get_book list_facets search_books" {
		t.Errorf("without the app: %s", got)
	}
	call(t, cs, "search_books", map[string]any{"query": "earthsea"}, &sr)
	if sr.Total != 1 {
		t.Fatalf("search without the app: %+v", sr)
	}
}

// TestToolGroups checks that Settings' lists name every tool there is.
func TestToolGroups(t *testing.T) {
	s := New(&testHost{lib: testLibrary(t)}, "test", true)
	var all []string
	for _, g := range []Group{ReadGroup, ChangeGroup, SourceGroup, PluginGroup} {
		all = append(all, Tools(g)...)
	}
	slices.Sort(all)
	if got := toolNames(t, connect(t, s)); !slices.Equal(got, all) {
		t.Errorf("tools %v, groups %v", got, all)
	}
}

// The host's actions: one that asks a form.
func (h *testHost) Actions(context.Context) []ActionInfo {
	return []ActionInfo{{Plugin: "com.example.dups", PluginName: "Find Duplicates", ID: "find", Title: "Find duplicates", Scope: "library"}}
}

func (h *testHost) RunAction(_ context.Context, run ActionRun) (*ActionOutcome, error) {
	switch {
	case run.Plugin != "com.example.dups":
		return nil, errors.New("no loaded plugin")
	case run.Answers["by"] == nil:
		return nil, &plugin.Error{Plugin: run.Plugin, Call: "run", Kind: plugin.KindInvalid, Msg: "asks for answers nobody is there to give; give them ahead: by (select: title | isbn)"}
	}
	return &ActionOutcome{Message: "by " + run.Answers["by"].(string), Applied: run.Apply,
		Groups: []ActionGroup{{Title: "Dune", Books: []ActionBook{{ID: 1, Title: "Dune"}, {ID: 2, Title: "Dune"}}}}}, nil
}
