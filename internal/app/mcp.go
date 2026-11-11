package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"dalibre/internal/calibre"
	"dalibre/internal/mcp"
	"dalibre/internal/plugin"
)

// Serving the library to the user's own agents over MCP (docs/mcp/design.md):
// a server on this computer's loopback address, started and stopped by
// Settings, that agents reach with the token kept in the settings.

// mcpState is the MCP server, while it runs or failed to start.
type mcpState struct {
	server *http.Server
	mcp    *mcp.Server
	port   int
	err    error
}

// applyMCP starts, stops or restarts the MCP server to match the
// settings. It makes the token when there is none.
func (a *App) applyMCP() {
	cfg := &a.settings.MCP
	if cfg.On && cfg.Token == "" {
		cfg.Token = mcp.NewToken()
		a.saveSettings()
	}
	port := cfg.ServerPort()
	if cur := a.mcpSrv; cur != nil && cur.server != nil {
		if cfg.On && cur.port == port {
			cur.mcp.SetAllowChanges(cfg.AllowsChanges())
			cur.mcp.SetToken(cfg.Token)
			return
		}
		srv := cur.server
		go srv.Close()
	}
	a.mcpSrv = nil
	if !cfg.On {
		return
	}
	st := &mcpState{port: port}
	a.mcpSrv = st
	s := mcp.New(mcpHost{a}, a.au.version, cfg.AllowsChanges())
	s.SetToken(cfg.Token)
	s.OnUse = func() { a.post(func() {}) } // the status shows who calls what
	a.run(func() {
		ln, err := mcp.Listen(port)
		srv := &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
		a.post(func() {
			if a.mcpSrv != st {
				if ln != nil {
					ln.Close()
				}
				return
			}
			if err != nil {
				st.err = err
				return
			}
			st.server, st.mcp = srv, s
			go srv.Serve(ln)
		})
	})
}

// mcpStatus is how the MCP server is, as Settings shows it.
type mcpStatus struct {
	Running bool
	Port    int
	// Err is why the server didn't start; PortInUse when another program
	// has the port.
	Err       error
	PortInUse bool
	// Client is the name the agent that called a tool last gave in its
	// handshake, as "claude-code"; Used is when, zero when none has.
	Client string
	Used   time.Time
	// Active are the agents calling tools now or lately, the most recent
	// first.
	Active []mcp.Agent
}

func (a *App) mcpStatus() mcpStatus {
	st := a.mcpSrv
	if st == nil {
		return mcpStatus{Port: a.settings.MCP.ServerPort()}
	}
	out := mcpStatus{Running: st.server != nil, Port: st.port, Err: st.err, PortInUse: errors.Is(st.err, syscall.EADDRINUSE)}
	if st.mcp != nil {
		out.Client, out.Used = st.mcp.LastUse()
		now := time.Now()
		for _, ag := range st.mcp.Agents() {
			if ag.Active(now) {
				out.Active = append(out.Active, ag)
			}
		}
	}
	return out
}

// mcpAgents are the agents that called tools since the server started,
// the most recent first.
func (a *App) mcpAgents() []mcp.Agent {
	if st := a.mcpSrv; st != nil && st.mcp != nil {
		return st.mcp.Agents()
	}
	return nil
}

// mcpCalls are the last n tool calls, the newest first; all the server
// keeps when n <= 0.
func (a *App) mcpCalls(n int) []mcp.Call {
	if st := a.mcpSrv; st != nil && st.mcp != nil {
		return st.mcp.Calls(n)
	}
	return nil
}

// clearMCPCalls forgets the tool calls so far.
func (a *App) clearMCPCalls() {
	if st := a.mcpSrv; st != nil && st.mcp != nil {
		st.mcp.ClearCalls()
	}
}

// mcpEndpoint is the address agents connect to.
func (a *App) mcpEndpoint() string { return mcp.Endpoint(a.settings.MCP.ServerPort()) }

// mcpToken is the token agents send.
func (a *App) mcpToken() string { return a.settings.MCP.Token }

// regenerateMCPToken replaces the token; the old one stops working at
// once, and agents set up with it need the new one.
func (a *App) regenerateMCPToken() {
	a.settings.MCP.Token = mcp.NewToken()
	a.saveSettings()
	if st := a.mcpSrv; st != nil && st.mcp != nil {
		st.mcp.SetToken(a.settings.MCP.Token)
	}
}

// mcpSnippet is how one agent is set up to use the server.
type mcpSnippet struct {
	Client string // "Claude Code"
	Text   string
}

// mcpSnippets set up Claude Code, Claude Desktop and Cursor. Claude
// Desktop starts stdio servers only, so it runs this app's stdio bridge,
// which reads the token from the settings.
func (a *App) mcpSnippets() []mcpSnippet {
	url, token := a.mcpEndpoint(), a.mcpToken()
	exe, err := os.Executable()
	if err != nil {
		exe = "Dalibre"
	}
	indent := func(v any) string {
		b, _ := json.MarshalIndent(v, "", "  ")
		return string(b)
	}
	type obj = map[string]any
	return []mcpSnippet{
		{"Claude Code", `claude mcp add --transport http dalibre ` + url + ` --header "Authorization: Bearer ` + token + `"`},
		{"Claude Desktop", indent(obj{"mcpServers": obj{"dalibre": obj{"command": exe, "args": []string{"mcp"}}}})},
		{"Cursor", indent(obj{"mcpServers": obj{"dalibre": obj{"url": url, "headers": obj{"Authorization": "Bearer " + token}}}})},
	}
}

// mcpHost is the app as the MCP server sees it. Its methods run on the
// server's goroutines.
type mcpHost struct{ a *App }

func (h mcpHost) Library() *calibre.Library { return h.a.libShared.Load() }

func (h mcpHost) Changed(ids ...int64) {
	h.a.post(func() {
		for _, id := range ids {
			h.a.covers.forget(id)
		}
		h.a.reload(nil)
	})
}

func (h mcpHost) Trash() func(path string) error { return h.a.trash }

// onMain runs fn on the main thread and waits for it.
func (h mcpHost) onMain(ctx context.Context, fn func()) bool {
	done := make(chan struct{})
	h.a.post(func() {
		fn()
		close(done)
	})
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

func (h mcpHost) BookSources(ctx context.Context) []*plugin.Plugin {
	var out []*plugin.Plugin
	if h.onMain(ctx, func() { out = h.a.sources() }) {
		return out
	}
	return nil
}

// Added calls the hooks for a file an agent added, as adding books in the
// window does.
func (h mcpHost) Added(ctx context.Context, id int64, format string, newBook bool) {
	var after []*plugin.Plugin
	if lib := h.Library(); lib != nil && h.onMain(ctx, func() { after = h.a.hookPlugins() }) {
		afterAdd(ctx, lib, after, id, format, newBook)
	}
}

// FormatRemoved calls the postdelete hooks for a format an agent removed.
func (h mcpHost) FormatRemoved(ctx context.Context, id int64, format string) {
	var after []*plugin.Plugin
	if lib := h.Library(); lib != nil && h.onMain(ctx, func() { after = h.a.hookPlugins() }) {
		runHooks(ctx, lib, after, plugin.HookPostDelete, id, format)
	}
}

// ProcessImport runs the file type plugins on a file an agent adds, as
// adding books in the window does.
func (h mcpHost) ProcessImport(ctx context.Context, file string) (string, func(), []error) {
	var hooks []*plugin.Plugin
	if !h.onMain(ctx, func() { hooks = h.a.importPlugins() }) {
		return file, func() {}, nil
	}
	return processImport(ctx, hooks, file)
}

func (h mcpHost) MetadataSources(ctx context.Context) []*plugin.Plugin {
	var out []*plugin.Plugin
	if h.onMain(ctx, func() { out = h.a.metadataSources() }) {
		return out
	}
	return nil
}

// Download adds r to the library as Get books' downloadBook does.
func (h mcpHost) Download(ctx context.Context, p *plugin.Plugin, r plugin.Result, format string) (int64, error) {
	lib := h.Library()
	if lib == nil {
		return 0, errors.New("no library is open in Dalibre")
	}
	dir, err := os.MkdirTemp("", "dalibre-download-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	file, err := p.Download(ctx, r, format, dir, nil)
	if err != nil {
		return 0, err
	}
	cover, _ := p.Cover(ctx, r) // the file's own cover comes first; a missing one is no failure
	file, done, _ := h.ProcessImport(ctx, file)
	defer done()
	id, err := lib.ImportWith(file, resultMetadata(r), cover)
	if err != nil {
		return 0, err
	}
	h.Added(ctx, id, strings.ToUpper(strings.TrimPrefix(filepath.Ext(file), ".")), true)
	h.Changed()
	return id, nil
}

// ---- Plugins, for an agent that writes one (mcp.Plugins)

// addWait is how long add_plugin waits for the user to answer; after
// it the plugin is reported as waiting, and the dialog stays.
const addWait = 2 * time.Minute

// pluginExports are the functions a plugin may export that agents are
// told about.
var pluginExports = []string{"search", "collections", "list", "get", "resolve", "identify", "process", "run", "check"}

// manifestInfo is a plugin as agents see it, from its manifest.
func manifestInfo(m *plugin.Manifest, state string) mcp.PluginInfo {
	return mcp.PluginInfo{ID: m.ID, Name: m.Name, Version: m.Version, Description: m.Description,
		Capabilities: m.Capabilities.Names(), State: state, Asks: m.Asks()}
}

// pluginList is the plugins as agents see them. Main thread.
func (a *App) pluginList() mcp.PluginList {
	var out mcp.PluginList
	for _, k := range a.pluginsKnown {
		m := k.manifest
		info := manifestInfo(m, mcp.PluginFailed)
		info.Builtin, info.Folder = k.builtin, k.linked
		st := a.settings.Plugins[m.ID]
		i := slices.IndexFunc(a.plugins, func(p *plugin.Plugin) bool { return p.Manifest.ID == m.ID })
		switch {
		case st != nil && st.Disabled:
			info.State = mcp.PluginDisabled
		case slices.ContainsFunc(a.pluginsPending, func(p *plugin.Manifest) bool { return p.ID == m.ID }):
			info.State = mcp.PluginNeedsApproval
		case i >= 0:
			p := a.plugins[i]
			info.State, info.MissingSettings = mcp.PluginLoaded, p.MissingSettings()
			for _, fn := range pluginExports {
				if p.Defines(fn) {
					info.Exports = append(info.Exports, fn)
				}
			}
		}
		out.Plugins = append(out.Plugins, info)
	}
	for _, err := range a.pluginErrs {
		out.Errors = append(out.Errors, err.Error())
	}
	return out
}

// loaded waits for the plugins' load that wait, taken on the main thread,
// stands for.
func loaded(ctx context.Context, wait <-chan struct{}) {
	if wait == nil {
		return
	}
	select {
	case <-wait:
	case <-ctx.Done():
	}
}

// settled waits for a load of the plugins under way.
func (h mcpHost) settled(ctx context.Context) {
	var wait <-chan struct{}
	h.onMain(ctx, func() {
		if h.a.pluginsLoading {
			wait = h.a.afterPluginLoad()
		}
	})
	loaded(ctx, wait)
}

func (h mcpHost) Plugins(ctx context.Context) mcp.PluginList {
	h.settled(ctx)
	var out mcp.PluginList
	h.onMain(ctx, func() { out = h.a.pluginList() })
	return out
}

func (h mcpHost) Plugin(ctx context.Context, id string) *plugin.Plugin {
	h.settled(ctx)
	var out *plugin.Plugin
	h.onMain(ctx, func() {
		if i := slices.IndexFunc(h.a.plugins, func(p *plugin.Plugin) bool { return p.Manifest.ID == id }); i >= 0 {
			out = h.a.plugins[i]
		}
	})
	return out
}

func (h mcpHost) ReloadPlugins(ctx context.Context) {
	var wait <-chan struct{}
	h.onMain(ctx, func() {
		wait = h.a.afterPluginLoad()
		h.a.loadPlugins()
	})
	loaded(ctx, wait)
}

// AddPlugin links the plugin in folder, as Plugins › Add from folder
// does. The user is asked first, in the window, unless they granted it
// what it asks for before.
func (h mcpHost) AddPlugin(ctx context.Context, folder string) (mcp.PluginInfo, error) {
	a := h.a
	m, err := plugin.Inspect(folder)
	if err != nil {
		return mcp.PluginInfo{}, err
	}
	if info, err := os.Stat(folder); err != nil || !info.IsDir() {
		return mcp.PluginInfo{}, errors.New("add_plugin takes a plugin’s folder")
	}
	// answer is the user's: the load to wait for, or nil for no.
	answer := make(chan (<-chan struct{}), 1)
	var refused error
	ok := h.onMain(ctx, func() {
		st := a.settings.Plugins[m.ID]
		// Another plugin's id would get that plugin's grant and settings,
		// its secrets among them.
		if a.knownManifest(m.ID) != nil && (st == nil || st.Path != folder) {
			refused = fmt.Errorf("Dalibre has another plugin with the id %s; give this one its own id", m.ID)
			return
		}
		if a.consent != nil {
			refused = errors.New("Dalibre is asking the user about another plugin; try again once they answered")
			return
		}
		link := func() <-chan struct{} {
			wait := a.afterPluginLoad()
			a.link(m, folder)
			return wait
		}
		if granted(st, m) {
			answer <- link()
			return
		}
		a.consent = &consentAsk{manifest: m, archive: folder, answered: func(yes bool) {
			if yes {
				answer <- link()
			} else {
				answer <- nil
			}
		}}
	})
	if !ok {
		return mcp.PluginInfo{}, ctx.Err()
	}
	if refused != nil {
		return mcp.PluginInfo{}, refused
	}
	timer := time.NewTimer(addWait)
	defer timer.Stop()
	select {
	case wait := <-answer:
		if wait == nil {
			return mcp.PluginInfo{}, fmt.Errorf("the user didn’t allow %s", m.Name)
		}
		loaded(ctx, wait)
	case <-timer.C:
		return manifestInfo(m, mcp.PluginNeedsApproval), nil
	case <-ctx.Done():
		return manifestInfo(m, mcp.PluginNeedsApproval), nil
	}
	for _, info := range h.Plugins(ctx).Plugins {
		if info.ID == m.ID {
			return info, nil
		}
	}
	// Its plugin.json stopped loading since it was read above.
	info := manifestInfo(m, mcp.PluginFailed)
	info.Folder = folder
	return info, nil
}

// knownPlugin is plugin id as Dalibre found it. Main thread.
func (a *App) knownPlugin(id string) (knownPlugin, error) {
	for _, k := range a.pluginsKnown {
		if k.manifest.ID == id {
			return k, nil
		}
	}
	return knownPlugin{}, fmt.Errorf("no plugin %q", id)
}

func (h mcpHost) SetPluginEnabled(ctx context.Context, id string, on bool) error {
	h.settled(ctx)
	var err error
	var wait <-chan struct{}
	h.onMain(ctx, func() {
		if _, err = h.a.knownPlugin(id); err != nil {
			return
		}
		wait = h.a.afterPluginLoad()
		h.a.setPluginEnabled(id, on)
	})
	loaded(ctx, wait)
	return err
}

// RemovePlugin removes a plugin added from a folder. The ones the user
// installed, with their settings, are theirs to remove.
func (h mcpHost) RemovePlugin(ctx context.Context, id string) error {
	h.settled(ctx)
	var err error
	var wait <-chan struct{}
	h.onMain(ctx, func() {
		var k knownPlugin
		if k, err = h.a.knownPlugin(id); err != nil {
			return
		}
		if k.linked == "" {
			err = fmt.Errorf("%s wasn’t added from a folder; the user removes it in Dalibre’s Plugins", k.manifest.Name)
			return
		}
		wait = h.a.afterPluginLoad()
		h.a.uninstallPlugin(id)
	})
	loaded(ctx, wait)
	return err
}

// ---- Plugins' actions, for an agent (mcp.Actions)

func (h mcpHost) Actions(ctx context.Context) []mcp.ActionInfo {
	h.settled(ctx)
	var out []mcp.ActionInfo
	h.onMain(ctx, func() {
		for _, p := range h.a.plugins {
			for _, act := range p.Manifest.Capabilities.Actions {
				info := mcp.ActionInfo{Plugin: p.Manifest.ID, PluginName: p.Manifest.Name, ID: act.ID, Title: act.Label(), Scope: act.Scope,
					Formats: act.Formats, ChangesBooks: p.Manifest.Permissions.Library == plugin.LibraryWrite}
				if act.Books != nil {
					info.MinBooks, info.MaxBooks = act.Books.Min, act.Books.Max
				}
				out = append(out, info)
			}
		}
	})
	return out
}

// RunAction runs an action for an agent: its forms are answered from
// run.Answers, and what it returns is written into the library only with
// run.Apply, the agent standing where the user reviews a plugin's
// changes.
func (h mcpHost) RunAction(ctx context.Context, run mcp.ActionRun) (*mcp.ActionOutcome, error) {
	h.settled(ctx)
	var (
		p            *plugin.Plugin
		act          *plugin.Action
		lib          *calibre.Library
		books, all   []*calibre.Book
		index        map[string]int64
		hooks, after []*plugin.Plugin
		err          error
	)
	if !h.onMain(ctx, func() {
		a := h.a
		if i := slices.IndexFunc(a.plugins, func(p *plugin.Plugin) bool { return p.Manifest.ID == run.Plugin }); i >= 0 {
			p = a.plugins[i]
			act = p.Manifest.Action(run.Action)
		}
		switch {
		case p == nil:
			err = fmt.Errorf("no loaded plugin %q (see list_actions)", run.Plugin)
			return
		case act == nil:
			err = fmt.Errorf("%s has no action %q (see list_actions)", p.Manifest.Name, run.Action)
			return
		case a.lib == nil:
			err = errors.New("no library is open")
			return
		}
		lib, all, index = a.lib, a.books, a.idIndex
		hooks, after = a.importPlugins(), a.hookPlugins()
		if act.Scope != plugin.ScopeBooks {
			return
		}
		for _, id := range run.Books {
			b := a.byID[id]
			if b == nil {
				err = fmt.Errorf("no book %d", id)
				return
			}
			books = append(books, b)
		}
		if !a.takes(act, run.Books) {
			err = fmt.Errorf("%s doesn’t take these books: see its min_books, max_books and formats in list_actions", act.Label())
		}
	}) {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	infos, files, err := actionBooks(lib, act, books)
	if err != nil {
		return nil, err
	}
	res, err := p.RunAction(ctx, act.ID, infos, files, &plugin.ActionHost{Answers: run.Answers, Books: libraryPager(lib, all, index), Covers: actionCovers(lib, act, books)})
	if err != nil {
		return nil, err
	}
	defer res.Done()
	ref := func(id int64) mcp.BookRef {
		if b, err := lib.Book(id); err == nil {
			return mcp.BookRef{ID: b.ID, Title: b.Title}
		}
		return mcp.BookRef{ID: id}
	}
	out := &mcp.ActionOutcome{Message: res.Message, Open: res.Open, Applied: run.Apply}
	for _, g := range res.Groups {
		ag := mcp.ActionGroup{Title: g.Title}
		for _, gb := range g.Books {
			ag.Books = append(ag.Books, mcp.ActionBook{ID: gb.ID, Title: ref(gb.ID).Title, Note: gb.Note})
		}
		out.Groups = append(out.Groups, ag)
	}
	for _, nb := range res.Add {
		out.Files = append(out.Files, mcp.NewFile{Name: filepath.Base(nb.File), Format: strings.ToUpper(strings.TrimPrefix(filepath.Ext(nb.File), "."))})
	}
	for _, f := range res.Formats {
		r := ref(f.Book)
		out.Files = append(out.Files, mcp.NewFile{Book: &r, Name: filepath.Base(f.File), Format: f.Format})
	}
	// What it would change, read before anything is written.
	for _, it := range applyActionTo(ctx, lib, res, false, false, nil, nil).review {
		bc := mcp.BookChanges{Book: mcp.BookRef{ID: it.id, Title: it.title}}
		for _, l := range it.lines {
			bc.Changes = append(bc.Changes, mcp.FieldChange{Field: l.name, From: l.old, To: l.now})
		}
		out.Changes = append(out.Changes, bc)
	}
	if !run.Apply {
		return out, nil
	}
	did := applyAction(ctx, lib, res, true, hooks, after)
	if did.files.undoDir != "" {
		os.RemoveAll(did.files.undoDir) // an agent has no Undo
	}
	for _, id := range did.touched {
		out.Changed = append(out.Changed, ref(id))
	}
	for _, id := range did.added {
		out.Added = append(out.Added, ref(id))
	}
	for _, e := range did.failed {
		out.Failed = append(out.Failed, e.Error())
	}
	if len(did.touched)+len(did.added) > 0 {
		h.Changed(did.touched...)
	}
	return out, nil
}
