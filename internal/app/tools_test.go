package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/plugin"
)

// toolPlugin packs a plugin running tool "fake", whose build is served at
// dl.example with the hash of body.
func toolPlugin(t *testing.T, version string, body []byte) string {
	t.Helper()
	sum := sha256.Sum256(body)
	src := filepath.Join(t.TempDir(), "src")
	os.MkdirAll(src, 0o755)
	os.WriteFile(filepath.Join(src, "plugin.json"), []byte(`{"id":"com.example.tooly","name":"Tooly","version":"`+version+`","api":1,
		"capabilities":["source"],"permissions":{"hosts":["a.example"]},
		"tools":{"fake":{"version":"1.0","packages":{"brew":"example/tap/fake-tool-dalibre-test"},
		"platforms":{"`+plugin.Platform()+`":{"url":"https://dl.example/fake","sha256":"`+hex.EncodeToString(sum[:])+`","size":`+strconv.Itoa(len(body))+`}}}}}`), 0o644)
	os.WriteFile(filepath.Join(src, "main.js"), []byte(`
		export async function search() { return { items: [] }; }
		export async function check() {
			const r = await dalibre.exec("fake", []);
			if (!r.stdout.includes("signed in")) throw dalibre.error("auth", "Sign in with fake login");
		}`), 0o644)
	pkg, err := plugin.Pack(src, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return pkg
}

// A plugin's tool is shown when it is installed, downloaded once
// granted, checked against its hash, and run for the plugin; a plugin
// update with another build asks again.
func TestPluginTools(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake tool is a shell script")
	}
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	script := []byte("#!/bin/sh\necho not yet\n")
	served := 0
	a.pluginTransport = fakeWeb{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "dl.example" {
			served++
			w.Write(script)
			return
		}
		http.NotFound(w, r)
	})}
	tt := ui.NewTester(a.View, 1440, 900)

	a.installPlugin(toolPlugin(t, "1.0.0", script))
	if a.consent == nil {
		t.Fatal("installed without asking")
	}
	tt.Frame()
	if !tt.HasText("Run fake 1.0 on this computer") || !tt.HasText("Download") {
		t.Fatalf("the dialog doesn't show the tool: %q", tt.Texts())
	}
	shot(t, tt, "tools-consent")
	if a.tl.picks["fake"] != plugin.SourceDownload {
		t.Fatalf("pick %q, want the download", a.tl.picks["fake"])
	}
	a.grantConsent()
	if served != 1 || len(a.plugins) != 1 {
		t.Fatalf("served %d, plugins %d, errs %v, missing %v, pending %d, jobs %+v", served, len(a.plugins), a.pluginErrs, a.tl.missing, len(a.pluginsPending), a.tl.jobs)
	}
	st := a.settings.Plugins["com.example.tooly"]
	if st.Tools["fake"].Source != plugin.SourceDownload || st.Granted.Tools["fake"] == "" {
		t.Fatalf("state %+v %+v", st.Tools["fake"], st.Granted)
	}
	place := a.tl.places["com.example.tooly"]["fake"]
	if !strings.HasPrefix(place.Path, plugin.ToolsDir(a.pluginDir)) {
		t.Fatalf("place %+v", place)
	}
	// Its check() ran the tool, which says it isn't signed in.
	if err := a.tl.checks["com.example.tooly"]; err == nil || checkText(err) != "Sign in with fake login" {
		t.Fatalf("check: %v", err)
	}
	a.openPlugins("com.example.tooly")
	tt.Frame()
	if !tt.HasText("Downloaded by Dalibre · 0 MB") && !tt.HasText("Sign in with fake login") {
		t.Fatalf("plugin page: %q", tt.Texts())
	}
	shot(t, tt, "tools-plugin-page")
	a.pluginsOpen = false

	// Another build is a new permission: the update waits for the user.
	script2 := []byte("#!/bin/sh\necho signed in\n")
	if _, err := plugin.Install(toolPlugin(t, "1.1.0", script2), a.pluginDir); err != nil {
		t.Fatal(err)
	}
	a.loadPlugins()
	if len(a.plugins) != 0 || len(a.pluginsPending) != 1 {
		t.Fatalf("plugins %d, pending %d", len(a.plugins), len(a.pluginsPending))
	}
	script = script2
	a.askConsent("com.example.tooly")
	tt.Frame()
	if !tt.HasText("Run fake 1.0 on this computer") {
		t.Fatalf("the update's dialog doesn't ask about the new build: %q", tt.Texts())
	}
	a.grantConsent()
	if len(a.plugins) != 1 || a.tl.checks["com.example.tooly"] != nil {
		t.Fatalf("plugins %d, check %v", len(a.plugins), a.tl.checks["com.example.tooly"])
	}

	// A file the user chose instead; one that is gone leaves the plugin
	// off, saying what it needs.
	m := a.knownManifest("com.example.tooly")
	mine := filepath.Join(t.TempDir(), "fake")
	os.WriteFile(mine, script2, 0o755)
	a.getTool(m, "fake", plugin.SourceFile, mine, true)
	if p := a.tl.places["com.example.tooly"]["fake"]; p.Source != plugin.SourceFile || p.Path != mine || len(a.plugins) != 1 {
		t.Fatalf("file: %+v, plugins %d", p, len(a.plugins))
	}
	os.Remove(mine)
	a.loadPlugins()
	if len(a.plugins) != 0 || len(a.tl.missing["com.example.tooly"]) != 1 {
		t.Fatalf("plugins %d, missing %v", len(a.plugins), a.tl.missing)
	}
	status, _, _ := a.pluginStatus(a.pluginsKnown[0])
	if status != "Needs fake" {
		t.Errorf("status %q", status)
	}

	// Uninstalling keeps only the builds other plugins declare.
	a.uninstallPlugin("com.example.tooly")
	if entries, _ := os.ReadDir(plugin.ToolsDir(a.pluginDir)); len(entries) != 0 {
		t.Errorf("the store kept %d builds no plugin declares", len(entries))
	}
}

// A download that isn't the build named is refused, and the plugin stays
// off with Try again.
func TestPluginToolBadDownload(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	a.pluginTransport = fakeWeb{http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tampered!!!!")) })}
	a.installPlugin(toolPlugin(t, "1.0.0", []byte("the real one")))
	a.grantConsent()
	j := a.toolJobFor("com.example.tooly", "fake")
	if len(a.plugins) != 0 || j == nil || !strings.Contains(j.err, "SHA-256") {
		t.Fatalf("plugins %d, job %+v", len(a.plugins), j)
	}
	tt := ui.NewTester(a.View, 1440, 900)
	a.openPlugins("com.example.tooly")
	tt.Frame()
	if !tt.HasText("Try again") {
		t.Fatalf("no Try again: %q", tt.Texts())
	}
	shot(t, tt, "tools-failed")
}

// A package manager's install shows its output as it runs, and what it
// said when it failed, with the download to fall back on.
func TestToolInstallDialog(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	a.pluginTransport = fakeWeb{http.NotFoundHandler()}
	a.installPlugin(toolPlugin(t, "1.0.0", []byte("x")))
	m := a.consent.manifest
	a.consent = nil
	a.pluginsKnown = append(a.pluginsKnown, knownPlugin{manifest: m})
	key := toolKey(m.ID, "fake")
	a.tl.jobs = map[string]*toolJob{key: {plugin: m.ID, tool: "fake", name: m.Name, source: "brew", pkg: "example/tap/fake",
		running: true, cancel: func() {}, started: time.Now().Add(-72 * time.Second),
		lines: []string{"==> Fetching downloads for: fake", "==> Downloading https://ghcr.io/v2/example/tap/fake/blobs/sha256:7f2c", "######## 76.2%"}}}
	a.tl.dialog = key
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if !tt.HasText("Running brew install example/tap/fake…") || !tt.HasText("1:12") {
		t.Fatalf("running: %q", tt.Texts())
	}
	shot(t, tt, "tools-installing")
	j := a.tl.jobs[key]
	j.running, j.err = false, "Homebrew stopped with exit code 1"
	j.lines = append(j.lines, "Error: Another active Homebrew process is already in progress.")
	tt.Frame()
	if !tt.HasText("Homebrew couldn’t install fake") || !tt.HasText("Copy log") || !tt.HasText("Download with Dalibre · 1 KB") {
		t.Fatalf("failed: %q", tt.Texts())
	}
	shot(t, tt, "tools-install-failed")
}
