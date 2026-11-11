package app

import (
	"context"
	"errors"
	"github.com/egoist/mygo/i18n"
	"os"
	"slices"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// Plugins from GitHub (Figma "08 · OPDS & Settings", Plugins): installed
// from a release or a branch of a repository, and kept up to date from
// there. An update is a newer release, or a new commit on the branch. One
// that asks for no new permissions installs when the user says, or by
// itself when they ticked that; one that asks for more waits for them, as
// any update does.

// updateEvery is how often plugins are checked for updates.
const updateEvery = 24 * time.Hour

// pluginUpdate is a newer version of a plugin found on GitHub.
type pluginUpdate struct {
	ref       string // the release's tag, or the commit
	label     string // "v1.1.0", or "main at 8d41e07"
	notes     string // the release's notes, or the commit's message
	published time.Time
	archive   string // where to download it
	busy      bool   // downloading it
}

// ghInstall is the Install from GitHub sheet.
type ghInstall struct {
	open     bool
	text     string // the repository box
	repo     plugin.Repo
	finding  bool
	err      string
	releases []plugin.Release
	branches []plugin.Branch
	// tag or branch is what is chosen: a release, or a branch.
	tag, branch string
	// What was downloaded for the choice, to show and install.
	preparing bool
	archive   string
	manifest  *plugin.Manifest
	ref       string // the tag, or the commit
	detail    string // "released 3 days ago · test-1.1.0.dalibre-plugin"
	keep      bool   // keep it up to date
	// readme is the repository's README at what is chosen.
	readme        string
	readmeLoading bool
}

func (a *App) gh() *plugin.GitHub {
	if a.github == nil {
		a.github = &plugin.GitHub{}
	}
	return a.github
}

// openGitHubInstall opens the Install from GitHub sheet.
func (a *App) openGitHubInstall() {
	a.ghi.discard()
	a.ghi = ghInstall{open: true, keep: true}
}

// discard removes what the sheet downloaded.
func (g *ghInstall) discard() {
	if g.archive != "" {
		os.Remove(g.archive)
		g.archive = ""
	}
}

// ghFind looks up the repository the box names: its releases and
// branches. The latest release is chosen, or else the default branch.
func (a *App) ghFind() {
	g := &a.ghi
	repo, err := plugin.ParseRepo(g.text)
	if err != nil {
		g.err = err.Error()
		return
	}
	g.discard()
	*g = ghInstall{open: true, keep: g.keep, text: g.text, repo: repo, finding: true}
	gh := a.gh()
	a.run(func() {
		ctx := context.Background()
		def, err := gh.DefaultBranch(ctx, repo)
		var rels []plugin.Release
		var brs []plugin.Branch
		if err == nil {
			rels, err = gh.Releases(ctx, repo)
		}
		if err == nil {
			brs, err = gh.Branches(ctx, repo)
		}
		a.post(func() {
			if a.ghi.repo != repo || !a.ghi.finding {
				return
			}
			g.finding = false
			if err != nil {
				g.err = err.Error()
				return
			}
			// The default branch first.
			slices.SortStableFunc(brs, func(x, y plugin.Branch) int {
				switch {
				case x.Name == def:
					return -1
				case y.Name == def:
					return 1
				}
				return 0
			})
			g.releases, g.branches = rels, brs
			if r, ok := plugin.Latest(rels); ok {
				a.ghChoose(r.Tag, "")
			} else if def != "" {
				a.ghChoose("", def)
			} else {
				g.err = locales.PluginsRepoEmpty()
			}
		})
	})
}

// ghChoose picks a release (tag) or a branch, and downloads it to show
// what it is.
func (a *App) ghChoose(tag, branch string) {
	g := &a.ghi
	g.discard()
	g.tag, g.branch, g.manifest, g.err, g.preparing = tag, branch, nil, "", true
	repo, gh := g.repo, a.gh()
	var rel plugin.Release
	for _, r := range g.releases {
		if r.Tag == tag {
			rel = r
		}
	}
	a.run(func() {
		ctx := context.Background()
		var archive, ref, detail string
		var err error
		if branch != "" {
			var c plugin.Commit
			c, err = gh.Commit(ctx, repo, branch)
			if err == nil {
				ref = c.SHA
				detail = locales.PluginsDetailBranch(branch, c.Short(), ago(c.Date))
				archive, err = gh.Download(ctx, c.Archive, os.TempDir())
			}
		} else {
			ref = rel.Tag
			if rel.Asset != "" {
				detail = locales.PluginsDetailReleaseAsset(rel.Tag, ago(rel.Published), rel.Asset, humanSize(rel.Size))
			} else {
				detail = locales.PluginsDetailRelease(rel.Tag, ago(rel.Published))
			}
			archive, err = gh.Download(ctx, rel.Archive, os.TempDir())
		}
		var m *plugin.Manifest
		if err == nil {
			m, err = plugin.Inspect(archive)
		}
		readme := ""
		if err == nil {
			readme, _ = gh.Readme(ctx, repo, ref) // a missing README is no failure
		}
		a.post(func() {
			if a.ghi.repo != repo || a.ghi.tag != tag || a.ghi.branch != branch || !a.ghi.preparing {
				if archive != "" {
					os.Remove(archive)
				}
				return
			}
			g.preparing = false
			if err != nil {
				if archive != "" {
					os.Remove(archive)
				}
				g.err = locales.PluginsNotAPlugin(err.Error())
				return
			}
			g.archive, g.manifest, g.ref, g.detail, g.readme = archive, m, ref, detail, readme
		})
	})
}

// ghInstallChosen installs what the sheet shows. Its permissions were
// shown in the sheet, so installing grants them.
func (a *App) ghInstallChosen() {
	g := &a.ghi
	if g.manifest == nil || g.archive == "" {
		return
	}
	m := g.manifest
	st := a.settings.Plugin(m.ID)
	st.Granted = grantFor(m)
	if len(m.Tools) > 0 {
		a.getPickedTools(m)
	}
	src := &settings.PluginSource{Repo: g.repo.String(), Branch: g.branch, Ref: g.ref, Auto: g.keep}
	archive := g.archive
	g.archive = "" // finishInstall removes it
	a.ghi = ghInstall{}
	a.pl.sel = m.ID
	a.finishInstallFrom(archive, src)
}

// ago says how long ago t was, roughly.
func ago(t time.Time) string {
	if t.IsZero() {
		return locales.PluginsAgoUnknown()
	}
	d := time.Since(t)
	switch {
	case d < time.Hour:
		return locales.PluginsAgoNow()
	case d < 24*time.Hour:
		n := int(d.Hours())
		return locales.PluginsAgoHours(n)
	case d < 60*24*time.Hour:
		n := int(d.Hours() / 24)
		return locales.PluginsAgoDays(n)
	}
	return i18n.FormatDate(i18n.Locale(), t, i18n.DateOptions{Skeleton: "yMMMd"})
}

// ---- Updates

// checkUpdates looks for newer versions of the plugins installed from
// GitHub: when force, now; else when the last check is a day old.
func (a *App) checkUpdates(force bool) {
	if a.updatesChecking || !force && time.Since(a.settings.UpdatesChecked) < updateEvery {
		return
	}
	type item struct {
		id  string
		src settings.PluginSource
	}
	var items []item
	for id, st := range a.settings.Plugins {
		if st.Source != nil {
			items = append(items, item{id, *st.Source})
		}
	}
	if len(items) == 0 {
		return
	}
	a.updatesChecking = true
	gh, marketURL := a.gh(), a.marketURL
	a.run(func() {
		found := map[string]*pluginUpdate{}
		var errs []error
		// The Marketplace's index, read once, for the plugins from there.
		var index *plugin.Marketplace
		var indexErr error
		market := func() (*plugin.Marketplace, error) {
			if index == nil && indexErr == nil {
				index, _, indexErr = gh.FetchMarketplace(context.Background(), marketURL)
			}
			return index, indexErr
		}
		for _, it := range items {
			u, err := latestUpdate(gh, it.id, it.src, market)
			if err != nil {
				errs = append(errs, errors.New(locales.PluginsCheckFailed(it.id, err.Error())))
				continue
			}
			if u != nil {
				found[it.id] = u
			}
		}
		a.post(func() {
			a.updatesChecking = false
			a.settings.UpdatesChecked = time.Now()
			a.saveSettings()
			if a.updates == nil {
				a.updates = map[string]*pluginUpdate{}
			}
			for _, it := range items {
				delete(a.updates, it.id)
			}
			for id, u := range found {
				a.updates[id] = u
				if st := a.settings.Plugins[id]; st != nil && st.Source != nil && st.Source.Auto {
					a.updatePlugin(id, true)
				}
			}
			if force {
				for _, err := range errs {
					a.toastError(err)
				}
				if len(errs) == 0 && len(found) == 0 {
					a.toast("%s", locales.PluginsUpToDate())
				}
			}
		})
	})
}

// latestUpdate is the update src has on GitHub, or in the Marketplace's
// index (market) for a plugin from there; nil when it is up to date.
func latestUpdate(gh *plugin.GitHub, id string, src settings.PluginSource, market func() (*plugin.Marketplace, error)) (*pluginUpdate, error) {
	if src.Marketplace {
		index, err := market()
		if err != nil {
			return nil, err
		}
		return marketUpdate(index, id, src.Ref), nil
	}
	repo, err := plugin.ParseRepo(src.Repo)
	if err != nil {
		return nil, err
	}
	ctx := context.Background()
	if src.Branch != "" {
		c, err := gh.Commit(ctx, repo, src.Branch)
		if err != nil || c.SHA == src.Ref {
			return nil, err
		}
		return &pluginUpdate{ref: c.SHA, label: locales.PluginsUpdateBranchLabel(src.Branch, c.Short()), notes: c.Message, published: c.Date, archive: c.Archive}, nil
	}
	rels, err := gh.Releases(ctx, repo)
	if err != nil {
		return nil, err
	}
	r, ok := plugin.Latest(rels)
	if !ok || r.Tag == src.Ref {
		return nil, nil
	}
	return &pluginUpdate{ref: r.Tag, label: r.Tag, notes: r.Notes, published: r.Published, archive: r.Archive}, nil
}

// updatePlugin downloads plugin id's update and installs it, or asks the
// user when it asks for more than they granted. auto keeps quiet about
// one that has to ask: it waits in the plugin's row.
func (a *App) updatePlugin(id string, auto bool) {
	u := a.updates[id]
	st := a.settings.Plugins[id]
	if u == nil || u.busy || st == nil || st.Source == nil {
		return
	}
	u.busy = true
	src := *st.Source
	src.Ref = u.ref
	gh := a.gh()
	a.run(func() {
		archive, err := gh.Download(context.Background(), u.archive, os.TempDir())
		var m *plugin.Manifest
		if err == nil {
			m, err = plugin.Inspect(archive)
		}
		if err == nil && m.ID != id {
			err = errors.New(locales.PluginsUpdateWrongPlugin(m.ID))
		}
		a.post(func() {
			u.busy = false
			if err != nil {
				if archive != "" {
					os.Remove(archive)
				}
				a.toastError(errors.New(locales.PluginsUpdateFailed(a.pluginName(id), err.Error())))
				return
			}
			if !granted(a.settings.Plugins[id], m) {
				if auto {
					os.Remove(archive)
					return
				}
				a.consent = &consentAsk{manifest: m, archive: archive, source: &src}
				return
			}
			a.finishInstallFrom(archive, &src)
		})
	})
}
