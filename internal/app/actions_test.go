package app

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/mcp"
	"dalibre/internal/plugin"
)

// actionsApp is a test app with two plugins that have actions: Find
// Duplicates, which may only read the library, and Tidy, which may
// change it.
func actionsApp(t *testing.T) *App {
	t.Helper()
	a := newTestApp(t)
	load := func(name, manifest, script string) {
		dir := filepath.Join(t.TempDir(), name)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644)
		os.WriteFile(filepath.Join(dir, "main.js"), []byte(script), 0o644)
		p, err := plugin.Load(dir, plugin.Options{DataDir: filepath.Join(t.TempDir(), "own")})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(p.Close)
		a.plugins = append(a.plugins, p)
	}
	load("dups", `{"id":"com.example.dups","name":"Find Duplicates","version":"1.0.0","api":1,
		"capabilities":{"actions":[
			{"id":"find","title":"Find duplicates…","icon":"copy","scope":"library"},
			{"id":"exempt","title":"Not duplicates","icon":"x","scope":"books","books":{"min":2},"in":"results"},
			{"id":"tag","title":"Tag as checked","icon":"tag","scope":"books"}
		]},
		"permissions":{"hosts":[],"library":"read"}}`, `
		export async function run(action, input) {
			if (action === "exempt") return { message: input.books.length + " books left alone" };
			if (action === "tag") return { changes: input.books.map((b) => ({ book: b.id, metadata: { tags: [...b.tags, "Checked"], publisher: "Ace" } })) };
			const { by, strict } = await dalibre.form({ submit: "Find duplicates", fields: [
				{ key: "by", title: "Compare", type: "select", value: "author",
				  options: [{ value: "author", title: "Author" }, { value: "title", title: "Title and author" }] },
				{ key: "strict", title: "Ignore case and punctuation", type: "toggle", value: true },
			]});
			const seen = new Map();
			for await (const b of dalibre.library.books()) {
				const k = by === "author" ? b.authors.join() : b.title;
				seen.set(k, [...(seen.get(k) ?? []), b]);
			}
			const groups = [...seen.entries()].filter(([, bs]) => bs.length > 1)
				.map(([k, bs]) => ({ title: k, books: bs.map((b) => ({ id: b.id, note: "Same " + by })) }));
			return groups.length ? { groups, message: "Compared by " + by } : null;
		}`)
	load("tidy", `{"id":"com.example.tidy","name":"Tidy","version":"1.0.0","api":1,
		"capabilities":{"actions":[
			{"id":"shout","title":"Shout titles","scope":"books"},
			{"id":"merge","title":"Merge into one book","icon":"layers","scope":"books","books":{"min":2},"formats":["EPUB"],"files":true}
		]},
		"permissions":{"hosts":[],"library":"write"}}`, `
		import { readFile, writeFile } from "node:fs/promises";
		export async function run(action, input) {
			if (action === "shout") return { changes: input.books.map((b) => ({ book: b.id, metadata: { title: b.title.toUpperCase() } })) };
			let text = "";
			for (const b of input.books) text += await readFile(input.files[b.id].path, "utf8");
			await writeFile("/work/merged.epub", text);
			return { add: [{ path: "/work/merged.epub", metadata: { title: "Merged", authors: ["Several"] } }] };
		}`)
	return a
}

func (a *App) pluginByName(name string) *plugin.Plugin {
	for _, p := range a.plugins {
		if p.Manifest.Name == name {
			return p
		}
	}
	return nil
}

// TestLibraryActionShowsResults runs a library action from the sidebar's
// Tools: its form is answered, and the books it found show as a list in
// its groups, where the plugin's own action for them comes first.
func TestLibraryActionShowsResults(t *testing.T) {
	a := actionsApp(t)
	var asked plugin.Form
	a.act.answer = func(f plugin.Form) (map[string]any, error) {
		asked = f
		return map[string]any{"by": "author", "strict": true}, nil
	}
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	for _, s := range []string{"TOOLS", "Find duplicates…"} {
		if !tt.HasText(s) {
			t.Fatalf("sidebar misses %q; texts %q", s, tt.Texts())
		}
	}
	if err := tt.Click("Find duplicates…"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(asked.Fields) != 2 {
		t.Fatalf("form %+v", asked)
	}
	r := a.act.results
	if r == nil || len(r.rows) != 3 || r.rows[0].header != "Liu Cixin" || r.rows[0].count != 2 || r.rows[1].note != "Same author" {
		t.Fatalf("results %+v", r)
	}
	for _, s := range []string{"Find duplicates", "1 group", "Liu Cixin", "2 books", "The Three-Body Problem", "Same author", "Compared by author", "Run again"} {
		if !tt.HasText(s) {
			t.Errorf("results miss %q; texts %q", s, tt.Texts())
		}
	}
	if tt.HasText("Dune") {
		t.Error("a book the action didn't find shows")
	}
	shot(t, tt, "action-results")

	// The plugin's action for its results heads the book menu; it takes
	// two books, so one greys it.
	if err := tt.Click("The Three-Body Problem"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b := a.current(); b == nil || b.Title != "The Three-Body Problem" {
		t.Fatalf("current %+v", b)
	}
	if err := tt.RightClick("The Three-Body Problem"); err != nil {
		t.Fatal(err)
	}
	menu := tt.Menu()
	if i, j := slices.Index(menu, "Not duplicates"), slices.Index(menu, "Plugins"); i < 0 || j < 0 || i > j {
		t.Errorf("menu %q", menu)
	}
	shot(t, tt, "action-results-menu")
	tt.CloseMenu()
	a.chosen.Add(bookID(t, a, "The Three-Body Problem"))
	a.chosen.Add(bookID(t, a, "The Dark Forest"))
	tt.Frame()
	shot(t, tt, "action-results-selection")
	if err := tt.Click("Not duplicates"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Not duplicates · 2 books left alone") {
		t.Errorf("not told; texts %q", tt.Texts())
	}

	// A book that goes leaves the results; the shelf takes them away.
	if err := a.lib.RemoveBooks([]int64{bookID(t, a, "The Dark Forest")}, nil); err != nil {
		t.Fatal(err)
	}
	a.reload(nil)
	tt.Frame()
	if len(a.visible()) != 1 || !tt.HasText("1 book") {
		t.Errorf("after removing: %d shown", len(a.visible()))
	}
	if err := tt.Click("All books"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.act.results != nil || !tt.HasText("Dune") {
		t.Error("the library didn't come back")
	}
}

// TestActionFormDialog asks the form in a dialog: the action waits for
// the answer, and Cancel ends it quietly.
func TestActionFormDialog(t *testing.T) {
	a := actionsApp(t)
	posts := make(chan func(), 64)
	a.post = func(fn func()) { posts <- fn }
	a.run = func(fn func()) { go fn() }
	t.Cleanup(func() {
		if a.act.cancel != nil {
			a.act.cancel() // a form left open ends with its run
		}
	})
	tt := ui.NewTester(a.View, 1440, 900)
	wait := func(what string, done func() bool) {
		t.Helper()
		deadline := time.After(10 * time.Second)
		for !done() {
			select {
			case fn := <-posts:
				fn()
			case <-deadline:
				t.Fatalf("waited for %s", what)
			}
		}
		tt.Frame()
	}
	tt.Frame()
	if err := tt.Click("Find duplicates…"); err != nil {
		t.Fatal(err)
	}
	wait("the form", func() bool { return a.act.form != nil })
	for _, s := range []string{"Find Duplicates", "Compare", "Author", "Ignore case and punctuation", "Cancel", "Stop Find duplicates"} {
		if !tt.HasText(s) {
			t.Errorf("form misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "action-form")
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	wait("the run to end", func() bool { return a.busy == "" })
	wait("the library", func() bool { return len(posts) == 0 })
	if a.act.results != nil || a.act.form != nil {
		t.Error("a cancelled form left something")
	}
	for _, s := range tt.Texts() {
		if strings.Contains(s, "failed") || strings.Contains(s, "Nothing to change") {
			t.Errorf("cancelling said %q", s)
		}
	}

	// Answered, it finds.
	if err := tt.Click("Find duplicates…"); err != nil {
		t.Fatal(err)
	}
	wait("the form", func() bool { return a.act.form != nil })
	a.answerForm(true)
	wait("the results", func() bool { return a.act.results != nil })
	if !tt.HasText("Compared by author") {
		t.Errorf("results: %q", tt.Texts())
	}
}

// TestActionChanges writes a plugin's changes at once when it may change
// books, with Undo, and shows them first when it may only read.
func TestActionChanges(t *testing.T) {
	a := actionsApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	dune := bookID(t, a, "Dune")

	a.runAction(a.pluginByName("Tidy"), a.pluginByName("Tidy").Manifest.Action("shout"), []int64{dune})
	tt.Frame()
	if a.byID[dune].Title != "DUNE" || !tt.HasText("Shout titles · 1 book changed") {
		t.Fatalf("title %q; texts %q", a.byID[dune].Title, tt.Texts())
	}
	if err := tt.Click("Undo"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.byID[dune].Title != "Dune" {
		t.Errorf("after Undo: %q", a.byID[dune].Title)
	}

	// Find Duplicates may only read: its changes wait in a dialog.
	dups := a.pluginByName("Find Duplicates")
	ids := []int64{dune, bookID(t, a, "Piranesi")}
	a.runAction(dups, dups.Manifest.Action("tag"), ids)
	tt.Frame()
	rv := a.act.review
	if rv == nil || len(rv.items) != 2 || len(rv.items[0].lines) != 2 || slices.Contains(a.byID[dune].Tags, "Checked") {
		t.Fatalf("review %+v", rv)
	}
	for _, s := range []string{"Tag as checked", "Classics, Science Fiction", "Classics, Science Fiction, Checked", "Ace", "2 books · 4 changes", "Apply changes"} {
		if !tt.HasText(s) {
			t.Errorf("review misses %q; texts %q", s, tt.Texts())
		}
	}
	shot(t, tt, "action-review")
	rv.items[1].on = false // leave Piranesi
	tt.Frame()
	if !tt.HasText("1 book · 2 changes") {
		t.Errorf("count: %q", tt.Texts())
	}
	if err := tt.Click("Apply changes"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b := a.byID[dune]; !slices.Contains(b.Tags, "Checked") || b.Publisher != "Ace" {
		t.Errorf("Dune: %v %q", b.Tags, b.Publisher)
	}
	if b := a.byID[ids[1]]; slices.Contains(b.Tags, "Checked") {
		t.Errorf("Piranesi changed: %v", b.Tags)
	}
	if !tt.HasText("Tag as checked · 1 book changed") {
		t.Errorf("not told; texts %q", tt.Texts())
	}
}

// TestActionAddsBooks gives an action the books' files and adds the file
// it makes as a new book.
func TestActionAddsBooks(t *testing.T) {
	a := actionsApp(t)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tidy := a.pluginByName("Tidy")
	merge := tidy.Manifest.Action("merge")
	one, two := bookID(t, a, "Dune"), bookID(t, a, "Piranesi")
	if a.takes(merge, []int64{one}) || !a.takes(merge, []int64{one, two}) {
		t.Fatal("takes")
	}
	before := len(a.books)
	a.runAction(tidy, merge, []int64{one, two})
	tt.Frame()
	if len(a.books) != before+1 || fileOf(t, a, "Merged", "EPUB") != "xx" {
		t.Fatalf("%d books", len(a.books))
	}
	if b := a.byID[bookID(t, a, "Merged")]; strings.Join(b.Authors, ",") != "Several" {
		t.Errorf("authors %v", b.Authors)
	}
	if !tt.HasText("Merge into one book · 1 book added") {
		t.Errorf("not told; texts %q", tt.Texts())
	}
	if err := tt.Click("Show"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if b := a.current(); b == nil || b.Title != "Merged" {
		t.Errorf("shown %+v", b)
	}
}

// TestActionsForAgents runs actions as an agent does: forms answered
// ahead, and nothing written until it says to apply.
func TestActionsForAgents(t *testing.T) {
	a := actionsApp(t)
	h := mcpHost{a}
	ctx := t.Context()
	infos := h.Actions(ctx)
	if len(infos) != 5 || infos[0].ID != "find" || infos[0].Title != "Find duplicates" || infos[1].MinBooks != 2 || !infos[3].ChangesBooks || infos[0].ChangesBooks {
		t.Fatalf("actions %+v", infos)
	}
	// A form nobody answered says what it asks.
	_, err := h.RunAction(ctx, mcp.ActionRun{Plugin: "com.example.dups", Action: "find"})
	if errKind(err) != plugin.KindInvalid || !strings.Contains(err.Error(), "by (select: author | title), strict (toggle)") {
		t.Fatalf("no answers: %v", err)
	}
	out, err := h.RunAction(ctx, mcp.ActionRun{Plugin: "com.example.dups", Action: "find", Answers: map[string]any{"by": "author", "strict": true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Groups) != 1 || out.Groups[0].Title != "Liu Cixin" || len(out.Groups[0].Books) != 2 || out.Groups[0].Books[0].Title == "" || out.Message != "Compared by author" {
		t.Fatalf("found %+v", out)
	}
	if a.act.results != nil {
		t.Error("an agent's run changed what the window shows")
	}

	// Changes are returned, and written only with apply.
	dune := bookID(t, a, "Dune")
	run := mcp.ActionRun{Plugin: "com.example.dups", Action: "tag", Books: []int64{dune}}
	if out, err = h.RunAction(ctx, run); err != nil {
		t.Fatal(err)
	}
	if out.Applied || len(out.Changes) != 1 || out.Changes[0].Book.Title != "Dune" || len(out.Changes[0].Changes) != 2 ||
		out.Changes[0].Changes[0] != (mcp.FieldChange{Field: "tags", From: "Classics, Science Fiction", To: "Classics, Science Fiction, Checked"}) {
		t.Fatalf("dry run %+v", out)
	}
	a.reload(nil)
	if slices.Contains(a.byID[dune].Tags, "Checked") {
		t.Fatal("a dry run wrote")
	}
	run.Apply = true
	if out, err = h.RunAction(ctx, run); err != nil {
		t.Fatal(err)
	}
	if !out.Applied || len(out.Changed) != 1 || out.Changed[0].ID != dune || !slices.Contains(a.byID[dune].Tags, "Checked") {
		t.Fatalf("applied %+v; tags %v", out, a.byID[dune].Tags)
	}

	// Files: named before, added with apply.
	merge := mcp.ActionRun{Plugin: "com.example.tidy", Action: "merge", Books: []int64{dune, bookID(t, a, "Piranesi")}}
	before := len(a.books)
	if out, err = h.RunAction(ctx, merge); err != nil || len(out.Files) != 1 || out.Files[0].Format != "EPUB" || len(a.books) != before {
		t.Fatalf("dry merge %+v %v", out, err)
	}
	merge.Apply = true
	if out, err = h.RunAction(ctx, merge); err != nil || len(out.Added) != 1 || out.Added[0].Title != "Merged" || len(a.books) != before+1 {
		t.Fatalf("merge %+v %v", out, err)
	}

	// What is wrong with the call isn't the plugin's error.
	for _, bad := range []mcp.ActionRun{
		{Plugin: "nope", Action: "find"}, {Plugin: "com.example.dups", Action: "nope"},
		{Plugin: "com.example.tidy", Action: "merge", Books: []int64{dune}}, {Plugin: "com.example.tidy", Action: "shout", Books: []int64{99999}},
	} {
		if _, err := h.RunAction(ctx, bad); err == nil || errKind(err) != "" {
			t.Errorf("%+v: %v", bad, err)
		}
	}
}
