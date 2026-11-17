package mcp

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// An agent starts a plugin from the template, writes it, adds it, tries
// it, and reloads it after an edit.
func TestPluginTools(t *testing.T) {
	h := &testHost{lib: testLibrary(t)}
	cs := connect(t, New(h, "test", true))
	dir := filepath.Join(t.TempDir(), "my-plugin")

	if msg := call(t, cs, "create_plugin", map[string]any{"path": "relative", "id": "com.example.mine"}, nil); !strings.Contains(msg, "absolute") {
		t.Errorf("relative path: %q", msg)
	}
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "Not An Id"}, nil); msg == "" {
		t.Error("a bad id made a plugin")
	}
	if _, err := os.Stat(dir); err == nil {
		t.Error("a bad id left a folder")
	}
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "dalibre.mine"}, nil); !strings.Contains(msg, "kept for plugins") {
		t.Errorf("reserved id: %q", msg)
	}
	var co createOut
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "com.example.mine", "name": "Mine"}, &co); msg != "" {
		t.Fatal(msg)
	}
	if co.Folder != dir || co.Docs != PluginDocs || !slices.Contains(co.Files, "plugin.json") || !slices.Contains(co.Files, "dalibre.d.ts") {
		t.Fatalf("created %+v", co)
	}
	if msg := call(t, cs, "create_plugin", map[string]any{"path": dir, "id": "com.example.mine"}, nil); !strings.Contains(msg, "isn’t empty") {
		t.Errorf("a folder with files: %q", msg)
	}

	script := func(title string) {
		os.WriteFile(filepath.Join(dir, "main.js"), []byte(`
			export function search(q) {
				console.log("searching", q.text, q.filters.language);
				if (q.text === "boom") throw dalibre.error("unavailable", "down");
				if (q.text === "bug") null.x;
				return { items: [
					{ id: "1", title: "`+title+`", formats: [{ format: "EPUB", url: "https://api.example.com/1.epub" }], extra: { k: 1 } },
					{ id: "2", title: "", formats: [] },
				] };
			}
			export const get = (item) => ({ ...item, description: "About " + item.title + " " + item.extra.k });
		`), 0o644)
	}
	script("First")
	var ao addPluginOut
	if msg := call(t, cs, "add_plugin", map[string]any{"path": dir}, &ao); msg != "" {
		t.Fatal(msg)
	}
	if ao.Plugin.ID != "com.example.mine" || ao.Plugin.State != PluginLoaded || ao.Plugin.Folder != dir {
		t.Fatalf("added %+v", ao)
	}

	type page struct {
		Items []map[string]any `json:"items"`
	}
	var to struct {
		OK     bool       `json:"ok"`
		Result page       `json:"result"`
		Error  *TestError `json:"error"`
		Logs   []string   `json:"logs"`
	}
	if msg := call(t, cs, "test_plugin", map[string]any{"id": "com.example.mine", "call": "search", "text": "dune", "filters": map[string]string{"language": "fr"}}, &to); msg != "" {
		t.Fatal(msg)
	}
	if !to.OK || len(to.Result.Items) != 1 || to.Result.Items[0]["title"] != "First" {
		t.Fatalf("search: %+v", to)
	}
	// What the script logged, and what Dalibre dropped of its answer.
	logs := strings.Join(to.Logs, "\n")
	if !strings.Contains(logs, "searching dune fr") || !strings.Contains(logs, "warning") {
		t.Errorf("logs: %q", to.Logs)
	}

	// get takes a result back, with the plugin's own extra.
	var gto struct {
		OK     bool           `json:"ok"`
		Result map[string]any `json:"result"`
	}
	if msg := call(t, cs, "test_plugin", map[string]any{"id": "com.example.mine", "call": "get", "item": to.Result.Items[0]}, &gto); msg != "" {
		t.Fatal(msg)
	}
	if gto.Result["description"] != "About First 1" {
		t.Errorf("get: %+v", gto)
	}

	// The script's errors come back with their kind and stack.
	to.Error, to.OK = nil, false
	call(t, cs, "test_plugin", map[string]any{"id": "com.example.mine", "call": "search", "text": "boom"}, &to)
	if to.OK || to.Error == nil || to.Error.Kind != "unavailable" || !strings.Contains(to.Error.Message, "down") {
		t.Errorf("thrown: %+v", to.Error)
	}
	to.Error = nil
	call(t, cs, "test_plugin", map[string]any{"id": "com.example.mine", "call": "search", "text": "bug"}, &to)
	if to.Error == nil || to.Error.Kind != "script" || !strings.Contains(to.Error.Stack, "main.js") {
		t.Errorf("a bug: %+v", to.Error)
	}

	// Mistakes in the call are the tool's errors.
	for args, want := range map[*map[string]any]string{
		{"id": "com.example.mine", "call": "identify"}:                                          "not a metadata source",
		{"id": "com.example.mine", "call": "get"}:                                               "needs item",
		{"id": "com.example.mine", "call": "fly"}:                                               "no call",
		{"id": "com.example.mine", "call": "search", "filters": map[string]string{"sort": "x"}}: "no filter",
		{"id": "com.example.other", "call": "search"}:                                           "add_plugin",
	} {
		if msg := call(t, cs, "test_plugin", *args, nil); !strings.Contains(msg, want) {
			t.Errorf("%v: %q, want %q", *args, msg, want)
		}
	}

	// An edit shows after reloading.
	script("Second")
	var lo PluginList
	if msg := call(t, cs, "reload_plugins", map[string]any{}, &lo); msg != "" || len(lo.Plugins) != 1 {
		t.Fatalf("reload: %q %+v", msg, lo)
	}
	call(t, cs, "test_plugin", map[string]any{"id": "com.example.mine", "call": "search", "text": "dune"}, &to)
	if to.Result.Items[0]["title"] != "Second" {
		t.Errorf("after the edit: %+v", to.Result)
	}

	var po pluginOut
	call(t, cs, "enable_plugin", map[string]any{"id": "com.example.mine", "enabled": false}, &po)
	if po.Plugins[0].State != PluginDisabled {
		t.Errorf("turned off: %+v", po)
	}
	if msg := call(t, cs, "test_plugin", map[string]any{"id": "com.example.mine", "call": "search"}, nil); !strings.Contains(msg, "it is disabled") {
		t.Errorf("testing one turned off: %q", msg)
	}
	call(t, cs, "enable_plugin", map[string]any{"id": "com.example.mine"}, &po)
	if po.Plugins[0].State != PluginLoaded {
		t.Errorf("turned on: %+v", po)
	}
	call(t, cs, "remove_plugin", map[string]any{"id": "com.example.mine"}, &po)
	call(t, cs, "list_plugins", map[string]any{}, &lo)
	if len(po.Plugins) != 0 || len(lo.Plugins) != 0 {
		t.Errorf("removed: %+v %+v", po, lo)
	}
	if _, err := os.Stat(filepath.Join(dir, "main.js")); err != nil {
		t.Error("removing deleted the folder")
	}
}

// Without changes allowed, and without Dalibre's window, there are no
// plugin tools.
func TestPluginToolsNeedChanges(t *testing.T) {
	s := New(&testHost{lib: testLibrary(t)}, "test", false)
	cs := connect(t, s)
	if got := toolNames(t, cs); slices.Contains(got, "add_plugin") {
		t.Errorf("read only: %v", got)
	}
	s.SetAllowChanges(true)
	if got := toolNames(t, cs); !slices.Contains(got, "add_plugin") {
		t.Errorf("changes allowed: %v", got)
	}
	s.SetAllowChanges(false)
	if got := toolNames(t, cs); slices.Contains(got, "test_plugin") {
		t.Errorf("read only again: %v", got)
	}
}
