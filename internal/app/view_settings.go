package app

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
	"dalibre/internal/settings"
)

// The Settings window (Figma "08 · OPDS & Settings"): one window for all
// of Dalibre's settings, in sections. ⌘, or Settings at the sidebar's
// foot opens it; other places open it at the section they are about.

type settingsSection int

const (
	sectionGeneral settingsSection = iota
	sectionLibrary
	sectionOPDS
	sectionPlugins
	sectionMCP
	sectionWireless
)

// sections lists the Settings sections. id is the stable key; the title
// shown comes from title(), so it follows the language.
var sections = []struct {
	section settingsSection
	id      string
	title   func() string
	icon    *ui.SVG
}{
	{sectionGeneral, "General", locales.SettingsSectionGeneral, icons.Sliders},
	{sectionLibrary, "Library", locales.CommonLibrary, icons.Library},
	{sectionOPDS, "OPDS", locales.SettingsSectionOPDS, icons.Rss},
	{sectionPlugins, "Plugins", locales.SettingsSectionPlugins, icons.Puzzle},
	{sectionMCP, "MCP", locales.SettingsSectionMCP, icons.Bot},
	{sectionWireless, "Wireless", locales.WirelessName, icons.Wifi},
}

// settingsWidth is the Settings window's width: Plugins' list and plugin
// beside the sections.
const settingsWidth = 920

// openSettings opens the Settings window at section.
func (a *App) openSettings(s settingsSection) {
	a.settingsOpen, a.section = true, s
	if s == sectionPlugins {
		a.checkUpdates(false)
	}
}

func (a *App) settingsDialog(c *ui.Context) {
	if !a.settingsOpen {
		return
	}
	// One width for every section, the widest's: the window doesn't jump
	// as the user moves between them.
	widgets.CompactDialogFrame(c, &a.settingsOpen, settingsWidth, func() {
		ui.Text(c, locales.CommonSettings()).FontSize(14).FontWeight(600).Grow(1)
	}, func() {
		ui.Row(c).Height(540).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(168).Padding(theme.Space8).Gap(theme.Space2).Background(pal().Chrome).BorderWidth(0, 1, 0, 0).BorderColor(pal().BorderSubtle).AlignItems(ui.Stretch).Children(func() {
				for _, s := range sections {
					a.navItem(c, "settings:"+s.id, s.icon, s.title(), 0, a.section == s.section, nil, func() { a.openSettings(s.section) })
				}
			})
			switch a.section {
			case sectionGeneral:
				a.generalPane(c)
			case sectionLibrary:
				a.libraryPane(c)
			case sectionOPDS:
				a.opdsPane(c)
			case sectionPlugins:
				a.pluginsPane(c)
			case sectionMCP:
				a.mcpPane(c)
			case sectionWireless:
				a.wirelessPane(c)
			}
		})
	}, func() {
		ui.Text(c, locales.SettingsChangesApplyNote()).FontSize(12).TextColor(pal().TextTertiary).Grow(1)
		if widgets.ButtonSized(c, widgets.Primary, nil, locales.CommonDone(), theme.ControlS).Clicked() {
			a.settingsOpen = false
		}
	})
	a.catalogSheet(c)
	a.githubSheet(c)
}

// pane is a section's scrolling column.
func pane(c *ui.Context, fn func()) {
	ui.Scroll(c).Grow(1).MinWidth(0).Padding(theme.Space20).Gap(theme.Space16).AlignItems(ui.Stretch).Children(fn)
}

// labeled is a control with its label above, in a column; widgets.Field
// is for a row of fields.
func labeled(c *ui.Context, label string, control func()) {
	ui.Column(c.Key(label)).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(pal().TextSecondary)
		control()
	})
}

// heading is a group's title and what it is for.
func heading(c *ui.Context, title, body string) {
	ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space4).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(600)
		if body != "" {
			ui.Text(c, body).FontSize(12).TextColor(pal().TextSecondary)
		}
	})
}

// ---- General

func (a *App) generalPane(c *ui.Context) {
	pane(c, func() {
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Center).Children(func() {
			heading(c, locales.CommonLanguage(), "")
			a.languageSelect(c)
		})
		widgets.Hairline(c)
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Center).Children(func() {
			heading(c, locales.SettingsAppearance(), "")
			a.appearanceSelect(c)
		})
		widgets.Hairline(c)
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Center).Children(func() {
			heading(c, locales.SettingsShowListTitle(), locales.SettingsShowListBody())
			on := a.settings.ListView
			ui.Switch(c, &on).Label(locales.SettingsShowListTitle())
			if on != a.settings.ListView {
				a.settings.ListView = on
				a.saveSettings()
			}
		})
		widgets.Hairline(c)
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Center).Children(func() {
			heading(c, locales.SettingsShowDetailsTitle(), locales.SettingsShowDetailsBody())
			on := !a.settings.HideDetail
			ui.Switch(c, &on).Label(locales.SettingsShowDetailsTitle())
			if on == a.settings.HideDetail {
				a.settings.HideDetail = !on
				a.saveSettings()
			}
		})
		a.updatesGroup(c)
	})
}

// appearanceSelect picks the app's appearance: the system's, light or dark.
func (a *App) appearanceSelect(c *ui.Context) {
	values := []string{"", "light", "dark"}
	names := []string{locales.SettingsAppearanceSystem(), locales.SettingsAppearanceLight(), locales.SettingsAppearanceDark()}
	cur := names[0]
	for i, v := range values {
		if v == a.settings.Appearance {
			cur = names[i]
		}
	}
	if ui.Select(c, &cur, names).Width(200).Label(locales.SettingsAppearance()).Changed() {
		for i, n := range names {
			if n == cur && values[i] != a.settings.Appearance {
				a.settings.Appearance = values[i]
				a.saveSettings()
				ApplyAppearance(values[i])
			}
		}
	}
}

// ApplyAppearance gives the app the appearance a setting names: "light",
// "dark", or "" for the system's.
func ApplyAppearance(v string) {
	switch v {
	case "light":
		mygo.Theme.SetSource(mygo.ThemeLight)
	case "dark":
		mygo.Theme.SetSource(mygo.ThemeDark)
	default:
		mygo.Theme.SetSource(mygo.ThemeSystem)
	}
}

// Appearance is the appearance the user chose, as ApplyAppearance takes it.
func (a *App) Appearance() string { return a.settings.Appearance }

// ---- Library

func (a *App) libraryPane(c *ui.Context) {
	pane(c, func() {
		heading(c, locales.CommonLibrary(), locales.SettingsLibraryBody())
		ui.Row(c).Padding(theme.Space6, theme.Space6, theme.Space6, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Library).FontSize(16).TextColor(pal().Icon)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				if a.lib == nil {
					ui.Text(c, locales.SettingsNoLibraryOpen()).FontSize(13).FontWeight(500)
					return
				}
				ui.Text(c, a.lib.Root).Font(theme.FontMono).FontSize(12).SingleLine().Selectable()
				ui.Text(c, locales.CommonBookCount(a.counts.All)).FontSize(12).TextColor(pal().TextTertiary)
			})
			if a.lib != nil {
				if widgets.ButtonSized(c, widgets.Ghost, icons.Folder, revealLabel(), theme.ControlS).Clicked() {
					revealPath(a.lib.Root)
				}
			}
		})
		ui.Row(c).Gap(theme.Space8).Children(func() {
			if widgets.Button(c, widgets.Secondary, icons.Folder, locales.SettingsOpenAnotherLibrary()).Clicked() {
				a.chooseLibrary()
			}
		})
		if a.lib != nil {
			a.fileNamesGroup(c)
		}
	})
}

// fileNamesState is Settings › Library's check of the books' folder and
// file names, and the renaming of those that don't match.
type fileNamesState struct {
	lib      *calibre.Library // the library checked
	version  int              // a.version when checked
	checking bool
	err      error
	ids      []int64 // the misnamed books
	renaming bool
	done     int
}

// fileNamesGroup finds the books whose names calibre transliterated
// (Liu Ci Xin/San Ti for 刘慈欣's 三体) and renames them on request.
func (a *App) fileNamesGroup(c *ui.Context) {
	fn := &a.fileNames
	if !fn.checking && !fn.renaming && (fn.lib != a.lib || fn.version != a.version) {
		a.checkFileNames()
	}
	ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
			heading(c, locales.SettingsFileNamesTitle(), locales.SettingsFileNamesBody())
			a.fileNamesSelect(c)
		})
		ui.Row(c).Padding(theme.Space6, theme.Space6, theme.Space6, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
			switch {
			case fn.renaming:
				ui.Spinner(c).Size(14, 14).Label(locales.SettingsFileNamesRenaming(fn.done, len(fn.ids)))
				ui.Text(c, locales.SettingsFileNamesRenaming(fn.done, len(fn.ids))).FontSize(12).TextColor(pal().TextSecondary).Grow(1)
			case fn.checking:
				ui.Spinner(c).Size(14, 14).Label(locales.SettingsFileNamesChecking())
				ui.Text(c, locales.SettingsFileNamesChecking()).FontSize(12).TextColor(pal().TextSecondary).Grow(1)
			case fn.err != nil:
				ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Danger.FG)
				ui.Text(c, fn.err.Error()).FontSize(12).TextColor(pal().Danger.FG).Grow(1)
			case len(fn.ids) == 0:
				ui.Icon(c, icons.CheckCircle).FontSize(16).TextColor(pal().Success.FG)
				ui.Text(c, locales.SettingsFileNamesAllMatch()).FontSize(12).TextColor(pal().TextSecondary).Grow(1)
			default:
				ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Icon)
				ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
					ui.Text(c, locales.SettingsFileNamesMismatch(len(fn.ids))).FontSize(12).FontWeight(500)
					if a.lib.Format().FileNames == calibre.FileNamesOriginal {
						ui.Text(c, locales.SettingsFileNamesCalibreNote()).FontSize(12).TextColor(pal().TextTertiary)
					}
				})
				if widgets.ButtonSized(c, widgets.Secondary, nil, locales.SettingsFileNamesRename(), theme.ControlS).Clicked() {
					a.renameBooks()
				}
			}
		})
	})
}

// fileNamesSelect picks how the library names book folders and files,
// which is saved in the library itself.
func (a *App) fileNamesSelect(c *ui.Context) {
	values := []calibre.FileNames{calibre.FileNamesOriginal, calibre.FileNamesASCII}
	names := []string{locales.SettingsFileNamesOriginal(), locales.SettingsFileNamesASCII()}
	mode := a.lib.Format().FileNames
	cur := names[0]
	for i, v := range values {
		if v == mode {
			cur = names[i]
		}
	}
	if !ui.Select(c, &cur, names).Width(240).Label(locales.SettingsFileNamesTitle()).Changed() {
		return
	}
	for i, n := range names {
		if n == cur && values[i] != mode {
			lib, v := a.lib, values[i]
			a.run(func() {
				err := lib.SetFileNames(v)
				a.post(func() {
					if err != nil {
						a.toastError(err)
					}
					a.fileNames.lib = nil // check again
				})
			})
		}
	}
}

// checkFileNames lists the books whose names don't match, in the
// background.
func (a *App) checkFileNames() {
	fn, lib, version := &a.fileNames, a.lib, a.version
	fn.lib, fn.version, fn.checking = lib, version, true
	a.run(func() {
		books, err := lib.MisnamedBooks()
		a.post(func() {
			fn.checking = false
			if fn.lib != lib || fn.version != version {
				return // the library changed meanwhile; the next frame checks again
			}
			fn.err, fn.ids = err, nil
			for _, b := range books {
				fn.ids = append(fn.ids, b.ID)
			}
		})
	})
}

// renameBooks gives the misnamed books the names their titles and authors
// give, then reads the library again.
func (a *App) renameBooks() {
	fn, lib := &a.fileNames, a.lib
	ids := fn.ids
	fn.renaming, fn.done = true, 0
	a.run(func() {
		n, err := lib.RenameBooks(ids, func(done int) {
			a.post(func() { fn.done = done })
		})
		a.post(func() {
			fn.renaming = false
			fn.lib = nil // check again
			a.reload(func() {
				if err != nil {
					a.toastError(err)
				}
				if n > 0 {
					a.toast("%s", locales.AppToastRenamedBooks(n))
				}
			})
		})
	})
}

// ---- OPDS

// catalogEdit is the Add or Edit catalog sheet.
type catalogEdit struct {
	open     bool
	id       string // the catalog edited; "" adds one
	url      string
	name     string
	user     string
	password string
	probe    *catalogProbe
	named    bool // the user typed the name
}

// shareEdit is Settings › OPDS's sharing fields, as edited.
type shareEdit struct {
	loaded         bool
	port, user, pw string
}

func (a *App) opdsPane(c *ui.Context) {
	pane(c, func() {
		ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
				heading(c, locales.SettingsCatalogsTitle(), locales.SettingsCatalogsBody())
				if widgets.ButtonSized(c, widgets.Secondary, icons.Plus, locales.SettingsAddCatalogButton(), theme.ControlS).Clicked() {
					a.openCatalogSheet("")
				}
			})
			for _, cat := range a.settings.OPDS.Catalogs {
				a.catalogRow(c.Key(cat.ID), cat)
			}
			if len(a.settings.OPDS.Catalogs) == 0 {
				ui.Text(c, locales.SettingsNoCatalogs()).FontSize(13).TextColor(pal().TextTertiary)
			}
			in := !a.settings.OPDS.NotInGetBooks
			ui.Checkbox(c, &in, locales.SettingsSearchCatalogsCheckbox())
			if in == a.settings.OPDS.NotInGetBooks {
				a.settings.OPDS.NotInGetBooks = !in
				a.saveSettings()
				a.setPlugins(a.plugins)
			}
		})
		widgets.Hairline(c)
		a.sharePane(c)
	})
}

// catalogRow is the CatalogRow component.
func (a *App) catalogRow(c *ui.Context, cat *settings.Catalog) {
	st := a.catalogStatus[cat.ID]
	label, tone := st.text()
	col := pal().TextTertiary
	switch tone {
	case "success":
		col = pal().Success.FG
	case "warning":
		col = pal().Warning.FG
	case "danger":
		col = pal().Danger.FG
	}
	row := ui.ButtonBase(c).Padding(theme.Space6, theme.Space6, theme.Space6, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).
		Background(pal().Surface).Border(1, pal().BorderSubtle).AlignItems(ui.Center).Label(cat.Name)
	if st != nil && st.err != nil {
		row.Tooltip(errText(st.err))
	}
	row.OnClick(func() { a.openCatalogSheet(cat.ID) }).Children(func() {
		ui.Box(c).Size(28, 28).Radius(theme.RadiusSM).Background(pal().AccentSubtle).Center().Children(func() {
			ui.Icon(c, icons.Rss).FontSize(16).TextColor(pal().TextAccent)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, cat.Name).FontSize(13).FontWeight(500).SingleLine()
			ui.Text(c, cat.URL).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		})
		ui.Row(c).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(6, 6).Radius(theme.RadiusFull).Background(col)
			ui.Text(c, label).FontSize(12).TextColor(col)
		})
		more := ui.ButtonBase(c).Size(theme.ControlM, theme.ControlM).Radius(theme.RadiusSM).Center().Label(locales.SettingsMoreLabel(cat.Name))
		if more.Hovered() {
			more.Background(pal().Hover)
		}
		more.PopoverMenuButton(func(m *ui.Menu) {
			if m.Item(locales.CommonOpen()).Icon(icons.Menu(icons.BookOpen)).Chosen() {
				a.settingsOpen = false
				a.openCatalog(cat.ID)
			}
			if m.Item(locales.SettingsEditItem()).Icon(icons.Menu(icons.Pencil)).Chosen() {
				a.openCatalogSheet(cat.ID)
			}
			if m.Item(locales.SettingsCheckAgain()).Icon(icons.Menu(icons.Refresh)).Chosen() {
				if p := a.catalogByID(cat.ID); p != nil {
					a.checkCatalog(p)
				}
			}
			m.Separator()
			if m.Item(locales.CommonRemove()).Icon(icons.Menu(icons.Trash)).Danger().Chosen() {
				a.removeCatalog(cat.ID)
			}
		})
		more.Children(func() { ui.Icon(c, icons.More).FontSize(16).TextColor(pal().Icon) })
	})
}

// openCatalogSheet opens the sheet adding a catalog (id "") or editing
// one.
func (a *App) openCatalogSheet(id string) {
	a.catEdit = catalogEdit{open: true, id: id}
	if cat := a.settings.OPDS.Catalog(id); cat != nil {
		a.catEdit.url, a.catEdit.name, a.catEdit.user, a.catEdit.named = cat.URL, cat.Name, cat.Username, true
		if cat.Username != "" && a.secrets != nil {
			secrets := a.secrets
			a.run(func() {
				pw, _ := secrets.Get(catalogSecret(id))
				a.post(func() {
					if a.catEdit.id == id && a.catEdit.password == "" {
						a.catEdit.password = pw
					}
				})
			})
		}
	}
	if !a.settingsOpen {
		a.openSettings(sectionOPDS)
	}
}

// checkCatalogSheet reads the address the sheet holds.
func (a *App) checkCatalogSheet() {
	e := &a.catEdit
	u := strings.TrimSpace(e.url)
	if u == "" {
		return
	}
	if !strings.Contains(u, "://") {
		u = "https://" + u
		e.url = u
	}
	e.probe = a.probeCatalog(u, strings.TrimSpace(e.user), e.password)
}

func (a *App) catalogSheet(c *ui.Context) {
	e := &a.catEdit
	if !e.open {
		return
	}
	if pr := e.probe; pr != nil && !pr.checking && pr.err == nil && !e.named && pr.title != "" {
		e.name = pr.title
	}
	title := locales.SettingsAddCatalogTitle()
	if e.id != "" {
		title = locales.SettingsEditCatalogTitle()
	}
	widgets.DialogFrame(c, &e.open, 480, func() {
		ui.Column(c).Grow(1).Gap(theme.Space4).Children(func() {
			ui.Text(c, title).FontSize(16).FontWeight(600)
			ui.Text(c, locales.SettingsCatalogSheetBody()).FontSize(13).TextColor(pal().TextSecondary)
		})
	}, func() {
		ui.Column(c).Padding(theme.Space16, theme.Space24, theme.Space20, theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			labeled(c, locales.SettingsAddressLabel(), func() {
				in := ui.TextInput(c, &e.url).Label(locales.SettingsAddressLabel()).Placeholder("https://example.com/opds").AutoFocus()
				if in.Submitted() {
					a.checkCatalogSheet()
				}
				if in.Changed() {
					e.probe = nil
				}
			})
			a.probeResult(c)
			labeled(c, locales.CommonName(), func() {
				before := e.name
				ui.TextInput(c, &e.name).Label(locales.CommonName()).Placeholder(locales.SettingsNamePlaceholder())
				if e.name != before {
					e.named = true
				}
				ui.Text(c, locales.SettingsNameHint()).FontSize(12).TextColor(pal().TextTertiary)
			})
			ui.Row(c).Gap(theme.Space12).Children(func() {
				widgets.Field(c, locales.CommonUsername(), locales.SettingsUsernameHint(), func() {
					ui.TextInput(c, &e.user).Label(locales.CommonUsername())
				})
				widgets.Field(c, locales.CommonPassword(), "", func() {
					ui.TextInput(c, &e.password).Label(locales.CommonPassword()).Password()
				})
			})
			if pr := e.probe; pr != nil && pr.local {
				ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.Network).FontSize(14).TextColor(pal().Icon)
					ui.Column(c).Grow(1).Gap(theme.Space2).Children(func() {
						ui.Text(c, locales.SettingsLocalNetworkTitle()).FontSize(13).FontWeight(500)
						ui.Text(c, locales.SettingsLocalNetworkBody()).FontSize(12).TextColor(pal().TextSecondary)
					})
				})
			}
		})
	}, func() {
		checking := e.probe != nil && e.probe.checking
		if widgets.Button(c, widgets.Ghost, icons.Refresh, map[bool]string{true: locales.SettingsCheckAgain(), false: locales.SettingsCheck()}[e.probe != nil]).Disabled(checking || strings.TrimSpace(e.url) == "").Clicked() {
			a.checkCatalogSheet()
		}
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			e.open = false
		}
		ok := e.probe != nil && !e.probe.checking && e.probe.url == strings.TrimSpace(e.url) && (e.probe.err == nil || errKind(e.probe.err) == plugin.KindAuth && e.user != "")
		label := locales.SettingsAddCatalogSubmit()
		if e.id != "" {
			label = locales.CommonSave()
			ok = ok || e.probe == nil && strings.TrimSpace(e.url) != ""
		}
		if widgets.Button(c, widgets.Primary, nil, label).Disabled(!ok || strings.TrimSpace(e.name) == "").Clicked() {
			local := e.probe != nil && e.probe.local
			if e.probe == nil {
				if old := a.settings.OPDS.Catalog(e.id); old != nil {
					local = old.LocalNetwork
				}
			}
			a.saveCatalog(settings.Catalog{ID: e.id, Name: strings.TrimSpace(e.name), URL: strings.TrimSpace(e.url),
				Username: strings.TrimSpace(e.user), LocalNetwork: local}, e.password)
			e.open = false
		}
	})
}

// probeResult says what checking the address found.
func (a *App) probeResult(c *ui.Context) {
	pr := a.catEdit.probe
	if pr == nil {
		return
	}
	bg, fg, ic := pal().Success.BG, pal().Success.FG, icons.CheckCircle
	title, body := "", ""
	switch {
	case pr.checking:
		ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(14, 14).Label(locales.SettingsCheckingLabel())
			ui.Text(c, locales.SettingsCheckingCatalog()).FontSize(13).TextColor(pal().TextSecondary)
		})
		return
	case pr.err == nil:
		title = locales.SettingsFoundTitle(pr.title)
		if pr.searchable {
			body = locales.SettingsEntriesSearch(pr.entries)
		} else {
			body = locales.SettingsEntriesNoSearch(pr.entries)
		}
	case errKind(pr.err) == plugin.KindAuth:
		bg, fg, ic = pal().Warning.BG, pal().Warning.FG, icons.Key
		title, body = locales.SettingsSignInTitle(), locales.SettingsSignInBody()
	default:
		bg, fg, ic = pal().Danger.BG, pal().Danger.FG, icons.Alert
		title, body = locales.SettingsNotCatalogTitle(), errText(pr.err)
	}
	ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(bg).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, ic).FontSize(14).TextColor(fg)
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, title).FontSize(13).FontWeight(500).TextColor(fg)
			ui.Text(c, body).FontSize(12).TextColor(pal().TextSecondary).MaxLines(3)
		})
	})
}

// sharePane is Share this library over OPDS.
func (a *App) sharePane(c *ui.Context) {
	sh := &a.settings.OPDS.Share
	ed := &a.shareEdit
	if !ed.loaded {
		ed.loaded = true
		ed.port, ed.user = strconv.Itoa(sh.SharePort()), sh.Username
		if sh.Password && a.secrets != nil {
			secrets := a.secrets
			a.run(func() {
				pw, _ := secrets.Get(shareSecret)
				a.post(func() { ed.pw = pw })
			})
		}
	}
	ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
			heading(c, locales.SettingsShareTitle(), locales.SettingsShareBody())
			on := sh.On
			ui.Switch(c, &on).Label(locales.SettingsShareTitle())
			if on != sh.On {
				sh.On = on
				a.saveSettings()
				a.applyShare()
			}
		})
		if !sh.On {
			return
		}
		st := a.share
		ui.Row(c).Padding(theme.Space6, theme.Space6, theme.Space6, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
			switch {
			case st != nil && st.err != nil:
				ui.Icon(c, icons.Alert).FontSize(16).TextColor(pal().Danger.FG)
				ui.Text(c, st.err.Error()).FontSize(12).TextColor(pal().Danger.FG).Grow(1)
			case st == nil || st.server == nil:
				ui.Spinner(c).Size(14, 14).Label(locales.SettingsStartingLabel())
				ui.Text(c, locales.SettingsStarting()).FontSize(12).TextColor(pal().TextSecondary).Grow(1)
			default:
				addr := a.shareAddress()
				ui.Icon(c, icons.Wifi).FontSize(16).TextColor(pal().Success.FG)
				ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
					ui.Text(c, addr).Font(theme.FontMono).FontSize(12).Selectable()
					status := locales.SettingsSharing()
					if a.lib == nil {
						status = locales.SettingsSharingNoLibrary()
					} else if n := st.opds.Readers(); n > 0 {
						status = locales.SettingsSharingReaders(n)
					}
					ui.Text(c, status).FontSize(12).TextColor(pal().Success.FG)
				})
				if widgets.IconButton(c, icons.Copy, locales.SettingsCopyAddress(), false).Clicked() {
					mygo.Clipboard.Write(transfer.TextData(addr))
					a.toast("%s", locales.SettingsCopied(addr))
				}
			}
		})
		req := sh.Password
		ui.Checkbox(c, &req, locales.SettingsRequireLogin())
		ui.Row(c).Gap(theme.Space12).AlignItems(ui.End).Children(func() {
			ui.Column(c).Width(120).AlignItems(ui.Stretch).Children(func() {
				labeled(c, locales.SettingsPortLabel(), func() { ui.TextInput(c, &ed.port).Label(locales.SettingsPortLabel()) })
			})
			if req {
				widgets.Field(c, locales.CommonUsername(), "", func() { ui.TextInput(c, &ed.user).Label(locales.CommonUsername()) })
				widgets.Field(c, locales.CommonPassword(), "", func() { ui.TextInput(c, &ed.pw).Label(locales.CommonPassword()).Password() })
			}
		})
		port, perr := strconv.Atoi(strings.TrimSpace(ed.port))
		changed := req != sh.Password || perr == nil && port != sh.SharePort() || req && strings.TrimSpace(ed.user) != sh.Username
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, locales.SettingsShareNote()).
				FontSize(12).TextColor(pal().TextTertiary).Grow(1)
			bad := perr != nil || port < 1 || port > 65535 || req && (strings.TrimSpace(ed.user) == "" || ed.pw == "")
			if widgets.ButtonSized(c, widgets.Primary, nil, locales.SettingsApply(), theme.ControlS).Disabled(bad || !changed && !req).Clicked() {
				sh.Port, sh.Password, sh.Username = port, req, strings.TrimSpace(ed.user)
				a.saveSettings()
				pw, secrets := ed.pw, a.secrets
				a.run(func() {
					if secrets != nil && req {
						secrets.Set(shareSecret, pw)
					}
					a.post(func() {
						a.share = nil // restart with the new password
						a.applyShare()
					})
				})
			}
		})
	})
}

// ---- Plugins from GitHub

// updateBanner offers a plugin's update.
func (a *App) updateBanner(c *ui.Context, m *plugin.Manifest, u *pluginUpdate) {
	ui.Row(c).Padding(theme.Space12, theme.Space12).Gap(theme.Space12).Radius(theme.RadiusMD).Background(pal().AccentSubtle).AlignItems(ui.Start).Children(func() {
		ui.Icon(c, icons.ArrowUpCircle).FontSize(16).TextColor(pal().TextAccent)
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space8).Children(func() {
			ui.Column(c).Gap(theme.Space2).Children(func() {
				ui.Text(c, locales.SettingsUpdateAvailable(u.label)).FontSize(13).FontWeight(500).TextColor(pal().TextAccent)
				body := locales.SettingsPublishedOnGitHub(ago(u.published))
				if n := strings.TrimSpace(u.notes); n != "" {
					body += " " + firstLines(n, 2)
				}
				ui.Text(c, body).FontSize(12).TextColor(pal().TextSecondary).MaxLines(3)
			})
			ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
				if u.busy {
					ui.Spinner(c).Size(14, 14).Label(locales.SettingsUpdatingLabel())
					ui.Text(c, locales.SettingsUpdating()).FontSize(12).TextColor(pal().TextSecondary)
					return
				}
				if widgets.ButtonSized(c, widgets.Primary, nil, locales.CommonUpdate(), theme.ControlS).Clicked() {
					a.updatePlugin(m.ID, false)
				}
				if st := a.settings.Plugins[m.ID]; st != nil && st.Source != nil {
					if repo, err := plugin.ParseRepo(st.Source.Repo); err == nil {
						link := repo.URL() + "/releases"
						if st.Source.Branch != "" {
							link = repo.URL() + "/commits/" + url.PathEscape(st.Source.Branch)
						}
						if widgets.ButtonSized(c, widgets.Ghost, icons.External, locales.SettingsReleaseNotes(), theme.ControlS).Clicked() {
							openURL(link)
						}
					}
				}
			})
		})
	})
}

func firstLines(s string, n int) string {
	var out []string
	for line := range strings.Lines(s) {
		line = strings.TrimSpace(strings.TrimLeft(line, "#*- "))
		if line != "" {
			out = append(out, line)
		}
		if len(out) == n {
			break
		}
	}
	return strings.Join(out, " ")
}

// pluginSource is SOURCE: the repository a plugin came from, what of it
// it follows, and updating it.
func (a *App) pluginSource(c *ui.Context, m *plugin.Manifest, src *settings.PluginSource) {
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.SettingsSourceOverline())
		ui.Row(c).Padding(theme.Space8, theme.Space6, theme.Space8, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.GitBranch).FontSize(16).TextColor(pal().Icon)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				ui.Text(c, "github.com/"+src.Repo).Font(theme.FontMono).FontSize(12).SingleLine().Selectable()
				checked := !a.settings.UpdatesChecked.IsZero()
				var what string
				switch {
				case src.Branch != "" && checked:
					what = locales.SettingsTrackingBranchChecked(src.Branch, plugin.ShortSHA(src.Ref), ago(a.settings.UpdatesChecked))
				case src.Branch != "":
					what = locales.SettingsTrackingBranch(src.Branch, plugin.ShortSHA(src.Ref))
				case checked:
					what = locales.SettingsTrackingReleasesChecked(src.Ref, ago(a.settings.UpdatesChecked))
				default:
					what = locales.SettingsTrackingReleases(src.Ref)
				}
				ui.Text(c, what).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
			})
			if a.updatesChecking {
				ui.Spinner(c).Size(14, 14).Label(locales.SettingsCheckingUpdatesLabel())
			} else if widgets.ButtonSized(c, widgets.Ghost, nil, locales.SettingsCheckNow(), theme.ControlS).Clicked() {
				a.checkUpdates(true)
			}
		})
		auto := src.Auto
		ui.Checkbox(c, &auto, locales.SettingsAutoUpdate())
		if auto != src.Auto {
			src.Auto = auto
			a.saveSettings()
		}
	})
}

// githubSheet is Install from GitHub.
func (a *App) githubSheet(c *ui.Context) {
	g := &a.ghi
	if !g.open {
		return
	}
	widgets.DialogFrame(c, &g.open, 520, func() {
		ui.Column(c).Grow(1).Gap(theme.Space4).Children(func() {
			ui.Text(c, locales.SettingsInstallGitHubTitle()).FontSize(16).FontWeight(600)
			ui.Text(c, locales.SettingsInstallGitHubBody()).FontSize(13).TextColor(pal().TextSecondary)
		})
	}, func() {
		ui.Column(c).Padding(theme.Space16, theme.Space24, theme.Space20, theme.Space24).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			labeled(c, locales.SettingsRepositoryLabel(), func() {
				ui.Row(c).Gap(theme.Space8).Children(func() {
					in := ui.TextInput(c, &g.text).Label(locales.SettingsRepositoryLabel()).Placeholder("owner/repo").AutoFocus().Grow(1)
					if in.Submitted() {
						a.ghFind()
					}
					if widgets.Button(c, widgets.Secondary, nil, locales.SettingsFind()).Disabled(strings.TrimSpace(g.text) == "" || g.finding).Clicked() {
						a.ghFind()
					}
				})
				ui.Text(c, locales.SettingsRepoHint()).FontSize(12).TextColor(pal().TextTertiary)
			})
			switch {
			case g.finding:
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14).Label(locales.SettingsFindingLabel())
					ui.Text(c, locales.SettingsLookingAt(g.repo.String())).FontSize(13).TextColor(pal().TextSecondary)
				})
			case g.err != "":
				ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Danger.BG).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Danger.FG)
					ui.Text(c, g.err).FontSize(12).TextColor(pal().Danger.FG).Grow(1)
				})
			}
			if len(g.releases) > 0 || len(g.branches) > 0 {
				a.ghFound(c)
			}
		})
	}, func() {
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			g.open = false
		}
		if widgets.Button(c, widgets.Primary, nil, locales.SettingsInstall()).Disabled(g.manifest == nil || g.preparing).Clicked() {
			a.ghInstallChosen()
		}
	})
	if !g.open {
		g.discard()
	}
}

// ghFound is what the repository holds: the version chosen, the plugin
// it is and what it may reach.
func (a *App) ghFound(c *ui.Context) {
	g := &a.ghi
	ui.Row(c).Padding(theme.Space12).Gap(theme.Space12).Radius(theme.RadiusMD).Background(pal().Subtle).AlignItems(ui.Center).Children(func() {
		switch {
		case g.preparing:
			ui.Spinner(c).Size(16, 16).Label(locales.SettingsDownloadingLabel())
			ui.Text(c, locales.SettingsDownloading()).FontSize(13).TextColor(pal().TextSecondary).Grow(1)
		case g.manifest != nil:
			pluginTile(c, g.manifest, 32)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				ui.Text(c, g.manifest.Name+" "+g.manifest.Version).FontSize(13).FontWeight(500)
				by := g.detail
				if g.manifest.Author != "" {
					by = locales.SettingsByAuthor(g.manifest.Author, by)
				}
				ui.Text(c, by).FontSize(12).TextColor(pal().TextSecondary).MaxLines(2)
			})
		default:
			ui.Spacer(c)
		}
		label := locales.SettingsLatestRelease()
		if latest, ok := plugin.Latest(g.releases); !ok || g.tag != latest.Tag {
			label = g.tag
		}
		if g.branch != "" {
			label = locales.SettingsBranchLabel(g.branch)
		}
		b := ui.ButtonBase(c).Width(170).Height(theme.ControlM).Padding(0, theme.Space8, 0, theme.Space10).Gap(theme.Space6).AlignItems(ui.Center).
			Radius(theme.RadiusSM).Background(pal().Surface).Border(1, pal().Border).Label(locales.SettingsVersionLabel(label))
		b.PopoverMenuButton(func(m *ui.Menu) {
			m.MaxWidth(300)
			latest, _ := plugin.Latest(g.releases)
			if len(g.releases) > 0 {
				for i, r := range g.releases {
					if i >= 8 {
						break
					}
					title := r.Tag
					if r.Tag == latest.Tag {
						title = locales.SettingsLatestRelease()
					}
					sub := locales.SettingsReleaseSub(r.Tag, ago(r.Published))
					if r.Prerelease {
						sub = locales.SettingsReleaseSubPre(r.Tag, ago(r.Published))
					}
					if m.Item(title).ID("tag:" + r.Tag).Subtitle(sub).Icon(icons.Menu(icons.Tag)).Checked(g.branch == "" && g.tag == r.Tag).Chosen() {
						a.ghChoose(r.Tag, "")
					}
				}
			}
			if len(g.branches) > 0 {
				if len(g.releases) > 0 {
					m.Separator()
				}
				for i, br := range g.branches {
					if i >= 8 {
						break
					}
					if m.Item(br.Name).ID("branch:" + br.Name).Trailing(plugin.ShortSHA(br.Commit)).Icon(icons.Menu(icons.GitBranch)).Checked(g.branch == br.Name).Chosen() {
						a.ghChoose("", br.Name)
					}
				}
			}
		})
		b.Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(500).SingleLine().Grow(1)
			ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(pal().IconMuted)
		})
	})
	if g.branch != "" {
		ui.Row(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space10).Radius(theme.RadiusMD).Background(pal().Warning.BG).AlignItems(ui.Start).Children(func() {
			ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Warning.FG)
			ui.Text(c, locales.SettingsBranchWarning()).
				FontSize(12).TextColor(pal().Warning.FG).Grow(1)
		})
	}
	if m := g.manifest; m != nil {
		ui.Column(c).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
			widgets.Overline(c, locales.SettingsAbleTo())
			for _, h := range m.Permissions.Hosts {
				permissionItem(c.Key(h), icons.Globe, locales.SettingsConnectTo(h), "", false)
			}
			if m.Permissions.LocalNetwork {
				permissionItem(c, icons.Network, locales.SettingsConnectLocal(), locales.SettingsConnectLocalNote(), true)
			}
			if len(m.Permissions.Hosts) == 0 && !m.Permissions.LocalNetwork {
				ui.Text(c, locales.SettingsNothing()).FontSize(12).TextColor(pal().TextTertiary)
			}
		})
		keepLabel := locales.SettingsKeepUpToDate()
		if g.branch != "" {
			keepLabel = locales.SettingsFollowCommits(g.branch)
		}
		ui.Checkbox(c, &g.keep, keepLabel)
		ui.Text(c, locales.SettingsGitHubSandboxNote()).
			FontSize(12).TextColor(pal().TextTertiary)
	}
}
