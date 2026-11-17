package plugin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const hooksManifest = `{"id":"test.hooks","name":"Hooks","version":"1.0.0","api":1,
	"capabilities":{"fileType":{"onImport":false,"onPostImport":true,"onPostAdd":true,"onPostDelete":true}},
	"permissions":{"library":"write"}}`

func TestHooks(t *testing.T) {
	dir := write(t, hooksManifest, `
		import { readFile } from "node:fs/promises";
		export async function postimport(ev) {
			const text = await readFile(ev.file.path, "utf8");
			return { metadata: { tags: [...ev.book.tags, "imported:" + ev.format + ":" + text] } };
		}
		export function postadd(ev) {
			if (ev.book.title === "bad") return { metadata: { pubdate: "someday" } };
			return { metadata: { series: "Added", seriesIndex: 2, identifiers: { hooks: String(ev.book.id) } } };
		}
		export function postdelete(ev) { return ev.book.formats.length ? null : { metadata: { comments: "no " + ev.format } }; }`)
	p, err := Load(dir, Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f := filepath.Join(t.TempDir(), "Dune.epub")
	os.WriteFile(f, []byte("spice"), 0o644)
	book := BookInfo{ID: 7, Title: "Dune", Authors: []string{"Frank Herbert"}, Tags: []string{"SF"}, Formats: []string{"EPUB"}}
	ctx := context.Background()

	ch, err := p.Hook(ctx, HookPostImport, HookEvent{Book: book, Format: "EPUB"}, map[string]string{"EPUB": f})
	if err != nil || ch == nil || strings.Join(*ch.Tags, ",") != "SF,imported:EPUB:spice" {
		t.Errorf("postimport: %+v, %v", ch, err)
	}
	ch, err = p.Hook(ctx, HookPostAdd, HookEvent{Book: book}, map[string]string{"EPUB": f})
	if err != nil || ch == nil || *ch.Series != "Added" || *ch.SeriesIndex != 2 || ch.Identifiers["hooks"] != "7" || ch.Tags != nil {
		t.Errorf("postadd: %+v, %v", ch, err)
	}
	book.Formats = nil
	ch, err = p.Hook(ctx, HookPostDelete, HookEvent{Book: book, Format: "PDF"}, nil)
	if err != nil || ch == nil || *ch.Comments != "no PDF" {
		t.Errorf("postdelete: %+v, %v", ch, err)
	}
	book.Title = "bad"
	if _, err := p.Hook(ctx, HookPostAdd, HookEvent{Book: book}, nil); kind(err) != KindInvalid || !strings.Contains(err.Error(), "pubdate") {
		t.Errorf("bad change: %v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(p.dataDir, workFolder)); len(entries) != 0 {
		t.Errorf("work left: %v", entries)
	}

	// Without "write", changes are refused; without "read", hooks can't be asked for.
	readOnly := strings.Replace(hooksManifest, `"write"`, `"read"`, 1)
	p2, err := Load(write(t, readOnly, `export const postimport = () => ({ metadata: { title: "x" } }); export const postadd = () => null; export const postdelete = () => null;`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if _, err := p2.Hook(ctx, HookPostImport, HookEvent{Book: book}, nil); kind(err) != KindInvalid || !strings.Contains(err.Error(), `"write"`) {
		t.Errorf("read only: %v", err)
	}
	if _, err := ParseManifest([]byte(strings.Replace(hooksManifest, `,
	"permissions":{"library":"write"}`, `,"permissions":{}`, 1))); err == nil || !strings.Contains(err.Error(), "hooks see the books") {
		t.Errorf("no library permission: %v", err)
	}
	// A hook asked for must be exported.
	if _, err := Load(write(t, hooksManifest, `export const postimport = () => null;`), Options{}); err == nil || !strings.Contains(err.Error(), "onPostAdd must export postadd") {
		t.Errorf("missing export: %v", err)
	}
	// A hook not asked for isn't called.
	if ch, err := p.Hook(ctx, "postconvert", HookEvent{Book: book}, nil); ch != nil || err != nil {
		t.Errorf("unknown hook: %+v %v", ch, err)
	}
}

func TestLibraryPermissions(t *testing.T) {
	read, write := Permissions{Library: LibraryRead}, Permissions{Library: LibraryWrite}
	if !write.Covers(read) || read.Covers(write) || !read.Covers(read) || (Permissions{}).Covers(read) {
		t.Error("write covers read, read doesn't cover write")
	}
}
