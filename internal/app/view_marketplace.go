package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/plugin"
)

// The Plugins window's Marketplace tab (Figma "18 · Plugin
// marketplace"): search, categories and a grid of the plugins listed,
// and the one chosen, with what it adds and may reach.

// pluginsTabs are the Plugins window's tabs, in its header.
func (a *App) pluginsTabs(c *ui.Context) {
	tab := a.plTab
	ui.Tabs(c, &tab, locales.PluginsTabInstalled(), locales.PluginsTabMarketplace())
	if tab != a.plTab {
		a.plTab = tab
		if tab == pluginsTabMarketplace {
			a.loadMarketplace(false)
		}
	}
}

// cardWidth is a PluginCard's narrowest; the grid's width is shared
// between as many as fit.
const cardWidth = 220

// cardGap is the space between cards.
const cardGap = theme.Space12

// cardsWide is a PluginCard's width in a grid w wide.
func cardsWide(w float32) float32 {
	if w <= 0 {
		return 242
	}
	n := max(1, int((w+cardGap)/(cardWidth+cardGap)))
	return float32(int((w - cardGap*float32(n-1)) / float32(n)))
}

// marketplacePane is the Marketplace tab: the grid, and the plugin chosen.
func (a *App) marketplacePane(c *ui.Context) {
	mk := &a.mk
	if mk.index == nil && !mk.loading && mk.err == "" {
		a.loadMarketplace(false)
	}
	list := a.marketListings()
	if (mk.sel == "" || mk.index.Find(mk.sel) == nil) && len(list) > 0 {
		mk.sel = list[0].ID
	}
	// The grid's scroll bar runs in a gutter of its own, off the cards.
	ui.Column(c).Grow(1).MinWidth(0).Padding(theme.Space20, theme.Space12, 0, theme.Space24).Gap(theme.Space16).Background(pal().BgApp).AlignItems(ui.Stretch).Children(func() {
		ui.SearchField(c, &mk.query).Placeholder(locales.MarketSearch()).Label(locales.MarketSearch()).Margin(0, theme.Space12, 0, 0)
		ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Margin(0, theme.Space12, 0, 0).Children(func() {
			ui.Row(c).Grow(1).MinWidth(0).Wrap().Gap(theme.Space6).Children(func() {
				if mk.index == nil {
					return
				}
				if widgets.Tag(c.Key("all"), locales.MarketAll(), mk.cat == "", nil).Clicked() {
					mk.cat = ""
				}
				for _, cat := range mk.index.Categories {
					if widgets.Tag(c.Key(cat.ID), cat.Title, mk.cat == cat.ID, nil).Clicked() {
						mk.cat = cat.ID
					}
				}
			})
			if mk.index != nil {
				ui.Text(c, locales.MarketCount(len(list))).FontSize(13).TextColor(pal().TextSecondary).NoWrap()
			}
		})
		if mk.index != nil && mk.err != "" {
			a.marketError(c, locales.MarketStale())
		}
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, theme.Space12, theme.Space24, 0).AlignItems(ui.Stretch).Children(func() {
			switch {
			case mk.index == nil && mk.loading:
				ui.Row(c).Padding(theme.Space12, 0).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					ui.Spinner(c).Size(14, 14)
					ui.Text(c, locales.MarketLoading()).FontSize(13).TextColor(pal().TextTertiary)
				})
			case mk.index == nil && mk.err != "":
				a.marketError(c, locales.MarketFailed(mk.err))
			case len(list) == 0:
				ui.Text(c, locales.MarketNoMatch()).FontSize(13).TextColor(pal().TextTertiary).Padding(theme.Space12, 0)
			}
			width := cardsWide(a.gridBox.Bounds(c).W)
			ui.Row(c).Wrap().Gap(cardGap).AlignItems(ui.Stretch).Bind(&a.gridBox).Children(func() {
				for _, l := range list {
					a.pluginCard(c.Key(l.ID), l, width)
				}
			})
		})
	})
	ui.Scroll(c).Width(400).Padding(theme.Space24).Gap(theme.Space20).Background(pal().SurfaceRaised).BorderWidth(0, 0, 0, 1).BorderColor(pal().BorderSubtle).AlignItems(ui.Stretch).Children(func() {
		if l := mk.index.Find(mk.sel); l != nil {
			a.listingPane(c, l)
		}
	})
}

// marketError says why the index couldn't be read, with Try again.
func (a *App) marketError(c *ui.Context, msg string) {
	ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, msg).FontSize(13).TextColor(pal().TextSecondary).MaxLines(4).Grow(1).MinWidth(0)
		if widgets.ButtonSized(c, widgets.Secondary, nil, locales.MarketTryAgain(), theme.ControlS).Disabled(a.mk.loading).Clicked() {
			a.loadMarketplace(true)
		}
	})
}

// pluginCard is the PluginCard component: a plugin in the grid.
func (a *App) pluginCard(c *ui.Context, l *plugin.Listing, width float32) {
	sel := a.mk.sel == l.ID
	state := a.listingState(l)
	card := ui.ButtonBase(c).Width(width).Padding(theme.Space16).Radius(theme.RadiusLG).AlignItems(ui.Stretch).Label(l.Name)
	switch {
	case sel:
		card.Background(pal().Selected).Border(1.5, pal().BorderSelected)
	case card.Hovered():
		card.Background(pal().Surface).Border(1, pal().BorderStrong)
	default:
		card.Background(pal().Surface).Border(1, pal().BorderSubtle)
	}
	card.OnClick(func() { a.mk.sel = l.ID }).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				pluginTile(c, l.Manifest(), 40)
				ui.Spacer(c)
				a.listingButton(c, l, state, false)
			})
			ui.Column(c).Gap(theme.Space4).AlignItems(ui.Stretch).Children(func() {
				ui.Text(c, l.Name).FontSize(14).FontWeight(600).SingleLine()
				// Two lines, always: the cards of a row line up.
				desc, tone := l.Description, pal().TextSecondary
				if state == listingUpdate {
					desc, tone = locales.MarketUpdateTo(l.Version), pal().TextAccent
				}
				ui.Text(c, desc).FontSize(13).TextColor(tone).MaxLines(2).MinHeight(36)
			})
		})
	})
}

// listingButton is what a listing offers: Install, Update, or that it is
// installed. primary is the detail pane's larger button.
func (a *App) listingButton(c *ui.Context, l *plugin.Listing, state listingState, primary bool) {
	variant, height := widgets.Secondary, float32(theme.ControlS)
	if primary {
		variant, height = widgets.Primary, theme.ControlM
	}
	switch {
	case a.mk.installing == l.ID || state == listingUpdate && a.updates[l.ID] != nil && a.updates[l.ID].busy:
		ui.Spinner(c).Size(16, 16).Label(locales.MarketInstallingLabel(l.Name))
	case state == listingNeedsDalibre:
		ui.Text(c, locales.MarketNeedsDalibre(l.Manifest().NeedsDalibre())).FontSize(12).TextColor(pal().TextTertiary).NoWrap()
	case state == listingInstalled:
		ui.Row(c).Gap(theme.Space4).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.CheckCircle).FontSize(14).TextColor(pal().Success.FG)
			ui.Text(c, locales.MarketInstalled()).FontSize(12).FontWeight(500).TextColor(pal().Success.FG).NoWrap()
		})
	case state == listingUpdate:
		var icon *ui.SVG
		if primary {
			icon = icons.Refresh
		}
		if widgets.ButtonSized(c, variant, icon, locales.CommonUpdate(), height).Clicked() {
			a.updatePlugin(l.ID, false)
		}
	default:
		var icon *ui.SVG
		if primary {
			icon = icons.Download
		}
		if widgets.ButtonSized(c, variant, icon, locales.MarketInstall(), height).Disabled(a.mk.installing != "" || a.pluginDir == "").Clicked() {
			a.installListing(l)
		}
	}
}

// listingPane is the plugin chosen in the Marketplace: what it is, does
// and may reach.
func (a *App) listingPane(c *ui.Context, l *plugin.Listing) {
	m := l.Manifest()
	state := a.listingState(l)
	ui.Column(c).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Gap(theme.Space12).AlignItems(ui.Center).Children(func() {
			pluginTile(c, m, 48)
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
				ui.Text(c, l.Name).FontSize(18).FontWeight(600).MaxLines(2)
				meta := locales.PluginsVersion(l.Version)
				if state == listingUpdate {
					meta = locales.MarketVersionInstalled(a.installedVersion(l.ID, nil), l.Version)
				} else if l.Author != "" {
					meta += " · " + l.Author
				}
				ui.Text(c, meta).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
			})
		})
		if l.Description != "" {
			ui.Text(c, l.Description).FontSize(13).TextColor(pal().TextSecondary)
		}
		a.listingAction(c, l, state)
	})
	if places := listingPlaces(m); len(places) > 0 {
		widgets.Hairline(c)
		ui.Column(c).Gap(theme.Space10).AlignItems(ui.Stretch).Children(func() {
			widgets.Overline(c, locales.PluginsAddsToTitle())
			for i, p := range places {
				ui.Row(c.Key(i)).Gap(theme.Space10).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, p.icon).FontSize(14).TextColor(pal().IconMuted)
					ui.Text(c, p.title).FontSize(13).SingleLine().Grow(1).MinWidth(0)
					if p.where != "" {
						ui.Text(c, p.where).FontSize(12).TextColor(pal().TextTertiary).NoWrap()
					}
				})
			}
		})
	}
	widgets.Hairline(c)
	ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.PluginsCanAccess())
		for _, h := range m.Permissions.Hosts {
			permissionItem(c.Key(h), icons.Globe, h, "", false)
		}
		if len(m.Permissions.Hosts) == 0 && !m.Permissions.LocalNetwork {
			permissionItem(c, icons.ShieldCheck, locales.MarketNoInternet(), "", false)
		}
		coversPermission(c, m, nil)
		if m.Permissions.LocalNetwork {
			permissionItem(c, icons.Network, locales.PluginsLocalNetwork(), locales.PluginsLocalNetworkDetail(), true)
		}
		if m.IsFileType() {
			permissionItem(c, icons.File, locales.PluginsBookFilesAccess(), locales.PluginsBookFilesAccessDetail(), false)
		}
		libraryPermission(c, m, false, nil)
		note := locales.MarketNoteAllow()
		switch state {
		case listingNeedsDalibre:
			note = locales.MarketNeedsDalibreNote(m.NeedsDalibre())
		case listingInstalled, listingUpdate:
			note = locales.PluginsNoteAllowed()
		}
		ui.Text(c, note).FontSize(12).TextColor(pal().TextTertiary)
	})
	ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		// Its folder for one of the Marketplace's own, else its repository.
		src := l.Homepage
		if l.Community || !strings.HasPrefix(src, "https://github.com/") {
			src = "https://github.com/" + l.Repo
		}
		if l.Repo != "" {
			if widgets.ButtonSized(c, widgets.Ghost, icons.External, locales.PluginsViewOnGithub(), theme.ControlS).Tooltip(strings.TrimPrefix(src, "https://")).Clicked() {
				openURL(src)
			}
		}
		ui.Spacer(c)
		if l.Community {
			widgets.Tag(c, locales.MarketCommunity(), false, nil).Tooltip(locales.MarketCommunityNote(l.Repo))
		}
		if l.License != "" {
			ui.Text(c, l.License).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
}

// listingAction is the detail pane's button: Install, Update, or that it
// is installed, with its settings a click away.
func (a *App) listingAction(c *ui.Context, l *plugin.Listing, state listingState) {
	switch {
	case a.mk.installing == l.ID || state == listingUpdate && a.updates[l.ID] != nil && a.updates[l.ID].busy:
		ui.Row(c).Height(theme.ControlM).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Size(16, 16)
			ui.Text(c, locales.MarketInstallingLabel(l.Name)).FontSize(13).TextColor(pal().TextSecondary)
		})
	case state == listingNeedsDalibre:
		ui.Text(c, locales.MarketNeedsDalibre(l.Manifest().NeedsDalibre())).FontSize(13).TextColor(pal().TextTertiary)
	case state == listingInstalled:
		ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.CheckCircle).FontSize(16).TextColor(pal().Success.FG)
			ui.Text(c, locales.MarketInstalled()).FontSize(13).FontWeight(500).TextColor(pal().Success.FG).Grow(1)
			if widgets.ButtonSized(c, widgets.Secondary, icons.Settings, locales.MarketShowInstalled(), theme.ControlS).Clicked() {
				a.plTab, a.pl.sel = pluginsTabInstalled, l.ID
			}
		})
	case state == listingUpdate:
		if widgets.Button(c, widgets.Primary, icons.Refresh, locales.CommonUpdate()).Clicked() {
			a.updatePlugin(l.ID, false)
		}
	default:
		if widgets.Button(c, widgets.Primary, icons.Download, locales.MarketInstall()).Disabled(a.mk.installing != "" || a.pluginDir == "").Clicked() {
			a.installListing(l)
		}
	}
}

// place is where a plugin shows up in Dalibre.
type place struct {
	icon         *ui.SVG
	title, where string
}

// listingPlaces are where the plugin shows up: its actions by title, in
// Tools or the book menu, and what else it adds.
func listingPlaces(m *plugin.Manifest) []place {
	var out []place
	for i := range m.Capabilities.Actions {
		act := &m.Capabilities.Actions[i]
		if act.In != "" {
			continue // only among its own results
		}
		where := locales.PluginsCapBookMenu()
		if act.Scope == plugin.ScopeLibrary {
			where = locales.MainNavTools()
		}
		out = append(out, place{actionIcon(act), act.Title, where})
	}
	if m.IsBookSource() {
		out = append(out, place{icons.Download, locales.CommonGetBooks(), ""})
	}
	if m.IsMetadataSource() {
		out = append(out, place{icons.Search, locales.CommonFetchMetadata(), ""})
	}
	if m.IsFileType() {
		out = append(out, place{icons.File, locales.PluginsCapBookFiles(), ""})
	}
	return out
}
