package app

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
	"dalibre/internal/plugin"
)

// pluginIcon is the icon a plugin picked from Dalibre's set.
func pluginIcon(m *plugin.Manifest) *ui.SVG {
	if strings.HasPrefix(m.ID, catalogPrefix) {
		return icons.Rss
	}
	switch m.IconName() {
	case "library":
		return icons.Library
	case "book-open":
		return icons.BookOpen
	case "database":
		return icons.Database
	case "network":
		return icons.Network
	case "building":
		return icons.Building
	case "bookmark":
		return icons.Bookmark
	case "layers":
		return icons.Layers
	}
	return icons.Globe
}

// formatPurpose says what a format is for, beside its badge.
func formatPurpose(format string) string {
	switch format {
	case "EPUB":
		return locales.GetBooksPurposeEpub()
	case "KEPUB":
		return locales.GetBooksPurposeKepub()
	case "AZW3":
		return locales.GetBooksPurposeAzw3()
	case "MOBI":
		return locales.GetBooksPurposeMobi()
	case "PDF":
		return locales.GetBooksPurposePdf()
	case "TXT":
		return locales.GetBooksPurposeTxt()
	case "FB2":
		return locales.GetBooksPurposeFb2()
	case "CBZ":
		return locales.GetBooksPurposeCbz()
	case "DJVU":
		return locales.GetBooksPurposeDjvu()
	}
	return ""
}

// optionTitle names a filter option: its title, or a language's name.
func optionTitle(f plugin.Filter, o plugin.Option) string {
	if o.Title != "" {
		return o.Title
	}
	if f.Type == plugin.FilterLanguage {
		if code := calibre.LanguageCode(o.Value); code != "" {
			return languageName(code)
		}
		if o.Value == "" {
			return locales.GetBooksAnyLanguage()
		}
	}
	return o.Value
}

// ---- Sidebar

// getBooksNav is the sidebar's Get books entry, in a group of its own.
func (a *App) getBooksNav(c *ui.Context) {
	ui.Column(c).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		a.navItem(c, "getbooks", icons.Download, locales.CommonGetBooks(), 0, a.page == pageGetBooks, nil, a.openGetBooks)
	})
}

// ---- Toolbar

func (a *App) getBooksToolbar(c *ui.Context) {
	right := float32(theme.Space24)
	if !a.detailShown() {
		right += c.TitleBar().Right
	}
	srcs := a.gbSources()
	var one *plugin.Plugin
	if a.gb.source != allSources && len(srcs) == 1 {
		one = srcs[0]
	}
	ui.Row(c).Height(theme.ToolbarHeight).Padding(0, right, 0, theme.Space32).Gap(theme.Space8).AlignItems(ui.Center).DragWindow().Children(func() {
		ui.Text(c, locales.CommonGetBooks()).FontWeight(600).FontSize(22).LetterSpacing(-0.4).SingleLine().Grow(1)
		if one != nil {
			if widgets.IconButton(c, icons.Sliders, locales.GetBooksSourceSettings(one.Manifest.Name), false).Clicked() {
				a.openPlugins(one.Manifest.ID)
			}
		}
		if widgets.IconButton(c, icons.PanelRight, locales.GetBooksDetailsTooltip(), !a.settings.HideDetail).Clicked() {
			a.settings.HideDetail = !a.settings.HideDetail
			a.saveSettings()
		}
		a.sharingButton(c)
		a.agentsButton(c)
		a.pluginsButton(c)
		if one != nil && one.Manifest.Homepage != "" {
			ui.Box(c).Size(1, 20).Background(pal().Border).Margin(0, theme.Space4)
			if widgets.Button(c, widgets.Secondary, icons.External, locales.GetBooksOpenSite()).Tooltip(one.Manifest.Homepage).Clicked() {
				openURL(one.Manifest.Homepage)
			}
		}
	})
}

// ---- Main column

func (a *App) getBooksContent(c *ui.Context) {
	g := &a.gb
	srcs := a.gbSources()
	ui.Column(c).Padding(0, theme.Space32, theme.Space12, theme.Space32).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			a.sourcePicker(c)
			placeholder := locales.GetBooksSearchAll()
			if g.source != allSources && len(srcs) == 1 {
				placeholder = locales.GetBooksSearchSource(srcs[0].Manifest.Name)
			}
			f := ui.SearchField(c, &g.text).Placeholder(placeholder).Label(locales.GetBooksSearchLabel()).Grow(1).Bind(&g.focus)
			if f.Submitted() {
				a.gbSearch()
			}
			if f.Changed() && strings.TrimSpace(g.text) == "" && g.query != "" {
				a.gbSearch() // cleared: back to browsing
			}
		})
		a.getBooksFilters(c, srcs)
	})
	switch {
	case len(a.sources()) == 0 && a.pluginsLoading:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Size(24, 24).Label(locales.GetBooksLoadingPlugins()) })
	case len(a.sources()) == 0:
		a.noSources(c)
	case g.source == allSources:
		a.allSourcesBody(c, srcs)
	case len(srcs) == 1:
		a.sourceBody(c, srcs[0])
	}
}

// sourcePicker is the drop-down left of the search box: All sources and
// every book source, with what each holds and whether it is set up.
func (a *App) sourcePicker(c *ui.Context) {
	g := &a.gb
	label, icon := locales.CommonAllSources(), icons.Layers
	if p := a.pluginByID(g.source); p != nil && g.source != allSources {
		label, icon = p.Manifest.Name, pluginIcon(p.Manifest)
	}
	b := ui.ButtonBase(c).Width(220).Height(theme.ControlM).Padding(0, theme.Space8, 0, theme.Space10).Gap(theme.Space8).AlignItems(ui.Center).
		Radius(theme.RadiusSM).Background(pal().Surface).Border(1, pal().Border).Label(locales.GetBooksSourceLabel(label))
	if b.Hovered() {
		b.Border(1, pal().BorderStrong)
	}
	b.PopoverMenuButton(func(m *ui.Menu) {
		m.MaxWidth(320)
		if m.Item(locales.CommonAllSources()).Subtitle(locales.GetBooksSearchAllAtOnce()).Icon(icons.Menu(icons.Layers)).Checked(g.source == allSources).Chosen() {
			a.gbSetSource(allSources)
		}
		// A separator only between groups that have items: with every
		// source off, All sources sits right above Get more sources.
		pending := 0
		for _, mf := range a.pluginsPending {
			if mf.IsBookSource() {
				pending++
			}
		}
		if len(a.sources())+pending > 0 {
			m.Separator()
		}
		for _, p := range a.sources() {
			mf := p.Manifest
			it := m.Item(mf.Name).ID(mf.ID).Subtitle(mf.Description).Icon(icons.Menu(pluginIcon(mf))).Checked(g.source == mf.ID)
			if p.MissingSettings() != nil {
				it.Trailing(locales.GetBooksSetUp())
			}
			if it.Chosen() {
				a.gbSetSource(mf.ID)
			}
		}
		for _, mf := range a.pluginsPending {
			if mf.IsBookSource() && m.Item(mf.Name).ID("pending:"+mf.ID).Subtitle(locales.GetBooksWaitingPermission()).Icon(icons.Menu(pluginIcon(mf))).Trailing(locales.GetBooksReview()).Chosen() {
				a.askConsent(mf.ID)
			}
		}
		m.Separator()
		if m.Item(locales.GetBooksGetMoreSources()).Icon(icons.Menu(icons.Puzzle)).Chosen() {
			a.openPlugins("")
		}
	})
	b.Children(func() {
		ui.Icon(c, icon).FontSize(15).TextColor(pal().Icon)
		ui.Text(c, label).FontSize(13).SingleLine().Grow(1)
		ui.Icon(c, icons.ChevronDown).FontSize(14).TextColor(pal().IconMuted)
	})
}

// getBooksFilters is the row under the search box: the source's filters
// and how many results there are.
func (a *App) getBooksFilters(c *ui.Context, srcs []*plugin.Plugin) {
	g := &a.gb
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Wrap().Children(func() {
		if g.source == allSources {
			// The language, when a source has one, is shared by all.
			var langs []plugin.Option
			seen := map[string]bool{}
			var lf plugin.Filter
			for _, p := range srcs {
				for _, f := range p.Manifest.Filters() {
					if f.Type != plugin.FilterLanguage {
						continue
					}
					lf = f
					for _, o := range f.Options {
						if !seen[o.Value] {
							seen[o.Value] = true
							langs = append(langs, o)
						}
					}
				}
			}
			if len(langs) > 0 {
				lf.Options = langs
				a.filterSelect(c.Key("language"), lf, g.language, a.gbSetLanguage)
			}
		} else if len(srcs) == 1 {
			p := srcs[0]
			values := a.gbFilters(p)
			browsing := g.query == ""
			for _, f := range p.Manifest.Filters() {
				if browsing && f.Type != plugin.FilterLanguage {
					continue // a collection keeps its own order
				}
				a.filterSelect(c.Key(f.Key), f, values[f.Key], func(v string) { a.gbSetFilter(p, f.Key, v) })
			}
		}
		ui.Spacer(c)
		if s := a.resultsCount(srcs); s != "" {
			ui.Text(c, s).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
}

// resultsCount says how much the page shows.
func (a *App) resultsCount(srcs []*plugin.Plugin) string {
	g := &a.gb
	if g.query == "" && g.collection == "" {
		return ""
	}
	if g.source == allSources {
		answered := 0
		for _, p := range srcs {
			if l := a.gbList(p, ""); l != nil && !l.loading && l.err == nil {
				answered++
			}
		}
		return locales.GetBooksSourcesAnswered(answered, len(srcs))
	}
	if len(srcs) == 1 {
		if l := a.gbList(srcs[0], g.collection); l != nil && l.page > 0 {
			return resultCountText(len(l.items), l.hasMore)
		}
	}
	return ""
}

// resultCountText is "12 results", or "12+ results" while more can be loaded.
func resultCountText(n int, more bool) string {
	if more {
		return locales.GetBooksResultCountMore(n)
	}
	return locales.GetBooksResultCount(n)
}

// filterSelect is a filter's drop-down.
func (a *App) filterSelect(c *ui.Context, f plugin.Filter, value string, pick func(string)) {
	value = f.Value(value)
	label := f.Title
	for _, o := range f.Options {
		if o.Value == value {
			label = optionTitle(f, o)
		}
	}
	b := ui.ButtonBase(c).Height(28).Padding(0, theme.Space8, 0, theme.Space10).Gap(theme.Space6).AlignItems(ui.Center).
		Radius(theme.RadiusSM).Background(pal().Surface).Border(1, pal().Border).Label(locales.GetBooksLabelValue(f.Title, label))
	if b.Hovered() {
		b.Border(1, pal().BorderStrong)
	}
	b.PopoverMenuButton(func(m *ui.Menu) {
		m.MaxWidth(280)
		for _, o := range f.Options {
			if m.Item(optionTitle(f, o)).ID("v:" + o.Value).Radio(o.Value == value).Chosen() {
				pick(o.Value)
			}
		}
	})
	b.Children(func() {
		if f.Type == plugin.FilterLanguage {
			ui.Icon(c, icons.Globe).FontSize(13).TextColor(pal().IconMuted)
		}
		ui.Text(c, label).FontSize(12).FontWeight(500).SingleLine()
		ui.Icon(c, icons.ChevronDown).FontSize(12).TextColor(pal().IconMuted)
	})
}

// sourceBody is one source's page: its search, a collection, its browse
// page or why it can't be used.
func (a *App) sourceBody(c *ui.Context, p *plugin.Plugin) {
	g := &a.gb
	if p.MissingSettings() != nil {
		a.sourceProblem(c, p, plugin.KindSettings, nil, nil)
		return
	}
	switch {
	case g.collection != "":
		title := g.collection
		if b := g.browse[p.Manifest.ID]; b != nil {
			for _, cl := range b.collections {
				if cl.ID == g.collection {
					title = cl.Title
				}
			}
		}
		ui.Row(c).Padding(0, theme.Space32, theme.Space8, theme.Space24).Children(func() {
			back := ui.ButtonBase(c).Padding(theme.Space2, theme.Space8, theme.Space2, theme.Space4).Gap(theme.Space2).Radius(theme.RadiusXS).AlignItems(ui.Center).Label(locales.GetBooksBackTo(p.Manifest.Name))
			if back.Hovered() {
				back.Background(pal().Hover)
			}
			back.OnClick(func() {
				g.collection = ""
				a.gbRefresh()
			}).Children(func() {
				ui.Icon(c, icons.ChevronLeft).FontSize(14).TextColor(pal().Icon)
				ui.Text(c, title).FontSize(14).FontWeight(600)
			})
		})
		a.resultsList(c, p, g.collection)
	case g.query != "":
		a.resultsList(c, p, "")
	case p.Manifest.HasCollections():
		a.browsePage(c, p)
	default:
		a.getBooksPrompt(c, pluginIcon(p.Manifest), locales.GetBooksSearchSource(p.Manifest.Name), p.Manifest.Description)
	}
}

// resultsList is a scrolling list of a source's results, with more on
// demand.
func (a *App) resultsList(c *ui.Context, p *plugin.Plugin, collection string) {
	l := a.gbList(p, collection)
	if l == nil || l.page == 0 {
		switch {
		case l != nil && l.err != nil:
			a.sourceProblem(c, p, sourceState(p, l.err), l.err, func() { a.gbRetry(p, collection) })
		default:
			ui.Column(c).Grow(1).Center().Gap(theme.Space12).Children(func() {
				ui.Spinner(c).Size(24, 24).Label(locales.CommonSearching())
				ui.Text(c, locales.GetBooksSearchingSource(p.Manifest.Name)).TextColor(pal().TextSecondary)
			})
		}
		return
	}
	if len(l.items) == 0 {
		a.noResults(c, p)
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, theme.Space24, theme.Space24, theme.Space24).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
		for _, r := range l.items {
			a.resultRow(c.Key(r.ID), p, r)
		}
		ui.Row(c).Padding(theme.Space12, 0).Justify(ui.Center).Children(func() {
			switch {
			case l.loading:
				ui.Spinner(c).Size(20, 20).Label(locales.GetBooksLoadingMore())
			case l.err != nil:
				a.inlineProblem(c, p, l.err, func() {
					l.err = nil
					a.gbLoad(p, collection, true)
				})
			case l.hasMore:
				if widgets.Button(c, widgets.Ghost, nil, locales.GetBooksShowMore()).Clicked() {
					a.gbLoad(p, collection, true)
				}
			}
		})
	})
}

// resultRow is the ResultRow component: a book a source found, and what
// can be done with it.
func (a *App) resultRow(c *ui.Context, p *plugin.Plugin, r plugin.Result) {
	src := p.Manifest.ID
	sel := a.gb.selected != nil && a.gb.selected.source == src && a.gb.selected.result.ID == r.ID
	row := ui.ButtonBase(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space16).Radius(theme.RadiusMD).AlignItems(ui.Center).Label(r.Title)
	switch {
	case sel:
		row.Background(pal().Selected).Border(1, pal().BorderSelected)
	case row.Hovered():
		row.Background(pal().Hover)
	}
	row.OnClick(func() { a.gbSelect(src, r) }).Children(func() {
		coverOf(c, &calibre.Book{Title: r.Title, Authors: r.Authors}, a.resultCover(src, r), 40, 58, true)
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			t := ui.Text(c, r.Title).FontSize(14).FontWeight(500).SingleLine()
			if sel {
				t.TextColor(pal().TextAccent)
			}
			if len(r.Authors) > 0 {
				ui.Text(c, strings.Join(r.Authors, ", ")).FontSize(13).TextColor(pal().TextSecondary).SingleLine()
			}
			if meta := resultMeta(r); meta != "" {
				ui.Text(c, meta).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
			}
		})
		ui.Row(c).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			for i, f := range r.Formats {
				if i < 3 {
					widgets.FormatBadge(c.Key(f.Format), f.Format, true)
				}
			}
			if s := r.Formats[0].Size; s > 0 {
				ui.Text(c, humanSize(s)).Font(theme.FontMono).FontSize(11).TextColor(pal().TextTertiary)
			}
		})
		ui.Row(c).Width(132).Justify(ui.End).Children(func() { a.resultAction(c, p, r, sel) })
	})
}

// resultMeta is the line under a result's author: language, year, tags.
func resultMeta(r plugin.Result) string {
	var parts []string
	if code := calibre.LanguageCode(r.Language); code != "" {
		parts = append(parts, languageName(code))
	}
	if t := plugin.ParseDate(r.Pubdate); !t.IsZero() {
		parts = append(parts, fmt.Sprint(t.Year()))
	}
	if len(r.Tags) > 0 {
		parts = append(parts, strings.Join(r.Tags[:min(2, len(r.Tags))], ", "))
	}
	return strings.Join(parts, " · ")
}

// resultAction is what a row offers: Get, the download's progress, or
// that the book is in the library already.
func (a *App) resultAction(c *ui.Context, p *plugin.Plugin, r plugin.Result, sel bool) {
	src := p.Manifest.ID
	if id := a.inLibrary(r); id != 0 {
		b := ui.ButtonBase(c).Padding(theme.Space4, theme.Space6).Gap(theme.Space6).Radius(theme.RadiusSM).AlignItems(ui.Center).Label(locales.GetBooksInLibraryShow(r.Title))
		if b.Hovered() {
			b.Background(pal().Hover)
		}
		b.OnClick(func() { a.showInLibrary(id) }).Children(func() {
			ui.Icon(c, icons.CheckCircle).FontSize(14).TextColor(pal().Success.FG)
			ui.Text(c, locales.GetBooksInLibrary()).FontSize(12).FontWeight(500).TextColor(pal().Success.FG)
		})
		return
	}
	if d := a.downloadOf(src, r); d != nil && !d.finished {
		ui.Column(c).Width(120).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
			ui.Progress(c, d.fraction()).Height(4)
			ui.Text(c, d.progressText()).Font(theme.FontMono).FontSize(11).TextColor(pal().TextTertiary).TextAlign(ui.End).SingleLine()
		})
		return
	}
	format := a.gbFormat(src, a.gbFull(src, r))
	v := widgets.Secondary
	if sel {
		v = widgets.Primary
	}
	if widgets.ButtonSized(c, v, icons.Download, locales.GetBooksGetFormat(format), theme.ControlS).Tooltip(locales.GetBooksDownloadTooltip(format)).Clicked() {
		a.getBook(p, a.gbFull(src, r), format)
	}
}

// gbFormat is the format chosen for r in the detail panel, or its first.
func (a *App) gbFormat(source string, r plugin.Result) string {
	f := a.gb.format[resultKey{source, r.ID}]
	if f != "" && slices.ContainsFunc(r.Formats, func(x plugin.Format) bool { return x.Format == f }) {
		return f
	}
	return r.Formats[0].Format
}

// getBook downloads r as format into the library.
func (a *App) getBook(p *plugin.Plugin, r plugin.Result, format string) {
	if a.lib == nil {
		a.toast("%s", locales.GetBooksOpenLibraryFirst())
		return
	}
	a.downloadBook(p, r, format)
}

// showInLibrary goes to book id in the library.
func (a *App) showInLibrary(id int64) {
	a.query.Search = ""
	a.setQuery(catalog.AllBooks, catalog.Filter{})
	a.curID = id
}

// browsePage shows a source's collections as shelves of covers.
func (a *App) browsePage(c *ui.Context, p *plugin.Plugin) {
	b := a.gb.browse[p.Manifest.ID]
	switch {
	case b == nil || b.loading:
		ui.Column(c).Grow(1).Center().Children(func() { ui.Spinner(c).Size(24, 24).Label(locales.CommonLoading()) })
		return
	case b.err != nil:
		a.sourceProblem(c, p, sourceState(p, b.err), b.err, func() {
			delete(a.gb.browse, p.Manifest.ID)
			a.gbBrowse(p)
		})
		return
	case len(b.collections) == 0:
		a.getBooksPrompt(c, pluginIcon(p.Manifest), locales.GetBooksSearchSource(p.Manifest.Name), p.Manifest.Description)
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(theme.Space8, theme.Space32, theme.Space32, theme.Space32).Gap(theme.Space32).AlignItems(ui.Stretch).Children(func() {
		for _, cl := range b.collections {
			a.shelf(c.Key(cl.ID), p, cl, cl.Title)
		}
	})
}

// shelf is a collection's first books in a row, with See all.
func (a *App) shelf(c *ui.Context, p *plugin.Plugin, cl plugin.Collection, title string) {
	l := a.gbList(p, cl.ID)
	ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(600).Grow(1)
			all := ui.ButtonBase(c).Padding(theme.Space2, theme.Space4).Radius(theme.RadiusXS).Label(locales.GetBooksSeeAllTitle(title))
			all.OnClick(func() { a.gbOpenCollection(p.Manifest.ID, cl.ID) }).Children(func() {
				t := ui.Text(c, locales.CommonSeeAll()).FontSize(12).FontWeight(500).TextColor(pal().TextAccent)
				if all.Hovered() {
					t.Underline()
				}
			})
		})
		switch {
		case l == nil || l.page == 0 && l.err == nil:
			ui.Row(c).Height(theme.CoverH + 44).Center().Children(func() { ui.Spinner(c).Size(20, 20).Label(locales.CommonLoading()) })
		case l.err != nil:
			a.inlineProblem(c, p, l.err, func() { a.gbRetry(p, cl.ID) })
		default:
			// As many books as fit the row, by its width in the last frame.
			n := shelfPreview
			if a.gb.shelfW > 0 {
				n = max(1, int((a.gb.shelfW+theme.Space24)/(theme.CoverW+theme.Space24)))
			}
			row := ui.Row(c).Gap(theme.Space24).Clip()
			if w := row.Bounds().W; w > 0 {
				a.gb.shelfW = w
			}
			row.Children(func() {
				for _, r := range l.items[:min(n, len(l.items))] {
					a.shelfCard(c.Key(r.ID), p, r)
				}
			})
		}
	})
}

// shelfCard is a book on a browse shelf: its cover and title.
func (a *App) shelfCard(c *ui.Context, p *plugin.Plugin, r plugin.Result) {
	src := p.Manifest.ID
	sel := a.gb.selected != nil && a.gb.selected.source == src && a.gb.selected.result.ID == r.ID
	card := ui.ButtonBase(c).Width(theme.CoverW).Label(r.Title)
	card.OnClick(func() { a.gbSelect(src, r) }).Children(func() {
		ui.Column(c).Grow(1).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
			cov := coverOf(c, &calibre.Book{Title: r.Title, Authors: r.Authors}, a.resultCover(src, r), theme.CoverW, theme.CoverH, false)
			if sel || card.Hovered() {
				cov.Shadow(0, 14, 22, -6, ui.RGBA(26, 23, 18, 0.26))
			}
			ui.Column(c).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
				t := ui.Text(c, r.Title).FontSize(13).FontWeight(500).SingleLine()
				if sel {
					t.TextColor(pal().TextAccent)
				}
				ui.Text(c, strings.Join(r.Authors, ", ")).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
			})
		})
	})
}

// allSourcesBody is All sources: every source's answer, as it comes,
// under its own header.
func (a *App) allSourcesBody(c *ui.Context, srcs []*plugin.Plugin) {
	g := &a.gb
	if g.query == "" {
		var browsing []*plugin.Plugin
		for _, p := range srcs {
			if p.Manifest.HasCollections() && p.MissingSettings() == nil {
				browsing = append(browsing, p)
			}
		}
		if len(browsing) == 0 {
			a.getBooksPrompt(c, icons.Layers, locales.GetBooksSearchAllAtOnce(), locales.GetBooksAllSourcesHint())
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(theme.Space8, theme.Space32, theme.Space32, theme.Space32).Gap(theme.Space32).AlignItems(ui.Stretch).Children(func() {
			for _, p := range browsing {
				b := g.browse[p.Manifest.ID]
				if b == nil || len(b.collections) == 0 {
					continue
				}
				cl := b.collections[0]
				a.shelf(c.Key(p.Manifest.ID), p, cl, locales.GetBooksShelfTitle(cl.Title, p.Manifest.Name))
			}
		})
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, theme.Space24, theme.Space24, theme.Space24).Gap(theme.Space20).AlignItems(ui.Stretch).Children(func() {
		for _, p := range srcs {
			ui.Column(c.Key(p.Manifest.ID)).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() { a.sourceGroup(c, p) })
		}
	})
}

// sourceGroup is the SourceGroupHeader component and the source's first
// results.
func (a *App) sourceGroup(c *ui.Context, p *plugin.Plugin) {
	m := p.Manifest
	l := a.gbList(p, "")
	var err error
	if l != nil {
		err = l.err
	}
	kind := sourceState(p, err)
	ui.Row(c).Padding(theme.Space8, theme.Space4).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, pluginIcon(m)).FontSize(16).TextColor(pal().Icon)
		ui.Text(c, m.Name).FontSize(13).FontWeight(600)
		link := func(label string, chevron bool, onClick func()) {
			b := ui.ButtonBase(c).Padding(theme.Space2, theme.Space4).Gap(theme.Space2).Radius(theme.RadiusXS).AlignItems(ui.Center).Label(locales.GetBooksLabelValue(label, m.Name))
			b.OnClick(onClick).Children(func() {
				t := ui.Text(c, label).FontSize(12).FontWeight(500).TextColor(pal().TextAccent)
				if b.Hovered() {
					t.Underline()
				}
				if chevron {
					ui.Icon(c, icons.ChevronRight).FontSize(12).TextColor(pal().TextAccent)
				}
			})
		}
		switch {
		case kind == plugin.KindSettings || kind == plugin.KindAuth:
			ui.Text(c, problemTitle(p, kind)).FontSize(12).TextColor(pal().Warning.FG).Grow(1).SingleLine()
			link(locales.GetBooksSetUp(), false, func() { a.openPlugins(m.ID) })
		case err != nil:
			ui.Text(c, problemTitle(p, kind)).FontSize(12).TextColor(pal().Danger.FG).Grow(1).SingleLine()
			link(locales.CommonRetry(), false, func() { a.gbRetry(p, "") })
		case l == nil || l.loading && l.page == 0:
			ui.Spinner(c).Size(14, 14).Label(locales.CommonSearching())
			ui.Text(c, locales.GetBooksSearching()).FontSize(12).TextColor(pal().TextTertiary).Grow(1)
		default:
			ui.Text(c, resultCountText(len(l.items), l.hasMore)).Font(theme.FontMono).FontSize(11).TextColor(pal().TextTertiary).Grow(1)
			if len(l.items) > 0 {
				link(locales.CommonSeeAll(), true, func() {
					a.gb.source = m.ID
					a.settings.GetBooksSource = m.ID
					a.saveSettings()
					a.gbRefresh()
				})
			}
		}
	})
	if l != nil && err == nil {
		for _, r := range l.items[:min(allPreview, len(l.items))] {
			a.resultRow(c.Key(r.ID), p, r)
		}
	}
}

// ---- States

// problemTitle says in a line why a source can't be used.
func problemTitle(p *plugin.Plugin, kind plugin.ErrorKind) string {
	switch kind {
	case plugin.KindSettings:
		return locales.GetBooksNeeds(strings.Join(p.MissingSettings(), ", "))
	case plugin.KindAuth:
		return locales.GetBooksNeedsSignIn()
	case plugin.KindRateLimited:
		return locales.GetBooksIsBusy()
	case plugin.KindUnavailable, plugin.KindTimeout:
		return locales.GetBooksIsntAnswering()
	case plugin.KindNotFound:
		return locales.GetBooksDoesntHaveThat()
	}
	return locales.GetBooksPluginFailedShort()
}

// problemSentence is problemTitle as a sentence about the source, for the
// banner above a list.
func problemSentence(p *plugin.Plugin, kind plugin.ErrorKind) string {
	name := p.Manifest.Name
	switch kind {
	case plugin.KindSettings:
		return locales.GetBooksInlineNeeds(name, strings.Join(p.MissingSettings(), ", "))
	case plugin.KindAuth:
		return locales.GetBooksInlineNeedsSignIn(name)
	case plugin.KindRateLimited:
		return locales.GetBooksInlineBusy(name)
	case plugin.KindUnavailable, plugin.KindTimeout:
		return locales.GetBooksInlineNotAnswering(name)
	case plugin.KindNotFound:
		return locales.GetBooksInlineDoesntHaveThat(name)
	}
	return locales.GetBooksInlinePluginFailed(name)
}

// sourceProblem fills the page with why a source can't be used, and what
// to do about it.
func (a *App) sourceProblem(c *ui.Context, p *plugin.Plugin, kind plugin.ErrorKind, err error, retry func()) {
	m := p.Manifest
	ui.Column(c).Grow(1).Center().Gap(theme.Space12).Padding(theme.Space48).Children(func() {
		ic := icons.Alert
		title, body := locales.GetBooksSourceNotAnswering(m.Name), locales.GetBooksCheckConnection()
		if msg := errText(err); msg != "" {
			body = msg // what the plugin knows is wrong beats a guess
		}
		switch kind {
		case plugin.KindSettings, plugin.KindAuth:
			ic = icons.Key
			title = locales.GetBooksSourceNeedsSetup(m.Name)
			body = locales.GetBooksFillInSettings(strings.Join(p.MissingSettings(), ", "))
			if kind == plugin.KindAuth {
				body = locales.GetBooksAuthRefused(errText(err))
			}
		case plugin.KindRateLimited:
			title, body = locales.GetBooksSourceBusy(m.Name), locales.GetBooksSlowDown()
		case plugin.KindInvalid, plugin.KindScript, plugin.KindPermission:
			title, body = locales.GetBooksPluginFailed(m.Name), errText(err)
		}
		ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().Subtle).Center().Children(func() {
			ui.Icon(c, ic).FontSize(20).TextColor(pal().IconMuted)
		})
		ui.Text(c, title).FontWeight(600).FontSize(16).TextAlign(ui.Center)
		ui.Text(c, body).FontSize(13).TextColor(pal().TextSecondary).TextAlign(ui.Center).MaxWidth(420)
		if kind == plugin.KindSettings || kind == plugin.KindAuth {
			if widgets.Button(c, widgets.Primary, nil, locales.GetBooksOpenPluginSettings()).Margin(theme.Space8, 0, 0, 0).Clicked() {
				a.openPlugins(m.ID)
			}
		} else if retry != nil {
			if widgets.Button(c, widgets.Secondary, icons.Refresh, locales.CommonTryAgain()).Margin(theme.Space8, 0, 0, 0).Clicked() {
				retry()
			}
		}
	})
}

// inlineProblem is a banner for a failure that keeps what loaded.
func (a *App) inlineProblem(c *ui.Context, p *plugin.Plugin, err error, retry func()) {
	kind := sourceState(p, err)
	ui.Row(c).Grow(1).Padding(theme.Space8, theme.Space8, theme.Space8, theme.Space12).Gap(theme.Space8).Radius(theme.RadiusMD).
		Background(pal().Warning.BG).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icons.Alert).FontSize(14).TextColor(pal().Warning.FG)
		ui.Text(c, problemSentence(p, kind)).FontSize(12).TextColor(pal().Warning.FG).Grow(1)
		if widgets.ButtonSized(c, widgets.Secondary, icons.Refresh, locales.CommonRetry(), theme.ControlS).Clicked() {
			retry()
		}
	})
}

func errText(err error) string {
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return pe.Msg
	}
	if err != nil {
		return err.Error()
	}
	return ""
}

func (a *App) noResults(c *ui.Context, p *plugin.Plugin) {
	ui.Column(c).Grow(1).Center().Gap(theme.Space12).Padding(theme.Space48).Children(func() {
		ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().Subtle).Center().Children(func() {
			ui.Icon(c, icons.Search).FontSize(20).TextColor(pal().IconMuted)
		})
		ui.Text(c, locales.GetBooksNoMatch(a.gb.query)).FontWeight(600).FontSize(16)
		ui.Text(c, locales.GetBooksNoMatchHint()).FontSize(13).TextColor(pal().TextSecondary).TextAlign(ui.Center).MaxWidth(420)
		if len(a.sources()) > 1 {
			if widgets.Button(c, widgets.Secondary, icons.Layers, locales.GetBooksSearchAllSources()).Margin(theme.Space8, 0, 0, 0).Clicked() {
				a.gbSetSource(allSources)
			}
		}
	})
}

func (a *App) getBooksPrompt(c *ui.Context, icon *ui.SVG, title, body string) {
	ui.Column(c).Grow(1).Center().Gap(theme.Space12).Padding(theme.Space48).Children(func() {
		ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().Subtle).Center().Children(func() {
			ui.Icon(c, icon).FontSize(20).TextColor(pal().IconMuted)
		})
		ui.Text(c, title).FontWeight(600).FontSize(16)
		if body != "" {
			ui.Text(c, body).FontSize(13).TextColor(pal().TextSecondary).TextAlign(ui.Center).MaxWidth(420)
		}
	})
}

func (a *App) noSources(c *ui.Context) {
	ui.Column(c).Grow(1).Center().Gap(theme.Space12).Padding(theme.Space48).Children(func() {
		ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().Subtle).Center().Children(func() {
			ui.Icon(c, icons.Puzzle).FontSize(20).TextColor(pal().IconMuted)
		})
		ui.Text(c, locales.GetBooksNoSourcesTitle()).FontWeight(600).FontSize(16)
		ui.Text(c, locales.GetBooksNoSourcesBody()).FontSize(13).TextColor(pal().TextSecondary).TextAlign(ui.Center)
		if widgets.Button(c, widgets.Primary, icons.Puzzle, locales.GetBooksManagePlugins()).Margin(theme.Space8, 0, 0, 0).Clicked() {
			a.openPlugins("")
		}
	})
}

// ---- Detail panel

// resultDetail is the detail panel for the result picked: cover, the
// formats to download, and all the source says about it.
func (a *App) resultDetail(c *ui.Context) {
	ref := a.gb.selected
	if ref == nil || a.pluginByID(ref.source) == nil {
		ui.Column(c).Grow(1).Center().Gap(theme.Space8).Padding(theme.Space32).Children(func() {
			ui.Icon(c, icons.BookOpen).FontSize(28).TextColor(pal().IconMuted)
			ui.Text(c, locales.GetBooksSelectBook()).TextColor(pal().TextTertiary).TextAlign(ui.Center)
		})
		return
	}
	p := a.pluginByID(ref.source)
	src := ref.source
	r := a.gbFull(src, ref.result)
	f := a.gb.full[resultKey{src, r.ID}]
	ui.Scroll(c).Grow(1).Padding(theme.Space24).Gap(theme.Space24).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Gap(theme.Space16).AlignItems(ui.Center).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).Padding(theme.Space24, 0).Radius(theme.RadiusLG).Background(pal().Subtle).Children(func() {
				coverOf(c, &calibre.Book{Title: r.Title, Authors: r.Authors}, a.resultCover(src, r), 152, 228, false)
			})
			ui.Column(c).FillWidth().Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, r.Title).FontWeight(600).FontSize(20).FixedLineHeight(26).LetterSpacing(-0.3).TextAlign(ui.Center).Selectable()
				if len(r.Authors) > 0 {
					ui.Text(c, strings.Join(r.Authors, ", ")).FontSize(14).TextColor(pal().TextAccent).TextAlign(ui.Center)
				}
			})
		})
		a.resultDownload(c, p, r)
		widgets.Hairline(c)
		a.resultFacts(c, p, r, f)
		if r.Description != "" {
			widgets.Hairline(c)
			ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
				widgets.Overline(c, locales.GetBooksAbout())
				ui.Text(c, htmlToText(r.Description)).FontSize(13).FixedLineHeight(20).TextColor(pal().TextSecondary).Selectable()
			})
		}
		ui.Row(c).Gap(theme.Space6).AlignItems(ui.Start).Children(func() {
			ic, from := icons.Puzzle, locales.GetBooksFromPlugin(p.Manifest.Name)
			if p.Native() != nil {
				ic, from = icons.Rss, locales.GetBooksFromCatalog(p.Manifest.Name)
			}
			ui.Icon(c, ic).FontSize(12).TextColor(pal().IconMuted)
			ui.Text(c, from).
				FontSize(11).TextColor(pal().TextTertiary).Grow(1)
		})
	})
}

// resultDownload is DOWNLOAD AS: a radio list of the formats with their
// sizes, and the button adding the book.
func (a *App) resultDownload(c *ui.Context, p *plugin.Plugin, r plugin.Result) {
	src := p.Manifest.ID
	k := resultKey{src, r.ID}
	chosen := a.gbFormat(src, r)
	inLib := a.inLibrary(r)
	d := a.downloadOf(src, r)
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.GetBooksDownloadAs())
		for _, f := range r.Formats {
			opt := ui.ButtonBase(c.Key(f.Format)).Height(36).Padding(0, theme.Space10).Gap(theme.Space10).Radius(theme.RadiusMD).AlignItems(ui.Center).
				Role(ui.RoleRadio).Label(f.Format)
			switch {
			case f.Format == chosen:
				opt.Background(pal().Selected).Border(1.5, pal().BorderSelected)
			case opt.Hovered():
				opt.Background(pal().Hover).Border(1, pal().BorderStrong)
			default:
				opt.Background(pal().Surface).Border(1, pal().Border)
			}
			opt.OnClick(func() { a.gb.format[k] = f.Format }).Children(func() {
				widgets.FormatBadge(c, f.Format, false)
				ui.Text(c, formatPurpose(f.Format)).FontSize(13).Grow(1).SingleLine()
				if f.Size > 0 {
					ui.Text(c, humanSize(f.Size)).Font(theme.FontMono).FontSize(11).TextColor(pal().TextTertiary)
				}
			})
		}
		switch {
		case inLib != 0:
			if widgets.Button(c, widgets.Secondary, icons.CheckCircle, locales.GetBooksInLibraryShowButton()).Clicked() {
				a.showInLibrary(inLib)
			}
		case d != nil && !d.finished:
			ui.Column(c).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
				ui.Progress(c, d.fraction()).Height(4)
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, locales.GetBooksDownloading(d.progressText())).FontSize(12).TextColor(pal().TextSecondary).Grow(1)
					if widgets.ButtonSized(c, widgets.Ghost, icons.X, locales.CommonCancel(), theme.ControlS).Clicked() {
						d.cancel()
					}
				})
			})
		default:
			if widgets.Button(c, widgets.Primary, icons.Download, locales.GetBooksAddToLibrary()).Disabled(a.lib == nil).Clicked() {
				a.getBook(p, r, chosen)
			}
			if d != nil && d.err != nil {
				ui.Text(c, pluginErrorText(a, d.result.Title, d.err)).FontSize(12).TextColor(pal().Danger.FG)
			}
		}
	})
}

// resultFacts is DETAILS: what the source knows, and the subjects.
func (a *App) resultFacts(c *ui.Context, p *plugin.Plugin, r plugin.Result, f *fullResult) {
	ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			widgets.Overline(c, locales.GetBooksDetails()).Grow(1)
			if f != nil && f.loading {
				ui.Spinner(c).Size(12, 12).Label(locales.GetBooksLoadingDetails())
			}
		})
		row := func(k, label, v string) {
			if v == "" {
				return
			}
			ui.Row(c.Key(k)).Gap(theme.Space12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, label).FontSize(13).TextColor(pal().TextTertiary).Width(84)
				ui.Text(c, v).FontSize(13).Grow(1).Selectable()
			})
		}
		if code := calibre.LanguageCode(r.Language); code != "" {
			row("language", locales.CommonLanguage(), languageName(code))
		}
		if t := plugin.ParseDate(r.Pubdate); !t.IsZero() {
			row("published", locales.CommonPublished(), r.Pubdate)
		}
		row("publisher", locales.CommonPublisher(), r.Publisher)
		if r.Series != "" {
			s := r.Series
			if r.SeriesIndex > 0 {
				s += " #" + seriesIndex(r.SeriesIndex)
			}
			row("series", locales.CommonSeries(), s)
		}
		for _, fl := range r.Fields {
			row("field:"+fl.Label, fl.Label, fl.Value)
		}
		if f != nil && f.err != nil {
			ui.Text(c, locales.GetBooksDetailsError(errText(f.err))).FontSize(12).TextColor(pal().TextTertiary)
		}
		if len(r.Tags) > 0 {
			ui.Row(c).Gap(theme.Space6).Wrap().Children(func() {
				for _, t := range r.Tags {
					if widgets.Tag(c.Key(t), t, false, nil).Tooltip(locales.GetBooksSearchTag(t)).Clicked() {
						a.gb.text = t
						a.gbSearch()
					}
				}
			})
		}
		if r.URL != "" {
			l := ui.ButtonBase(c).Gap(theme.Space4).AlignItems(ui.Center).Label(locales.GetBooksOpenBookPageOn(p.Manifest.Name))
			l.OnClick(func() { openURL(r.URL) }).Children(func() {
				t := ui.Text(c, locales.GetBooksOpenBookPage()).FontSize(13).TextColor(pal().TextAccent)
				if l.Hovered() {
					t.Underline()
				}
				ui.Icon(c, icons.External).FontSize(12).TextColor(pal().TextAccent)
			})
		}
	})
}
