package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// The Marketplace (Figma "18 · Plugin marketplace"): the Plugins window's
// second tab lists the plugins in the Marketplace's index (marketplace/), its
// own and the community's, to install with a click. Installing asks for
// the plugin's permissions as any install does; updates are the versions
// the index lists.

// marketRefresh is how old the index may be before the tab reads it again.
const marketRefresh = time.Hour

// The Plugins window's tabs.
const (
	pluginsTabInstalled = iota
	pluginsTabMarketplace
)

type marketState struct {
	index   *plugin.Marketplace
	fetched time.Time // when index was read from the network
	loading bool
	err     string // why the last read failed
	query   string
	cat     string // the category shown; "" for all
	sel     string // the plugin shown
	// installing is the plugin being downloaded.
	installing string
}

// marketCachePath is where the last index read is kept, beside the
// plugins folder; "" keeps none.
func (a *App) marketCachePath() string {
	if a.pluginDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(a.pluginDir), "marketplace.json")
}

// loadMarketplace reads the index: the copy kept, at once, then the
// network's when force or when it is over marketRefresh old.
func (a *App) loadMarketplace(force bool) {
	mk := &a.mk
	if mk.index == nil {
		if p := a.marketCachePath(); p != "" {
			if data, err := os.ReadFile(p); err == nil {
				if m, err := plugin.ParseMarketplace(data); err == nil {
					mk.index = m
					a.marketUpdates()
				}
			}
		}
	}
	if mk.loading || !force && !mk.fetched.IsZero() && time.Since(mk.fetched) < marketRefresh {
		return
	}
	mk.loading = true
	gh, url, cache := a.gh(), a.marketURL, a.marketCachePath()
	a.run(func() {
		m, data, err := gh.FetchMarketplace(context.Background(), url)
		if err == nil && cache != "" {
			os.MkdirAll(filepath.Dir(cache), 0o755)
			os.WriteFile(cache, data, 0o644)
		}
		a.post(func() {
			mk.loading = false
			if err != nil {
				mk.err = err.Error()
				return
			}
			mk.index, mk.fetched, mk.err = m, time.Now(), ""
			a.marketUpdates()
		})
	})
}

// marketUpdates notes, as updates, the newer versions the index lists of
// the plugins installed from the Marketplace.
func (a *App) marketUpdates() {
	for id, st := range a.settings.Plugins {
		if st.Source == nil || !st.Source.Marketplace {
			continue
		}
		u := marketUpdate(a.mk.index, id, a.installedVersion(id, st.Source))
		if u == nil {
			if old := a.updates[id]; old != nil && !old.busy {
				delete(a.updates, id)
			}
			continue
		}
		if old := a.updates[id]; old != nil && (old.busy || old.ref == u.ref) {
			continue
		}
		if a.updates == nil {
			a.updates = map[string]*pluginUpdate{}
		}
		a.updates[id] = u
	}
}

// installedVersion is plugin id's version as loaded, else as its source
// says.
func (a *App) installedVersion(id string, src *settings.PluginSource) string {
	if m := a.knownManifest(id); m != nil {
		return m.Version
	}
	if src != nil {
		return src.Ref
	}
	return ""
}

// marketUpdate is the update index lists of plugin id at version have,
// nil when there is none.
func marketUpdate(index *plugin.Marketplace, id, have string) *pluginUpdate {
	l := index.Find(id)
	if l == nil || !plugin.NewerVersion(l.Version, have) || l.Manifest().NeedsDalibre() != "" {
		return nil
	}
	return &pluginUpdate{ref: l.Version, label: l.Version, archive: l.Package}
}

// marketListings are the index's plugins the search and category show.
func (a *App) marketListings() []*plugin.Listing {
	mk := &a.mk
	if mk.index == nil {
		return nil
	}
	q := strings.ToLower(strings.TrimSpace(mk.query))
	var out []*plugin.Listing
	for i := range mk.index.Plugins {
		l := &mk.index.Plugins[i]
		if mk.cat != "" && l.Category != mk.cat {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(l.Name+"\n"+l.Description+"\n"+l.Calibre+"\n"+l.Author), q) {
			continue
		}
		out = append(out, l)
	}
	// Most downloaded on calibre first; the community's after the
	// Marketplace's own, as they are listed.
	slices.SortStableFunc(out, func(x, y *plugin.Listing) int { return y.Downloads - x.Downloads })
	return out
}

// listingState is what a listing's row offers.
type listingState int

const (
	listingAvailable listingState = iota
	listingInstalled
	listingUpdate
	// listingNeedsDalibre is a plugin this Dalibre is too old for.
	listingNeedsDalibre
)

func (a *App) listingState(l *plugin.Listing) listingState {
	if a.knownManifest(l.ID) == nil {
		if l.Manifest().NeedsDalibre() != "" {
			return listingNeedsDalibre
		}
		return listingAvailable
	}
	if a.updates[l.ID] != nil {
		return listingUpdate
	}
	return listingInstalled
}

// installListing downloads the plugin l lists and asks the user to allow
// what it asks for, as installing a package does.
func (a *App) installListing(l *plugin.Listing) {
	if a.mk.installing != "" || a.pluginDir == "" {
		return
	}
	a.mk.installing = l.ID
	gh, want := a.gh(), *l
	a.run(func() {
		archive, err := gh.Download(context.Background(), want.Package, os.TempDir())
		var m *plugin.Manifest
		if err == nil {
			m, err = plugin.Inspect(archive)
		}
		if err == nil && m.ID != want.ID {
			err = errors.New(locales.PluginsUpdateWrongPlugin(m.ID))
		}
		a.post(func() {
			a.mk.installing = ""
			if err != nil {
				if archive != "" {
					os.Remove(archive)
				}
				a.toastError(errors.New(locales.PluginsInstallFailed(err.Error())))
				return
			}
			src := &settings.PluginSource{Repo: want.Repo, Ref: m.Version, Auto: true, Marketplace: true}
			if !granted(a.settings.Plugins[m.ID], m) {
				a.consent = &consentAsk{manifest: m, archive: archive, source: src}
				return
			}
			a.finishInstallFrom(archive, src)
		})
	})
}
