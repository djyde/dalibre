package plugin

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// page runs search and returns the first item's title.
func firstTitle(t *testing.T, p *Plugin, text string) string {
	t.Helper()
	page, err := p.Search(context.Background(), Query{Text: text})
	if err != nil {
		t.Fatalf("%s: %v", text, err)
	}
	if len(page.Items) == 0 {
		t.Fatalf("%s: no items", text)
	}
	return page.Items[0].Title
}

const itemOf = `const item = (title) => ({ items: [{ id: "1", title: String(title), formats: [{ format: "TXT", url: "https://books.example/" }] }] });`

func TestTimers(t *testing.T) {
	dir := write(t, testManifest, itemOf+`
		const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
		export async function search(q) {
			if (q.text === "forever") { await sleep(1e9); }
			if (q.text === "interval") {
				let n = 0;
				await new Promise((r) => { const id = setInterval(() => { if (++n === 3) { clearInterval(id); r(); } }, 1); });
				return item(n);
			}
			if (q.text === "immediate") { await new Promise((r) => setImmediate(r)); return item("next turn"); }
			if (q.text === "throws") { setTimeout(() => { throw new Error("late"); }, 1); await sleep(50); }
			const start = Date.now();
			await sleep(20);
			return item(Date.now() - start >= 20 ? "slept" : "woke early");
		}`)
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for text, want := range map[string]string{"": "slept", "interval": "3", "immediate": "next turn"} {
		if got := firstTitle(t, p, text); got != want {
			t.Errorf("%q: %q, want %q", text, got, want)
		}
	}
	// A timer far past the deadline is stopped by it.
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := p.Search(ctx, Query{Text: "forever"}); kind(err) != KindTimeout || time.Since(start) > 2*time.Second {
		t.Errorf("forever: %v after %v", err, time.Since(start))
	}
	// An exception in a timer's callback fails the call.
	if _, err := p.Search(context.Background(), Query{Text: "throws"}); kind(err) != KindScript || !strings.Contains(err.Error(), "late") {
		t.Errorf("throws: %v", err)
	}
	if firstTitle(t, p, "") != "slept" {
		t.Error("the plugin doesn’t answer after a failed call")
	}
}

func TestWebAPIs(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("books.example/bin", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("User-Agent"), "Dalibre test/1.0.0") {
			t.Errorf("user agent %q", r.Header.Get("User-Agent"))
		}
		w.Write([]byte{1, 2, 3, 250})
	})
	mux.HandleFunc("books.example/big", func(w http.ResponseWriter, r *http.Request) {
		w.Write(make([]byte, MaxFetchBody+10))
	})
	mux.HandleFunc("books.example/echo", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Write([]byte(r.Method + " " + r.Form.Get("q")))
	})
	dir := write(t, testManifest, itemOf+`
		export async function search(q) {
			switch (q.text) {
			case "bytes": {
				const b = new Uint8Array(await (await fetch("https://books.example/bin")).arrayBuffer());
				return item(Array.from(b).join(","));
			}
			case "post": {
				const u = new URL("https://books.example/echo");
				const r = await fetch(u, { method: "POST", body: new URLSearchParams({ q: "dune" }) });
				return item(await r.text());
			}
			case "text": {
				const s = new TextDecoder().decode(new TextEncoder().encode("héllo"));
				return item(s + " " + btoa("hi") + " " + Buffer.from("hi").toString("hex") + " " + structuredClone({ a: 1 }).a);
			}
			case "stream": {
				const r = await fetch("https://books.example/bin");
				let n = 0;
				for (const reader = r.body.getReader();;) { const { done, value } = await reader.read(); if (done) break; n += value.length; }
				return item(n);
			}
			case "abort": {
				const c = new AbortController(); c.abort();
				try { await fetch("https://books.example/bin", { signal: c.signal }); } catch (e) { return item(e.name); }
			}
			case "evil": await fetch("https://evil.example/"); break;
			case "big": await (await fetch("https://books.example/big")).text(); break;
			}
		}`)
	p, err := Load(dir, Options{Transport: serve{mux}})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	for text, want := range map[string]string{
		"bytes": "1,2,3,250", "post": "POST dune", "text": "héllo aGk= 6869 1", "stream": "4", "abort": "AbortError",
	} {
		if got := firstTitle(t, p, text); got != want {
			t.Errorf("%s: %q, want %q", text, got, want)
		}
	}
	// What fetch rejects with, uncaught, is an error of its kind.
	if _, err := p.Search(context.Background(), Query{Text: "evil"}); kind(err) != KindPermission || !strings.Contains(err.Error(), "evil.example") {
		t.Errorf("evil: %v (%s)", err, kind(err))
	}
	if _, err := p.Search(context.Background(), Query{Text: "big"}); kind(err) != KindUnavailable || !strings.Contains(err.Error(), "over 16 MB") {
		t.Errorf("big: %v (%s)", err, kind(err))
	}
}

func TestDataFolder(t *testing.T) {
	root := t.TempDir()
	script := itemOf + `
		import { readFile, writeFile, mkdir } from "node:fs/promises";
		import { existsSync } from "node:fs";
		export async function search(q) {
			if (q.text === "write") { await mkdir("/data/cache", { recursive: true }); await writeFile("/data/cache/token", "t0k"); return item("ok"); }
			if (q.text === "read") return item(existsSync("token") ? "relative" : await readFile("/data/cache/token", "utf8"));
			if (q.text === "escape") { try { await readFile("/data/../../../plugin.json", "utf8"); return item("escaped"); } catch (e) { return item(e.code); } }
			if (q.text === "fill") {
				const chunk = "x".repeat(8 << 20);
				try { for (let i = 0; i < 20; i++) await writeFile("/data/f" + i, chunk); } catch (e) { return item(e.code); }
				return item("no limit");
			}
		}`
	dir := write(t, testManifest, script)
	data := DataDir(root, "test")
	p, err := Load(dir, Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	if firstTitle(t, p, "write") != "ok" {
		t.Fatal("write failed")
	}
	p.Close()
	if b, _ := os.ReadFile(filepath.Join(data, "data", "cache", "token")); string(b) != "t0k" {
		t.Fatalf("on disk: %q", b)
	}
	// It lasts across loads.
	p, err = Load(dir, Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := firstTitle(t, p, "read"); got != "t0k" {
		t.Errorf("read: %q", got)
	}
	if got := firstTitle(t, p, "escape"); got != "EACCES" {
		t.Errorf("escape: %q", got)
	}
	if got := firstTitle(t, p, "fill"); got != "ENOSPC" {
		t.Errorf("fill: %q", got)
	}
	if folderSize(data) > MaxData+(8<<20) {
		t.Errorf("the data folder grew to %d", folderSize(data))
	}

	// Without a data folder, node:fs can't be imported; node:buffer can.
	if _, err := Load(write(t, testManifest, script), Options{}); err == nil {
		t.Error("node:fs without a data folder")
	}
	nofs, err := Load(write(t, testManifest, itemOf+`import { Buffer } from "node:buffer"; export const search = () => item(Buffer.byteLength("é"));`), Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer nofs.Close()
	if got := firstTitle(t, nofs, ""); got != "2" {
		t.Errorf("buffer: %q", got)
	}
}

func TestTypeScript(t *testing.T) {
	m := strings.Replace(testManifest, `"api":1,`, `"api":1,"main":"main.ts",`, 1)
	dir := write(t, m, "")
	os.Remove(filepath.Join(dir, "main.js"))
	os.WriteFile(filepath.Join(dir, "main.ts"), []byte(`
		import { shout } from "./lib.js";
		import type { Thing } from "./types";
		import { twice } from "./more";
		interface Query { text: string }
		export function search(q: Query): Dalibre.Page {
			const t: Thing = { name: shout(q.text) };
			return { items: [{ id: "1", title: twice(t.name), formats: [{ format: "TXT", url: "https://books.example/" }] }] };
		}`), 0o644)
	os.WriteFile(filepath.Join(dir, "lib.ts"), []byte(`export const shout = (s: string): string => s.toUpperCase();`), 0o644)
	os.WriteFile(filepath.Join(dir, "types.ts"), []byte(`export type Thing = { name: string };`), 0o644)
	os.WriteFile(filepath.Join(dir, "more.js"), []byte(`export const twice = (s) => s + s;`), 0o644)
	p, err := Load(dir, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if got := firstTitle(t, p, "ab"); got != "ABAB" {
		t.Errorf("got %q", got)
	}
	os.WriteFile(filepath.Join(dir, "lib.ts"), []byte(`export enum E { A }`), 0o644)
	if _, err := Load(dir, Options{}); err == nil || !strings.Contains(err.Error(), "lib.ts") {
		t.Errorf("enum: %v", err)
	}
}

func TestUninstallRemovesData(t *testing.T) {
	root := t.TempDir()
	if _, err := Install(write(t, testManifest, `exports.search = () => ({ items: [] })`), root); err != nil {
		t.Fatal(err)
	}
	data := DataDir(root, "test")
	os.MkdirAll(data, 0o700)
	os.WriteFile(filepath.Join(data, "x"), []byte("x"), 0o600)
	if ps, errs := Discover(root, nil); len(ps) != 1 || len(errs) != 0 {
		t.Fatalf("discover: %d plugins, %v", len(ps), errs)
	}
	if err := Uninstall(root, "test"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Errorf("data left: %v", err)
	}
}
