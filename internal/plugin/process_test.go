package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const fileTypeManifest = `{"id":"test.files","name":"Files","version":"1.0.0","api":1,
	"capabilities":{"fileType":{"title":"Shout"}}}`

func TestProcess(t *testing.T) {
	dir := write(t, fileTypeManifest, `
		import { readFile, writeFile } from "node:fs/promises";
		export async function process(file) {
			const text = await readFile(file.path, "utf8");
			if (text.startsWith("leave")) return null;
			if (text.startsWith("report")) return { report: ["looked, changed nothing"] };
			if (text.startsWith("escape")) return "/etc/passwd";
			if (text.startsWith("missing")) return "/work/nope.txt";
			if (text.startsWith("noext")) { await writeFile("/work/out", "x"); return "/work/out"; }
			if (text.startsWith("convert")) {
				const out = file.path.replace(/\.txt$/, ".epub");
				await writeFile(out, "converted " + file.name + " " + file.format + " " + file.reason + " " + (file.book?.title ?? "-"));
				return { path: out, report: ["Converted to EPUB"] };
			}
			await writeFile(file.path, text.toUpperCase()); // in place
			return file.path;
		}`)
	p, err := Load(dir, Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	book := func(text string) string {
		f := filepath.Join(t.TempDir(), "My Book.txt")
		os.WriteFile(f, []byte(text), 0o644)
		return f
	}
	ctx := context.Background()

	in := book("hello")
	r, err := p.Process(ctx, in, ReasonImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(r.File); string(b) != "HELLO" || filepath.Base(r.File) != "My Book.txt" || r.Format != "TXT" {
		t.Errorf("in place: %s %q %s", r.File, b, r.Format)
	}
	if b, _ := os.ReadFile(in); string(b) != "hello" {
		t.Error("the original changed")
	}
	r.Done()
	if _, err := os.Stat(r.File); !os.IsNotExist(err) {
		t.Error("Done left the file")
	}

	r, err = p.Process(ctx, book("convert"), ReasonManual, &Book{Title: "Dune"})
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(r.File); string(b) != "converted My Book.txt TXT manual Dune" || r.Format != "EPUB" ||
		filepath.Base(r.File) != "My Book.epub" || len(r.Report) != 1 {
		t.Errorf("convert: %q %s %v", b, r.Format, r.Report)
	}
	r.Done()

	for text, want := range map[string]string{"leave": "", "report": "looked, changed nothing"} {
		r, err := p.Process(ctx, book(text), ReasonImport, nil)
		if err != nil || r.File != "" || strings.Join(r.Report, "") != want {
			t.Errorf("%s: %+v, %v", text, r, err)
		}
	}
	for text, want := range map[string]string{"escape": "must be in /work or /data", "missing": "doesn’t exist", "noext": "without an extension"} {
		if _, err := p.Process(ctx, book(text), ReasonImport, nil); kind(err) != KindInvalid || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v", text, err)
		}
	}
	// Each call's folder in /work is gone after it.
	if entries, _ := os.ReadDir(filepath.Join(p.dataDir, workFolder)); len(entries) != 0 {
		t.Errorf("work left: %v", entries)
	}
}

func TestFileAccess(t *testing.T) {
	dir := write(t, fileTypeManifest, `
		import { readFile, writeFile, readdir } from "node:fs/promises";
		export async function process(file) {
			const tries = { root: () => readdir("/"), other: () => writeFile("/elsewhere", "x"), data: () => writeFile("/data/k", "v"), work: () => readdir("/work") };
			const out = [];
			for (const [k, f] of Object.entries(tries)) { try { await f(); out.push(k + ":ok"); } catch (e) { out.push(k + ":" + e.code); } }
			return { report: out };
		}`)
	p, err := Load(dir, Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f := filepath.Join(t.TempDir(), "b.epub")
	os.WriteFile(f, []byte("x"), 0o644)
	r, err := p.Process(context.Background(), f, ReasonImport, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(r.Report, " "); got != "root:EACCES other:EACCES data:ok work:ok" {
		t.Errorf("got %s", got)
	}
}

func TestFileTypeManifest(t *testing.T) {
	m, err := ParseManifest([]byte(fileTypeManifest))
	if err != nil {
		t.Fatal(err)
	}
	if !m.IsFileType() || !m.RunsOnImport() || m.ActionTitle() != "Shout" {
		t.Errorf("%+v", m.Capabilities.FileType)
	}
	m, err = ParseManifest([]byte(strings.Replace(fileTypeManifest, `"title":"Shout"`, `"onImport":false`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if m.RunsOnImport() || m.ActionTitle() != "Files" {
		t.Errorf("onImport false: %v %q", m.RunsOnImport(), m.ActionTitle())
	}
	if _, err := Load(write(t, fileTypeManifest, `export function search() {}`), Options{}); err == nil || !strings.Contains(err.Error(), "must export process") {
		t.Errorf("no process: %v", err)
	}
}

// Libraries made for browsers find the global object as self.
func TestSelf(t *testing.T) {
	p, err := Load(write(t, testManifest, itemOf+`export const search = () => item(self === globalThis);`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := firstTitle(t, p, ""); got != "true" {
		t.Errorf("self: %s", got)
	}
}

func TestSettingFile(t *testing.T) {
	own := t.TempDir()
	key := filepath.Join(t.TempDir(), "user-key.der")
	os.WriteFile(key, []byte("k1"), 0o600)
	v, err := StoreSettingFile(own, "key", key)
	if err != nil || v != "/data/.settings/key/user-key.der" {
		t.Fatalf("%q, %v", v, err)
	}
	other := filepath.Join(t.TempDir(), "other.k4i")
	os.WriteFile(other, []byte("k2"), 0o600)
	v, _ = StoreSettingFile(own, "key", other) // replaces the first
	if entries, _ := os.ReadDir(filepath.Join(own, "data", ".settings", "key")); len(entries) != 1 || entries[0].Name() != "other.k4i" {
		t.Errorf("entries %v", entries)
	}
	if b, _ := os.ReadFile(SettingFile(own, v)); string(b) != "k2" {
		t.Errorf("SettingFile: %q", b)
	}
	// The script reads it at the path the setting holds.
	m := strings.Replace(fileTypeManifest, `"capabilities"`, `"settings":[{"key":"key","title":"Key","type":"file"}],"capabilities"`, 1)
	dir := write(t, m, `import { readFile } from "node:fs/promises";
		export async function process() { return { report: [await readFile(dalibre.settings.key, "utf8")] }; }`)
	p, err := Load(dir, Options{DataDir: own, Settings: map[string]string{"key": v}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f := filepath.Join(t.TempDir(), "b.epub")
	os.WriteFile(f, []byte("x"), 0o644)
	if r, err := p.Process(context.Background(), f, ReasonImport, nil); err != nil || strings.Join(r.Report, "") != "k2" {
		t.Errorf("read %+v, %v", r, err)
	}
	if err := RemoveSettingFile(own, "key"); err != nil || SettingFile(own, "/etc/passwd") != "" {
		t.Error("remove, or a path outside the settings folder")
	}
}
