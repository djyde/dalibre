package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, _ := zw.Create(name)
		w.Write([]byte(body))
	}
	zw.Close()
	return buf.Bytes()
}

func TestParseRepo(t *testing.T) {
	for in, want := range map[string]string{
		"standardebooks/dalibre-plugin":                    "standardebooks/dalibre-plugin",
		"https://github.com/standardebooks/dalibre-plugin": "standardebooks/dalibre-plugin",
		"https://github.com/a/b.git":                       "a/b",
		"github.com/a/b/tree/main":                         "a/b",
		"git@github.com:a/b.git":                           "a/b",
	} {
		r, err := ParseRepo(in)
		if err != nil || r.String() != want {
			t.Errorf("ParseRepo(%q) = %v, %v", in, r, err)
		}
	}
	for _, bad := range []string{"", "justone", "https://gitlab.com/a/b", "a b/c"} {
		if _, err := ParseRepo(bad); err == nil {
			t.Errorf("ParseRepo(%q) passed", bad)
		}
	}
}

func TestGitHubInstall(t *testing.T) {
	js := `exports.search = () => ({items: []})`
	asset := zipOf(t, map[string]string{"plugin.json": testManifest, "main.js": js})
	// A branch's zipball: the repository in a top folder, the plugin in plugin/.
	ball := zipOf(t, map[string]string{"o-r-abc1234/README.md": "hi", "o-r-abc1234/plugin/plugin.json": testManifest, "o-r-abc1234/plugin/main.js": js})
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/o/r/releases", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{
			{"tag_name": "v2.0.0-beta", "prerelease": true, "zipball_url": srv.URL + "/zip/beta"},
			{"tag_name": "v1.1.0", "body": "Faster", "zipball_url": srv.URL + "/zip/v1.1.0",
				"assets": []map[string]any{{"name": "test-1.1.0.dalibre-plugin", "size": len(asset), "browser_download_url": srv.URL + "/asset"}}},
			{"tag_name": "v1.0.0", "draft": true},
		})
	})
	mux.HandleFunc("/repos/o/r/commits/main", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"sha": "abc1234def", "commit": map[string]any{"message": "Fix search\n\nmore"}})
	})
	mux.HandleFunc("/repos/o/r/zipball/abc1234def", func(w http.ResponseWriter, r *http.Request) { w.Write(ball) })
	mux.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) { w.Write(asset) })
	srv = httptest.NewServer(mux)
	defer srv.Close()
	g := &GitHub{API: srv.URL}
	ctx := context.Background()
	repo := Repo{"o", "r"}

	rels, err := g.Releases(ctx, repo)
	if err != nil || len(rels) != 2 {
		t.Fatalf("releases %v %v", rels, err)
	}
	latest, ok := Latest(rels)
	if !ok || latest.Tag != "v1.1.0" || latest.Asset == "" {
		t.Fatalf("latest %+v", latest)
	}
	root := t.TempDir()
	file, err := g.Download(ctx, latest.Archive, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m, err := Install(file, root); err != nil || m.ID != "test" {
		t.Fatalf("install release: %v %v", m, err)
	}

	c, err := g.Commit(ctx, repo, "main")
	if err != nil || c.Short() != "abc1234" || c.Message != "Fix search" {
		t.Fatalf("commit %+v %v", c, err)
	}
	file, err = g.Download(ctx, c.Archive, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if m, err := Inspect(file); err != nil || m.ID != "test" {
		t.Fatalf("inspect branch: %v %v", m, err)
	}
	if _, err := Install(file, root); err != nil {
		t.Fatalf("install branch: %v", err)
	}
	if _, err := Load(root+"/test", Options{}); err != nil {
		t.Fatalf("load: %v", err)
	}
}

func TestReadme(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/ann/books/readme", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ref") != "v1.0.0" || r.Header.Get("Accept") != "application/vnd.github.raw" {
			http.Error(w, "bad", 400)
			return
		}
		w.Write([]byte("# Books\n"))
	})
	mux.HandleFunc("/repos/ann/none/readme", http.NotFound)
	srv := httptest.NewServer(mux)
	defer srv.Close()
	g := &GitHub{API: srv.URL}
	if s, err := g.Readme(context.Background(), Repo{"ann", "books"}, "v1.0.0"); err != nil || s != "# Books\n" {
		t.Errorf("readme %q %v", s, err)
	}
	if s, err := g.Readme(context.Background(), Repo{"ann", "none"}, ""); err != nil || s != "" {
		t.Errorf("no readme %q %v", s, err)
	}
}
