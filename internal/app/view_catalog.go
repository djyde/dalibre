package app

import (
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/opds"
	"dalibre/internal/plugin"
)

// The catalog page (Figma "08 · OPDS & Settings", Catalog — Navigation
// and Book feed): an OPDS catalog's feeds, opened one inside another as
// the breadcrumb shows. Navigation entries lead to other feeds; books
// show as Get books' results, and download the same way.

// ---- Sidebar

// catalogsNav is the sidebar's OPDS CATALOGS group.
func (a *App) catalogsNav(c *ui.Context) {
	key := "catalogs"
	if _, ok := a.sectionsOn[key]; !ok {
		a.sectionsOn[key] = true
	}
	open := a.sectionsOn[key]
	ui.Column(c.Key(key)).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		h := ui.ButtonBase(c).Height(24).Padding(0, theme.Space8).AlignItems(ui.Center).Radius(theme.RadiusXS).Label(locales.CatalogSectionLabel())
		h.OnClick(func() { a.sectionsOn[key] = !a.sectionsOn[key] }).Children(func() {
			widgets.Overline(c, locales.CatalogSectionTitle()).Grow(1)
			chev := icons.ChevronDown
			if !open {
				chev = icons.ChevronRight
			}
			ui.Icon(c, chev).FontSize(12).TextColor(pal().IconMuted)
		})
		if !open {
			return
		}
		for _, cat := range a.settings.OPDS.Catalogs {
			id := cat.ID
			var col *ui.Color
			if st := a.catalogStatus[id]; st != nil && st.err != nil {
				fg := pal().Warning.FG
				col = &fg
			}
			a.navItem(c, "catalog:"+id, icons.Rss, cat.Name, 0, a.page == pageCatalog && a.cp.id == id, col, func() { a.openCatalog(id) })
		}
		muted := pal().IconMuted
		add := ui.ButtonBase(c.Key("catalog:add")).Height(30).Radius(theme.RadiusSM).Padding(0, theme.Space8).Gap(theme.Space8).AlignItems(ui.Center).Justify(ui.Start).Label(locales.CatalogAdd())
		if add.Hovered() {
			add.Background(pal().Hover)
		}
		add.OnClick(func() { a.openCatalogSheet("") }).Children(func() {
			ui.Icon(c, icons.Plus).FontSize(16).TextColor(muted)
			ui.Text(c, locales.CatalogAdd()).TextColor(pal().TextTertiary).SingleLine()
		})
	})
}

// settingsNav is Feedback and Settings at the sidebar's foot; sharing
// shows in the toolbar (sharingButton).
func (a *App) settingsNav(c *ui.Context) {
	ui.Column(c).Padding(0, theme.Space8, theme.Space8, theme.Space8).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		a.feedbackNav(c)
		row := ui.ButtonBase(c.Key("settings")).Height(30).Radius(theme.RadiusSM).Padding(0, theme.Space8).Gap(theme.Space8).AlignItems(ui.Center).Label(locales.CommonSettings())
		if row.Hovered() {
			row.Background(pal().Hover)
		}
		row.OnClick(func() { a.openSettings(a.section) }).Children(func() {
			ui.Icon(c, icons.Settings).FontSize(16).TextColor(pal().Icon)
			ui.Text(c, locales.CommonSettings()).Grow(1).SingleLine()
			ui.Text(c, "⌘,").Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
		})
	})
}

// ---- Toolbar

func (a *App) catalogToolbar(c *ui.Context) {
	right := float32(theme.Space24)
	if !a.detailShown() {
		right += c.TitleBar().Right
	}
	p := a.catalogByID(a.cp.id)
	title, sub := locales.CatalogTitleDefault(), ""
	if n := len(a.cp.path); n > 0 {
		title = a.cp.path[n-1].title
	}
	if cat := a.settings.OPDS.Catalog(a.cp.id); cat != nil {
		sub = "OPDS"
		if u, err := url.Parse(cat.URL); err == nil {
			sub += " · " + u.Host
		}
	}
	ui.Row(c).Height(theme.ToolbarHeight).Padding(0, right, 0, theme.Space32).Gap(theme.Space8).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontWeight(600).FontSize(22).LetterSpacing(-0.4).SingleLine()
			if sub != "" {
				ui.Text(c, sub).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
			}
		})
		if widgets.IconButton(c, icons.Settings, locales.CatalogSettingsTooltip(), false).Clicked() {
			a.openSettings(sectionOPDS)
		}
		if widgets.IconButton(c, icons.PanelRight, locales.GetBooksDetailsTooltip(), !a.settings.HideDetail).Clicked() {
			a.settings.HideDetail = !a.settings.HideDetail
			a.saveSettings()
		}
		a.sharingButton(c)
		a.agentsButton(c)
		a.pluginsButton(c)
		if p != nil {
			ui.Box(c).Size(1, 20).Background(pal().Border).Margin(0, theme.Space4)
			if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.CatalogRefresh()).Clicked() {
				a.retryFeed()
			}
		}
	})
}

// ---- Main column

func (a *App) catalogContent(c *ui.Context) {
	p := a.catalogByID(a.cp.id)
	if p == nil {
		a.getBooksPrompt(c, icons.Rss, locales.CatalogRemoved(), "")
		return
	}
	st := a.catalogStatus[a.cp.id]
	fs := a.curFeed()
	ui.Column(c).Padding(theme.Space4, theme.Space32, theme.Space12, theme.Space32).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
			a.breadcrumb(c)
			if st != nil && st.searchable {
				f := ui.SearchField(c, &a.cp.text).Placeholder(locales.GetBooksSearchSource(p.Manifest.Name)).Label(locales.GetBooksSearchSource(p.Manifest.Name)).Width(280)
				if f.Submitted() {
					a.catalogSearch()
				}
			}
		})
		if fs != nil && fs.feed != nil {
			a.facets(c, fs)
		}
	})
	switch {
	case fs == nil || fs.loading:
		ui.Column(c).Grow(1).Center().Gap(theme.Space12).Children(func() {
			ui.Spinner(c).Size(24, 24).Label(locales.CommonLoading())
			ui.Text(c, locales.CatalogReading(p.Manifest.Name)).TextColor(pal().TextSecondary)
		})
	case fs.err != nil:
		a.feedProblem(c, p, fs.err)
	case len(fs.entries) == 0:
		a.getBooksPrompt(c, icons.Rss, locales.CatalogEmptyTitle(), locales.CatalogEmptyBody())
	default:
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, theme.Space24, theme.Space24, theme.Space24).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
			for i, e := range fs.entries {
				if r, ok := opds.EntryResult(e); ok {
					a.resultRow(c.Key(i), p, r)
				} else if nav := e.Navigation(); nav != nil {
					a.catalogEntry(c.Key(i), e, nav)
				}
			}
			ui.Row(c).Padding(theme.Space12, 0).Justify(ui.Center).Children(func() {
				switch {
				case fs.loadingMore:
					ui.Spinner(c).Size(20, 20).Label(locales.GetBooksLoadingMore())
				case fs.next != "":
					if widgets.Button(c, widgets.Ghost, nil, locales.CatalogNextPage()).Clicked() {
						a.loadFeed(a.cp.path[len(a.cp.path)-1].url, true)
					}
				}
			})
		})
	}
}

// breadcrumb is the path of feeds opened; each but the last goes back.
func (a *App) breadcrumb(c *ui.Context) {
	ui.Row(c).Grow(1).MinWidth(0).Gap(theme.Space4).AlignItems(ui.Center).Children(func() {
		for i, cr := range a.cp.path {
			last := i == len(a.cp.path)-1
			if i > 0 {
				ui.Icon(c.Key(i), icons.ChevronRight).FontSize(12).TextColor(pal().IconMuted)
			}
			if last {
				ui.Text(c.Key(cr.url), cr.title).FontSize(13).FontWeight(500).SingleLine()
				continue
			}
			b := ui.ButtonBase(c.Key(cr.url)).Padding(theme.Space2, theme.Space4).Radius(theme.RadiusXS).Label(locales.GetBooksBackTo(cr.title))
			if b.Hovered() {
				b.Background(pal().Hover)
			}
			b.OnClick(func() { a.cp.path = a.cp.path[:i+1] }).Children(func() {
				ui.Text(c, cr.title).FontSize(13).TextColor(pal().TextSecondary).SingleLine()
			})
		}
	})
}

// defaultFacetGroup is the group of facet links that name none.
const defaultFacetGroup = "View"

// facets are the feed's ways to sort or narrow it, a menu per group.
func (a *App) facets(c *ui.Context, fs *feedState) {
	links := fs.feed.Facets()
	if len(links) == 0 {
		return
	}
	var groups []string
	byGroup := map[string][]opds.Link{}
	for _, l := range links {
		g := l.FacetGroup
		if g == "" {
			g = defaultFacetGroup
		}
		if byGroup[g] == nil {
			groups = append(groups, g)
		}
		byGroup[g] = append(byGroup[g], l)
	}
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Wrap().Children(func() {
		for _, g := range groups {
			ls := byGroup[g]
			name := g
			if g == defaultFacetGroup {
				name = locales.CatalogFacetView()
			}
			label := name
			for _, l := range ls {
				if l.Active {
					label = l.Title
				}
			}
			b := ui.ButtonBase(c.Key(g)).Height(28).Padding(0, theme.Space8, 0, theme.Space10).Gap(theme.Space6).AlignItems(ui.Center).
				Radius(theme.RadiusSM).Background(pal().Surface).Border(1, pal().Border).Label(locales.GetBooksLabelValue(name, label))
			b.PopoverMenuButton(func(m *ui.Menu) {
				for _, l := range ls {
					if m.Item(l.Title).ID(l.Href).Radio(l.Active).Chosen() {
						title := a.cp.path[len(a.cp.path)-1].title
						a.replaceFeed(title, l.Href)
					}
				}
			})
			b.Children(func() {
				ui.Text(c, label).FontSize(12).FontWeight(500).SingleLine()
				ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(pal().IconMuted)
			})
		}
	})
}

// catalogEntry is the CatalogEntry component: a link to another feed.
func (a *App) catalogEntry(c *ui.Context, e opds.Entry, nav *opds.Link) {
	row := ui.ButtonBase(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space12).Radius(theme.RadiusMD).AlignItems(ui.Center).Label(e.Title)
	if row.Hovered() {
		row.Background(pal().Hover)
	}
	row.OnClick(func() { a.openFeed(e.Title, nav.Href) }).Children(func() {
		ui.Box(c).Size(36, 36).Radius(theme.RadiusSM).Background(pal().Subtle).Center().Children(func() {
			ui.Icon(c, entryIcon(e.Title)).FontSize(16).TextColor(pal().Icon)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, e.Title).FontSize(14).FontWeight(500).SingleLine()
			detail := e.Summary
			if detail == "" {
				detail = htmlToText(e.Content)
			}
			if detail != "" {
				ui.Text(c, detail).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
			}
		})
		ui.Icon(c, icons.ChevronRight).FontSize(16).TextColor(pal().IconMuted)
	})
}

// entryIcon picks an icon for a navigation entry by what it is called.
func entryIcon(title string) *ui.SVG {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "author"):
		return icons.User
	case strings.Contains(t, "series"):
		return icons.Layers
	case strings.Contains(t, "tag") || strings.Contains(t, "subject") || strings.Contains(t, "genre") || strings.Contains(t, "categor"):
		return icons.Tag
	case strings.Contains(t, "new") || strings.Contains(t, "recent") || strings.Contains(t, "latest"):
		return icons.Clock
	case strings.Contains(t, "rating") || strings.Contains(t, "popular") || strings.Contains(t, "best"):
		return icons.StarOutline
	case strings.Contains(t, "language"):
		return icons.Globe
	}
	return icons.Folder
}

// feedProblem fills the page with why a feed couldn't be read.
func (a *App) feedProblem(c *ui.Context, p *plugin.Plugin, err error) {
	kind := errKind(err)
	ui.Column(c).Grow(1).Center().Gap(theme.Space12).Padding(theme.Space48).Children(func() {
		ic, title, body := icons.Alert, locales.GetBooksSourceNotAnswering(p.Manifest.Name), errText(err)
		switch kind {
		case plugin.KindAuth:
			ic, title, body = icons.Key, locales.CatalogSignInTitle(p.Manifest.Name), locales.CatalogSignInBody()
		case plugin.KindInvalid:
			title = locales.CatalogCantRead(p.Manifest.Name)
		}
		ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().Subtle).Center().Children(func() {
			ui.Icon(c, ic).FontSize(20).TextColor(pal().IconMuted)
		})
		ui.Text(c, title).FontWeight(600).FontSize(16).TextAlign(ui.Center)
		ui.Text(c, body).FontSize(13).TextColor(pal().TextSecondary).TextAlign(ui.Center).MaxWidth(460)
		ui.Row(c).Gap(theme.Space8).Margin(theme.Space8, 0, 0, 0).Children(func() {
			if kind == plugin.KindAuth {
				if widgets.Button(c, widgets.Primary, nil, locales.CatalogSignIn()).Clicked() {
					a.openCatalogSheet(a.cp.id)
				}
			}
			if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.CommonTryAgain()).Clicked() {
				a.retryFeed()
			}
		})
	})
}
