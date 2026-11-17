package app

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

// fetchDialog is the Edit metadata dialog's Fetch metadata step (Figma,
// "Edit Metadata — Fetch metadata · Light" and "· States").
func (a *App) fetchDialog(c *ui.Context, b *calibre.Book) {
	f := &a.fetch
	srcs := a.metadataSources()
	cands := a.fetchCandidates()
	sel, hasSel := a.fetchSelected()
	widgets.DialogFrame(c, &f.open, 880, func() {
		ui.Text(c, locales.CommonFetchMetadata()).FontSize(16).FontWeight(600)
		ui.Text(c, a.edit.title).FontSize(13).TextColor(pal().TextTertiary).SingleLine().Grow(1).MinWidth(0)
	}, func() {
		ui.Column(c).Padding(theme.Space24).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
			ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
				ui.TextInput(c, &f.text).Label(locales.FetchMetaLookUp()).Placeholder(locales.FetchMetaLookUpPlaceholder()).Grow(1).
					Disabled(len(srcs) == 0).OnSubmit(func() { a.fetchSearch() })
				if len(srcs) > 1 {
					names := []string{locales.CommonAllSources()}
					for _, p := range srcs {
						names = append(names, p.Manifest.Name)
					}
					cur := locales.CommonAllSources()
					if p := a.pluginByID(f.source); p != nil {
						cur = p.Manifest.Name
					}
					if ui.Select(c, &cur, names).Width(160).Label(locales.FetchMetaSource()).Changed() {
						f.source = ""
						for _, p := range srcs {
							if p.Manifest.Name == cur {
								f.source = p.Manifest.ID
							}
						}
						a.fetchSearch()
					}
				}
				if widgets.Button(c, widgets.Secondary, icons.Search, locales.FetchMetaSearch()).Disabled(len(srcs) == 0).Clicked() {
					a.fetchSearch()
				}
			})
			ui.Row(c).Height(480).AlignItems(ui.Stretch).Border(1, pal().BorderSubtle).Radius(theme.RadiusLG).Clip().Children(func() {
				ui.Column(c).Width(300).Padding(theme.Space8).Gap(theme.Space2).Background(pal().Subtle).
					BorderWidth(0, 1, 0, 0).BorderColor(pal().BorderSubtle).AlignItems(ui.Stretch).Children(func() {
					a.fetchCandidatesColumn(c, srcs, cands)
				})
				ui.Scroll(c).Grow(1).MinWidth(0).Background(pal().Surface).Padding(theme.Space8, theme.Space20, theme.Space12, theme.Space20).AlignItems(ui.Stretch).Children(func() {
					if hasSel {
						a.fetchComparison(c, b, sel)
					}
				})
			})
		})
	}, func() {
		ui.Row(c).Grow(1).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			if !hasSel {
				return
			}
			ui.Icon(c, icons.Globe).FontSize(12).TextColor(pal().IconMuted)
			ui.Text(c, locales.FetchMetaFromSource(sel.plugin.Manifest.Name)).FontSize(12).TextColor(pal().TextTertiary)
			link := sel.r.URL
			if link == "" {
				link = sel.plugin.Manifest.Homepage
			}
			if u, err := url.Parse(link); err == nil && u.Host != "" {
				ui.Link(c, u.Host, link).FontSize(12).TextColor(pal().TextAccent).Underline()
			}
		})
		if widgets.Button(c, widgets.Secondary, nil, locales.FetchMetaBack()).Clicked() {
			f.open = false
		}
		if widgets.Button(c, widgets.Primary, nil, locales.FetchMetaUseSelected()).Disabled(!hasSel || !anyTrue(f.use)).Clicked() {
			a.fetchApply()
		}
	})
}

// fetchCandidatesColumn lists the candidates, or why there are none.
func (a *App) fetchCandidatesColumn(c *ui.Context, srcs []*plugin.Plugin, cands []candidate) {
	f := &a.fetch
	name := locales.FetchMetaYourSources()
	if ps := a.fetchSources(); len(ps) == 1 {
		name = ps[0].Manifest.Name
	}
	if len(srcs) == 0 {
		fetchEmpty(c, icons.Puzzle, false, locales.FetchMetaNoPluginTitle(), locales.FetchMetaNoPluginBody(),
			icons.Settings, locales.FetchMetaOpenSettings(), func() { a.openPlugins("") })
		return
	}
	if len(cands) == 0 {
		// A source that needs setting up wins: the user can do something about it.
		var failed error
		var setup *plugin.Plugin
		for _, p := range a.fetchSources() {
			if err := f.errs[p.Manifest.ID]; err != nil {
				if k := errKind(err); setup == nil && (k == plugin.KindSettings || k == plugin.KindAuth) {
					setup, failed = p, err
				} else if setup == nil {
					failed = err
				}
			}
		}
		switch {
		case len(f.pending) > 0:
			ui.Column(c).Grow(1).Center().Gap(theme.Space8).Children(func() {
				ui.Spinner(c).Size(20, 20).Label(locales.CommonSearching())
				ui.Text(c, locales.FetchMetaSearching(name)).FontSize(13).FontWeight(500)
			})
		case setup != nil:
			body := errText(failed)
			if errKind(failed) == plugin.KindSettings {
				body = locales.GetBooksFillInSettings(strings.Join(setup.MissingSettings(), ", "))
			}
			id := setup.Manifest.ID
			fetchEmpty(c, icons.Key, false, locales.GetBooksSourceNeedsSetup(setup.Manifest.Name), body,
				icons.Settings, locales.GetBooksOpenPluginSettings(), func() { a.openPlugins(id) })
		case failed != nil:
			body := locales.FetchMetaCheckConnection()
			if msg := errText(failed); msg != "" {
				body = msg
			}
			fetchEmpty(c, icons.Alert, true, locales.FetchMetaNotAnswering(name), body, icons.Refresh, locales.CommonRetry(), a.fetchSearch)
		default:
			fetchEmpty(c, icons.Search, false, locales.FetchMetaNothingFound(name), locales.FetchMetaTryFewer(), nil, "", nil)
		}
		return
	}
	ui.Row(c).Padding(theme.Space4, theme.Space8, theme.Space6, theme.Space8).AlignItems(ui.Center).Children(func() {
		widgets.Overline(c, locales.FetchMetaCandidates()).Grow(1)
		if len(f.pending) > 0 {
			ui.Spinner(c).Size(12, 12).Label(locales.CommonSearching())
		}
		ui.Text(c, fmt.Sprint(len(cands))).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
	})
	ui.Scroll(c).Grow(1).MinHeight(0).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		for _, cd := range cands {
			a.candidateRow(c.Key(cd.key()), cd)
		}
	})
}

// candidateRow is a candidate in the list: cover, title, authors and
// year, publisher and source.
func (a *App) candidateRow(c *ui.Context, cd candidate) {
	sel := a.fetch.sel == cd.key()
	row := ui.ButtonBase(c).Padding(theme.Space8, 10).Gap(theme.Space12).Radius(theme.RadiusMD).AlignItems(ui.Center).Label(cd.r.Title)
	switch {
	case sel:
		row.Background(pal().Selected)
	case row.Hovered():
		row.Background(pal().Hover)
	}
	var bmp *ui.Bitmap
	if rc := a.fetchCover(cd); rc != nil {
		bmp = rc.bmp
	}
	row.OnClick(func() { a.fetchSelect(cd) }).Children(func() {
		coverOf(c, &calibre.Book{Title: cd.r.Title, Authors: cd.r.Authors}, bmp, 40, 60, true)
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			t := ui.Text(c, cd.r.Title).FontSize(14).FontWeight(500).SingleLine()
			if sel {
				t.TextColor(pal().TextAccent)
			}
			first, second := candidateLines(cd)
			if first != "" {
				ui.Text(c, first).FontSize(13).TextColor(pal().TextSecondary).SingleLine()
			}
			ui.Text(c, second).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		})
	})
}

// fetchComparison is the table of fields: what the book has, what the
// candidate has, and whether to use it.
func (a *App) fetchComparison(c *ui.Context, b *calibre.Book, cd candidate) {
	f := &a.fetch
	line := func(fn func()) ui.Element {
		return ui.Row(c).Gap(theme.Space16).Padding(theme.Space8, 0).AlignItems(ui.Start).
			BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle).Children(fn)
	}
	line(func() {
		widgets.Overline(c, locales.FetchMetaFieldHeader()).Width(128)
		widgets.Overline(c, locales.FetchMetaCurrentHeader()).Grow(1).Basis(0)
		widgets.Overline(c, locales.FetchMetaFromHeader(strings.ToUpper(cd.plugin.Manifest.Name))).Grow(1).Basis(0)
	})
	for _, fd := range a.fetchFields(cd) {
		on := f.use[fd.key] && fd.changes
		row := line(func() {
			ui.Row(c).Width(128).Children(func() {
				v := on
				box := ui.Checkbox(c, &v, fd.label).Disabled(!fd.changes)
				if !fd.changes {
					box.Opacity(0.5)
				}
				if box.Changed() {
					f.use[fd.key] = v
				}
			})
			if fd.key == "cover" {
				a.coverCell(c, b, nil, on, true)
				var rc *remoteCover
				if cd.r.Cover != "" {
					rc = a.fetchCover(cd)
				}
				a.coverCell(c, nil, rc, false, false)
				return
			}
			cur := ui.Text(c, orDash(fd.current)).FontSize(13).MaxLines(2).Grow(1).Basis(0).MinWidth(0)
			switch {
			case on && fd.key == "identifiers":
				cur.TextColor(pal().TextSecondary) // kept: the new ones are added
			case on:
				cur.TextColor(pal().TextTertiary).Strikethrough()
			case fd.current == "":
				cur.TextColor(pal().TextDisabled)
			default:
				cur.TextColor(pal().TextSecondary)
			}
			next := fd.next
			switch {
			case next == "":
				next = "—"
			case !fd.changes:
				next = locales.FetchMetaSame()
			case fd.key == "identifiers":
				next = "+ " + strings.ReplaceAll(next, ", ", "\n+ ")
			}
			nt := ui.Text(c, next).FontSize(13).MaxLines(2).Grow(1).Basis(0).MinWidth(0)
			if fd.key == "identifiers" && fd.changes {
				nt.Font(theme.FontMono).FontSize(12).MaxLines(4)
			}
			if fd.changes {
				nt.TextColor(pal().Text)
			} else {
				nt.TextColor(pal().TextDisabled)
			}
		})
		if fd.key == "cover" {
			row.AlignItems(ui.Center)
		}
	}
}

// coverCell is a side of the Cover row: the book's cover, or the
// candidate's, at 40×60 with its size.
func (a *App) coverCell(c *ui.Context, b *calibre.Book, rc *remoteCover, replaced, current bool) {
	ui.Row(c).Grow(1).Basis(0).MinWidth(0).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		var size string
		switch {
		case current:
			var el ui.Element
			if a.edit.coverBmp != nil {
				el = ui.Image(c, a.edit.coverBmp).Size(40, 60).Fit(ui.Cover).Radius(2, 4, 4, 2)
			} else {
				el = a.coverView(c, b, 40, 60, true)
			}
			if replaced {
				el.Opacity(0.5)
			}
		case rc == nil:
			ui.Text(c, "—").FontSize(13).TextColor(pal().TextDisabled)
			return
		case rc.loading:
			ui.Box(c).Size(40, 60).Radius(2, 4, 4, 2).Background(pal().Subtle).Center().Children(func() {
				ui.Spinner(c).Size(12, 12).Label(locales.FetchMetaLoadingCover())
			})
		case rc.bmp == nil:
			ui.Text(c, locales.FetchMetaCoverFailed()).FontSize(12).TextColor(pal().TextDisabled)
			return
		default:
			coverOf(c, &calibre.Book{}, rc.bmp, 40, 60, true)
			size = fmt.Sprintf("%d × %d", rc.size.X, rc.size.Y)
		}
		if size != "" {
			ui.Text(c, size).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
		}
	})
}

// fetchEmpty is a state of the candidates column: an icon, a title, a
// line, and maybe a button.
func fetchEmpty(c *ui.Context, icon *ui.SVG, danger bool, title, body string, btnIcon *ui.SVG, btn string, onClick func()) {
	ui.Column(c).Grow(1).Center().Gap(theme.Space8).Padding(theme.Space24).Children(func() {
		ic := ui.Icon(c, icon).FontSize(20).TextColor(pal().IconMuted)
		if danger {
			ic.TextColor(pal().Danger.FG)
		}
		ui.Text(c, title).FontSize(13).FontWeight(500).TextAlign(ui.Center)
		ui.Text(c, body).FontSize(12).TextColor(pal().TextSecondary).TextAlign(ui.Center).MaxWidth(220)
		if btn != "" && widgets.ButtonSized(c, widgets.Secondary, btnIcon, btn, theme.ControlS).Clicked() {
			onClick()
		}
	})
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func anyTrue(m map[string]bool) bool {
	for _, v := range m {
		if v {
			return true
		}
	}
	return false
}
