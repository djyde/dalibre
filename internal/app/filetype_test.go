package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/plugin"
)

// fileTypeApp is a test app with a file type plugin that writes
// "processed:" before a file's text, and fails on "fail".
func fileTypeApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "upper")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"com.example.upper","name":"Upper","version":"1.0.0","api":1,
		"capabilities":{"fileType":{"title":"Process"}}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`
		import { readFile, writeFile } from "node:fs/promises";
		export async function process(file) {
			const text = await readFile(file.path, "utf8");
			if (text === "fail") throw new Error("broken");
			if (text === "leave") return null;
			await writeFile(file.path, "processed:" + text);
			return { path: file.path, report: ["Processed " + file.format] };
		}`), 0o644)
	p, err := plugin.Load(dir, plugin.Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	a.plugins = append(a.plugins, p)
	return a
}

// fileOf is the content of book title's file of format.
func fileOf(t *testing.T, a *App, title, format string) string {
	t.Helper()
	for _, b := range a.books {
		if b.Title != title {
			continue
		}
		d, err := a.lib.Details(b)
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range d.Files {
			if f.Format == format {
				data, _ := os.ReadFile(f.Path)
				return string(data)
			}
		}
	}
	t.Fatalf("no %s of %q", format, title)
	return ""
}

func TestImportRunsFileTypePlugins(t *testing.T) {
	a := fileTypeApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		os.WriteFile(p, []byte(text), 0o644)
		return p
	}
	if !tt.DropFiles(700, 400, write("Klara and the Sun - Kazuo Ishiguro.epub", "story"), write("The Overstory - Richard Powers.epub", "fail")) {
		t.Fatal("drop not taken")
	}
	tt.Frame()
	if got := fileOf(t, a, "Klara and the Sun", "EPUB"); got != "processed:story" {
		t.Errorf("added %q", got)
	}
	// A plugin that fails leaves the file as it was, and says so.
	if got := fileOf(t, a, "The Overstory", "EPUB"); got != "fail" {
		t.Errorf("added %q", got)
	}
	tt.Frame()
	if !tt.HasText("Upper couldn’t process The Overstory - Richard Powers.epub, so it was added as it was: broken") {
		t.Errorf("not told; texts %q", tt.Texts())
	}
	// What the plugin made is gone once the book is in.
	if entries, _ := filepath.Glob(filepath.Join(os.TempDir(), "dalibre-processed-*", "Klara*")); len(entries) != 0 {
		t.Errorf("left %v", entries)
	}
}

// TestRunPluginOnBooks runs the plugin from the book menu: no dialog, the
// new file is written at once with the old one kept as ORIGINAL_EPUB, and
// a toast says so, with Undo.
func TestRunPluginOnBooks(t *testing.T) {
	a := fileTypeApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	shot(t, tt, "run-plugin-menu")
	if err := tt.ChooseMenuItem("Process"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := fileOf(t, a, "Dune", "EPUB"); got != "processed:x" {
		t.Errorf("EPUB is %q", got)
	}
	if got := fileOf(t, a, "Dune", "ORIGINAL_EPUB"); got != "x" {
		t.Errorf("ORIGINAL_EPUB is %q", got)
	}
	for _, s := range []string{"Process · 1 book changed", "Undo"} {
		if !tt.HasText(s) {
			t.Errorf("misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "run-plugin-done")

	// Undo puts the old file back, and takes ORIGINAL_EPUB away.
	if err := tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := fileOf(t, a, "Dune", "EPUB"); got != "x" {
		t.Errorf("after Undo, EPUB is %q", got)
	}
	for _, b := range a.books {
		if b.Title == "Dune" && slices.Contains(b.Formats, "ORIGINAL_EPUB") {
			t.Errorf("after Undo, formats %v", b.Formats)
		}
	}

	// A book the plugin leaves as it was.
	a.lib.AddFormat(bookID(t, a, "Piranesi"), writeTemp(t, "Piranesi.epub", "leave"))
	a.reload(nil)
	tt.Frame()
	if err := tt.RightClick("Piranesi"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Process"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Process · Nothing to change") {
		t.Errorf("nothing to change: texts %q", tt.Texts())
	}

	// A plugin that needs setting up stops, and says where to.
	a.plugins[len(a.plugins)-1].Manifest.Settings = []plugin.Setting{{Key: "key", Title: "Key", Required: true}}
	if err := tt.RightClick("Dune"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Process"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, s := range []string{"Upper needs setting up: fill in Key", "Open plugin settings"} {
		if !tt.HasText(s) {
			t.Errorf("needs setup: misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "run-plugin-needs-setup")
}

func bookID(t *testing.T, a *App, title string) int64 {
	t.Helper()
	for _, b := range a.books {
		if b.Title == title {
			return b.ID
		}
	}
	t.Fatalf("no %q", title)
	return 0
}

func writeTemp(t *testing.T, name, text string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	os.WriteFile(p, []byte(text), 0o644)
	return p
}

// TestFileTypePluginSettings shows a file type plugin in Settings ›
// Plugins: what it adds, what it reaches, and its file setting.
func TestFileTypePluginSettings(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	m, err := plugin.ParseManifest([]byte(`{"id":"com.example.files","name":"Example Files","version":"0.1.0","api":1,
		"capabilities":{"fileType":{}},"settings":[{"key":"key","title":"Key file","type":"file"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	a.pluginsKnown = append(a.pluginsKnown, knownPlugin{manifest: m})
	tt := ui.NewTester(a.View, 1440, 900)
	a.openPlugins(m.ID)
	tt.Frame()
	for _, s := range []string{"Book files", "Example Files works on the file of each book you add", "Your books’ files", "Key file", "Choose file…"} {
		if !tt.HasText(s) {
			t.Errorf("misses %q; texts %q", s, tt.Texts())
		}
	}
	if tt.HasText("Save settings") {
		t.Error("Save settings for a file setting, which applies at once")
	}
	key := filepath.Join(t.TempDir(), "user-key.der")
	os.WriteFile(key, make([]byte, 1500), 0o600)
	v, err := plugin.StoreSettingFile(plugin.DataDir(a.pluginDir, m.ID), "key", key)
	if err != nil {
		t.Fatal(err)
	}
	a.pl.values["key"] = v
	tt.Frame()
	for _, s := range []string{"user-key.der", "2 KB", "Change…", "Remove"} {
		if !tt.HasText(s) {
			t.Errorf("chosen: misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "plugin-file-setting")
}

// TestHooks: a plugin's postimport and postadd run as a book is added,
// postdelete as a format is removed, and what they return is written.
func TestHooks(t *testing.T) {
	a := newTestApp(t)
	dir := filepath.Join(t.TempDir(), "hooks")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"com.example.hooks","name":"Hooks","version":"1.0.0","api":1,
		"capabilities":{"fileType":{"onImport":false,"onPostImport":true,"onPostAdd":true,"onPostDelete":true}},
		"permissions":{"library":"write"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`
		import { readFile } from "node:fs/promises";
		export async function postimport(ev) {
			return { metadata: { identifiers: { words: String((await readFile(ev.file.path, "utf8")).length) } } };
		}
		export const postadd = (ev) => ({ metadata: { tags: [...ev.book.tags, "Added by hook"] } });
		export const postdelete = (ev) => ({ metadata: { comments: "Lost its " + ev.format } });`), 0o644)
	p, err := plugin.Load(dir, plugin.Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	a.plugins = append(a.plugins, p)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if !tt.DropFiles(700, 400, writeTemp(t, "Klara and the Sun - Kazuo Ishiguro.epub", "story")) {
		t.Fatal("drop not taken")
	}
	tt.Frame()
	id := bookID(t, a, "Klara and the Sun")
	b := a.byID[id]
	d, _ := a.lib.Details(b)
	if !slices.Contains(b.Tags, "Added by hook") || d.Identifiers["words"] != "5" {
		t.Errorf("after add: tags %v, identifiers %v", b.Tags, d.Identifiers)
	}
	a.removeFormat(id, "EPUB")
	tt.Frame()
	if d, _ := a.lib.Details(a.byID[id]); d.Comments != "Lost its EPUB" {
		t.Errorf("after delete: comments %q", d.Comments)
	}
}

// TestHookPluginSettings: a plugin whose hooks may change books says so,
// in the warning tone.
func TestHookPluginSettings(t *testing.T) {
	a := newTestApp(t)
	a.pluginDir = t.TempDir()
	dir := filepath.Join(t.TempDir(), "tagger")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(`{"id":"com.example.tagger","name":"Tagger","version":"0.1.0","api":1,
		"capabilities":{"fileType":{"onImport":false,"onPostAdd":true}},"permissions":{"library":"write"}}`), 0o644)
	os.WriteFile(filepath.Join(dir, "main.js"), []byte(`export const postadd = () => null;`), 0o644)
	p, err := plugin.Load(dir, plugin.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	m := p.Manifest
	a.plugins = append(a.plugins, p)
	a.pluginsKnown = append(a.pluginsKnown, knownPlugin{manifest: m})
	tt := ui.NewTester(a.View, 1440, 900)
	a.openPlugins(m.ID)
	tt.Frame()
	for _, s := range []string{"Book files: Tagger runs after books are added and formats removed.", "Change your books’ details",
		"Title, authors, tags and other details of the books it runs for, without asking each time."} {
		if !tt.HasText(s) {
			t.Errorf("misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "plugin-hooks")
	// It has no process, so the book menu doesn't offer it.
	if len(a.fileTypePlugins()) != 0 {
		t.Error("a plugin without process in the book menu")
	}
}
