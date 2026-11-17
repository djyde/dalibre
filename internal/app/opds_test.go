package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/calibre"
	"dalibre/internal/opds"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// remoteCatalog serves a library of its own over OPDS, with a password,
// as a server at home would.
func remoteCatalog(t *testing.T) *httptest.Server {
	t.Helper()
	lib, err := calibre.Create(filepath.Join(t.TempDir(), "Remote"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { lib.Close() })
	for _, b := range []struct{ title, author string }{
		{"The Left Hand of Darkness", "Ursula K. Le Guin"},
		{"A Wizard of Earthsea", "Ursula K. Le Guin"},
		{"Emma", "Jane Austen"},
	} {
		f := filepath.Join(t.TempDir(), "in.epub")
		os.WriteFile(f, []byte("epub of "+b.title), 0o644)
		if _, err := lib.AddBook(calibre.NewBook{File: f, Metadata: calibre.Metadata{Title: b.title, Authors: []string{b.author},
			Identifiers: map[string]string{"isbn": "97800000000" + strconv.Itoa(len(b.title))}}}); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(&opds.Server{Library: func() *calibre.Library { return lib }, Title: "Remote", Username: "randy", Password: "secret"})
	t.Cleanup(srv.Close)
	return srv
}

func TestOPDSCatalog(t *testing.T) {
	a := newTestApp(t)
	a.secrets = memorySecrets{}
	srv := remoteCatalog(t)
	tt := ui.NewTester(a.View, 1440, 900)

	// Settings › OPDS, from ⌘, as from the sidebar.
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	a.openSettings(sectionOPDS)
	tt.Frame()
	if !tt.HasText("Catalogs") || !tt.HasText("Share this library over OPDS") || !tt.HasText("No catalogs yet.") {
		t.Fatalf("OPDS pane: %q", tt.Texts())
	}

	// Adding a catalog: it asks to sign in, then is found.
	a.openCatalogSheet("")
	a.catEdit.url = srv.URL + "/opds"
	a.checkCatalogSheet()
	tt.Frame()
	if !tt.HasText("It asks you to sign in") {
		t.Fatalf("without an account: %q", tt.Texts())
	}
	a.catEdit.user, a.catEdit.password = "randy", "secret"
	a.checkCatalogSheet()
	tt.Frame()
	if !tt.HasText("Found “Remote”") || !tt.HasText("search supported") || !tt.HasText("On your local network") || a.catEdit.name != "Remote" {
		t.Fatalf("with an account: %q", tt.Texts())
	}
	shot(t, tt, "opds-add-catalog")
	if err := tt.Click("Add catalog"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	cats := a.settings.OPDS.Catalogs
	if len(cats) != 1 || cats[0].Username != "randy" || !cats[0].LocalNetwork || a.catEdit.open {
		t.Fatalf("catalogs %+v", cats)
	}
	if pw, _ := a.secrets.Get(catalogSecret(cats[0].ID)); pw != "secret" {
		t.Fatalf("password kept as %q", pw)
	}
	if !tt.HasText("Connected") {
		t.Fatalf("catalog row: %q", tt.Texts())
	}
	shot(t, tt, "opds-settings")
	a.settingsOpen = false
	tt.Frame()

	// The sidebar opens it; its feeds open one inside another.
	if err := tt.Click("Remote"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageCatalog || !tt.HasText("By author") || !tt.HasText("New books") {
		t.Fatalf("catalog root: %q", tt.Texts())
	}
	shot(t, tt, "opds-root")
	if err := tt.Click("By author"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Ursula K. Le Guin"); err != nil {
		t.Fatalf("authors: %v %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("The Left Hand of Darkness") || !tt.HasText("A Wizard of Earthsea") || len(a.cp.path) != 3 {
		t.Fatalf("author's books: %q", tt.Texts())
	}

	// A book downloads into the library, with the catalog's account.
	if err := tt.Click("The Left Hand of Darkness"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	shot(t, tt, "opds-books")
	before := len(a.books)
	if err := tt.Click("Add to library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.books) != before+1 || a.downloads[len(a.downloads)-1].err != nil {
		t.Fatalf("download: %d books, %+v", len(a.books), a.downloads[len(a.downloads)-1])
	}

	// Back up the breadcrumb, and search.
	if err := tt.Click("Back to Remote"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(a.cp.path) != 1 || !tt.HasText("By series") {
		t.Fatalf("back: %q", tt.Texts())
	}
	a.cp.text = "emma"
	a.catalogSearch()
	tt.Frame()
	if !tt.HasText("Search: emma") || !tt.HasText("Jane Austen") {
		t.Fatalf("search: %q", tt.Texts())
	}

	// It is a Get books source too, unless kept out.
	if p := a.pluginByID(catalogPrefix + cats[0].ID); p == nil || !containsSource(a.sources(), p) {
		t.Fatal("not a Get books source")
	}
	a.settings.OPDS.NotInGetBooks = true
	if containsSource(a.sources(), a.pluginByID(catalogPrefix+cats[0].ID)) {
		t.Fatal("kept out, but a source")
	}

	a.removeCatalog(cats[0].ID)
	tt.Frame()
	if len(a.catalogs) != 0 || a.page != pageLibrary {
		t.Fatalf("after removing: page %d, %d catalogs", a.page, len(a.catalogs))
	}
}

func containsSource(ps []*plugin.Plugin, p *plugin.Plugin) bool {
	for _, x := range ps {
		if x == p {
			return true
		}
	}
	return false
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func TestShareLibrary(t *testing.T) {
	a := newTestApp(t)
	a.secrets = memorySecrets{shareSecret: "pw"}
	port := freePort(t)
	a.settings.OPDS.Share = settings.Share{On: true, Port: port, Username: "me", Password: true}
	a.applyShare()
	if a.share == nil || a.share.server == nil || a.share.err != nil {
		t.Fatalf("share %+v", a.share)
	}
	defer func() {
		a.settings.OPDS.Share.On = false
		a.applyShare()
	}()
	get := func(user string) (int, string) {
		req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/opds/all", nil)
		if user != "" {
			req.SetBasicAuth(user, "pw")
		}
		var resp *http.Response
		var err error
		for range 20 { // Serve starts on its own goroutine
			if resp, err = http.DefaultClient.Do(req); err == nil {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	if code, _ := get(""); code != 401 {
		t.Fatalf("without the password: %d", code)
	}
	code, body := get("me")
	if code != 200 || !strings.Contains(body, "Piranesi") {
		t.Fatalf("all books: %d %.200s", code, body)
	}
	if !strings.HasSuffix(a.shareAddress(), ":"+strconv.Itoa(port)+"/opds") {
		t.Errorf("address %q", a.shareAddress())
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.Click("OPDS"); err != nil {
		t.Fatalf("toolbar: %v; %q", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText(a.shareAddress()) {
		t.Fatalf("sharing popover: %q", tt.Texts())
	}
	a.sharingOpen = false
	a.openSettings(sectionOPDS)
	tt.Frame()
	if !tt.HasText(a.shareAddress()) || !tt.HasText("Require a username and password") {
		t.Fatalf("share pane: %q", tt.Texts())
	}
	shot(t, tt, "opds-share")
}

// fakeGitHub answers GitHub's API for one repository, o/r, whose
// releases the test changes.
type fakeGitHub struct {
	srv      *httptest.Server
	releases []map[string]any
	assets   map[string][]byte
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	g := &fakeGitHub{assets: map[string][]byte{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"default_branch": "main"})
	})
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(g.releases) })
	mux.HandleFunc("/repos/o/r/branches", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"name": "dev", "commit": map[string]string{"sha": "bbb"}}, {"name": "main", "commit": map[string]string{"sha": "aaa"}}})
	})
	mux.HandleFunc("/repos/o/r/readme", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("# GitHub Books\n\nSearch and download books from **books.example.com**.\n\n## Settings\n\n- Token: from your account page\n- Language: two-letter codes\n\n```sh\nbrew install example\n```\n"))
	})
	mux.HandleFunc("/asset/", func(w http.ResponseWriter, r *http.Request) {
		w.Write(g.assets[strings.TrimPrefix(r.URL.Path, "/asset/")])
	})
	g.srv = httptest.NewServer(mux)
	t.Cleanup(g.srv.Close)
	return g
}

func (g *fakeGitHub) release(t *testing.T, version string, hosts string) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("plugin.json")
	w.Write([]byte(`{"id":"com.example.gh","name":"GitHub Books","version":"` + version + `","api":1,"author":"o",
		"capabilities":["source"],"permissions":{"hosts":[` + hosts + `]}}`))
	w, _ = zw.Create("main.js")
	w.Write([]byte(`export const search = () => ({items: []})`))
	zw.Close()
	g.assets[version] = buf.Bytes()
	g.releases = append([]map[string]any{{"tag_name": "v" + version, "published_at": time.Now().Add(-72 * time.Hour), "body": "Faster search",
		"assets": []map[string]any{{"name": "gh-" + version + ".dalibre-plugin", "size": buf.Len(), "browser_download_url": g.srv.URL + "/asset/" + version}}}}, g.releases...)
}

func TestInstallFromGitHubAndUpdate(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	gh := newFakeGitHub(t)
	gh.release(t, "1.0.0", `"books.example.com"`)
	a.github = &plugin.GitHub{API: gh.srv.URL}
	tt := ui.NewTester(a.View, 1440, 900)

	a.openPlugins("")
	tt.Frame()
	if err := tt.Click("Add plugin"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	tt.Frame()
	if err := tt.Click("From GitHub…"); err != nil {
		t.Fatalf("%v: %q", err, tt.Texts())
	}
	if !a.ghi.open {
		t.Fatal("the GitHub sheet didn’t open")
	}
	a.ghi.text = "https://github.com/o/r"
	a.ghFind()
	tt.Frame()
	for _, s := range []string{"GitHub Books 1.0.0", "Latest release", "Connect to books.example.com", "Keep it up to date from this repository", "README.md", "Settings"} {
		if !tt.HasText(s) {
			t.Fatalf("sheet misses %q: %q", s, tt.Texts())
		}
	}
	shot(t, tt, "github-install")
	if err := tt.Click("Install"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	st := a.settings.Plugins["com.example.gh"]
	if a.pluginByID("com.example.gh") == nil || st == nil || st.Source == nil || st.Source.Ref != "v1.0.0" || st.Source.Repo != "o/r" || !st.Source.Auto {
		t.Fatalf("installed: %+v %+v", a.pluginByID("com.example.gh"), st)
	}
	if !tt.HasText("github.com/o/r") || !tt.HasText("Tracking releases · on v1.0.0") {
		t.Fatalf("source: %q", tt.Texts())
	}

	// A release asking for nothing new installs by itself.
	gh.release(t, "1.1.0", `"books.example.com"`)
	a.checkUpdates(true)
	tt.Frame()
	if st.Source.Ref != "v1.1.0" || a.pluginByID("com.example.gh").Manifest.Version != "1.1.0" || a.updates["com.example.gh"] != nil {
		t.Fatalf("auto update: ref %s, updates %v", st.Source.Ref, a.updates)
	}

	// One asking for more waits, and asks when the user updates.
	gh.release(t, "2.0.0", `"books.example.com","cdn.example.com"`)
	a.checkUpdates(true)
	a.pl.sel = "com.example.gh"
	tt.Frame()
	if a.updates["com.example.gh"] == nil || !tt.HasText("v2.0.0 is available") || st.Source.Ref != "v1.1.0" {
		t.Fatalf("update waiting: %q", tt.Texts())
	}
	shot(t, tt, "github-update")
	a.updatePlugin("com.example.gh", false)
	tt.Frame()
	if a.consent == nil || !tt.HasText("Connect to cdn.example.com") {
		t.Fatalf("consent: %q", tt.Texts())
	}
	a.grantConsent()
	tt.Frame()
	if st.Source.Ref != "v2.0.0" || a.pluginByID("com.example.gh").Manifest.Version != "2.0.0" {
		t.Fatalf("after consent: ref %s", st.Source.Ref)
	}

	// A branch is offered too.
	a.openGitHubInstall()
	a.ghi.text = "o/r"
	a.ghFind()
	if len(a.ghi.branches) != 2 || a.ghi.branches[0].Name != "main" {
		t.Fatalf("branches %+v", a.ghi.branches)
	}
	a.ghi.open = false
}
