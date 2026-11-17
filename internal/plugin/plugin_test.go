package plugin

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// write makes a plugin folder in a temporary root.
func write(t *testing.T, manifest, script string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "p")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0o644)
	return dir
}

// serve answers every host's requests with h, as if the plugin reached
// the real ones.
type serve struct{ h http.Handler }

func (s serve) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Host == "" {
		r.Host = r.URL.Host
	}
	rec := httptest.NewRecorder()
	s.h.ServeHTTP(rec, r)
	resp := rec.Result()
	resp.Request = r
	return resp, nil
}

const testManifest = `{"id":"test","name":"Test","version":"1.0.0","api":1,"capabilities":["source"],
	"permissions":{"hosts":["books.example","*.cdn.example"]},
	"settings":[{"key":"token","title":"Token","type":"secret","default":"none"}]}`

// kind is the kind of a plugin error, or "" for another error.
func kind(err error) ErrorKind {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	return ""
}

func TestSearchAndDownload(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("books.example/search", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer s3cret" {
			http.Error(w, "no token", 401)
			return
		}
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Dalibre test/1.0.0") {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte(`{"books":[{"key":"b1","name":"A Book","by":["Ann Author"]}],"next":"c2"}`))
	})
	mux.HandleFunc("books.example/link/b1", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://files.cdn.example/b1.epub", http.StatusFound)
	})
	mux.HandleFunc("files.cdn.example/b1.epub", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Disposition", `attachment; filename="a-book.epub"`)
		w.Write([]byte("PK fake epub"))
	})
	dir := write(t, testManifest, `
		exports.search = async function (q) {
			const res = await fetch("https://books.example/search?q=" + encodeURIComponent(q.text),
				{ headers: { Authorization: "Bearer " + dalibre.settings.token } });
			if (res.status === 401) throw dalibre.error("auth", "token refused");
			const data = await res.json();
			const items = data.books.map(b => ({
				id: b.key, title: b.name, authors: b.by, formats: [{ format: "epub" }], extra: { key: b.key },
			}));
			return { items, hasMore: true, cursor: data.next };
		};
		exports.resolve = function (item, format) {
			return { url: "https://books.example/link/" + item.extra.key };
		};`)
	p, err := Load(dir, Options{Settings: map[string]string{"token": "s3cret"}, Transport: serve{mux}})
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.Search(context.Background(), Query{Text: "a book"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Title != "A Book" || page.Items[0].Formats[0].Format != "EPUB" ||
		!page.HasMore || page.Cursor != "c2" || len(page.Warnings) != 0 {
		t.Fatalf("page = %+v", page)
	}
	var last int64
	path, err := p.Download(context.Background(), page.Items[0], "epub", t.TempDir(), func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "a-book.epub" || last != int64(len("PK fake epub")) {
		t.Fatalf("path %s, progress %d", path, last)
	}

	// The wrong token is an auth error, which the app turns into "check
	// the plugin's settings".
	p, _ = Load(dir, Options{Transport: serve{mux}})
	if _, err := p.Search(context.Background(), Query{Text: "x"}); kind(err) != KindAuth {
		t.Fatalf("err = %v (%s)", err, kind(err))
	}
}

func TestResultsAreChecked(t *testing.T) {
	dir := write(t, testManifest, `
		exports.search = () => ({ items: [
			{ id: "1", title: "Good", formats: [{ format: "EPUB", url: "https://books.example/1" }] },
			{ id: "2", title: "Typo", author: "x", formats: [{ format: "PDF", url: "https://books.example/2" }] },
			{ id: "3", title: "", formats: [{ format: "EPUB" }] },
			{ id: "4", title: "Bad format", formats: [{ format: "EPUBB" }] },
			{ id: "1", title: "Same id", formats: [{ format: "EPUB" }] },
			{ id: "6", title: "Bad cover", cover: "/c.jpg", pubdate: "someday", formats: [{ format: "TXT" }] },
			{ id: 7, title: "Number id", formats: [{ format: "TXT" }] },
		]})`)
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := p.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, r := range page.Items {
		ids = append(ids, r.ID)
	}
	if strings.Join(ids, ",") != "1,2,6" {
		t.Errorf("kept %v", ids)
	}
	warnings := strings.Join(page.Warnings, "\n")
	for _, want := range []string{"items[1]: an unknown field \"author\"", "items[2].title: missing", "items[3].formats[0].format",
		"items[4].id", "items[5].cover", "items[5].pubdate", "items[6]: id as a number; it must be a string"} {
		if !strings.Contains(warnings, want) {
			t.Errorf("warnings miss %q:\n%s", want, warnings)
		}
	}

	for script, want := range map[string]string{
		`exports.search = () => [1, 2]`:                "an array",
		`exports.search = () => ({ results: [] })`:     "unknown field \"results\"",
		`exports.search = () => ({ hasMore: true })`:   "no items array",
		`exports.search = () => new Promise(() => {})`: "never settled",
	} {
		p, err := Load(write(t, testManifest, script), Options{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := p.Search(context.Background(), Query{}); kind(err) != KindInvalid || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", script, err, want)
		}
	}
}

func TestHostsAreEnforced(t *testing.T) {
	hit := false
	tr := serve{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		if r.Host == "books.example" {
			http.Redirect(w, r, "https://evil.example/", http.StatusFound)
		}
	})}
	dir := write(t, testManifest, `
		exports.search = async function (q) {
			await fetch(q.text);
			return { items: [] };
		};`)
	p, err := Load(dir, Options{Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"https://evil.example/", "https://cdn.example/", "file:///etc/passwd", "https://user:pw@books.example/", "/relative"} {
		hit = false
		if _, err := p.Search(context.Background(), Query{Text: u}); err == nil || hit {
			t.Errorf("%s: err %v, reached %v", u, err, hit)
		}
	}
	_, err = p.Search(context.Background(), Query{Text: "https://books.example/"})
	if kind(err) != KindPermission || !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("redirect to another host: %v (%s)", err, kind(err))
	}
}

// Without localNetwork a plugin can't reach this computer, even through
// a host it lists.
func TestLocalNetworkIsRefused(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"items":[]}`))
	}))
	defer srv.Close()
	script := `exports.search = async (q) => (await fetch(q.text)).json();`
	for _, local := range []bool{false, true} {
		m := `{"id":"t","name":"T","version":"1.0.0","api":1,"capabilities":["source"],
			"permissions":{"hosts":["127.0.0.1","localhost"],"localNetwork":` + map[bool]string{false: "false", true: "true"}[local] + `}}`
		p, err := Load(write(t, m, script), Options{})
		if err != nil {
			t.Fatal(err)
		}
		for _, u := range []string{srv.URL, strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)} {
			_, err := p.Search(context.Background(), Query{Text: u})
			if local && err != nil {
				t.Errorf("local network granted, %s: %v", u, err)
			}
			if !local && kind(err) != KindPermission {
				t.Errorf("local network not granted, %s: %v (%s)", u, err, kind(err))
			}
		}
	}
}

func TestLimits(t *testing.T) {
	calls := 0
	tr := serve{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++ })}
	dir := write(t, testManifest, `
		exports.search = async function (q) {
			if (q.text === "loop") for (;;) {}
			if (q.text === "many") for (let i = 0; i < 100; i++) await fetch("https://books.example/");
			if (q.text === "deep") { const f = () => f(); f(); }
			return { items: [] };
		};`)
	p, err := Load(dir, Options{Transport: tr})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Search(ctx, Query{Text: "loop"}); kind(err) != KindTimeout {
		t.Errorf("loop: %v", err)
	}
	if _, err := p.Search(context.Background(), Query{Text: "many"}); err == nil || calls != MaxRequests {
		t.Errorf("many: %v after %d requests", err, calls)
	}
	if _, err := p.Search(context.Background(), Query{Text: "deep"}); kind(err) != KindScript {
		t.Errorf("deep: %v", err)
	}
	// The plugin still answers after all that.
	if _, err := p.Search(context.Background(), Query{Text: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestErrors(t *testing.T) {
	dir := write(t, testManifest, `
		exports.search = async function (q) {
			if (q.text === "kind") throw dalibre.error("rateLimited", "slow down");
			if (q.text === "odd") throw dalibre.error("whatever", "hm");
			if (q.text === "string") throw "just a string";
			null.x;
		};`)
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	for text, want := range map[string]ErrorKind{"kind": KindRateLimited, "odd": KindScript, "string": KindScript, "type": KindScript} {
		_, err := p.Search(context.Background(), Query{Text: text})
		if kind(err) != want {
			t.Errorf("%s: %v (%s), want %s", text, err, kind(err), want)
		}
	}
	_, err = p.Search(context.Background(), Query{Text: "type"})
	var pe *Error
	if !errors.As(err, &pe) || !strings.Contains(pe.Stack, "main.js") {
		t.Errorf("no stack for the author: %+v", pe)
	}

	// A required setting left empty stops calls before they're made.
	m := strings.Replace(testManifest, `"default":"none"`, `"required":true`, 1)
	p, err = Load(write(t, m, `exports.search = () => ({items: []})`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Search(context.Background(), Query{}); kind(err) != KindSettings {
		t.Errorf("missing setting: %v", err)
	}
}

func TestHostIsFrozenAndSecretsRedacted(t *testing.T) {
	var logs []string
	dir := write(t, testManifest, `
		exports.search = function () {
			const threw = (f) => { try { f(); return false; } catch { return true; } };
			const frozen = threw(() => { dalibre.error = () => "stolen"; }) && threw(() => { dalibre.settings.token = "x"; }) &&
				threw(() => { globalThis.dalibre = {}; }) && typeof __host_fetch === "undefined" && typeof __dalibre_call === "function" &&
				threw(() => { globalThis.__dalibre_call = null; });
			console.log("token is", dalibre.settings.token, frozen, { n: 1 });
			return { items: [] };
		};`)
	p, err := Load(dir, Options{Settings: map[string]string{"token": "s3cret"}, Logf: func(f string, a ...any) {
		logs = append(logs, fmt.Sprintf(f, a...))
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err := p.Search(context.Background(), Query{}); err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || strings.Contains(logs[0], "s3cret") || !strings.Contains(logs[0], `token is ••• true {"n":1}`) {
		t.Errorf("logs = %q", logs)
	}
}

func TestModules(t *testing.T) {
	cases := map[string]string{
		"esm":     `import { title } from "./lib/util.js"; export async function search() { return { items: [{ id: "1", title, formats: [{ format: "EPUB", url: "https://books.example/1" }] }] }; }`,
		"default": `import { title } from "./lib/util.js"; export default { search: () => ({ items: [{ id: "1", title, formats: [{ format: "EPUB", url: "https://books.example/1" }] }] }) };`,
		"cjs":     `module.exports = { search: async () => ({ items: [{ id: "1", title: "From lib", formats: [{ format: "EPUB", url: "https://books.example/1" }] }] }) };`,
	}
	for name, script := range cases {
		dir := write(t, testManifest, script)
		os.MkdirAll(filepath.Join(dir, "lib"), 0o755)
		os.WriteFile(filepath.Join(dir, "lib", "util.js"), []byte(`import { x } from "../other.js"; export const title = "From " + x;`), 0o644)
		os.WriteFile(filepath.Join(dir, "other.js"), []byte(`export const x = "lib";`), 0o644)
		p, err := Load(dir, Options{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		page, err := p.Search(context.Background(), Query{})
		if err != nil || len(page.Items) != 1 || page.Items[0].Title != "From lib" {
			t.Errorf("%s: %+v, %v", name, page, err)
		}
		p.Close()
	}
	for script, want := range map[string]string{
		`import x from "lodash"; export function search() {}`:           "bundle packages",
		`import x from "../../outside.js"; export function search() {}`: "outside the plugin",
		`import x from "./data.json"; export function search() {}`:      "only .js and .ts files",
		`import x from "./missing.js"; export function search() {}`:     "missing.js",
		`export function search( {`:                                     "SyntaxError",
		`throw new Error("broken at load")`:                             "broken at load",
	} {
		if _, err := Load(write(t, testManifest, script), Options{}); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want %q", script, err, want)
		}
	}
}

// A plugin's module state lasts across calls, until the interpreter has
// to start over: after running out of time.
func TestRecovery(t *testing.T) {
	dir := write(t, testManifest, `
		let calls = 0;
		export function search(q) {
			calls++;
			if (q.text === "loop") for (;;) {}
			return { items: [{ id: String(calls), title: "t", formats: [{ format: "TXT", url: "https://books.example/" }] }] };
		}`)
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	count := func() string {
		page, err := p.Search(context.Background(), Query{})
		if err != nil {
			t.Fatal(err)
		}
		return page.Items[0].ID
	}
	if count() != "1" || count() != "2" {
		t.Fatal("module state isn’t kept between calls")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := p.Search(ctx, Query{Text: "loop"}); kind(err) != KindTimeout {
		t.Fatalf("loop: %v", err)
	}
	if count() != "1" {
		t.Error("the interpreter didn’t start over after running out of time")
	}
	p.Close()
	if _, err := p.Search(context.Background(), Query{}); err == nil {
		t.Error("a closed plugin answered")
	}
}

func TestBadManifests(t *testing.T) {
	for _, m := range []string{
		`{"id":"Bad Id","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{"teleport":{}},"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{},"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"icon":"skull","capabilities":["source"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{"bookSource":{"filters":[{"key":"k","title":"K","type":"select","options":[]}]}},"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{"bookSource":{"filters":[{"key":"k","title":"K","type":"range","options":[{"value":"a","title":"A"}]}]}},"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{"bookSource":{"filters":[{"key":"k","title":"K","type":"select","options":[{"value":"a","title":"A"}],"default":"b"}]}},"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{"bookSource":{"collections":true}},"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1","api":1,"capabilities":["source"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":99,"capabilities":["source"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["teleport"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":[]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["*"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["https://a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.com"]},"main":"../escape.js"}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.com"]},"perms":{}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.com"]},"settings":[{"key":"k","title":"K"},{"key":"k","title":"K"}]}`,
	} {
		if _, err := Load(write(t, m, `exports.search = () => ({items: []})`), Options{}); err == nil {
			t.Errorf("%s loaded", m)
		}
	}
	if _, err := Load(write(t, testManifest, `exports.other = 1`), Options{}); err == nil {
		t.Error("a source without search loaded")
	}
}

func TestPermissions(t *testing.T) {
	granted := Permissions{Hosts: []string{"a.com", "*.b.com"}}
	if !granted.Covers(Permissions{Hosts: []string{"a.com"}}) {
		t.Error("fewer hosts need no new grant")
	}
	if granted.Covers(Permissions{Hosts: []string{"a.com", "c.com"}}) || granted.Covers(Permissions{LocalNetwork: true}) {
		t.Error("more needs a new grant")
	}
	for host, want := range map[string]bool{"a.com": true, "A.COM.": true, "x.a.com": false, "x.b.com": true, "b.com": false, "xb.com": false} {
		if granted.allows(host) != want {
			t.Errorf("allows(%s) = %v", host, !want)
		}
	}
}

func TestDiscover(t *testing.T) {
	root := t.TempDir()
	mk := func(name, manifest string) {
		os.MkdirAll(filepath.Join(root, name), 0o755)
		os.WriteFile(filepath.Join(root, name, "plugin.json"), []byte(manifest), 0o644)
		os.WriteFile(filepath.Join(root, name, "main.js"), []byte(`exports.search = () => ({items: []})`), 0o644)
	}
	mk("one", testManifest)
	mk("dup", testManifest)
	mk("broken", `{`)
	mk(".install-123", testManifest)
	mk("official", strings.Replace(testManifest, `"id":"test"`, `"id":"dalibre.test"`, 1))
	os.MkdirAll(filepath.Join(root, "not-a-plugin"), 0o755)
	plugins, errs := Discover(root, nil)
	if len(plugins) != 1 || len(errs) != 3 {
		t.Fatalf("plugins %d, errors %v", len(plugins), errs)
	}
	plugins, _ = Discover(root, func(m *Manifest) (Options, bool) { return Options{}, false })
	if len(plugins) != 0 {
		t.Fatal("a refused plugin loaded")
	}
}

func TestPackAndInstall(t *testing.T) {
	src := write(t, testManifest, `exports.search = () => ({items: []})`)
	os.WriteFile(filepath.Join(src, ".secret"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(src, "lib"), 0o755)
	os.WriteFile(filepath.Join(src, "lib", "util.js"), []byte("//"), 0o644)
	pkg, err := Pack(src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(pkg) != "test-1.0.0"+PackageExt {
		t.Errorf("package %s", pkg)
	}
	m, err := Inspect(pkg)
	if err != nil || m.ID != "test" {
		t.Fatalf("inspect: %v %v", m, err)
	}
	root := t.TempDir()
	for range 2 { // installing again replaces
		if _, err := Install(pkg, root); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "test", "lib", "util.js")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(root, "test", ".secret")); err == nil {
		t.Error("hidden file packed")
	}
	if plugins, errs := Discover(root, nil); len(plugins) != 1 || len(errs) != 0 {
		t.Fatalf("installed: %d plugins, %v", len(plugins), errs)
	}
	if err := Uninstall(root, "test"); err != nil {
		t.Fatal(err)
	}
	if err := Uninstall(root, "../x"); err == nil {
		t.Error("uninstalled outside the root")
	}

	// A package writing outside its folder is refused.
	evil := filepath.Join(t.TempDir(), "evil.zip")
	f, _ := os.Create(evil)
	zw := zip.NewWriter(f)
	w, _ := zw.Create("plugin.json")
	w.Write([]byte(testManifest))
	w, _ = zw.Create("main.js")
	w.Write([]byte(`exports.search = () => ({items: []})`))
	w, _ = zw.Create("../../escaped.js")
	w.Write([]byte("x"))
	zw.Close()
	f.Close()
	if _, err := Install(evil, root); err == nil || !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("evil package: %v", err)
	}
}

func TestInstallFolder(t *testing.T) {
	src := write(t, testManifest, `exports.search = () => ({items: []})`)
	os.WriteFile(filepath.Join(src, ".secret"), []byte("x"), 0o644)
	os.MkdirAll(filepath.Join(src, "node_modules", "x"), 0o755)
	os.WriteFile(filepath.Join(src, "node_modules", "x", "i.js"), []byte("//"), 0o644)
	m, err := Inspect(src)
	if err != nil || m.ID != "test" {
		t.Fatalf("inspect: %v %v", m, err)
	}
	root := t.TempDir()
	if _, err := Install(src, root); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".secret", "node_modules"} {
		if _, err := os.Stat(filepath.Join(root, "test", name)); err == nil {
			t.Errorf("%s copied", name)
		}
	}
	if plugins, errs := Discover(root, nil); len(plugins) != 1 || len(errs) != 0 {
		t.Fatalf("installed: %d plugins, %v", len(plugins), errs)
	}
	if _, err := Inspect(t.TempDir()); err == nil || !strings.Contains(err.Error(), "no plugin.json") {
		t.Errorf("empty folder: %v", err)
	}
	broken := write(t, testManifest, `this is not javascript`)
	if _, err := Install(broken, root); err == nil {
		t.Error("installed a plugin whose script doesn’t load")
	}
}

// A plugin signs in with a form once: the session cookie is kept and
// sent with its later requests and downloads, but not to other plugins.
func TestCookies(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("books.example/login", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		if r.PostForm.Get("password") != "pw" {
			http.Error(w, "no", 403)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc", Path: "/", Domain: "books.example"})
		w.Write([]byte(`{}`))
	})
	mux.HandleFunc("books.example/me", func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie("session")
		if err != nil {
			http.Error(w, "signed out", 401)
			return
		}
		w.Write([]byte(c.Value))
	})
	mux.HandleFunc("books.example/file", func(w http.ResponseWriter, r *http.Request) {
		if _, err := r.Cookie("session"); err != nil {
			http.Error(w, "signed out", 401)
			return
		}
		w.Write([]byte("PK fake epub"))
	})
	script := `
		let signedIn = false;
		exports.search = async function () {
			if (!signedIn) {
				const r = await fetch("https://books.example/login", { method: "POST",
					headers: { "Content-Type": "application/x-www-form-urlencoded" }, body: "password=pw" });
				if (!r.ok) throw dalibre.error("auth", "refused");
				signedIn = true;
			}
			const me = await fetch("https://books.example/me");
			if (!me.ok) throw dalibre.error("auth", "signed out");
			return { items: [{ id: "1", title: await me.text(), formats: [{ format: "EPUB", url: "https://books.example/file" }] }] };
		};`
	p, err := Load(write(t, testManifest, script), Options{Transport: serve{mux}})
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		page, err := p.Search(context.Background(), Query{})
		if err != nil {
			t.Fatal(err)
		}
		if page.Items[0].Title != "abc" {
			t.Fatalf("title %q", page.Items[0].Title)
		}
		if _, err := p.Download(context.Background(), page.Items[0], "epub", t.TempDir(), nil); err != nil {
			t.Fatalf("download without the session: %v", err)
		}
	}
	// Another plugin has its own cookies.
	other, err := Load(write(t, testManifest, `exports.search = async () => {
		const me = await fetch("https://books.example/me");
		return { items: [{ id: "1", title: String(me.status), formats: [{ format: "EPUB" }] }] };
	}`), Options{Transport: serve{mux}})
	if err != nil {
		t.Fatal(err)
	}
	if page, err := other.Search(context.Background(), Query{}); err != nil || page.Items[0].Title != "401" {
		t.Fatalf("another plugin: %+v %v", page, err)
	}
}

func TestFileName(t *testing.T) {
	for _, c := range [][4]string{
		{"", "Pride: and Prejudice?", "EPUB", "Pride_ and Prejudice_.epub"},
		{"../../x.epub", "t", "epub", "x.epub"},
		{`..\..\x.epub`, "t", "epub", "x.epub"},
		{"book.zip", "t", "pdf", "book.zip.pdf"},
		{"", "", "txt", "book.txt"},
	} {
		if got := fileName(c[0], c[1], c[2]); got != c[3] {
			t.Errorf("fileName(%q, %q, %q) = %q, want %q", c[0], c[1], c[2], got, c[3])
		}
	}
}

// The Gutenberg plugin shipped in plugins/, against a recorded answer.
func TestBookSourceCalls(t *testing.T) {
	manifest := `{"id":"dalibre.books","name":"Books","version":"1.0.0","api":1,"icon":"library",
		"capabilities":{"bookSource":{"collections":true,"filters":[
			{"key":"lang","title":"Language","type":"language","options":[{"value":""},{"value":"en"},{"value":"fr"}]},
			{"key":"sort","title":"Sort","type":"select","default":"popular","options":[{"value":"new","title":"Newest"},{"value":"popular","title":"Most popular"}]}]}},
		"permissions":{"hosts":["books.example"]}}`
	script := `
		const book = (id, extra) => ({id, title: "Book " + id, formats: [{format: "epub", url: "https://books.example/" + id + ".epub"}], ...extra});
		export function search(q) { return {items: [book("s", {description: JSON.stringify(q.filters)})]}; }
		export function collections() { return [{id: "pop", title: "Most popular"}, {id: "", title: "no id"}, {id: "pop", title: "again"}]; }
		export function list(q) { return {items: [book(q.collection + q.page)], hasMore: q.page < 2}; }
		export function get(r) { return {id: "other", title: "", description: "Full", url: "https://books.example/" + r.id,
			fields: [{label: "Rights", value: "Public domain"}, {label: "", value: "x"}]}; }`
	fsys := fstest.MapFS{"plugin.json": {Data: []byte(manifest)}, "main.js": {Data: []byte(script)}}
	p, err := LoadBuiltin(fsys, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if !p.Builtin || p.Manifest.IconName() != "library" || !p.Manifest.HasCollections() {
		t.Fatalf("manifest %+v", p.Manifest)
	}
	ctx := context.Background()
	page, err := p.Search(ctx, Query{Text: "x", Filters: map[string]string{"lang": "fr", "sort": "bogus", "other": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Items[0].Description; got != `{"lang":"fr","sort":"popular"}` {
		t.Errorf("filters passed as %s", got)
	}
	cs, err := p.Collections(ctx)
	if err != nil || len(cs) != 1 || cs[0].ID != "pop" {
		t.Fatalf("collections %v %v", cs, err)
	}
	page, err = p.List(ctx, Query{Collection: "pop", Page: 1})
	if err != nil || page.Items[0].ID != "pop1" || !page.HasMore {
		t.Fatalf("list %+v %v", page, err)
	}
	full, err := p.Get(ctx, page.Items[0])
	if err != nil {
		t.Fatal(err)
	}
	if full.ID != "pop1" || full.Title != "Book pop1" || full.Description != "Full" || len(full.Formats) != 1 ||
		full.URL != "https://books.example/pop1" || len(full.Fields) != 1 {
		t.Fatalf("get %+v", full)
	}
	// A reserved id loads only as a built-in plugin.
	if _, err := Inspect(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("inspected a missing package")
	}
	dir := write(t, manifest, script)
	pk, err := Pack(dir, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Inspect(pk); err == nil || !strings.Contains(err.Error(), "kept for plugins that ship with Dalibre") {
		t.Errorf("inspect reserved: %v", err)
	}
}

func TestCapabilitiesForms(t *testing.T) {
	for _, m := range []string{
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":["bookSource"],"permissions":{"hosts":["a.com"]}}`,
		`{"id":"x","name":"x","version":"1.0.0","api":1,"capabilities":{"bookSource":{}},"permissions":{"hosts":["a.com"]}}`,
	} {
		got, err := ParseManifest([]byte(m))
		if err != nil || !got.IsBookSource() {
			t.Errorf("%s: %v", m, err)
		}
	}
}
