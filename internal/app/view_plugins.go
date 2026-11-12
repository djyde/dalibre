package app

import (
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/mcp"
	"dalibre/internal/plugin"
)

// The Plugins dialog (Figma "06 · Plugins" and "07 · Get books"): the
// plugins Dalibre found, each one's switch, settings and access, and
// installing more.

type pluginsState struct {
	sel       string            // the plugin shown
	values    map[string]string // its settings as being edited
	valuesFor string            // whose values are being edited
	uninstall bool              // asking to uninstall sel
}

// openPlugins opens the Plugins window at Installed, on plugin id or the
// first.
func (a *App) openPlugins(id string) {
	a.pluginsOpen, a.plTab = true, pluginsTabInstalled
	a.checkUpdates(false)
	if id != "" {
		a.pl.sel = id
	}
}

// openMarketplace opens the Plugins window at the Marketplace.
func (a *App) openMarketplace() {
	a.pluginsOpen, a.plTab = true, pluginsTabMarketplace
	a.checkUpdates(false)
	a.loadMarketplace(false)
}

// pluginsButton is the toolbar's Plugins button (Figma "18 · Plugin
// marketplace"): it opens the Plugins window.
func (a *App) pluginsButton(c *ui.Context) {
	if widgets.IconButton(c, icons.Puzzle, locales.PluginsTitle(), a.pluginsOpen).Clicked() {
		a.openPlugins("")
	}
}

// pluginsDialog is the Plugins window: Installed and Marketplace, in tabs
// of its header.
func (a *App) pluginsDialog(c *ui.Context) {
	if !a.pluginsOpen {
		return
	}
	widgets.TallDialogFrame(c, &a.pluginsOpen, pluginsWidth, pluginsHeight, func() {
		ui.Text(c, locales.PluginsTitle()).FontSize(16).FontWeight(600)
		ui.Box(c).Width(theme.Space12)
		a.pluginsTabs(c)
		ui.Spacer(c)
	}, func() {
		ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
			if a.plTab == pluginsTabMarketplace {
				a.marketplacePane(c)
			} else {
				a.pluginsPane(c)
			}
		})
	}, func() {
		ui.Text(c, locales.PluginsSandboxNote()).FontSize(12).TextColor(pal().TextTertiary).Grow(1)
		if widgets.Button(c, widgets.Ghost, icons.External, locales.PluginsWriteOne()).Tooltip(strings.TrimPrefix(mcp.PluginDocs, "https://")).Clicked() {
			openURL(mcp.PluginDocs)
		}
		if widgets.Button(c, widgets.Primary, nil, locales.CommonDone()).Clicked() {
			a.pluginsOpen = false
		}
	})
	a.githubSheet(c)
}

// The Plugins window's size, as the window allows: wide enough for three
// cards beside a plugin's details.
const (
	pluginsWidth  = 1200
	pluginsHeight = 800
)

// pluginStatus says what state a plugin is in, as its row shows it.
func (a *App) pluginStatus(k knownPlugin) (text string, tone ui.Color, loaded bool) {
	id := k.manifest.ID
	if p := a.pluginByID(id); p != nil {
		if p.MissingSettings() != nil {
			return locales.PluginsStatusNeeds(strings.Join(p.MissingSettings(), ", ")), pal().Warning.FG, true
		}
		if err := a.tl.checks[id]; err != nil {
			return checkText(err), pal().Warning.FG, true
		}
		v := k.manifest.Version
		switch {
		case k.builtin:
			v = locales.PluginsStatusBuiltIn(v)
		case k.linked != "":
			v = locales.PluginsStatusFromFolder(v)
		case k.manifest.Author != "":
			v = locales.PluginsStatusVersionBy(v, k.manifest.Author)
		}
		return v, pal().TextTertiary, true
	}
	if slices.ContainsFunc(a.pluginsPending, func(m *plugin.Manifest) bool { return m.ID == id }) {
		return locales.PluginsStatusNeedsReview(), pal().Warning.FG, false
	}
	if missing := a.tl.missing[id]; len(missing) > 0 {
		for _, t := range missing {
			if j := a.toolJobFor(id, t); j != nil && j.running {
				return locales.ToolsStatusGetting(t), pal().TextAccent, false
			}
		}
		return locales.ToolsStatusNeeds(strings.Join(missing, ", ")), pal().Warning.FG, false
	}
	if u := a.updates[id]; u != nil && loadedOK(a, id) {
		return locales.PluginsStatusUpdate(k.manifest.Version, u.label), pal().TextAccent, true
	}
	if st := a.settings.Plugins[id]; st != nil && st.Disabled {
		return locales.PluginsStatusOff(k.manifest.Version), pal().TextTertiary, false
	}
	return locales.PluginsStatusFailed(), pal().Danger.FG, false
}

func loadedOK(a *App, id string) bool { return a.pluginByID(id) != nil }

// pluginsPane is the Plugins window's Installed tab: the plugins Dalibre found, and the
// one chosen.
func (a *App) pluginsPane(c *ui.Context) {
	known := a.pluginsKnown
	if a.pl.sel == "" || !slices.ContainsFunc(known, func(k knownPlugin) bool { return k.manifest.ID == a.pl.sel }) {
		a.pl.sel = ""
		if len(known) > 0 {
			a.pl.sel = known[0].manifest.ID
		}
	}
	ui.Column(c).Width(320).Padding(theme.Space12).Gap(theme.Space2).Background(pal().Chrome).BorderWidth(0, 1, 0, 0).BorderColor(pal().BorderSubtle).AlignItems(ui.Stretch).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
			for _, k := range known {
				a.pluginRow(c.Key(k.manifest.ID), k)
			}
			for i, err := range a.pluginErrs {
				ui.Row(c.Key(i)).Padding(theme.Space8, theme.Space10).Gap(theme.Space8).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Danger.FG)
					ui.Text(c, err.Error()).FontSize(12).TextColor(pal().Danger.FG).MaxLines(3).Grow(1)
				})
			}
		})
		a.pluginsFolderActions(c)
	})
	ui.Scroll(c).Grow(1).MinWidth(0).Padding(theme.Space24).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
		for _, k := range known {
			if k.manifest.ID == a.pl.sel {
				a.pluginPane(c, k)
			}
		}
		if len(known) == 0 {
			ui.Text(c, locales.PluginsEmpty()).TextColor(pal().TextSecondary)
		}
	})
	if a.pl.uninstall {
		name := a.pluginName(a.pl.sel)
		title, msg, ok := locales.PluginsUninstallTitle(name), locales.PluginsUninstallMessage(), locales.PluginsUninstallConfirm()
		if st := a.settings.Plugins[a.pl.sel]; st != nil && st.Path != "" {
			title, msg, ok = locales.PluginsRemoveTitle(name), locales.PluginsRemoveMessage(), locales.CommonRemove()
		}
		if ui.AlertDialog(c, &a.pl.uninstall, title, msg, locales.CommonCancel(), ok) == 1 {
			a.uninstallPlugin(a.pl.sel)
		}
	}
}

// pluginRow is the PluginRow component.
func (a *App) pluginRow(c *ui.Context, k knownPlugin) {
	m := k.manifest
	sel := a.pl.sel == m.ID
	status, tone, loaded := a.pluginStatus(k)
	pending := status == locales.PluginsStatusNeedsReview()
	row := ui.ButtonBase(c).Padding(theme.Space8, theme.Space10).Gap(theme.Space10).Radius(theme.RadiusMD).AlignItems(ui.Center).Label(m.Name)
	switch {
	case sel:
		row.Background(pal().SurfaceRaised).Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.06)).Shadow(0, 0, 0, 1, pal().BorderSubtle.Alpha(0.6))
	case row.Hovered():
		row.Background(pal().Hover)
	}
	row.OnClick(func() { a.pl.sel = m.ID }).Children(func() {
		pluginTile(c, m, 28)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, m.Name).FontSize(13).FontWeight(500).SingleLine()
			ui.Text(c, status).FontSize(12).TextColor(tone).SingleLine()
		})
		switch {
		case pending:
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.PluginsReview(), theme.ControlS).Clicked() {
				a.askConsent(m.ID)
			}
		case len(a.tl.missing[m.ID]) > 0:
			if tone == pal().TextAccent {
				ui.Spinner(c).Size(16, 16).Label(status)
			} else {
				ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Warning.FG)
			}
		case a.updates[m.ID] != nil && loaded:
			if a.updates[m.ID].busy {
				ui.Spinner(c).Size(16, 16).Label(locales.PluginsUpdatingLabel(m.Name))
			} else if widgets.ButtonSized(c, widgets.Secondary, nil, locales.CommonUpdate(), theme.ControlS).Clicked() {
				a.updatePlugin(m.ID, false)
			}
		case status == locales.PluginsStatusFailed():
			ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Danger.FG)
		default:
			on := loaded
			ui.Switch(c, &on).Label(locales.PluginsTurnOnLabel(m.Name))
			if on != loaded {
				a.setPluginEnabled(m.ID, on)
			}
		}
	})
}

// pluginTile is a plugin's icon on a tinted square.
func pluginTile(c *ui.Context, m *plugin.Manifest, size float32) {
	ui.Box(c).Size(size, size).Radius(theme.RadiusSM).Background(pal().AccentSubtle).Center().Children(func() {
		ui.Icon(c, pluginIcon(m)).FontSize(size * 0.55).TextColor(pal().TextAccent)
	})
}

// pluginsFolderActions ends the plugin list: Add plugin, a menu of the
// ways to install one, and ⋯ for the plugins folder.
func (a *App) pluginsFolderActions(c *ui.Context) {
	ui.Row(c).Padding(theme.Space8, 0, 0, 0).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		add := ui.ButtonBase(c).Grow(1).Height(theme.ControlM).Padding(0, theme.Space12).Gap(theme.Space6).Radius(theme.RadiusSM).
			AlignItems(ui.Center).Justify(ui.Center).Background(pal().Surface).Border(1, pal().Border).Label(locales.PluginsAdd())
		if add.Hovered() {
			add.Border(1, pal().BorderStrong)
		}
		add.PopoverMenuButton(func(m *ui.Menu) {
			if a.pluginDir != "" && m.Item(locales.PluginsAddFromGithub()).Icon(icons.Menu(icons.GitBranch)).Chosen() {
				a.openGitHubInstall()
			}
			if m.Item(locales.PluginsAddFromFile()).Icon(icons.Menu(icons.File)).Chosen() {
				a.choosePluginPackage()
			}
			if m.Item(locales.PluginsAddFromFolder()).Icon(icons.Menu(icons.Folder)).Chosen() {
				a.choosePluginFolder()
			}
		})
		add.Children(func() {
			ui.Icon(c, icons.Plus).FontSize(14).TextColor(pal().Icon)
			ui.Text(c, locales.PluginsAdd()).FontSize(13).FontWeight(500)
		})
		if a.pluginDir == "" {
			return
		}
		more := ui.ButtonBase(c).Size(theme.ControlM, theme.ControlM).Radius(theme.RadiusSM).Center().Label(locales.PluginsMoreActions()).Tooltip(locales.PluginsMore())
		if more.Hovered() {
			more.Background(pal().Hover)
		}
		more.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(locales.PluginsOpenFolder()).Icon(icons.Menu(icons.Folder)).Chosen() {
				os.MkdirAll(a.pluginDir, 0o755)
				go mygo.Shell.OpenPath(a.pluginDir)
			}
			if m.Item(locales.PluginsReload()).Icon(icons.Menu(icons.Refresh)).Chosen() {
				a.loadPlugins()
			}
		})
		more.Children(func() { ui.Icon(c, icons.More).FontSize(16).TextColor(pal().Icon) })
	})
}

// choosePluginPackage asks for a .dalibre-plugin file and installs it.
func (a *App) choosePluginPackage() {
	a.run(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: locales.PluginsInstallDialogTitle(),
			Filters: []mygo.FileFilter{{Name: locales.PluginsPackageFilter(), Extensions: []string{strings.TrimPrefix(plugin.PackageExt, ".")}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		a.post(func() { a.installPlugin(paths[0]) })
	})
}

// choosePluginFolder asks for a plugin's folder (one with plugin.json)
// and links it: the plugin loads from there, and reloads as it changes.
func (a *App) choosePluginFolder() {
	a.run(func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: locales.PluginsInstallFromFolderTitle(), Directory: true})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.post(func() { a.installPlugin(dirs[0]) })
	})
}

// pluginPane is the plugin chosen: what it is, adds and may reach, and
// its settings.
func (a *App) pluginPane(c *ui.Context, k knownPlugin) {
	m := k.manifest
	_, _, loaded := a.pluginStatus(k)
	ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
		pluginTile(c, m, 40)
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, m.Name).FontSize(16).FontWeight(600)
			parts := []string{locales.PluginsVersion(m.Version)}
			if m.Author != "" {
				parts = append(parts, locales.PluginsByAuthor(m.Author))
			}
			if m.License != "" {
				parts = append(parts, m.License)
			}
			ui.Text(c, strings.Join(parts, " · ")).FontSize(12).TextColor(pal().TextTertiary)
		})
	})
	if m.Description != "" {
		ui.Text(c, m.Description).FontSize(13).TextColor(pal().TextSecondary)
	}
	if u := a.updates[m.ID]; u != nil && loaded {
		a.updateBanner(c, m, u)
	}
	if st := a.settings.Plugins[m.ID]; st != nil && st.Source != nil && !k.builtin {
		a.pluginSource(c, m, st.Source)
	}
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.PluginsAddsToTitle())
		ui.Row(c).Gap(theme.Space6).Children(func() {
			if m.IsBookSource() {
				widgets.Tag(c, locales.CommonGetBooks(), false, nil)
			}
			if m.IsMetadataSource() {
				widgets.Tag(c, locales.CommonFetchMetadata(), false, nil)
			}
			if m.IsFileType() {
				widgets.Tag(c, locales.PluginsCapBookFiles(), false, nil)
			}
			if m.HasLibraryAction() {
				widgets.Tag(c, locales.MainNavTools(), false, nil)
			}
			if slices.ContainsFunc(m.Capabilities.Actions, func(act plugin.Action) bool { return act.Scope != plugin.ScopeLibrary }) {
				widgets.Tag(c, locales.PluginsCapBookMenu(), false, nil)
			}
		})
		if m.IsBookSource() {
			ui.Text(c, locales.PluginsGetBooksNote(m.Name)).FontSize(12).TextColor(pal().TextTertiary)
		}
		if m.IsMetadataSource() {
			ui.Text(c, locales.PluginsFetchMetadataNote(m.Name)).FontSize(12).TextColor(pal().TextTertiary)
		}
		if m.IsFileType() {
			// What it does with files: as they are added, from the book
			// menu (when it exports process), after books change.
			manual := true
			if p := a.pluginByID(m.ID); p != nil {
				manual = p.Defines("process")
			}
			note := ""
			switch {
			case m.RunsOnImport():
				note = locales.PluginsBookFilesNote(m.Name)
			case manual:
				note = locales.PluginsBookFilesManualNote(m.Name)
			}
			switch {
			case len(m.Hooks()) == 0:
			case note == "":
				note = locales.PluginsBookFilesHooksOnlyNote(m.Name)
			default:
				note += " " + locales.PluginsBookFilesHooksNote()
			}
			ui.Text(c, note).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
	if err := a.tl.checks[m.ID]; err != nil && loaded {
		ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Warning.BG).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Warning.FG)
			ui.Text(c, checkText(err)).FontSize(12).TextColor(pal().Warning.FG).Grow(1)
		})
	}
	if len(m.Settings) > 0 {
		a.pluginSettingsForm(c, m, loaded)
	}
	a.toolsSection(c, m)
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.PluginsCanAccess())
		for _, h := range m.Permissions.Hosts {
			permissionItem(c.Key(h), icons.Globe, h, "", false)
		}
		coversPermission(c, m, nil)
		if m.Permissions.LocalNetwork {
			permissionItem(c, icons.Network, locales.PluginsLocalNetwork(), locales.PluginsLocalNetworkDetail(), true)
		}
		if m.IsFileType() {
			permissionItem(c, icons.File, locales.PluginsBookFilesAccess(), locales.PluginsBookFilesAccessDetail(), false)
		}
		libraryPermission(c, m, false, nil)
		note := locales.PluginsNoteAllowed()
		if k.builtin {
			note = locales.PluginsNoteBuiltIn()
		} else if k.linked != "" {
			note = locales.PluginsNoteLinked(k.linked)
		}
		ui.Text(c, note).FontSize(12).TextColor(pal().TextTertiary)
	})
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		if k.linked != "" {
			if widgets.Button(c, widgets.Ghost, icons.Folder, locales.PluginsShowFolder()).Tooltip(k.linked).Clicked() {
				go mygo.Shell.OpenPath(k.linked)
			}
		} else if st := a.settings.Plugins[m.ID]; st != nil && st.Source != nil && !k.builtin {
			if widgets.Button(c, widgets.Ghost, icons.External, locales.PluginsViewOnGithub()).Clicked() {
				openURL("https://github.com/" + st.Source.Repo)
			}
		} else if m.Homepage != "" {
			if widgets.Button(c, widgets.Ghost, icons.External, locales.PluginsHomepage()).Tooltip(m.Homepage).Clicked() {
				openURL(m.Homepage)
			}
		}
		ui.Spacer(c)
		if !k.builtin {
			label := locales.PluginsUninstallButton()
			if k.linked != "" {
				label = locales.PluginsRemoveButton()
			}
			if widgets.Button(c, widgets.Secondary, nil, label).Clicked() {
				a.pl.uninstall = true
			}
		}
	})
}

// libraryPermission shows what the plugin may see and change of the
// library. When asking consent, only what had (granted before, when
// not nil) doesn't cover.
func libraryPermission(c *ui.Context, m *plugin.Manifest, consent bool, had *plugin.Permissions) {
	want := plugin.Permissions{Library: m.Permissions.Library}
	if want.Library == "" || had != nil && had.Covers(want) {
		return
	}
	if want.Library == plugin.LibraryWrite {
		permissionItem(c, icons.Pencil, locales.PluginsLibraryWrite(), locales.PluginsLibraryWriteDetail(), true)
		return
	}
	label, detail := locales.PluginsLibraryRead(), locales.PluginsLibraryReadDetail()
	if consent {
		label = locales.PluginsConsentSeeBooks()
	}
	if !m.IsMetadataSource() && len(m.Hooks()) > 0 {
		detail = locales.PluginsLibraryReadHooksDetail()
	}
	if len(m.Capabilities.Actions) > 0 {
		// An action sees the books it runs on: every one, for a tool.
		label, detail = locales.PluginsLibraryActions(), locales.PluginsLibraryActionsDetail()
		if m.HasLibraryAction() {
			detail = locales.PluginsLibraryAllDetail()
		}
	}
	permissionItem(c, icons.Library, label, detail, false)
}

// permissionItem is the PermissionItem component.
func permissionItem(c *ui.Context, icon *ui.SVG, label, detail string, warn bool) {
	bg, fg := pal().Subtle, pal().Text
	if warn {
		bg, fg = pal().Warning.BG, pal().Warning.FG
	}
	ui.Row(c).Padding(theme.Space8, theme.Space10).Gap(theme.Space10).Radius(theme.RadiusMD).Background(bg).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon).FontSize(14).TextColor(fg)
		ui.Column(c).Grow(1).Children(func() {
			ui.Text(c, label).FontSize(13).TextColor(fg)
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(fg.Alpha(0.8))
			}
		})
	})
}

// pluginSettingsForm edits a plugin's settings; Save reloads it with them.
func (a *App) pluginSettingsForm(c *ui.Context, m *plugin.Manifest, loaded bool) {
	saved := map[string]string{}
	if st := a.settings.Plugins[m.ID]; st != nil {
		maps.Copy(saved, st.Values)
	}
	maps.Copy(saved, a.pluginSecrets[m.ID])
	if a.pl.valuesFor != m.ID {
		a.pl.valuesFor, a.pl.values = m.ID, maps.Clone(saved)
		if a.pl.values == nil {
			a.pl.values = map[string]string{}
		}
	}
	ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.PluginsSettingsTitle())
		for _, s := range m.Settings {
			v := a.pl.values[s.Key]
			hint := ""
			if s.Required {
				hint = locales.PluginsRequired()
			}
			ui.Column(c.Key(s.Key)).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, s.Title).FontSize(12).FontWeight(500).TextColor(pal().TextSecondary).Grow(1)
					if hint != "" {
						ui.Text(c, hint).FontSize(12).TextColor(pal().TextTertiary)
					}
					if s.Type == plugin.SettingToggle {
						cur := v
						if cur == "" {
							cur = s.Default
						}
						on := cur == "true"
						ui.Switch(c, &on).Label(s.Title)
						if on != (cur == "true") {
							v = strconv.FormatBool(on)
						}
					}
				})
				switch s.Type {
				case plugin.SettingToggle:
				case plugin.SettingSelect:
					cur := v
					if cur == "" {
						cur = s.Default
					}
					names := make([]string, len(s.Options))
					for i, o := range s.Options {
						names[i] = o.Title
						if names[i] == "" {
							names[i] = o.Value
						}
					}
					title := names[0]
					for i, o := range s.Options {
						if o.Value == cur {
							title = names[i]
						}
					}
					before := title
					ui.Select(c, &title, names).Label(s.Title)
					if title != before {
						for i, o := range s.Options {
							if names[i] == title {
								v = o.Value
							}
						}
					}
				case plugin.SettingFileType:
					a.fileSettingField(c, m, s, &v)
				default:
					in := ui.TextInput(c, &v).Label(s.Title).Placeholder(s.Default)
					if s.Type == "secret" {
						in.Password()
					}
				}
				if s.Description != "" {
					ui.Text(c, s.Description).FontSize(12).TextColor(pal().TextTertiary)
				}
			})
			a.pl.values[s.Key] = v
		}
		// File settings apply as they are chosen; Save is for the others.
		if !slices.ContainsFunc(m.Settings, func(s plugin.Setting) bool { return s.Type != plugin.SettingFileType }) {
			return
		}
		changed := !maps.Equal(trimmed(a.pl.values), trimmed(saved))
		ui.Row(c).Gap(theme.Space8).Children(func() {
			ui.Spacer(c)
			if widgets.Button(c, widgets.Primary, nil, locales.PluginsSaveSettings()).Disabled(!changed).Clicked() {
				for _, st := range m.Settings {
					if v := strings.TrimSpace(a.pl.values[st.Key]); st.Type == plugin.SettingNumber && v != "" && !plugin.IsNumber(v) {
						a.toastError(errors.New(locales.PluginsNotANumber(st.Title)))
						return
					}
				}
				a.setPluginValues(m.ID, trimmed(a.pl.values))
				a.toast("%s", locales.PluginsSavedSettings(m.Name))
			}
		})
	})
}

// fileSettingField is a file setting (Figma, 16 · Run plugin on books):
// Choose file… when none is chosen, else the file with Change… and
// Remove. A choice applies at once, with no Save: the file is copied into
// the plugin's data folder, and the setting holds the path its script
// reads it at.
func (a *App) fileSettingField(c *ui.Context, m *plugin.Manifest, s plugin.Setting, v *string) {
	dataDir := ""
	if a.pluginDir != "" {
		dataDir = plugin.DataDir(a.pluginDir, m.ID)
	}
	name, size := "", int64(0)
	if f := plugin.SettingFile(dataDir, *v); dataDir != "" && f != "" {
		if st, err := os.Stat(f); err == nil {
			name, size = filepath.Base(f), st.Size()
		}
	}
	id, key := m.ID, s.Key
	choose := func() {
		a.run(func() {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: s.Title})
			if err != nil || len(paths) == 0 {
				return
			}
			p, err := plugin.StoreSettingFile(dataDir, key, paths[0])
			a.post(func() {
				if err != nil {
					a.toastError(err)
					return
				}
				a.setFileSetting(id, key, p)
			})
		})
	}
	if name == "" {
		ui.Row(c).Children(func() {
			if widgets.ButtonSized(c, widgets.Secondary, nil, locales.PluginsFileChoose(), theme.ControlS).Disabled(dataDir == "").Clicked() {
				choose()
			}
		})
		return
	}
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.File).FontSize(16).TextColor(pal().IconMuted)
		ui.Text(c, name).FontSize(13).SingleLine().MinWidth(0)
		ui.Text(c, humanSize(size)).FontSize(13).TextColor(pal().TextTertiary)
		ui.Spacer(c)
		if widgets.ButtonSized(c, widgets.Ghost, nil, locales.PluginsFileChange(), theme.ControlS).Clicked() {
			choose()
		}
		if widgets.ButtonSized(c, widgets.Ghost, nil, locales.CommonRemove(), theme.ControlS).Label(locales.CommonRemove() + " " + s.Title).Clicked() {
			plugin.RemoveSettingFile(dataDir, key)
			a.setFileSetting(id, key, "")
		}
	})
}

// setFileSetting saves file setting key of plugin id, with the plugin's
// other saved values as they are, and reloads it.
func (a *App) setFileSetting(id, key, v string) {
	values := map[string]string{}
	if st := a.settings.Plugins[id]; st != nil {
		maps.Copy(values, st.Values)
	}
	maps.Copy(values, a.pluginSecrets[id])
	if v == "" {
		delete(values, key)
	} else {
		values[key] = v
	}
	if a.pl.valuesFor == id {
		a.pl.values[key] = v
	}
	a.setPluginValues(id, values)
}

func trimmed(values map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			out[k] = v
		}
	}
	return out
}

// consentDialog asks the user to let a plugin reach what it asks for.
func (a *App) consentDialog(c *ui.Context) {
	ask := a.consent
	if ask == nil {
		return
	}
	open := true
	m := ask.manifest
	// For an update, only what is new is asked.
	var had *plugin.Permissions
	if st := a.settings.Plugins[m.ID]; st != nil && st.Granted != nil {
		had = &plugin.Permissions{Hosts: st.Granted.Hosts, LocalNetwork: st.Granted.LocalNetwork, Library: st.Granted.Library, CoverHosts: st.Granted.Covers}
	}
	title := locales.PluginsConsentInstallTitle(m.Name)
	if had != nil {
		title = locales.PluginsConsentAllowTitle(m.Name, m.Version)
	}
	widgets.DialogFrame(c, &open, 500, func() {
		ui.Text(c, title).FontSize(16).FontWeight(600).Grow(1)
	}, func() {
		ui.Column(c).Padding(theme.Space24).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
				pluginTile(c, m, 40)
				ui.Column(c).Grow(1).Children(func() {
					ui.Text(c, m.Name).FontSize(14).FontWeight(600)
					by := locales.PluginsVersion(m.Version)
					if m.Author != "" {
						by = locales.PluginsConsentVersionBy(m.Version, m.Author)
					}
					ui.Text(c, by).FontSize(12).TextColor(pal().TextTertiary)
				})
			})
			if m.Description != "" {
				ui.Text(c, m.Description).FontSize(13).TextColor(pal().TextSecondary)
			}
			overline := locales.PluginsConsentWillBeAble()
			if had != nil {
				overline = locales.PluginsConsentAsksMore()
			}
			widgets.Overline(c, overline)
			var hadTools map[string]string
			if st := a.settings.Plugins[m.ID]; had != nil && st != nil && st.Granted != nil {
				hadTools = st.Granted.Tools
				if hadTools == nil {
					hadTools = map[string]string{}
				}
			}
			a.toolPermissions(c, m, hadTools)
			for _, h := range m.Permissions.Hosts {
				if had != nil && slices.Contains(had.Hosts, h) {
					continue
				}
				permissionItem(c.Key(h), icons.Globe, locales.PluginsConsentConnect(h), "", false)
			}
			coversPermission(c, m, had)
			if m.Permissions.LocalNetwork && (had == nil || !had.LocalNetwork) {
				permissionItem(c, icons.Network, locales.PluginsConsentLocal(),
					locales.PluginsConsentLocalDetail(), true)
			}
			if m.IsFileType() && had == nil {
				permissionItem(c, icons.File, locales.PluginsBookFilesAccess(), locales.PluginsBookFilesAccessDetail(), false)
			}
			libraryPermission(c, m, true, had)
			if had != nil && len(had.Hosts) > 0 {
				ui.Text(c, locales.PluginsConsentAlreadyAllowed(strings.Join(had.Hosts, ", "))).FontSize(12).TextColor(pal().TextTertiary)
			}
			sandbox := locales.PluginsConsentSandbox()
			if m.Permissions.Library != "" {
				sandbox = locales.PluginsConsentSandboxLibrary()
			}
			if len(m.Tools) > 0 {
				sandbox = locales.ToolsSandboxNote(strings.Join(m.ToolIDs(), ", "))
			}
			ui.Text(c, sandbox).FontSize(12).TextColor(pal().TextTertiary)
			if m.Homepage != "" {
				ui.Text(c, m.Homepage).FontSize(12).TextColor(pal().TextTertiary).Selectable()
			}
		})
	}, func() {
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			open = false
		}
		label := locales.PluginsConsentInstall()
		if had != nil || ask.archive == "" {
			label = locales.PluginsConsentAllow()
		}
		if widgets.Button(c, widgets.Primary, nil, label).Disabled(len(m.Tools) > 0 && !a.picksReady(m)).Clicked() {
			a.grantConsent()
		}
	})
	if !open {
		a.consent = nil
		if ask.answered != nil {
			ask.answered(false)
		}
	}
}

// checkText is what a plugin's check() said is missing.
func checkText(err error) string {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return pe.Msg
	}
	return err.Error()
}

// libraryReadDetail: see locales.PluginsLibraryReadDetail.
