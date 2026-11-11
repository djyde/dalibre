package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
	builtin "dalibre/plugins"
)

// knownPlugin is a plugin Dalibre found, loaded or not, for the Plugins
// dialog.
type knownPlugin struct {
	manifest *plugin.Manifest
	builtin  bool
	linked   string // the folder it loads from, when linked
}

// download is a book coming from a source plugin into the library.
type download struct {
	plugin      *plugin.Plugin
	result      plugin.Result
	format      string
	done, total int64 // bytes; total is -1 when unknown
	err         error
	finished    bool
	bookID      int64 // the book it became
	cancel      context.CancelFunc
}

// fraction is how far the download is, -1 while its size is unknown.
func (d *download) fraction() float64 {
	if d.total <= 0 {
		return -1
	}
	return float64(d.done) / float64(d.total)
}

// progressText is "1.4 of 3.2 MB", or what has come so far.
func (d *download) progressText() string {
	if d.total <= 0 {
		return humanSize(d.done)
	}
	return locales.PluginsDownloadProgress(strings.TrimSuffix(humanSize(d.done), " MB"), humanSize(d.total))
}

// consentAsk is a plugin waiting for the user to grant its permissions:
// one being installed (archive set) or an installed one whose update asks
// for more.
type consentAsk struct {
	manifest *plugin.Manifest
	archive  string
	// source is the GitHub repository an update comes from.
	source *settings.PluginSource
	// answered, when set, takes the user's answer in place of installing:
	// an agent adding the plugin waits for it.
	answered func(granted bool)
}

// granted reports whether the user granted m everything it asks for.
func granted(st *settings.PluginState, m *plugin.Manifest) bool {
	if st == nil || st.Granted == nil {
		return false
	}
	return plugin.Permissions{Hosts: st.Granted.Hosts, LocalNetwork: st.Granted.LocalNetwork, Library: st.Granted.Library}.Covers(m.Permissions) &&
		grantedTools(st.Granted, m)
}

// loadPlugins loads the built-in plugins and the enabled ones in the
// plugins folder whose permissions the user granted, in the background.
// The others wait for the user in pluginsPending.
func (a *App) loadPlugins() {
	if stop := a.act.cancel; stop != nil {
		stop() // an action waiting on its form would keep its plugin from closing
	}
	dir, builtins, transport, store := a.pluginDir, a.builtins, a.pluginTransport, a.secrets
	tools := a.toolStore()
	if dir == "" && !builtins {
		a.pluginsSettled()
		return
	}
	a.loadSeq++
	seq := a.loadSeq
	states := map[string]settings.PluginState{}
	for id, st := range a.settings.Plugins {
		states[id] = *st
	}
	a.pluginsLoading = true
	a.run(func() {
		var plugins []*plugin.Plugin
		var pending []*plugin.Manifest
		var known []knownPlugin
		var errs []error
		secrets := map[string]map[string]string{}
		var moved []*plugin.Manifest
		needTools := map[string][]string{}
		places := map[string]map[string]toolPlace{}
		// withTools finds m's tools: false when one is missing, and m
		// can't load until it is here.
		withTools := func(m *plugin.Manifest, st settings.PluginState) (map[string]string, bool) {
			found, missing := locateTools(tools, m, st.Tools)
			if len(missing) > 0 {
				needTools[m.ID] = missing
				return nil, false
			}
			if found != nil {
				places[m.ID] = found
			}
			return toolPaths(found), true
		}
		// options are what m loads with: its values, secrets included.
		options := func(m *plugin.Manifest, st settings.PluginState) plugin.Options {
			values, sec, mv := pluginValues(store, m, st.Values)
			if sec != nil {
				secrets[m.ID] = sec
			}
			if mv {
				moved = append(moved, m)
			}
			o := plugin.Options{Settings: values, Transport: transport}
			if dir != "" {
				o.DataDir = plugin.DataDir(dir, m.ID)
			}
			return o
		}
		if builtins {
			// Dalibre vouches for its own plugins: they have what they ask.
			for _, fsys := range builtin.Builtin() {
				data, err := fs.ReadFile(fsys, "plugin.json")
				var m *plugin.Manifest
				if err == nil {
					m, err = plugin.ParseManifest(data)
				}
				if err != nil {
					errs = append(errs, err)
					continue
				}
				known = append(known, knownPlugin{manifest: m, builtin: true})
				st := states[m.ID]
				if st.Disabled {
					continue
				}
				p, err := plugin.LoadBuiltin(fsys, options(m, st))
				if err != nil {
					errs = append(errs, err)
					continue
				}
				plugins = append(plugins, p)
			}
		}
		if dir != "" {
			found, ferrs := plugin.Discover(dir, func(m *plugin.Manifest) (plugin.Options, bool) {
				st := states[m.ID]
				if st.Path != "" {
					return plugin.Options{}, false // linked: loaded from its folder below
				}
				known = append(known, knownPlugin{manifest: m})
				if st.Disabled {
					return plugin.Options{}, false
				}
				if !granted(&st, m) {
					pending = append(pending, m)
					return plugin.Options{}, false
				}
				paths, ok := withTools(m, st)
				if !ok {
					return plugin.Options{}, false
				}
				o := options(m, st)
				o.Tools = paths
				return o, true
			})
			plugins = append(plugins, found...)
			errs = append(errs, ferrs...)
		}
		// Linked plugins load from their folders, as they are.
		var linked []string
		for _, id := range slices.Sorted(maps.Keys(states)) {
			st := states[id]
			if st.Path == "" {
				continue
			}
			linked = append(linked, st.Path)
			m, err := plugin.ReadManifest(st.Path)
			switch {
			case err != nil:
				errs = append(errs, fmt.Errorf("%s: %w", st.Path, err))
				continue
			case m.ID != id:
				errs = append(errs, errors.New(locales.PluginsErrIDMismatch(st.Path, m.ID, id)))
				continue
			case plugin.Reserved(m.ID):
				errs = append(errs, errors.New(locales.PluginsErrIDReserved(st.Path, m.ID)))
				continue
			}
			known = append(known, knownPlugin{manifest: m, linked: st.Path})
			if st.Disabled {
				continue
			}
			if !granted(&st, m) {
				pending = append(pending, m)
				continue
			}
			paths, ok := withTools(m, st)
			if !ok {
				continue
			}
			o := options(m, st)
			o.Tools = paths
			p, err := plugin.Load(st.Path, o)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			plugins = append(plugins, p)
		}
		watch := newFolderWatch(linked)
		a.post(func() {
			a.pluginSecrets = secrets
			a.dropMovedSecrets(moved)
			a.watchLinked(watch)
			old := a.plugins
			for _, d := range a.downloads {
				if slices.Contains(old, d.plugin) && !d.finished {
					d.cancel()
				}
			}
			// Close ends what each old plugin has in flight (timers,
			// requests) and waits for a call in progress.
			a.run(func() {
				for _, p := range old {
					p.Close()
				}
			})
			a.pluginsLoading = false
			a.pluginErrs, a.pluginsPending, a.pluginsKnown = errs, pending, known
			a.tl.missing, a.tl.places = needTools, places
			a.setPlugins(plugins)
			if a.tl.prune {
				a.tl.prune = false
				a.keepTools(known)
			}
			a.checkPlugins(plugins)
			for _, err := range errs {
				a.toastError(errors.New(locales.PluginsErrToast(err.Error())))
			}
			if seq == a.loadSeq {
				a.pluginsSettled()
			}
		})
	})
}

// afterPluginLoad is closed once the plugins are loaded: by the load
// under way, or the next. Main thread.
func (a *App) afterPluginLoad() <-chan struct{} {
	ch := make(chan struct{})
	a.loadWait = append(a.loadWait, ch)
	return ch
}

// pluginsSettled tells those waiting in afterPluginLoad.
func (a *App) pluginsSettled() {
	for _, ch := range a.loadWait {
		close(ch)
	}
	a.loadWait = nil
}

// setPlugins makes plugins the loaded ones. What the Get books page holds
// from the ones before is dropped: a plugin's settings may have changed.
func (a *App) setPlugins(plugins []*plugin.Plugin) {
	a.plugins = plugins
	src, text, query, coll, filters, lang := a.gb.source, a.gb.text, a.gb.query, a.gb.collection, a.gb.filters, a.gb.language
	a.gb = newGetBooks()
	a.gb.source, a.gb.text, a.gb.query, a.gb.collection, a.gb.filters, a.gb.language = src, text, query, coll, filters, lang
	if a.page == pageGetBooks {
		a.openGetBooks()
	}
}

// sources are the loaded plugins that search for books, and the OPDS
// catalogs that can search, unless the user keeps them out.
func (a *App) sources() []*plugin.Plugin {
	var out []*plugin.Plugin
	for _, p := range a.plugins {
		if p.Manifest.IsBookSource() {
			out = append(out, p)
		}
	}
	if !a.settings.OPDS.NotInGetBooks {
		for _, p := range a.catalogs {
			if st := a.catalogStatus[strings.TrimPrefix(p.Manifest.ID, catalogPrefix)]; st != nil && st.searchable {
				out = append(out, p)
			}
		}
	}
	return out
}

// installPlugin installs a plugin package. When it asks for more than
// the user granted it, the user is asked first (a.consent).
func (a *App) installPlugin(archive string) {
	a.run(func() {
		m, err := plugin.Inspect(archive)
		a.post(func() {
			if err != nil {
				a.toastError(errors.New(locales.PluginsInstallFailed(err.Error())))
				return
			}
			if !granted(a.settings.Plugins[m.ID], m) {
				a.consent = &consentAsk{manifest: m, archive: archive}
				return
			}
			a.finishInstall(archive)
		})
	})
}

// grantConsent grants the plugin the user is asked about its
// permissions, and installs or loads it.
func (a *App) grantConsent() {
	c := a.consent
	if c == nil {
		return
	}
	a.consent = nil
	st := a.settings.Plugin(c.manifest.ID)
	st.Granted = grantFor(c.manifest)
	a.saveSettings()
	if len(c.manifest.Tools) > 0 {
		a.getPickedTools(c.manifest)
	}
	if c.answered != nil {
		c.answered(true)
		return
	}
	if c.archive != "" {
		a.finishInstallFrom(c.archive, c.source)
		return
	}
	a.loadPlugins()
}

// askConsent asks the user to grant an installed plugin waiting in
// pluginsPending.
func (a *App) askConsent(id string) {
	if i := slices.IndexFunc(a.pluginsPending, func(m *plugin.Manifest) bool { return m.ID == id }); i >= 0 {
		a.consent = &consentAsk{manifest: a.pluginsPending[i]}
	}
}

func (a *App) finishInstall(archive string) { a.finishInstallFrom(archive, nil) }

// finishInstallFrom installs a package; src, when set, is the GitHub
// repository it came from, whose download is removed afterwards.
func (a *App) finishInstallFrom(archive string, src *settings.PluginSource) {
	if info, err := os.Stat(archive); err == nil && info.IsDir() {
		a.linkPlugin(archive)
		return
	}
	dir := a.pluginDir
	a.run(func() {
		m, err := plugin.Install(archive, dir)
		if src != nil {
			os.Remove(archive)
		}
		a.post(func() {
			if err != nil {
				a.toastError(errors.New(locales.PluginsInstallFailed(err.Error())))
				return
			}
			st := a.settings.Plugin(m.ID)
			st.Path = "" // installed now, no longer linked
			if src != nil {
				st.Source = src
			} else {
				st.Source = nil // a file: nothing to update from
			}
			delete(a.updates, m.ID)
			a.saveSettings()
			a.toast("%s", locales.PluginsInstalled(m.Name, m.Version))
			a.loadPlugins()
		})
	})
}

// setPluginEnabled turns plugin id on or off.
func (a *App) setPluginEnabled(id string, on bool) {
	a.settings.Plugin(id).Disabled = !on
	a.saveSettings()
	a.loadPlugins()
}

// setPluginValues saves a plugin's settings and reloads it with them.
func (a *App) setPluginValues(id string, values map[string]string) {
	m := a.knownManifest(id)
	if m == nil {
		a.settings.Plugin(id).Values = values
		a.saveSettings()
		a.loadPlugins()
		return
	}
	a.savePluginValues(m, values, a.loadPlugins)
}

// knownManifest is the manifest of plugin id, if Dalibre found it.
func (a *App) knownManifest(id string) *plugin.Manifest {
	for _, k := range a.pluginsKnown {
		if k.manifest.ID == id {
			return k.manifest
		}
	}
	return nil
}

// linkPlugin links the plugin in folder: it loads from there, and
// reloads when the folder's files change.
func (a *App) linkPlugin(folder string) {
	a.run(func() {
		abs, err := filepath.Abs(folder)
		var m *plugin.Manifest
		if err == nil {
			m, err = plugin.Inspect(abs)
		}
		a.post(func() {
			if err != nil {
				a.toastError(errors.New(locales.PluginsAddFailed(err.Error())))
				return
			}
			a.link(m, abs)
		})
	})
}

// link makes the plugin with manifest m load from folder abs, and loads
// the plugins again.
func (a *App) link(m *plugin.Manifest, abs string) {
	st := a.settings.Plugin(m.ID)
	st.Path, st.Source = abs, nil
	delete(a.updates, m.ID)
	a.saveSettings()
	a.toast("%s", locales.PluginsAddedFrom(m.Name, filepath.Base(abs)))
	a.loadPlugins()
}

// uninstallPlugin removes plugin id and what the user decided about it.
// A linked plugin's folder stays as it is.
func (a *App) uninstallPlugin(id string) {
	if m := a.knownManifest(id); m != nil {
		a.forgetPluginSecrets(m)
	}
	if st := a.settings.Plugins[id]; st != nil && st.Path != "" {
		delete(a.settings.Plugins, id)
		a.saveSettings()
		a.loadPlugins()
		return
	}
	dir := a.pluginDir
	a.tl.prune = true // the builds only it declared go
	a.run(func() {
		err := plugin.Uninstall(dir, id)
		a.post(func() {
			if err != nil {
				a.toastError(err)
				return
			}
			delete(a.settings.Plugins, id)
			a.saveSettings()
			a.loadPlugins()
		})
	})
}

// downloadBook downloads r as format with its plugin and adds it to the
// library, with the metadata the plugin gave.
func (a *App) downloadBook(p *plugin.Plugin, r plugin.Result, format string) {
	lib := a.lib
	if lib == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	d := &download{plugin: p, result: r, format: strings.ToUpper(format), total: -1, cancel: cancel}
	a.downloads = append(a.downloads, d)
	hooks, after := a.importPlugins(), a.hookPlugins()
	var warned []error
	a.run(func() {
		defer cancel()
		id, err := func() (int64, error) {
			dir, err := os.MkdirTemp("", "dalibre-download-")
			if err != nil {
				return 0, err
			}
			defer os.RemoveAll(dir)
			var last time.Time
			file, err := p.Download(ctx, r, format, dir, func(done, total int64) {
				if now := time.Now(); now.Sub(last) > 100*time.Millisecond || done == total {
					last = now
					a.post(func() { d.done, d.total = done, total })
				}
			})
			if err != nil {
				return 0, err
			}
			cover, _ := p.Cover(ctx, r) // the file's own cover comes first; a missing one is no failure
			file, done, errs := processImport(ctx, hooks, file)
			defer done()
			warned = errs
			id, err := lib.ImportWith(file, resultMetadata(r), cover)
			if err == nil {
				afterAdd(ctx, lib, after, id, strings.ToUpper(strings.TrimPrefix(filepath.Ext(file), ".")), true)
			}
			return id, err
		}()
		a.post(func() {
			for _, e := range warned {
				a.toastError(e)
			}
			d.finished, d.err, d.bookID = true, err, id
			if err != nil {
				if !errors.Is(err, context.Canceled) && !isCanceled(err) {
					a.toastError(errors.New(pluginErrorText(a, r.Title, err)))
				}
				return
			}
			a.reload(func() {
				a.query = catalog.Query{Sort: catalog.SortDateAdded, Descending: true}
				a.chosen.Clear()
				a.curID = id
				a.toast("%s", locales.PluginsAddedBook(r.Title))
			})
		})
	})
}

// isCanceled reports whether a download failed because it was canceled.
func isCanceled(err error) bool {
	var pe *plugin.Error
	return errors.As(err, &pe) && pe.Kind == plugin.KindTimeout && errors.Is(pe.Err, context.Canceled)
}

// pluginName is the name of plugin id, as users know it.
func (a *App) pluginName(id string) string {
	for _, k := range a.pluginsKnown {
		if k.manifest.ID == id {
			return k.manifest.Name
		}
	}
	if p := a.pluginByID(id); p != nil {
		return p.Manifest.Name
	}
	return id
}

// pluginErrorText says what went wrong with a download, and what to do.
func pluginErrorText(a *App, title string, err error) string {
	var pe *plugin.Error
	if !errors.As(err, &pe) {
		return locales.PluginsDownloadFailedGeneric(title, err.Error())
	}
	pe = &plugin.Error{Plugin: a.pluginName(pe.Plugin), Call: pe.Call, Kind: pe.Kind, Msg: pe.Msg, Err: pe.Err}
	switch pe.Kind {
	case plugin.KindAuth, plugin.KindSettings:
		return locales.PluginsDownloadAuth(pe.Plugin, pe.Msg)
	case plugin.KindRateLimited:
		return locales.PluginsDownloadBusy(pe.Plugin)
	case plugin.KindUnavailable:
		return locales.PluginsDownloadUnavailable(pe.Plugin, pe.Msg)
	case plugin.KindNotFound:
		return locales.PluginsDownloadNotFound(title, pe.Plugin)
	case plugin.KindTimeout:
		return locales.PluginsDownloadTimeout(title, pe.Msg)
	}
	return locales.PluginsDownloadFailed(title, pe.Plugin, pe.Msg)
}

// resultMetadata is the library metadata for a plugin's result.
func resultMetadata(r plugin.Result) calibre.Metadata {
	m := calibre.Metadata{
		Title: r.Title, Authors: r.Authors, Publisher: r.Publisher,
		Series: r.Series, SeriesIndex: r.SeriesIndex, Tags: r.Tags, Comments: r.Description,
		Pubdate: plugin.ParseDate(r.Pubdate), Identifiers: map[string]string{},
	}
	if code := calibre.LanguageCode(r.Language); code != "" {
		m.Languages = []string{code}
	}
	for k, v := range r.Identifiers {
		if k = strings.ToLower(strings.TrimSpace(k)); k != "" && v != "" {
			m.Identifiers[k] = v
		}
	}
	return m
}
