package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/plugin"
	"dalibre/internal/settings"
	builtin "dalibre/plugins"
)

// fakeWeb answers the plugin's requests without a network.
type fakeWeb struct{ h http.Handler }

func (f fakeWeb) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Host == "" {
		r.Host = r.URL.Host
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

const opdsSearch = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
<link rel="next" href="/ebooks/search.opds/?start_index=26"/>
<entry><id>https://www.gutenberg.org/ebooks/84.opds</id><title>Frankenstein; Or, The Modern Prometheus</title>
<content type="text">Mary Wollstonecraft Shelley</content></entry>
<entry><id>https://www.gutenberg.org/ebooks/42324.opds</id><title>Frankenstein (1831 text)</title>
<content type="text">Mary Wollstonecraft Shelley</content></entry>
</feed>`

const opdsBook = `<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom" xmlns:dcterms="http://purl.org/dc/terms/">
<entry><title>Frankenstein; or, The modern Prometheus</title>
<content type="xhtml"><div><p>Summary: Victor Frankenstein builds a living being and recoils from it.</p><p>Downloads: 204693</p></div></content>
<published>1993-10-01T00:00:00+00:00</published><rights>Public domain in the USA.</rights>
<author><name>Shelley, Mary Wollstonecraft</name></author>
<category scheme="http://purl.org/dc/terms/LCSH" term="Horror tales"/>
<dcterms:language>en</dcterms:language>
<link type="application/epub+zip" rel="http://opds-spec.org/acquisition" length="440131" href="https://www.gutenberg.org/ebooks/84.epub3.images"/>
<link type="application/x-mobipocket-ebook" rel="http://opds-spec.org/acquisition" length="547590" href="https://www.gutenberg.org/ebooks/84.kf8.images"/>
</entry></feed>`

// gutenbergApp is a test app with the built-in Project Gutenberg plugin,
// answered by a fake catalog, and a source that needs an API key.
func gutenbergApp(t *testing.T) *App {
	a := newTestApp(t)
	mux := http.NewServeMux()
	mux.HandleFunc("www.gutenberg.org/ebooks/search.opds/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(opdsSearch)) })
	mux.HandleFunc("www.gutenberg.org/ebooks/84.opds", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(opdsBook)) })
	mux.HandleFunc("www.gutenberg.org/ebooks/84.epub3.images", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/epub+zip")
		w.Write([]byte("not really an epub"))
	})
	var ps []*plugin.Plugin
	for _, fsys := range builtin.Builtin() {
		p, err := plugin.LoadBuiltin(fsys, plugin.Options{Transport: fakeWeb{mux}})
		if err != nil {
			t.Fatal(err)
		}
		ps = append(ps, p)
	}
	keyed := fstest.MapFS{
		"plugin.json": {Data: []byte(`{"id":"com.example.books","name":"Example Books","version":"1.0.0","api":1,"icon":"bookmark",
			"description":"Books from example.com","capabilities":["source"],"permissions":{"hosts":["api.example.com"]},
			"settings":[{"key":"apiKey","title":"API key","type":"secret","required":true}]}`)},
		"main.js": {Data: []byte(`export const search = () => ({items: []})`)},
	}
	p, err := plugin.LoadBuiltin(keyed, plugin.Options{})
	if err != nil {
		t.Fatal(err)
	}
	ps = append(ps, p)
	a.pluginsKnown = []knownPlugin{{manifest: ps[0].Manifest, builtin: true}, {manifest: p.Manifest}}
	a.setPlugins(ps)
	return a
}

func TestDownloadBookFromPlugin(t *testing.T) {
	a := gutenbergApp(t)
	p := a.pluginByID("dalibre.gutenberg")
	if p == nil || len(a.sources()) != 2 {
		t.Fatal("no sources")
	}
	page, err := p.Search(t.Context(), plugin.Query{Text: "frankenstein"})
	if err != nil {
		t.Fatal(err)
	}
	full, err := p.Get(t.Context(), page.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	before := len(a.books)
	a.downloadBook(p, full, "EPUB")
	if d := a.downloads[0]; !d.finished || d.err != nil || d.bookID == 0 {
		t.Fatalf("download: %+v", d)
	}
	if len(a.books) != before+1 {
		t.Fatalf("books %d, want %d", len(a.books), before+1)
	}
	b := a.byID[a.curID]
	if b == nil || b.Title != "Frankenstein; or, The modern Prometheus" || b.Authors[0] != "Mary Wollstonecraft Shelley" || b.Tags[0] != "Horror tales" {
		t.Fatalf("added %+v", b)
	}
	if d, err := a.lib.Details(b); err != nil || d.Identifiers["gutenberg"] != "84" {
		t.Fatalf("details %+v, %v", d, err)
	}
	if _, err := os.Stat(filepath.Join(a.lib.Root, b.Path)); err != nil {
		t.Fatal(err)
	}
	if a.inLibrary(page.Items[0]) != b.ID || a.inLibrary(page.Items[1]) != 0 {
		t.Error("in library isn’t matched by identifier")
	}
}

func TestGetBooksPage(t *testing.T) {
	a := gutenbergApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click("Get books"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageGetBooks || a.gb.source != "dalibre.gutenberg" {
		t.Fatalf("page %d, source %q", a.page, a.gb.source)
	}
	// Browsing: the source's collections as shelves.
	if !tt.HasText("Most popular") || !tt.HasText("Recently added") || !tt.HasText("Frankenstein (1831 text)") {
		t.Fatalf("browse page: %q", tt.Texts())
	}
	shot(t, tt, "getbooks-browse")

	a.gb.text = "frankenstein"
	a.gbSearch()
	tt.Frame()
	if !tt.HasText("Frankenstein; Or, The Modern Prometheus") || !tt.HasText("2+ results") {
		t.Fatalf("results: %q", tt.Texts())
	}
	if err := tt.Click("Frankenstein; Or, The Modern Prometheus"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"DOWNLOAD AS", "Most e-readers", "430 KB", "204,693", "Public domain in the USA", "Horror tales", "Add to library"} {
		if !tt.HasText(s) {
			t.Errorf("detail panel misses %q: %q", s, tt.Texts())
		}
	}
	shot(t, tt, "getbooks-search")

	if err := tt.Click("Add to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("In library") || len(a.books) != len(samples)+1 {
		t.Fatalf("after adding: %q", tt.Texts())
	}
	shot(t, tt, "getbooks-added")

	// All sources: each source answers on its own.
	a.gbSetSource(allSources)
	tt.Frame()
	if !tt.HasText("Project Gutenberg") || !tt.HasText("Example Books") || !tt.HasText("Needs API key") || !tt.HasText("1 of 2 sources answered") {
		t.Fatalf("all sources: %q", tt.Texts())
	}
	shot(t, tt, "getbooks-all")

	// A source missing a required setting says so, and offers its settings.
	a.gbSetSource("com.example.books")
	tt.Frame()
	if !tt.HasText("Example Books needs setting up") {
		t.Fatalf("setup state: %q", tt.Texts())
	}
	if err := tt.Click("Open plugin settings"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !a.pluginsOpen || a.pl.sel != "com.example.books" || !tt.HasText("API key") || !tt.HasText("api.example.com") {
		t.Fatalf("plugins dialog: %q", tt.Texts())
	}
	shot(t, tt, "plugins")

	// Back in the library, the book is there.
	a.pluginsOpen = false
	tt.Frame()
	if err := tt.Click("All books"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageLibrary || !tt.HasText("Frankenstein; or, The modern Prometheus") {
		t.Fatalf("library: %q", tt.Texts())
	}
}

// A plugin loads only once the user grants its permissions, and an
// update asking for more waits for the user again.
func TestPluginConsent(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	pack := func(version, hosts string) string {
		src := filepath.Join(t.TempDir(), "src")
		os.MkdirAll(src, 0o755)
		os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.books","name":"Example","version":"`+version+
			`","api":1,"capabilities":["source"],"permissions":{"hosts":[`+hosts+`]}}`), 0o644)
		os.WriteFile(filepath.Join(src, "main.js"), []byte(`exports.search = () => ({items: []})`), 0o644)
		pkg, err := plugin.Pack(src, t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		return pkg
	}

	a.installPlugin(pack("1.0.0", `"a.example"`))
	if a.consent == nil || len(a.plugins) != 0 {
		t.Fatal("installed without asking")
	}
	a.grantConsent()
	if len(a.plugins) != 1 || a.plugins[0].Manifest.Version != "1.0.0" {
		t.Fatalf("plugins %d after granting", len(a.plugins))
	}

	// An update within what was granted installs at once.
	a.installPlugin(pack("1.1.0", `"a.example"`))
	if a.consent != nil || a.plugins[0].Manifest.Version != "1.1.0" {
		t.Fatal("an update needing nothing new asked")
	}

	// One asking for a new host is asked about; installed by hand, it
	// waits unloaded.
	if _, err := plugin.Install(pack("2.0.0", `"a.example","b.example"`), a.pluginDir); err != nil {
		t.Fatal(err)
	}
	a.loadPlugins()
	if len(a.plugins) != 0 || len(a.pluginsPending) != 1 {
		t.Fatalf("plugins %d, pending %d", len(a.plugins), len(a.pluginsPending))
	}
	a.askConsent("com.example.books")
	a.grantConsent()
	if len(a.plugins) != 1 || len(a.pluginsPending) != 0 {
		t.Fatal("not loaded after granting")
	}

	a.setPluginEnabled("com.example.books", false)
	if len(a.plugins) != 0 {
		t.Fatal("a disabled plugin loaded")
	}
	a.uninstallPlugin("com.example.books")
	if _, ok := a.settings.Plugins["com.example.books"]; ok {
		t.Fatal("uninstalling kept the plugin's state")
	}
}

// A plugin added from a folder loads from it, as it is, after the user
// grants it; removing it leaves the folder alone.
func TestLinkedPlugin(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	src := filepath.Join(t.TempDir(), "my-source")
	os.MkdirAll(src, 0o755)
	manifest := func(version string) {
		os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.dev","name":"Dev","version":"`+version+
			`","api":1,"capabilities":["source"],"permissions":{"hosts":["a.example"]}}`), 0o644)
	}
	manifest("0.1.0")
	os.WriteFile(filepath.Join(src, "main.js"), []byte(`exports.search = () => ({items: []})`), 0o644)

	a.installPlugin(src)
	if a.consent == nil {
		t.Fatal("linked without asking")
	}
	a.grantConsent()
	if len(a.plugins) != 1 || a.settings.Plugins["com.example.dev"].Path != src {
		t.Fatalf("plugins %d, state %+v", len(a.plugins), a.settings.Plugins["com.example.dev"])
	}
	if len(a.pluginsKnown) != 1 || a.pluginsKnown[0].linked != src {
		t.Fatalf("known %+v", a.pluginsKnown)
	}
	// Only its data folder (.data) is there, as for every plugin.
	if entries, _ := os.ReadDir(a.pluginDir); len(entries) != 1 || entries[0].Name() != ".data" {
		t.Fatalf("copied into the plugins folder: %v", entries)
	}

	// An edit shows on the next load, with no install.
	manifest("0.2.0")
	a.loadPlugins()
	if a.plugins[0].Manifest.Version != "0.2.0" {
		t.Fatalf("version %s after editing", a.plugins[0].Manifest.Version)
	}

	a.uninstallPlugin("com.example.dev")
	if len(a.plugins) != 0 || a.settings.Plugins["com.example.dev"] != nil {
		t.Fatal("still loaded after removing")
	}
	if _, err := os.Stat(filepath.Join(src, "main.js")); err != nil {
		t.Fatal("removing deleted the folder")
	}
}

// A plugin's secret settings live in the credential store, never in
// settings.json; one saved there before is moved.
func TestPluginSecrets(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	store := memorySecrets{}
	a.secrets = store
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.secret","name":"Secret","version":"1.0.0","api":1,
		"capabilities":["source"],"permissions":{"hosts":["a.example"]},
		"settings":[{"key":"email","title":"Email","required":true},
			{"key":"password","title":"Password","type":"secret","required":true}]}`), 0o644)
	os.WriteFile(filepath.Join(src, "main.js"), []byte(`exports.search = () => ({items: []})`), 0o644)
	const id = "com.example.secret"
	key := plugin.SecretKey(id, "password")

	// Linked, granted, with a password saved in settings.json as before.
	st := a.settings.Plugin(id)
	st.Path = src
	st.Granted = &settings.Grant{Hosts: []string{"a.example"}}
	st.Values = map[string]string{"email": "me@example.com", "password": "old"}
	a.loadPlugins()
	if len(a.plugins) != 1 || a.plugins[0].MissingSettings() != nil {
		t.Fatalf("plugins %d, missing %v", len(a.plugins), a.plugins[0].MissingSettings())
	}
	if _, ok := a.settings.Plugins[id].Values["password"]; ok || store[key] != "old" {
		t.Fatalf("not moved: values %v, store %v", a.settings.Plugins[id].Values, store)
	}
	if data, _ := json.Marshal(a.settings); strings.Contains(string(data), `"old"`) {
		t.Fatal("the password is still in the settings")
	}

	// Saving puts the password in the store; the email stays in settings.
	a.setPluginValues(id, map[string]string{"email": "me@example.com", "password": "new"})
	if store[key] != "new" || a.settings.Plugins[id].Values["email"] != "me@example.com" || a.settings.Plugins[id].Values["password"] != "" {
		t.Fatalf("saved: values %v, store %v", a.settings.Plugins[id].Values, store)
	}
	if a.pluginSecrets[id]["password"] != "new" || len(a.plugins) != 1 || a.plugins[0].MissingSettings() != nil {
		t.Fatal("not loaded with the new password")
	}

	// Clearing it removes it; removing the plugin forgets it.
	a.setPluginValues(id, map[string]string{"email": "me@example.com"})
	if _, ok := store[key]; ok || a.plugins[0].MissingSettings() == nil {
		t.Fatalf("cleared: store %v", store)
	}
	a.setPluginValues(id, map[string]string{"email": "me@example.com", "password": "again"})
	a.uninstallPlugin(id)
	if _, ok := store[key]; ok {
		t.Fatal("removing kept the password")
	}
}

func TestFolderWatch(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "main.js"), []byte("a"), 0o644)
	w := newFolderWatch([]string{dir})
	if ch, _ := w.changed(); ch {
		t.Fatal("changed with nothing changed")
	}
	os.WriteFile(filepath.Join(dir, ".hidden"), []byte("x"), 0o644)
	if ch, _ := w.changed(); ch {
		t.Fatal("a hidden file counted")
	}
	os.WriteFile(filepath.Join(dir, "main.js"), []byte("abc"), 0o644)
	ch, settled := w.changed()
	if !ch {
		t.Fatal("an edit not seen")
	}
	os.WriteFile(filepath.Join(dir, "lib.js"), []byte("x"), 0o644)
	if settled() {
		t.Fatal("settled while still changing")
	}
	if !settled() {
		t.Fatal("not settled once quiet")
	}
	if ch, _ := w.changed(); ch {
		t.Fatal("the same change seen twice")
	}
}

// The built-in plugins load with what they ask for, and can be turned off
// but stay listed.
func TestBuiltinPlugins(t *testing.T) {
	a := newTestApp(t)
	a.builtins = true
	a.pluginTransport = fakeWeb{http.NotFoundHandler()}
	a.loadPlugins()
	if a.pluginByID("dalibre.gutenberg") == nil || a.pluginByID("dalibre.neodb") == nil || a.pluginByID("dalibre.googlebooks") == nil || len(a.pluginsKnown) != 3 || !a.pluginsKnown[0].builtin {
		t.Fatalf("plugins %v, known %v, errors %v", a.plugins, a.pluginsKnown, a.pluginErrs)
	}
	a.openGetBooks()
	if a.gb.source != "dalibre.gutenberg" {
		t.Fatalf("source %q", a.gb.source)
	}
	a.setPluginEnabled("dalibre.gutenberg", false)
	if len(a.plugins) != 2 || len(a.pluginsKnown) != 3 {
		t.Fatalf("plugins %d, known %d after turning off", len(a.plugins), len(a.pluginsKnown))
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if !tt.HasText("No book sources are on") {
		t.Fatalf("page: %q", tt.Texts())
	}
	a.setPluginEnabled("dalibre.gutenberg", true)
	if a.pluginByID("dalibre.gutenberg") == nil {
		t.Fatal("not back on")
	}
}

func TestSourcePickerWithNoSources(t *testing.T) {
	a := newTestApp(t)
	a.setPlugins(nil)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click("Get books"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Source: All sources"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Get more sources…") {
		t.Fatalf("menu: %q", tt.Texts())
	}
	shot(t, tt, "source-picker-empty")
}
