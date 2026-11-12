package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"dalibre/internal/sysproxy"
)

const sum0 = "0000000000000000000000000000000000000000000000000000000000000000"

func toolManifest(tools string) string {
	return `{"id":"tooly","name":"Tooly","version":"1.0.0","api":1,
		"capabilities":["source"],
		"permissions":{"hosts":["books.example"]},"tools":` + tools + `}`
}

func TestToolsManifest(t *testing.T) {
	ok := `{"pandoc":{"version":"3.6.4","min":"3.6","packages":{"brew":"pandoc","winget":"JohnMacFarlane.Pandoc","scoop":"main/pandoc"},
		"platforms":{"darwin-arm64":{"url":"https://example.com/pandoc.zip","sha256":"` + sum0 + `","bin":"pandoc-3.6.4/bin/pandoc"},
		"linux-amd64":{"url":"https://example.com/pandoc","sha256":"` + sum0 + `"}}}}`
	m, err := ParseManifest([]byte(toolManifest(ok)))
	if err != nil {
		t.Fatal(err)
	}
	if m.Tools["pandoc"].MinVersion() != "3.6" || !m.Tools["pandoc"].Accepts("3.10") || m.Tools["pandoc"].Accepts("3.5.9") {
		t.Error("min version")
	}
	bad := map[string]string{
		"no sha":           `{"x":{"version":"1.0","platforms":{"darwin-arm64":{"url":"https://e.com/x"}}}}`,
		"http":             `{"x":{"version":"1.0","platforms":{"darwin-arm64":{"url":"http://e.com/x","sha256":"` + sum0 + `"}}}}`,
		"archive no bin":   `{"x":{"version":"1.0","platforms":{"darwin-arm64":{"url":"https://e.com/x.tar.gz","sha256":"` + sum0 + `"}}}}`,
		"bin escapes":      `{"x":{"version":"1.0","platforms":{"darwin-arm64":{"url":"https://e.com/x.zip","sha256":"` + sum0 + `","bin":"../x"}}}}`,
		"unknown platform": `{"x":{"version":"1.0","platforms":{"plan9-386":{"url":"https://e.com/x","sha256":"` + sum0 + `"}}}}`,
		"latest":           `{"x":{"version":"latest","platforms":{"darwin-arm64":{"url":"https://e.com/x","sha256":"` + sum0 + `"}}}}`,
		"min newer":        `{"x":{"version":"1.0","min":"2.0","platforms":{"darwin-arm64":{"url":"https://e.com/x","sha256":"` + sum0 + `"}}}}`,
		"bad brew":         `{"x":{"version":"1.0","packages":{"brew":"a/b"},"platforms":{"darwin-arm64":{"url":"https://e.com/x","sha256":"` + sum0 + `"}}}}`,
		"bad id":           `{"X Y":{"version":"1.0","platforms":{"darwin-arm64":{"url":"https://e.com/x","sha256":"` + sum0 + `"}}}}`,
	}
	for name, tools := range bad {
		if _, err := ParseManifest([]byte(toolManifest(tools))); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCompareVersions(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"3.10", "3.9", 1}, {"3.6.4", "3.6.4", 0}, {"v1.2", "1.2.0", 0}, {"3.6.4_1", "3.6.4", 0}, {"0.0.9", "0.0.12", -1}} {
		if got := CompareVersions(c.a, c.b); got != c.want {
			t.Errorf("%s vs %s = %d", c.a, c.b, got)
		}
	}
	if VersionIn("pandoc 3.6.4\nFeatures: +server") != "3.6.4" {
		t.Error("VersionIn")
	}
}

// fakeTool writes a script standing for a tool: it prints its arguments
// and its input, and a line per second when asked to wait.
func fakeTool(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	exe := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(exe, []byte(`#!/bin/sh
case "$1" in
  sleep) sleep 30 ;;
  fail) echo "bad things" >&2; exit 3 ;;
  env) echo "$FOO $HTTPS_PROXY" ;;
  write) echo made > "$2"; echo "$PWD" ;;
  *) echo "args: $*"; cat; echo "line two" ;;
esac
`), 0o755)
	return exe
}

func TestExec(t *testing.T) {
	exe := fakeTool(t)
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:9")
	sysproxy.Forget()
	defer sysproxy.Forget()
	dir := write(t, toolManifest(`{"fake":{"version":"1.0","platforms":{"`+Platform()+`":{"url":"https://e.com/fake","sha256":"`+sum0+`"}}}}`), `
		export async function search() { return { items: [] }; }
		export async function t() {
			const lines = [];
			const a = await dalibre.exec("fake", ["hello", "world"], { stdin: "from stdin\n", onLine: (s, l) => lines.push(s + ":" + l) });
			const b = await dalibre.exec("fake", ["fail"]);
			const c = await dalibre.exec("fake", ["env"], { env: { FOO: "foo" } });
			const d = await dalibre.exec("fake", ["write", "/work/out.txt"]);
			let e = "";
			try { await dalibre.exec("fake", ["sleep"], { timeout: 200 }); } catch (err) { e = err.kind; }
			let f = "";
			try { await dalibre.exec("other", []); } catch (err) { f = err.kind; }
			const made = (await import("node:fs")).readFileSync("/work/out.txt", "utf8");
			return JSON.stringify({ a, lines, b, c: c.stdout, d: d.stdout.trim(), e, f, made });
		}`)
	data, _ := filepath.EvalSymlinks(t.TempDir())
	p, err := Load(dir, Options{DataDir: data, Tools: map[string]string{"fake": exe}, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	raw, err := p.call(context.Background(), "t")
	if err != nil {
		t.Fatal(err)
	}
	var got string
	json.Unmarshal(raw, &got)
	for _, want := range []string{
		`"stdout":"args: hello world\nfrom stdin\nline two\n"`,
		`"lines":["stdout:args: hello world","stdout:from stdin","stdout:line two"]`,
		`"b":{"code":3,"stdout":"","stderr":"bad things\n"}`,
		`"c":"foo http://127.0.0.1:9\n"`,
		`"d":"` + filepath.Join(data, "work"),
		`"e":"timeout"`, `"f":"invalid"`, `"made":"made\n"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
}

// targz makes a .tar.gz of files (name → content), executable when the
// name ends in a '*'.
func targz(files map[string]string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		mode := int64(0o644)
		if n, ok := strings.CutSuffix(name, "*"); ok {
			name, mode = n, 0o755
		}
		tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.WriteHeader(&tar.Header{Name: "pkg/evil", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"})
	tw.WriteHeader(&tar.Header{Name: "../escape", Mode: 0o644, Size: 1, Typeflag: tar.TypeReg})
	tw.Write([]byte("x"))
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func sha(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func TestToolStore(t *testing.T) {
	tgz := targz(map[string]string{"pkg/bin/tool*": "#!/bin/sh\necho hi\n", "pkg/README": "read me"})
	zp := zipOf(t, map[string]string{"pkg/tool.exe": "MZ"})
	raw := []byte("raw tool")
	mux := http.NewServeMux()
	mux.HandleFunc("dl.example/t.tar.gz", func(w http.ResponseWriter, r *http.Request) { w.Write(tgz) })
	mux.HandleFunc("dl.example/t.zip", func(w http.ResponseWriter, r *http.Request) { w.Write(zp) })
	mux.HandleFunc("dl.example/tool", func(w http.ResponseWriter, r *http.Request) { w.Write(raw) })
	s := &ToolStore{Dir: filepath.Join(t.TempDir(), ".tools"), Client: &http.Client{Transport: serve{mux}}}
	ctx := context.Background()

	b := Build{URL: "https://dl.example/t.tar.gz", SHA256: sha(tgz), Size: int64(len(tgz)), Bin: "pkg/bin/tool"}
	var last int64
	exe, err := s.Fetch(ctx, b, func(done, total int64) { last = done })
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(exe); err != nil || st.Mode()&0o111 == 0 || last != int64(len(tgz)) {
		t.Fatalf("tool: %v %v, progress %d", st, err, last)
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(exe), "..", "evil")); err == nil {
		t.Error("kept a link leading out of the build")
	}
	if _, err := os.Stat(filepath.Join(s.Dir, "escape")); err == nil {
		t.Error("unpacked outside the build")
	}
	if p, ok := s.Path(b); !ok || p != exe {
		t.Error("the store doesn't have what it fetched")
	}
	if _, err := s.Fetch(ctx, Build{URL: "https://dl.example/t.zip", SHA256: sha(zp), Bin: "pkg/tool.exe"}, nil); err != nil {
		t.Fatal(err)
	}
	rb := Build{URL: "https://dl.example/tool", SHA256: sha(raw)}
	if exe, err := s.Fetch(ctx, rb, nil); err != nil || filepath.Base(exe) != "tool" {
		t.Fatal(exe, err)
	}
	// A download that isn't the build named is refused, and nothing kept.
	_, err = s.Fetch(ctx, Build{URL: "https://dl.example/tool", SHA256: strings.Repeat("a", 64)}, nil)
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Errorf("hash mismatch: %v", err)
	}
	if _, err := s.Fetch(ctx, Build{URL: "https://dl.example/t.tar.gz", SHA256: sha(tgz), Bin: "pkg/missing"}, nil); err == nil {
		t.Error("accepted a build without its executable")
	}
	s.Keep(map[string]bool{sha(raw): true})
	if _, ok := s.Path(b); ok {
		t.Error("Keep left a build no one declares")
	}
	if _, ok := s.Path(rb); !ok {
		t.Error("Keep removed a build that is declared")
	}
}

func TestResolvePath(t *testing.T) {
	exe := fakeTool(t)
	m := `{"id":"src","name":"Src","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["books.example"]},
		"tools":{"fake":{"version":"1.0","platforms":{"` + Platform() + `":{"url":"https://e.com/fake","sha256":"` + sum0 + `"}}}}}`
	dir := write(t, m, `
		export async function search() { return { items: [{ id: "1", title: "Made", formats: [{ format: "EPUB" }] }] }; }
		export async function resolve(item, format) {
			await dalibre.exec("fake", ["write", "/work/book.epub"]);
			return { path: "/work/book.epub" };
		}`)
	p, err := Load(dir, Options{DataDir: t.TempDir(), Tools: map[string]string{"fake": exe}, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	page, err := p.Search(context.Background(), Query{Text: "x"})
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	file, err := p.Download(context.Background(), page.Items[0], "EPUB", out, nil)
	if err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(file); string(data) != "made\n" || filepath.Dir(file) != out || filepath.Ext(file) != ".epub" {
		t.Errorf("file %s: %q", file, data)
	}
}

// With permissions.covers "any", covers come from any https host, as
// images only and without the plugin's credentials; without it, only
// from its hosts.
func TestCoversAny(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("cdn.mirror.example/c.jpg", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("sent the plugin's credentials to another host")
		}
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte("jpeg"))
	})
	mux.HandleFunc("cdn.mirror.example/page", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<html>"))
	})
	load := func(covers string) *Plugin {
		m := `{"id":"src","name":"Src","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["books.example"]` + covers + `}}`
		p, err := Load(write(t, m, `export async function search() { return { items: [] }; }`), Options{
			Transport: serve{mux}, Logf: t.Logf, Authorize: func(r *http.Request) { r.Header.Set("Authorization", "secret") }})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		return p
	}
	ctx := context.Background()
	if _, err := load("").Cover(ctx, Result{Cover: "https://cdn.mirror.example/c.jpg"}); !errors.Is(err, ErrHostNotAllowed) {
		t.Errorf("without covers: %v", err)
	}
	p := load(`,"covers":"any"`)
	if data, err := p.Cover(ctx, Result{Cover: "https://cdn.mirror.example/c.jpg"}); err != nil || string(data) != "jpeg" {
		t.Errorf("any: %q %v", data, err)
	}
	if _, err := p.Cover(ctx, Result{Cover: "http://cdn.mirror.example/c.jpg"}); err == nil {
		t.Error("took a cover over http from another host")
	}
	if _, err := p.Cover(ctx, Result{Cover: "https://cdn.mirror.example/page"}); err == nil {
		t.Error("took a web page as a cover")
	}
	granted := Permissions{Hosts: []string{"books.example"}}
	if granted.Covers(p.Manifest.Permissions) {
		t.Error("covers from anywhere is a new permission")
	}
	if _, err := ParseManifest([]byte(`{"id":"x","name":"X","version":"1.0.0","api":1,"capabilities":["source"],"permissions":{"hosts":["e.com"],"covers":"all"}}`)); err == nil {
		t.Error("accepted covers \"all\"")
	}
}
