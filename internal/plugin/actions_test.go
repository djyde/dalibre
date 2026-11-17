package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const actionsManifest = `{"id":"test.actions","name":"Actions","version":"1.0.0","api":1,
	"capabilities":{"actions":[
		{"id":"find","title":"Find duplicates…","icon":"copy","scope":"library"},
		{"id":"tag","title":"Tag","scope":"books","books":{"min":2}},
		{"id":"merge","title":"Merge","scope":"books","formats":["TXT"],"files":true},
		{"id":"covers","title":"Covers","scope":"books","covers":true},
		{"id":"bad","title":"Bad","scope":"books"}
	]},
	"permissions":{"hosts":["example.com"],"library":"read"}}`

const actionsScript = `
	import { readFile, writeFile } from "node:fs/promises";
	export async function run(action, input) {
		if (action === "find") {
			const { by, loose } = await dalibre.form({ submit: "Find", fields: [
				{ key: "by", title: "Compare", type: "select", options: [{ value: "title", title: "Title" }, { value: "isbn" }] },
				{ key: "loose", title: "Loose", type: "toggle", value: true },
			]});
			const seen = new Map();
			let n = 0;
			for await (const b of dalibre.library.books()) {
				dalibre.progress(++n, 0);
				const k = loose ? b.title.toLowerCase() : b.title;
				seen.set(k, [...(seen.get(k) ?? []), b]);
			}
			const groups = [...seen.values()].filter((bs) => bs.length > 1)
				.map((bs) => ({ title: bs[0].title, books: bs.map((b) => ({ id: b.id, note: by })) }));
			return groups.length ? { groups, message: n + " books" } : { message: "No duplicates" };
		}
		if (action === "tag") {
			return { changes: input.books.map((b) => ({ book: b.id, metadata: { tags: [...b.tags, "x"] } })), open: "https://example.com/a" };
		}
		if (action === "merge") {
			let text = "";
			for (const b of input.books) text += await readFile(input.files[b.id].path, "utf8");
			await writeFile("/work/merged.txt", text);
			await writeFile("/work/extra.txt", "extra");
			return { add: [{ path: "/work/merged.txt", metadata: { title: "Merged" } }], formats: [{ book: input.books[0].id, path: "/work/extra.txt" }] };
		}
		if (action === "covers") {
			const sizes = [];
			for (const b of input.books) sizes.push(input.covers[b.id] ? (await readFile(input.covers[b.id])).length : 0);
			return { message: sizes.join(",") };
		}
		if (input.books.length === 1) return { open: "https://elsewhere.org/" };
		return { nope: 1 };
	}`

func actionsPlugin(t *testing.T) *Plugin {
	t.Helper()
	p, err := Load(write(t, actionsManifest, actionsScript), Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func library(n int) func(after int64, limit int, comments bool) ([]BookInfo, error) {
	return func(after int64, limit int, comments bool) ([]BookInfo, error) {
		var out []BookInfo
		for id := after + 1; id <= int64(n) && len(out) < limit; id++ {
			title := "Book " + string(rune('A'+id%3))
			if id > 6 {
				title = "Other " + strings.Repeat("x", int(id))
			}
			out = append(out, BookInfo{ID: id, Title: title})
		}
		return out, nil
	}
}

func TestLibraryAction(t *testing.T) {
	p := actionsPlugin(t)
	ctx := context.Background()
	var asked Form
	progress := 0
	host := &ActionHost{
		Books: library(1203), // more than two pages
		Form: func(ctx context.Context, f Form) (map[string]any, error) {
			asked = f
			return map[string]any{"by": "isbn", "loose": true}, nil
		},
		Progress: func(done, total int) { progress = max(progress, done) },
	}
	res, err := p.RunAction(ctx, "find", nil, nil, host)
	if err != nil {
		t.Fatal(err)
	}
	if asked.Submit != "Find" || len(asked.Fields) != 2 || asked.Fields[0].Value != "title" || asked.Fields[1].Value != true || asked.Fields[0].Options[1].Title != "isbn" {
		t.Errorf("form: %+v", asked)
	}
	if len(res.Groups) != 3 || len(res.Groups[0].Books) != 2 || res.Groups[0].Books[0].Note != "isbn" || res.Message != "1203 books" {
		t.Errorf("groups: %+v %q", res.Groups, res.Message)
	}
	if progress == 0 {
		t.Error("no progress")
	}

	// Cancelling the form ends the run with its own kind.
	host.Form = func(context.Context, Form) (map[string]any, error) { return nil, ErrCancelled }
	if _, err := p.RunAction(ctx, "find", nil, nil, host); kind(err) != KindCancelled {
		t.Errorf("cancelled: %v", err)
	}
	// Without a window, answers given ahead answer the form, and missing
	// ones say what to give.
	res, err = p.RunAction(ctx, "find", nil, nil, &ActionHost{Books: library(3), Answers: map[string]any{"by": "title", "loose": false}})
	if err != nil || res.Message != "No duplicates" || len(res.Groups) != 0 {
		t.Errorf("answers: %+v %v", res, err)
	}
	_, err = p.RunAction(ctx, "find", nil, nil, &ActionHost{Answers: map[string]any{"by": "title"}})
	if kind(err) != KindInvalid || !strings.Contains(err.Error(), "by (select: title | isbn), loose (toggle)") {
		t.Errorf("missing answers: %v", err)
	}
	if _, err = p.RunAction(ctx, "find", nil, nil, &ActionHost{Answers: map[string]any{"by": "author", "loose": true}}); kind(err) != KindInvalid {
		t.Errorf("bad answer: %v", err)
	}
}

func TestFormStopsTheClock(t *testing.T) {
	p := actionsPlugin(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	host := &ActionHost{Form: func(ctx context.Context, f Form) (map[string]any, error) {
		p.limit.mu.Lock()
		paused := p.limit.paused
		p.limit.mu.Unlock()
		if paused != 1 {
			t.Errorf("paused %d", paused)
		}
		time.Sleep(20 * time.Millisecond)
		return map[string]any{"by": "title", "loose": true}, nil
	}}
	if _, err := p.RunAction(ctx, "find", nil, nil, host); err != nil {
		t.Fatal(err)
	}
	// A form left open ends when the run is cancelled.
	host.Form = func(ctx context.Context, f Form) (map[string]any, error) {
		cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	_, err := p.RunAction(ctx, "find", nil, nil, host)
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != KindTimeout || !errors.Is(pe.Err, context.Canceled) {
		t.Errorf("cancelled run: %v", err)
	}
}

func TestBooksAction(t *testing.T) {
	p := actionsPlugin(t)
	ctx := context.Background()
	books := []BookInfo{{ID: 4, Title: "A", Tags: []string{"t"}, Formats: []string{"TXT"}}, {ID: 9, Title: "B", Formats: []string{"TXT"}}}

	res, err := p.RunAction(ctx, "tag", books, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Changes) != 2 || res.Changes[0].Book != 4 || strings.Join(*res.Changes[0].Metadata.Tags, ",") != "t,x" ||
		strings.Join(*res.Changes[1].Metadata.Tags, ",") != "x" || res.Open != "https://example.com/a" {
		t.Errorf("changes: %+v", res)
	}

	files := map[int64]string{}
	for _, b := range books {
		f := filepath.Join(t.TempDir(), b.Title+".txt")
		os.WriteFile(f, []byte("text of "+b.Title+";"), 0o644)
		files[b.ID] = f
	}
	res, err = p.RunAction(ctx, "merge", books, files, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Add) != 1 || len(res.Formats) != 1 || *res.Add[0].Metadata.Title != "Merged" || res.Formats[0].Book != 4 || res.Formats[0].Format != "TXT" {
		t.Fatalf("files: %+v", res)
	}
	if b, _ := os.ReadFile(res.Add[0].File); string(b) != "text of A;text of B;" {
		t.Errorf("merged: %q", b)
	}
	if b, _ := os.ReadFile(res.Formats[0].File); string(b) != "extra" {
		t.Errorf("format: %q", b)
	}
	if left, _ := os.ReadDir(filepath.Join(p.dataDir, workFolder)); len(left) != 0 {
		t.Errorf("/work kept %d entries", len(left))
	}
	res.Done()
	if _, err := os.Stat(res.Add[0].File); !os.IsNotExist(err) {
		t.Error("Done left the file")
	}

	cover := filepath.Join(t.TempDir(), "cover.jpg")
	os.WriteFile(cover, []byte("jpeg"), 0o644)
	res, err = p.RunAction(ctx, "covers", books, nil, &ActionHost{Covers: map[int64]string{4: cover}})
	if err != nil || res.Message != "4,0" {
		t.Errorf("covers: %+v %v", res, err)
	}

	if _, err := p.RunAction(ctx, "bad", books[:1], nil, nil); kind(err) != KindInvalid || !strings.Contains(err.Error(), "permissions.hosts") {
		t.Errorf("open elsewhere: %v", err)
	}
	if _, err := p.RunAction(ctx, "bad", books, nil, nil); kind(err) != KindInvalid {
		t.Errorf("unknown field: %v", err)
	}
	if _, err := p.RunAction(ctx, "none", books, nil, nil); kind(err) != KindInvalid {
		t.Errorf("unknown action: %v", err)
	}
}

func TestActionManifests(t *testing.T) {
	m, err := ParseManifest([]byte(actionsManifest))
	if err != nil {
		t.Fatal(err)
	}
	a := m.Action("tag")
	if a.Takes([][]string{{"EPUB"}}) || !a.Takes([][]string{{"EPUB"}, {}}) || m.Action("find").Takes([][]string{{"EPUB"}}) {
		t.Error("books bounds")
	}
	if a := m.Action("merge"); !a.Takes([][]string{{"TXT", "EPUB"}}) || a.Takes([][]string{{"TXT"}, {"EPUB"}}) {
		t.Error("formats")
	}
	if l := m.Action("find").Label(); l != "Find duplicates" {
		t.Errorf("label %q", l)
	}
	for name, actions := range map[string]string{
		"no library":     `[{"id":"a","title":"A","scope":"books"}]`,
		"bad scope":      `[{"id":"a","title":"A","scope":"all"}]`,
		"bad id":         `[{"id":"A b","title":"A","scope":"books"}]`,
		"twice":          `[{"id":"a","title":"A","scope":"books"},{"id":"a","title":"B","scope":"books"}]`,
		"bad icon":       `[{"id":"a","title":"A","scope":"books","icon":"skull"}]`,
		"library books":  `[{"id":"a","title":"A","scope":"library","files":true}]`,
		"bad in":         `[{"id":"a","title":"A","scope":"books","in":"menu"}]`,
		"lower format":   `[{"id":"a","title":"A","scope":"books","formats":["epub"]}]`,
		"only ellipsis":  `[{"id":"a","title":"…","scope":"books"}]`,
		"max under min":  `[{"id":"a","title":"A","scope":"books","books":{"min":3,"max":2}}]`,
		"unknown member": `[{"id":"a","title":"A","scope":"books","options":[]}]`,
	} {
		lib := `"read"`
		if name == "no library" {
			lib = `""`
		}
		_, err := ParseManifest([]byte(`{"id":"t.a","name":"A","version":"1.0.0","api":1,"capabilities":{"actions":` + actions + `},"permissions":{"hosts":[],"library":` + lib + `}}`))
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A plugin with actions exports run.
	dir := write(t, `{"id":"t.norun","name":"A","version":"1.0.0","api":1,"capabilities":{"actions":[{"id":"a","title":"A","scope":"books"}]},"permissions":{"hosts":[],"library":"read"}}`, `export function other() {}`)
	if _, err := Load(dir, Options{}); kind(err) != KindInvalid {
		t.Errorf("no run: %v", err)
	}
}

func TestFormOutsideAnAction(t *testing.T) {
	dir := write(t, fileTypeManifest, `export async function process(file) { await dalibre.form({ fields: [{ key: "a", title: "A", type: "text" }] }); return null; }`)
	p, err := Load(dir, Options{DataDir: filepath.Join(t.TempDir(), "own")})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	f := filepath.Join(t.TempDir(), "a.txt")
	os.WriteFile(f, []byte("x"), 0o644)
	if _, err := p.Process(context.Background(), f, ReasonManual, nil); kind(err) != KindInvalid || !strings.Contains(err.Error(), "dalibre.form is for an action") {
		t.Errorf("form in process: %v", err)
	}
}
