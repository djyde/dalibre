package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/plugin"
	"dalibre/internal/plugin/pkgmgr"
)

// The views of plugin tools (Figma "20 · Plugin tools"): each tool as the
// first rows of what a plugin will be able to do (ToolPermissionItem),
// with where it comes from; the Tools section of a plugin's page
// (ToolRow); and a package manager's install, as it runs.

// toolPermissions are the tool rows of the dialog installing m: one per
// tool, before its other permissions. had, for an update, are the tools
// granted before, which aren't asked again.
func (a *App) toolPermissions(c *ui.Context, m *plugin.Manifest, had map[string]string) {
	if len(m.Tools) == 0 {
		return
	}
	a.pickTools(m)
	for _, id := range m.ToolIDs() {
		t := m.Tools[id]
		if had != nil && had[id] == t.Identity() {
			continue
		}
		a.toolPermission(c.Key("tool-"+id), m, id)
	}
}

// toolPermission is the ToolPermissionItem component: running the tool,
// in the warning tone, with the select of where it comes from.
func (a *App) toolPermission(c *ui.Context, m *plugin.Manifest, id string) {
	t := m.Tools[id]
	pick := a.tl.picks[id]
	fg := pal().Warning.FG
	detail := locales.ToolsNotSandboxed()
	_, found := a.tl.pickFound[id]
	switch {
	case pick == plugin.SourceFile && a.tl.pickFiles[id] == "":
		detail = locales.ToolsNoBuildChoose(id)
	case pick != plugin.SourceDownload && pick != plugin.SourceFile && !found:
		detail += " " + locales.ToolsInstallsWith(commandLine(pick, t.Packages.Name(pick)))
	}
	ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Warning.BG).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Padding(1, 0, 0, 0).Children(func() { ui.Icon(c, icons.Terminal).FontSize(14).TextColor(fg) })
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).AlignItems(ui.Start).Children(func() {
			ui.Text(c, locales.ToolsRun(id, t.Version)).FontSize(13).FontWeight(500).TextColor(fg)
			ui.Text(c, detail).FontSize(12).TextColor(fg.Alpha(0.8))
			ui.Box(c).Height(theme.Space4)
			a.toolSourceSelect(c, m, id)
		})
	})
}

// sourceLabel is what the select shows for where tool id of m comes from.
func (a *App) sourceLabel(m *plugin.Manifest, id string) string {
	t := m.Tools[id]
	switch pick := a.tl.picks[id]; pick {
	case plugin.SourceDownload:
		if size := toolSize(t); size != "" {
			return locales.ToolsSourceDownloadSize(size)
		}
		return locales.ToolsSourceDownload()
	case plugin.SourceFile:
		if f := a.tl.pickFiles[id]; f != "" {
			return shortPath(f)
		}
		return locales.ToolsChooseFile()
	default:
		if p, ok := a.tl.pickFound[id]; ok && p.Source == pick {
			return locales.ToolsSourceInstalled(pkgmgr.Title(pick))
		}
		return pkgmgr.Title(pick)
	}
}

// toolSourceSelect is the select of where a tool comes from, in the
// dialog installing its plugin.
func (a *App) toolSourceSelect(c *ui.Context, m *plugin.Manifest, id string) {
	label := a.sourceLabel(m, id)
	b := ui.ButtonBase(c).Height(theme.ControlS).Padding(0, theme.Space8, 0, theme.Space10).Gap(theme.Space6).AlignItems(ui.Center).
		Radius(theme.RadiusSM).Background(pal().Surface).Border(1, pal().Border).Label(locales.ToolsSourceLabel(id, label))
	if b.Hovered() {
		b.Border(1, pal().BorderStrong)
	}
	b.PopoverMenuButton(func(menu *ui.Menu) {
		a.toolSourceMenu(menu, m, id, a.tl.picks[id], func(source, file string) {
			if source == plugin.SourceFile && file == "" {
				a.chooseToolFile(id, func(f string) {
					a.tl.picks[id], a.tl.pickFiles[id] = plugin.SourceFile, f
				})
				return
			}
			a.tl.picks[id] = source
		})
	})
	b.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(pal().Text).SingleLine()
		ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(pal().IconMuted)
	})
}

// toolSourceMenu fills a menu of where tool id of m can come from: a
// ToolSourceOption each. chose gets the source picked, with "" for a
// file still to choose.
func (a *App) toolSourceMenu(menu *ui.Menu, m *plugin.Manifest, id, current string, chose func(source, file string)) {
	menu.MaxWidth(360)
	t := m.Tools[id]
	if _, ok := t.Build(); ok {
		it := menu.Item(locales.ToolsDownloadTitle()).ID("download").Subtitle(locales.ToolsDownloadDetail(toolHost(t))).Radio(current == plugin.SourceDownload)
		if size := toolSize(t); size != "" {
			it.Trailing(size)
		}
		if it.Chosen() {
			chose(plugin.SourceDownload, "")
		}
	}
	for _, mgr := range t.Packages.Names() {
		if !pkgmgr.Available(mgr) {
			continue
		}
		title, sub := locales.ToolsInstallWith(pkgmgr.Title(mgr)), locales.ToolsInstallWithDetail(commandLine(mgr, t.Packages.Name(mgr)))
		if mgr == pkgmgr.Winget {
			sub = locales.ToolsInstallWithWingetDetail()
		}
		if p, ok := plugin.FindInManager(t, id, mgr); ok {
			title, sub = locales.ToolsUseInstalled(pkgmgr.Title(mgr), id, p.Version), shortPath(p.Path)
		}
		if menu.Item(title).ID("mgr:" + mgr).Subtitle(sub).Radio(current == mgr).Chosen() {
			chose(mgr, "")
		}
	}
	if menu.Item(locales.ToolsUseMine(id)).ID("file").Subtitle(locales.ToolsUseMineDetail(t.MinVersion())).Radio(current == plugin.SourceFile).Chosen() {
		chose(plugin.SourceFile, "")
	}
}

// chooseToolFile asks for the program file of tool id.
func (a *App) chooseToolFile(id string, chosen func(path string)) {
	a.run(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: locales.ToolsChooseFileTitle(id)})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { chosen(paths[0]) })
	})
}

// toolsSection is the Tools section of plugin m's page: a ToolRow each.
func (a *App) toolsSection(c *ui.Context, m *plugin.Manifest) {
	if len(m.Tools) == 0 {
		return
	}
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.ToolsTitle())
		for _, id := range m.ToolIDs() {
			a.toolRow(c.Key(id), m, id)
		}
		ui.Text(c, locales.ToolsSectionNote(strings.Join(m.ToolIDs(), ", "))).FontSize(12).TextColor(pal().TextTertiary)
	})
}

// toolRow is the ToolRow component: a tool, where it comes from and its
// state.
func (a *App) toolRow(c *ui.Context, m *plugin.Manifest, id string) {
	t := m.Tools[id]
	job := a.toolJobFor(m.ID, id)
	place, here := a.tl.places[m.ID][id]
	missing := !here && slicesContains(a.tl.missing[m.ID], id)
	_, hasBuild := t.Build()

	icon, bg, fg := icons.Terminal, pal().Subtle, pal().Text
	name, detail := id+" "+t.Version, ""
	switch {
	case job != nil && job.running:
		icon = icons.Loader
		if job.source == plugin.SourceDownload {
			detail = locales.ToolsDownloading(job.host, progressOf(job.done, job.total))
		} else {
			detail = locales.ToolsRunning(commandLine(job.source, job.pkg), elapsed(job.started))
		}
	case job != nil && job.err != "":
		icon, bg, fg = icons.Alert, pal().Warning.BG, pal().Warning.FG
		detail = locales.ToolsFailed(job.err)
	case here:
		if place.Version != "" {
			name = id + " " + place.Version
		}
		switch place.Source {
		case plugin.SourceDownload:
			detail = locales.ToolsFromDownload(toolSize(t))
		case plugin.SourceFile:
			name, detail = id, locales.ToolsFromFile(shortPath(place.Path))
		default:
			detail = locales.ToolsFromManager(pkgmgr.Title(place.Source), shortPath(place.Path))
		}
	case missing && !hasBuild && len(t.Packages.Names()) == 0:
		detail = locales.ToolsNoBuildChoose(id)
	case missing:
		icon, bg, fg = icons.Alert, pal().Warning.BG, pal().Warning.FG
		detail = locales.ToolsMissing(m.Name)
	default:
		detail = locales.ToolsWaiting()
	}
	ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(bg).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(fg)
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, name).FontSize(13).FontWeight(500).TextColor(fg)
			ui.Text(c, detail).FontSize(12).TextColor(fg.Alpha(0.8)).MaxLines(3)
		})
		switch {
		case job != nil && job.running && job.source == plugin.SourceDownload:
			ui.Box(c).Width(120).Children(func() {
				f := float64(-1)
				if job.total > 0 {
					f = float64(job.done) / float64(job.total)
				}
				ui.Progress(c, f).Height(4)
			})
		case job != nil && job.running:
			if widgets.ButtonSized(c, widgets.Ghost, nil, locales.ToolsShowOutput(), theme.ControlS).Clicked() {
				a.tl.dialog = toolKey(m.ID, id)
			}
		case job != nil && job.err != "":
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.ToolsTryAgain(), theme.ControlS).Clicked() {
				a.retryTool(m, id)
			}
			a.toolChangeButton(c, m, id)
		case missing && !hasBuild && len(t.Packages.Names()) == 0:
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.ToolsChooseFile(), theme.ControlS).Clicked() {
				a.chooseToolFile(id, func(f string) { a.getTool(m, id, plugin.SourceFile, f, true) })
			}
		case missing:
			a.pickTools(m)
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.ToolsGet(), theme.ControlS).Clicked() {
				a.getTool(m, id, a.tl.picks[id], a.tl.pickFiles[id], true)
			}
			a.toolChangeButton(c, m, id)
		default:
			a.toolChangeButton(c, m, id)
		}
	})
}

// toolChangeButton is Change: the menu of where the tool comes from,
// which applies at once.
func (a *App) toolChangeButton(c *ui.Context, m *plugin.Manifest, id string) {
	current := ""
	if st := a.settings.Plugins[m.ID]; st != nil && st.Tools[id] != nil {
		current = st.Tools[id].Source
	} else if p, ok := a.tl.places[m.ID][id]; ok {
		current = p.Source
	}
	b := ui.ButtonBase(c).Height(theme.ControlS).Padding(0, theme.Space10).Radius(theme.RadiusSM).Center().Label(locales.ToolsChangeLabel(id))
	if b.Hovered() {
		b.Background(pal().Hover)
	}
	b.PopoverMenuButton(func(menu *ui.Menu) {
		a.toolSourceMenu(menu, m, id, current, func(source, file string) {
			if source == plugin.SourceFile && file == "" {
				a.chooseToolFile(id, func(f string) { a.getTool(m, id, plugin.SourceFile, f, true) })
				return
			}
			a.getTool(m, id, source, file, true)
		})
	})
	b.Children(func() { ui.Text(c, locales.ToolsChange()).FontSize(12).FontWeight(500) })
}

// retryTool gets a tool again from where it was chosen to come from.
func (a *App) retryTool(m *plugin.Manifest, id string) {
	source := plugin.SourceDownload
	if st := a.settings.Plugins[m.ID]; st != nil && st.Tools[id] != nil {
		source = st.Tools[id].Source
	}
	delete(a.tl.jobs, toolKey(m.ID, id))
	a.startToolJob(m, id, source)
}

// toolInstallDialog shows a package manager installing a tool: its
// output as it runs, and what it said when it failed.
func (a *App) toolInstallDialog(c *ui.Context) {
	key := a.tl.dialog
	j := a.tl.jobs[key]
	if key == "" || j == nil {
		a.tl.dialog = ""
		return
	}
	open := true
	m := a.knownManifest(j.plugin)
	failed := !j.running && j.err != ""
	widgets.DialogFrame(c, &open, 520, func() {
		title := locales.ToolsInstallingTitle(j.name)
		if failed {
			title = locales.ToolsInstallFailedTitle(pkgmgr.Title(j.source), j.tool)
		}
		ui.Text(c, title).FontSize(16).FontWeight(600).Grow(1)
	}, func() {
		ui.Column(c).Padding(theme.Space20, theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			if failed {
				ui.Text(c, locales.ToolsInstallFailedBody(j.name)).FontSize(13).TextColor(pal().TextSecondary)
				widgets.Overline(c, strings.ToUpper(locales.ToolsWhatItSaid(pkgmgr.Title(j.source))))
			} else {
				ui.Text(c, locales.ToolsInstallingBody(j.tool)).FontSize(13).TextColor(pal().TextSecondary)
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14).Label(locales.ToolsRunningLabel())
					ui.Text(c, locales.ToolsRunningCommand(commandLine(j.source, j.pkg))).FontSize(13).FontWeight(500).Grow(1).SingleLine()
					ui.Text(c, elapsed(j.started)).FontSize(12).TextColor(pal().TextTertiary)
				})
			}
			// The end of what it printed, as a terminal shows it.
			lines := j.lines
			if len(lines) > 9 {
				lines = lines[len(lines)-9:]
			}
			ui.Column(c).Height(180).Padding(theme.Space10, theme.Space12).Gap(theme.Space2).Radius(theme.RadiusSM).Background(pal().Subtle).Justify(ui.End).Clip().Children(func() {
				ui.Text(c, "$ "+commandLine(j.source, j.pkg)).FontSize(12).Font(theme.FontMono).TextColor(pal().Text).MaxLines(2)
				for i, l := range lines {
					col := pal().TextSecondary
					if failed && (strings.HasPrefix(l, "Error") || strings.HasPrefix(l, "error")) {
						col = pal().Danger.FG
					}
					ui.Text(c.Key(i), l).FontSize(12).Font(theme.FontMono).TextColor(col).MaxLines(2)
				}
				if failed {
					ui.Text(c, j.err).FontSize(12).Font(theme.FontMono).TextColor(pal().Danger.FG).MaxLines(2)
				}
			})
			if !failed {
				ui.Text(c, locales.ToolsCloseKeepsGoing()).FontSize(12).TextColor(pal().TextTertiary)
			}
		})
	}, func() {
		if failed {
			if widgets.Button(c, widgets.Ghost, icons.Copy, locales.ToolsCopyLog()).Clicked() {
				mygo.Clipboard.Write(transfer.TextData("$ " + commandLine(j.source, j.pkg) + "\n" + strings.Join(j.lines, "\n") + "\n" + j.err))
			}
			ui.Spacer(c)
			if widgets.Button(c, widgets.Secondary, nil, locales.ToolsTryAgain()).Clicked() && m != nil {
				a.retryTool(m, j.tool)
				a.tl.dialog = toolKey(j.plugin, j.tool)
			}
			if m != nil {
				if _, ok := m.Tools[j.tool].Build(); ok {
					label := locales.ToolsSourceDownload()
					if size := toolSize(m.Tools[j.tool]); size != "" {
						label = locales.ToolsDownloadInstead(size)
					}
					if widgets.Button(c, widgets.Primary, nil, label).Clicked() {
						a.tl.dialog = ""
						a.getTool(m, j.tool, plugin.SourceDownload, "", true)
					}
				}
			}
			return
		}
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.ToolsStop()).Clicked() {
			j.cancel()
			a.tl.dialog = ""
		}
		if widgets.Button(c, widgets.Secondary, nil, locales.ToolsClose()).Clicked() {
			open = false
		}
	})
	if !open {
		a.tl.dialog = ""
	}
}

// progressOf is "12 of 31 MB", or what has come so far.
func progressOf(done, total int64) string {
	if total <= 0 {
		return humanSize(done)
	}
	return locales.PluginsDownloadProgress(strings.TrimSuffix(humanSize(done), " MB"), humanSize(total))
}

// elapsed is how long since t, as "1:12".
func elapsed(t time.Time) string {
	s := int(time.Since(t).Seconds())
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func slicesContains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
