package plugin

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
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
		{"id":"search","title":"Search","scope":"books"},
		{"id":"device","title":"Device","scope":"books"},
		{"id":"bad","title":"Bad","scope":"books"}
	]},
	"permissions":{"hosts":["example.com"],"library":"read","device":true}}`

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
		if (action === "search") {
			const a = await dalibre.form({ submit: "Search", note: "Opens the search in your browser", fields: [
				{ key: "site", title: "Filter sites…", type: "list", options: [
					{ value: "wook", title: "Wook", group: "Portuguese stores", badge: "Book", starred: true },
					{ value: "goodreads", title: "Goodreads", badge: "ISBN" },
				] },
			]});
			if (!a.site) return { message: "No site chosen" };
			if (a.site === "wook") return { open: ["https://example.com/s?q=1", "https://example.com/s?q=2"] };
			return { open: "https://example.com/g" };
		}
		if (action === "covers") {
			const sizes = [];
			for (const b of input.books) sizes.push(input.covers[b.id] ? (await readFile(input.covers[b.id])).length : 0);
			return { message: sizes.join(",") };
		}
		if (action === "device") {
			const readers = await dalibre.device.list();
			const read = await dalibre.device.read(readers[0].id, "kll", "", "kll.en.zh.klld");
			const content = await readFile(read, "utf8");
			await writeFile("/work/out.klld", content.toUpperCase());
			await dalibre.device.write(readers[0].id, "kll", "", "kll.en.zh.klld", "/work/out.klld");
			return { message: content };
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

// TestListAction covers the list field: its groups, badges and stars come
// through, and open takes one URL or a list of them.
func TestListAction(t *testing.T) {
	p := actionsPlugin(t)
	ctx := context.Background()
	books := []BookInfo{{ID: 4, Title: "A", Authors: []string{"X"}, Formats: []string{"TXT"}}}
	var asked Form
	host := &ActionHost{Form: func(ctx context.Context, f Form) (map[string]any, error) {
		asked = f
		return map[string]any{"site": "wook"}, nil
	}}
	res, err := p.RunAction(ctx, "search", books, nil, host)
	if err != nil {
		t.Fatal(err)
	}
	fd := asked.Fields[0]
	if asked.Note != "Opens the search in your browser" || asked.Submit != "Search" || fd.Type != FieldList ||
		fd.Options[0].Group != "Portuguese stores" || fd.Options[0].Badge != "Book" || !fd.Options[0].Starred || fd.Value != "" {
		t.Errorf("form: %+v note %q", fd, asked.Note)
	}
	if len(res.Open) != 2 || res.Open[0] != "https://example.com/s?q=1" || res.Open[1] != "https://example.com/s?q=2" {
		t.Errorf("open: %+v", res.Open)
	}
	// A list field also answers "" before a row is chosen.
	host.Form = func(ctx context.Context, f Form) (map[string]any, error) { return map[string]any{"site": ""}, nil }
	if res, err = p.RunAction(ctx, "search", books, nil, host); err != nil || res.Message != "No site chosen" {
		t.Errorf("empty: %+v %v", res, err)
	}
	// One URL still works, and a value outside the options doesn't.
	host.Form = func(ctx context.Context, f Form) (map[string]any, error) {
		return map[string]any{"site": "goodreads"}, nil
	}
	if res, err = p.RunAction(ctx, "search", books, nil, host); err != nil || len(res.Open) != 1 || res.Open[0] != "https://example.com/g" {
		t.Errorf("one url: %+v %v", res, err)
	}
	host.Form = func(ctx context.Context, f Form) (map[string]any, error) { return map[string]any{"site": "nope"}, nil }
	if _, err = p.RunAction(ctx, "search", books, nil, host); kind(err) != KindInvalid {
		t.Errorf("bad answer: %v", err)
	}
	// Ahead-of-time answers describe the list as its options.
	if _, err = p.RunAction(ctx, "search", books, nil, &ActionHost{}); kind(err) != KindInvalid || !strings.Contains(err.Error(), "site (list: wook | goodreads)") {
		t.Errorf("missing answers: %v", err)
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
		strings.Join(*res.Changes[1].Metadata.Tags, ",") != "x" || len(res.Open) != 1 || res.Open[0] != "https://example.com/a" {
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

func TestDeviceAction(t *testing.T) {
	p := actionsPlugin(t)
	books := []BookInfo{{ID: 1, Title: "Dune"}}
	var written []byte
	host := &ActionHost{
		Device: DeviceHost{
			List: func(ctx context.Context) ([]DeviceInfo, error) {
				return []DeviceInfo{{ID: "kindle", Kind: "kindle", Name: "Kindle",
					Books: []DeviceBook{{Path: "documents/Dune.azw3"}}}}, nil
			},
			Read: func(ctx context.Context, id, kind, book, name string, dst io.Writer) error {
				if id != "kindle" || kind != "kll" || book != "" || name != "kll.en.zh.klld" {
					t.Errorf("read: %q %q %q %q", id, kind, book, name)
				}
				_, err := io.WriteString(dst, "dictionary")
				return err
			},
			Write: func(ctx context.Context, id, kind, book, name, src string) (err error) {
				written, err = os.ReadFile(src)
				return err
			},
		},
	}
	res, err := p.RunAction(context.Background(), "device", books, nil, host)
	if err != nil {
		t.Fatal(err)
	}
	if res.Message != "dictionary" {
		t.Errorf("message %q", res.Message)
	}
	if len(written) == 0 {
		t.Fatal("nothing written")
	}
	if string(written) != "DICTIONARY" {
		t.Errorf("written = %q", written)
	}
	entries, err := os.ReadDir(filepath.Join(p.dataDir, workFolder))
	if err != nil || len(entries) != 0 {
		t.Fatalf("finished action left work files: %v %v", entries, err)
	}
}

func TestDeviceWithoutHost(t *testing.T) {
	for _, op := range []string{"list", "read", "write"} {
		t.Run(op, func(t *testing.T) {
			script := `import { writeFile } from "node:fs/promises";
				export async function run() {
					await writeFile("/work/out.klld", "dictionary");
					if (` + strconv.Quote(op) + ` === "list") {
						return { message: String((await dalibre.device.list()).length) };
					}
					await dalibre.device.` + op + `("kindle", "kll", "", "kll.en.zh.klld", "/work/out.klld");
					return null;
				}`
			p, err := Load(write(t, actionsManifest, script), Options{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Close)
			res, err := p.RunAction(context.Background(), "device", nil, nil, nil)
			if op == "list" {
				if err != nil || res.Message != "0" {
					t.Fatalf("list without host: %+v %v", res, err)
				}
				res.Done()
			} else if kind(err) != KindUnavailable {
				t.Fatalf("%s without host: %v", op, err)
			}
		})
	}
}

func TestCancelledDeviceRequests(t *testing.T) {
	p := actionsPlugin(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	host := &ActionHost{Device: DeviceHost{
		List: func(context.Context) ([]DeviceInfo, error) { t.Error("listed after cancel"); return nil, nil },
		Read: func(context.Context, string, string, string, string, io.Writer) error {
			t.Error("read after cancel")
			return nil
		},
		Write: func(context.Context, string, string, string, string, string) error {
			t.Error("wrote after cancel")
			return nil
		},
	}}
	for _, op := range []string{"device.list", "device.read", "device.write"} {
		if _, err := p.ask(ctx, host, nil, op, `{}`); !errors.Is(err, context.Canceled) {
			t.Errorf("%s: %v", op, err)
		}
	}
}

func TestDeviceNeedsPermission(t *testing.T) {
	p := actionsPlugin(t)
	p.Manifest.Permissions.Device = false
	for _, op := range []string{"device.list", "device.read", "device.write"} {
		if _, err := p.ask(context.Background(), &ActionHost{}, nil, op, `{}`); kind(err) != KindPermission {
			t.Errorf("%s without permission: %v", op, err)
		}
	}
}

func TestDeviceReadFailureCleansWork(t *testing.T) {
	p := actionsPlugin(t)
	want := errors.New("reader disconnected")
	host := &ActionHost{Device: DeviceHost{
		Read: func(ctx context.Context, id, kind, book, name string, dst io.Writer) error {
			if _, err := io.WriteString(dst, "partial"); err != nil {
				t.Fatal(err)
			}
			return want
		},
	}}
	_, err := p.ask(context.Background(), host, nil, "device.read", `{"name":"kll.en.zh.klld"}`)
	if !errors.Is(err, want) {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(p.dataDir, workFolder))
	if err != nil || len(entries) != 0 {
		t.Fatalf("failed read left work files: %v %v", entries, err)
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
