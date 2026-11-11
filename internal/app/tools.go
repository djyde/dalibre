package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/plugin"
	"dalibre/internal/plugin/pkgmgr"
	"dalibre/internal/settings"
)

// Plugin tools (Figma "20 · Plugin tools", docs/plugins/tools.md): the
// programs plugins run outside their sandbox. Each comes from Dalibre's
// own download of its pinned build, from a package manager, or from a
// file the user chose. A plugin loads only once all its tools are here.

// toolPlace is where a tool was found.
type toolPlace = plugin.ToolPlace

// toolJob is a tool being got: downloaded, or installed with a package
// manager.
type toolJob struct {
	plugin, tool string // ids
	name         string // the plugin's name
	source       string // plugin.SourceDownload or a package manager
	pkg          string // the package, for a package manager
	host         string // where a download comes from
	done, total  int64  // a download's bytes; total -1 when unknown
	lines        []string
	started      time.Time
	running      bool
	err          string
	cancel       context.CancelFunc
}

// maxJobLines is how much of a package manager's output is kept.
const maxJobLines = 400

// toolsState is the tools of the plugins.
type toolsState struct {
	jobs map[string]*toolJob // by toolKey
	// missing are the tools each plugin lacks, as the last load found:
	// plugin id → tool ids.
	missing map[string][]string
	// places are where the loaded plugins' tools are: plugin id → tool id.
	places map[string]map[string]toolPlace
	// checks are what the plugins' check() said, by plugin id; nil when
	// ready.
	checks map[string]error
	// picks are the sources chosen in the dialog asking about plugin
	// picksFor, by tool id; pickFiles the files chosen, pickFound what a
	// package manager already has.
	picksFor  string
	picks     map[string]string
	pickFiles map[string]string
	pickFound map[string]toolPlace
	// dialog is the job whose package manager's output shows.
	dialog string
	// prune, after an uninstall, removes the builds no plugin declares
	// once the plugins are loaded again.
	prune bool
}

func toolKey(pluginID, toolID string) string { return pluginID + "/" + toolID }

func (a *App) toolStore() *plugin.ToolStore {
	if a.pluginDir == "" {
		return nil
	}
	s := &plugin.ToolStore{Dir: plugin.ToolsDir(a.pluginDir)}
	if a.pluginTransport != nil {
		s.Client = &http.Client{Transport: a.pluginTransport}
	}
	return s
}

// locateTool finds tool id where choice says it comes from. With no
// choice yet, a package manager's install the plugin takes will do, then
// Dalibre's download. Any goroutine.
func locateTool(store *plugin.ToolStore, t plugin.Tool, id string, choice *settings.ToolChoice) (toolPlace, bool) {
	inStore := func() (toolPlace, bool) {
		b, ok := t.Build()
		if !ok {
			return toolPlace{}, false
		}
		exe, ok := store.Path(b)
		return toolPlace{Path: exe, Version: t.Version, Source: plugin.SourceDownload}, ok
	}
	if choice == nil {
		if p, ok := plugin.FoundInManager(t, id); ok {
			return p, true
		}
		return inStore()
	}
	switch choice.Source {
	case plugin.SourceFile:
		if st, err := os.Stat(choice.Path); err == nil && !st.IsDir() {
			return toolPlace{Path: choice.Path, Source: plugin.SourceFile}, true
		}
		return toolPlace{}, false
	case plugin.SourceDownload:
		return inStore()
	default:
		return plugin.FindInManager(t, id, choice.Source)
	}
}

// locateTools finds every tool of m, and says which are missing.
func locateTools(store *plugin.ToolStore, m *plugin.Manifest, choices map[string]*settings.ToolChoice) (map[string]toolPlace, []string) {
	if len(m.Tools) == 0 {
		return nil, nil
	}
	places := map[string]toolPlace{}
	var missing []string
	for _, id := range m.ToolIDs() {
		if p, ok := locateTool(store, m.Tools[id], id, choices[id]); ok {
			places[id] = p
		} else {
			missing = append(missing, id)
		}
	}
	return places, missing
}

// toolPaths are the executables of places, by tool id, for the plugin.
func toolPaths(places map[string]toolPlace) map[string]string {
	if len(places) == 0 {
		return nil
	}
	out := map[string]string{}
	for id, p := range places {
		out[id] = p.Path
	}
	return out
}

// grantedTools reports whether the user let m run each of its tools, as
// they are now.
func grantedTools(g *settings.Grant, m *plugin.Manifest) bool {
	for id, ident := range m.ToolIdentities() {
		if g.Tools[id] != ident {
			return false
		}
	}
	return true
}

// grantFor is what the user grants plugin m by installing it.
func grantFor(m *plugin.Manifest) *settings.Grant {
	return &settings.Grant{Hosts: slices.Clone(m.Permissions.Hosts), LocalNetwork: m.Permissions.LocalNetwork,
		Library: m.Permissions.Library, Tools: m.ToolIdentities()}
}

// pickTools starts the choices of where m's tools come from, for the
// dialog asking about it: what a package manager already has, else the
// source the user last chose, when this computer has it, else Dalibre's
// download, or a file when there is nothing to download. Main thread.
func (a *App) pickTools(m *plugin.Manifest) {
	if a.tl.picksFor == m.ID && a.tl.picks != nil {
		return
	}
	a.tl.picksFor = m.ID
	a.tl.picks, a.tl.pickFiles, a.tl.pickFound = map[string]string{}, map[string]string{}, map[string]toolPlace{}
	for _, id := range m.ToolIDs() {
		t := m.Tools[id]
		if st := a.settings.Plugins[m.ID]; st != nil && st.Tools[id] != nil {
			a.tl.picks[id] = st.Tools[id].Source
			a.tl.pickFiles[id] = st.Tools[id].Path
		}
		if p, ok := plugin.FoundInManager(t, id); ok {
			a.tl.pickFound[id] = p
			if a.tl.picks[id] == "" {
				a.tl.picks[id] = p.Source
			}
		}
		if a.tl.picks[id] != "" {
			continue
		}
		_, hasBuild := t.Build()
		last := a.settings.ToolSource
		switch {
		case last != "" && last != plugin.SourceDownload && t.Packages.Name(last) != "" && pkgmgr.Available(last):
			a.tl.picks[id] = last
		case hasBuild:
			a.tl.picks[id] = plugin.SourceDownload
		default:
			a.tl.picks[id] = plugin.SourceFile
		}
	}
}

// picksReady reports whether every tool has a source: a file chosen when
// that is the source.
func (a *App) picksReady(m *plugin.Manifest) bool {
	for _, id := range m.ToolIDs() {
		if a.tl.picks[id] == plugin.SourceFile && a.tl.pickFiles[id] == "" {
			return false
		}
	}
	return true
}

// getPickedTools gets m's tools from where the dialog's picks say, once
// the user has installed it.
func (a *App) getPickedTools(m *plugin.Manifest) {
	if a.tl.picksFor != m.ID {
		a.pickTools(m)
	}
	for _, id := range m.ToolIDs() {
		a.getTool(m, id, a.tl.picks[id], a.tl.pickFiles[id], false)
	}
	a.tl.picksFor, a.tl.picks = "", nil
}

// getTool makes source (with file, for SourceFile) where plugin m's tool
// id comes from, and gets it from there if it isn't here yet. reload
// loads the plugins again when the tool was already here.
func (a *App) getTool(m *plugin.Manifest, id, source, file string, reload bool) {
	t, ok := m.Tools[id]
	if !ok || source == "" {
		return
	}
	st := a.settings.Plugin(m.ID)
	if st.Tools == nil {
		st.Tools = map[string]*settings.ToolChoice{}
	}
	choice := &settings.ToolChoice{Source: source}
	if source == plugin.SourceFile {
		choice.Path = file
	} else {
		a.settings.ToolSource = source
	}
	st.Tools[id] = choice
	a.saveSettings()
	if j := a.tl.jobs[toolKey(m.ID, id)]; j != nil && j.running {
		if j.source == source {
			return
		}
		j.cancel()
	}
	delete(a.tl.jobs, toolKey(m.ID, id))
	store := a.toolStore()
	a.run(func() {
		_, here := locateTool(store, t, id, choice)
		a.post(func() {
			switch {
			case here:
				if reload || len(a.tl.missing[m.ID]) > 0 {
					a.loadPlugins()
				}
			case source == plugin.SourceFile:
				a.toastError(errors.New(locales.ToolsFileGone(file)))
			default:
				a.startToolJob(m, id, source)
			}
		})
	})
}

// startToolJob downloads plugin m's tool id, or installs it with package
// manager source.
func (a *App) startToolJob(m *plugin.Manifest, id, source string) {
	t := m.Tools[id]
	key := toolKey(m.ID, id)
	ctx, cancel := context.WithCancel(context.Background())
	j := &toolJob{plugin: m.ID, tool: id, name: m.Name, source: source, total: -1, started: time.Now(), running: true, cancel: cancel}
	if a.tl.jobs == nil {
		a.tl.jobs = map[string]*toolJob{}
	}
	a.tl.jobs[key] = j
	store := a.toolStore()
	if source == plugin.SourceDownload {
		b, ok := t.Build()
		if !ok || store == nil {
			j.running, j.err = false, locales.ToolsNoBuild(id)
			cancel()
			return
		}
		if u, err := url.Parse(b.URL); err == nil {
			j.host = u.Host
		}
		a.run(func() {
			defer cancel()
			var last time.Time
			_, err := store.Fetch(ctx, b, func(done, total int64) {
				if now := time.Now(); now.Sub(last) > 100*time.Millisecond || done == total {
					last = now
					a.post(func() { j.done, j.total = done, total })
				}
			})
			a.post(func() { a.toolJobDone(j, err) })
		})
		return
	}
	j.pkg = t.Packages.Name(source)
	a.tl.dialog = key
	// The elapsed time ticks while it runs.
	go func() {
		tick := time.NewTicker(time.Second)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				a.post(func() {})
			}
		}
	}()
	a.run(func() {
		defer cancel()
		err := pkgmgr.Install(ctx, source, j.pkg, func(line string) {
			a.post(func() {
				j.lines = append(j.lines, line)
				if len(j.lines) > maxJobLines {
					j.lines = j.lines[len(j.lines)-maxJobLines:]
				}
			})
		})
		if err == nil {
			if _, ok := plugin.FindInManager(t, id, source); !ok {
				err = errors.New(locales.ToolsInstalledNotFound(pkgmgr.Title(source), id, t.MinVersion()))
			}
		}
		a.post(func() { a.toolJobDone(j, err) })
	})
}

// toolJobDone ends job j: a tool got loads its plugin.
func (a *App) toolJobDone(j *toolJob, err error) {
	j.running = false
	if a.tl.jobs[toolKey(j.plugin, j.tool)] != j {
		return // replaced by another choice
	}
	if err != nil {
		if errors.Is(err, context.Canceled) {
			delete(a.tl.jobs, toolKey(j.plugin, j.tool))
			if a.tl.dialog == toolKey(j.plugin, j.tool) {
				a.tl.dialog = ""
			}
			return
		}
		j.err = err.Error()
		return
	}
	delete(a.tl.jobs, toolKey(j.plugin, j.tool))
	if a.tl.dialog == toolKey(j.plugin, j.tool) {
		a.tl.dialog = ""
	}
	a.toast("%s", locales.ToolsReady(j.tool, j.name))
	a.loadPlugins()
}

// toolJobFor is the job getting plugin id's tool, if any.
func (a *App) toolJobFor(pluginID, toolID string) *toolJob {
	return a.tl.jobs[toolKey(pluginID, toolID)]
}

// keepTools removes the downloaded builds no known plugin declares.
func (a *App) keepTools(known []knownPlugin) {
	store := a.toolStore()
	if store == nil {
		return
	}
	keep := map[string]bool{}
	for _, k := range known {
		for _, t := range k.manifest.Tools {
			for _, b := range t.Platforms {
				keep[b.SHA256] = true
			}
		}
	}
	a.run(func() { store.Keep(keep) })
}

// checkPlugins asks the loaded plugins that export check() whether they
// are ready, in the background.
func (a *App) checkPlugins(plugins []*plugin.Plugin) {
	a.run(func() {
		res := map[string]error{}
		for _, p := range plugins {
			if !p.Defines("check") {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			if err := p.Check(ctx); err != nil {
				res[p.Manifest.ID] = err
			}
			cancel()
		}
		a.post(func() { a.tl.checks = res })
	})
}

// toolSize is a build's size to show, "" when unknown.
func toolSize(t plugin.Tool) string {
	if b, ok := t.Build(); ok && b.Size > 0 {
		return humanSize(b.Size)
	}
	return ""
}

// toolHost is where a tool's download comes from: "github.com/jgm/pandoc"
// for a GitHub release, else the host.
func toolHost(t plugin.Tool) string {
	b, ok := t.Build()
	if !ok {
		return ""
	}
	u, err := url.Parse(b.URL)
	if err != nil {
		return ""
	}
	if u.Host == "github.com" {
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) >= 2 {
			return "github.com/" + parts[0] + "/" + parts[1]
		}
	}
	return u.Host
}

// shortPath is a path with the home folder as ~.
func shortPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return filepath.Join("~", rel)
		}
	}
	return p
}

// commandLine is a package manager's command, as shown.
func commandLine(mgr, pkg string) string { return strings.Join(pkgmgr.Command(mgr, pkg), " ") }
