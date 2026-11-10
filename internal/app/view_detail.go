package app

import (
	"fmt"
	"html"
	"regexp"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
)

// detailShown reports whether the detail panel shows: when the user keeps
// it on and something is selected to show in it. With nothing selected
// it is away, not an empty panel.
func (a *App) detailShown() bool {
	if a.settings.HideDetail {
		return false
	}
	if a.page != pageLibrary {
		return a.gb.selected != nil && a.pluginByID(a.gb.selected.source) != nil
	}
	return a.lib != nil && len(a.selectedIDs()) > 0
}

func (a *App) detailPanel(c *ui.Context) {
	a.detailPanelOf(c, a.page, a.current(), a.selectedIDs())
}

func (a *App) detailPanelOf(c *ui.Context, pg page, b *calibre.Book, ids []int64) {
	bar := c.TitleBar()
	ui.Column(c).Width(theme.DetailWidth).Shrink(0).Background(pal().Surface).BorderWidth(0, 0, 0, 1).BorderColor(pal().BorderSubtle).
		AlignItems(ui.Stretch).Children(func() {
		if bar.Right > 0 {
			// Room for the caption buttons over the panel, to drag by.
			ui.Box(c).Height(bar.Height).DragWindow()
		}
		if pg != pageLibrary {
			a.resultDetail(c)
			return
		}
		switch {
		case len(ids) > 1:
			a.multiDetail(c, ids)
		case b != nil:
			ui.Scroll(c).Grow(1).Padding(theme.Space24).Gap(theme.Space24).AlignItems(ui.Stretch).Children(func() { a.bookDetail(c, b) })
		default:
			ui.Column(c).Grow(1).Center().Gap(theme.Space8).Padding(theme.Space32).Children(func() {
				ui.Icon(c, icons.BookOpen).FontSize(28).TextColor(pal().IconMuted)
				ui.Text(c, locales.DetailSelectBook()).TextColor(pal().TextTertiary).TextAlign(ui.Center)
			})
		}
	})
}

func (a *App) multiDetail(c *ui.Context, ids []int64) {
	ui.Column(c).Grow(1).Center().Gap(theme.Space16).Padding(theme.Space32).Children(func() {
		ui.Box(c).Size(140, 120).Children(func() {
			for i, id := range ids[:min(3, len(ids))] {
				if b := a.byID[id]; b != nil {
					ui.Box(c.Key(id)).Absolute().Left(float32(i) * 28).Top(float32(i) * 8).Children(func() {
						a.coverView(c, b, 64, 96, false)
					})
				}
			}
		})
		ui.Text(c, locales.DetailBooksSelected(len(ids))).FontWeight(600).FontSize(18)
		ui.Column(c).Gap(theme.Space8).Width(220).AlignItems(ui.Stretch).Children(func() {
			a.selectionActions(c, ids)
			if widgets.Button(c, widgets.Secondary, icons.Trash, locales.CommonRemoveFromLibrary()).Clicked() {
				a.askDelete(ids)
			}
		})
	})
}

func (a *App) bookDetail(c *ui.Context, b *calibre.Book) {
	d := a.detailsOf(b)
	// Cover, title, actions.
	ui.Column(c).Gap(theme.Space20).AlignItems(ui.Center).Children(func() {
		ui.Row(c).FillWidth().Justify(ui.Center).Padding(theme.Space24, 0).Radius(theme.RadiusLG).Background(pal().Subtle).Children(func() {
			a.coverView(c, b, 152, 228, false)
			// The book's actions, in the cover's corner.
			ui.Box(c).Absolute().Top(theme.Space8).Right(theme.Space8).Children(func() {
				// Raised off the cover's backdrop so it reads as a button.
				more := widgets.IconButton(c, icons.More, locales.DetailMore(), false).Border(1, pal().Border).
					Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.08))
				if !more.Hovered() {
					more.Background(pal().SurfaceRaised)
				}
				more.PopoverMenuButton(func(m *ui.Menu) {
					if m.Item(locales.DetailMenuEditMetadata()).Icon(icons.Menu(icons.Pencil)).Trailing("Ctrl+I").Chosen() {
						a.openEditor(b)
					}
					if editorFormat(b) != "" && m.Item(locales.EditorMenuEditBook()).Icon(icons.Menu(icons.File)).Trailing("Ctrl+E").Chosen() {
						a.editBook(b, "")
					}
					a.pluginActions(m, []int64{b.ID})
					if m.Item(locales.DetailMenuAddFormat()).Icon(icons.Menu(icons.Plus)).Chosen() {
						a.addFormat(b.ID)
					}
					a.shareMenu(m, b)
					if m.Item(locales.CommonShowInFolder()).Icon(icons.Menu(icons.Folder)).Chosen() {
						a.revealBook(b)
					}
					m.Separator()
					if m.Item(locales.CommonRemoveFromLibrary()).Icon(icons.Menu(icons.Trash)).Danger().Chosen() {
						a.askDelete([]int64{b.ID})
					}
				})
			})
		})
		ui.Column(c).FillWidth().Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, b.Title).FontWeight(600).FontSize(20).FixedLineHeight(26).LetterSpacing(-0.3).TextAlign(ui.Center).Selectable()
			ui.Row(c).Gap(theme.Space4).Wrap().Justify(ui.Center).Children(func() {
				for i, au := range b.Authors {
					l := ui.ButtonBase(c.Key(au)).Label(locales.DetailBooksBy(au))
					l.OnClick(func() { a.setQuery(catalog.AllBooks, catalog.Filter{Kind: catalog.ByAuthor, Value: au}) }).Children(func() {
						t := ui.Text(c, au).FontSize(14).TextColor(pal().TextAccent)
						if l.Hovered() {
							t.Underline()
						}
					})
					if i < len(b.Authors)-1 {
						ui.Text(c, "·").TextColor(pal().TextTertiary)
					}
				}
			})
			if b.Rating > 0 {
				widgets.Stars(c, b.Rating, 13)
			}
		})
	})
	widgets.Hairline(c)
	// Details.
	ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Children(func() {
			widgets.Overline(c, locales.DetailOverlineDetails()).Grow(1)
			link := func(key, label, text string, onClick func()) {
				e := ui.ButtonBase(c.Key(key)).Padding(theme.Space2, theme.Space6).Radius(theme.RadiusXS).Label(label)
				if e.Hovered() {
					e.Background(pal().Hover)
				}
				e.OnClick(onClick).Children(func() {
					ui.Text(c, text).FontSize(12).FontWeight(500).TextColor(pal().TextAccent)
				})
			}
			// With a metadata plugin on, the book can be looked up from
			// here: the editor opens on its Fetch metadata step.
			if len(a.metadataSources()) > 0 {
				link("Look up metadata online", locales.DetailLookUpMetadataOnline(), locales.DetailLookUpOnline(), func() {
					a.openEditor(b)
					if a.editOpen {
						a.openFetch()
					}
				})
			}
			link("Edit metadata", locales.DetailEditMetadataLabel(), locales.DetailEdit(), func() { a.openEditor(b) })
		})
		row := func(k, label, v string, mono bool, onClick func()) {
			if v == "" {
				return
			}
			ui.Row(c.Key(k)).Gap(theme.Space12).AlignItems(ui.Start).Children(func() {
				ui.Text(c, label).FontSize(13).TextColor(pal().TextTertiary).Width(84)
				t := ui.Text(c, v).FontSize(13).Grow(1).Selectable()
				if mono {
					t.Font(theme.FontMono).FontSize(12)
				}
				if onClick != nil {
					t.TextColor(pal().TextAccent).Cursor(ui.CursorPointer).OnClick(onClick)
				}
			})
		}
		if b.Series != "" {
			row("Series", locales.CommonSeries(), fmt.Sprintf("%s · %s", b.Series, seriesIndex(b.SeriesIndex)), false, func() {
				a.setQuery(catalog.AllBooks, catalog.Filter{Kind: catalog.BySeries, Value: b.Series})
			})
		}
		row("Publisher", locales.CommonPublisher(), b.Publisher, false, nil)
		if !b.Pubdate.IsZero() {
			row("Published", locales.CommonPublished(), b.Pubdate.Format("2006-01-02"), true, nil)
		}
		langs := make([]string, len(b.Languages))
		for i, l := range b.Languages {
			langs[i] = languageName(l)
		}
		row("Language", locales.CommonLanguage(), strings.Join(langs, ", "), false, nil)
		if d != nil {
			keys := make([]string, 0, len(d.Identifiers))
			for k := range d.Identifiers {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				label := strings.ToUpper(k)
				if k != "isbn" {
					label = strings.ToUpper(k[:1]) + k[1:]
				}
				row(label, label, d.Identifiers[k], true, nil)
			}
		}
		row("Added", locales.DetailAdded(), b.Timestamp.Local().Format("2006-01-02 15:04"), true, nil)
		if len(b.Tags) > 0 {
			ui.Row(c).Gap(theme.Space6).Wrap().Padding(theme.Space4, 0, 0, 0).Children(func() {
				for _, tg := range b.Tags {
					if widgets.Tag(c.Key(tg), tg, false, nil).Clicked() {
						a.setQuery(catalog.AllBooks, catalog.Filter{Kind: catalog.ByTag, Value: tg})
					}
				}
			})
		}
	})
	widgets.Hairline(c)
	// Formats on disk.
	ui.Column(c).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
		widgets.Overline(c, locales.DetailOverlineFormats()).Margin(0, 0, theme.Space6, 0)
		if d == nil {
			return
		}
		if len(d.Files) == 0 {
			ui.Text(c, locales.DetailNoFiles()).TextColor(pal().TextTertiary).FontSize(13)
		}
		for _, f := range d.Files {
			r := ui.ButtonBase(c.Key(f.Format)).Padding(theme.Space8).Gap(theme.Space10).AlignItems(ui.Center).Radius(theme.RadiusSM).Border(1, pal().BorderSubtle).Label(locales.DetailOpenFormat(f.Format))
			if r.Hovered() {
				r.Background(pal().Hover)
			}
			r.OnClick(func() { a.openBook(b, f.Format) })
			editable := isEditorFormat(f.Format)
			r.ContextMenu(func(m *ui.Menu) {
				if m.Item(locales.CommonOpen()).Chosen() {
					a.openBook(b, f.Format)
				}
				if editable && m.Item(locales.DetailEdit()).Chosen() {
					a.editBook(b, f.Format)
				}
				if shareSupported && m.Item(locales.DetailShare()).Chosen() {
					shareFiles(a.win, []string{f.Path})
				}
				if m.Item(locales.CommonShowInFolder()).Chosen() {
					revealPath(f.Path)
				}
				m.Separator()
				if m.Item(locales.DetailRemoveFormat(f.Format)).Danger().Chosen() {
					a.removeFormat(b.ID, f.Format)
				}
			})
			r.Children(func() {
				widgets.FormatBadge(c, f.Format, false)
				ui.Text(c, f.Name+"."+strings.ToLower(f.Format)).FontSize(12).TextColor(pal().TextSecondary).Grow(1).SingleLine()
				// An editable file offers Edit in place of its size while the
				// pointer is over its row, in a slot of the badge's height so
				// the row keeps its size.
				ui.Row(c).Height(20).AlignItems(ui.Center).Children(func() {
					if editable && r.Hovered() {
						// It sits on the row's hover, so its own hover raises it
						// rather than darkening.
						e := ui.ButtonBase(c.Key("edit")).Height(20).Padding(0, theme.Space6).Margin(0, -theme.Space6, 0, 0).Gap(theme.Space4).
							AlignItems(ui.Center).Radius(theme.RadiusXS).Label(locales.DetailEdit())
						if e.Hovered() {
							e.Background(pal().SurfaceRaised).Shadow(0, 1, 2, 0, ui.RGBA(26, 23, 18, 0.08)).Shadow(0, 0, 0, 1, pal().Border.Alpha(0.7))
						}
						if e.Pressed() {
							e.Opacity(0.88)
						}
						e.OnClick(func() { a.editBook(b, f.Format) }).Children(func() {
							ui.Icon(c, icons.Pencil).FontSize(13)
							ui.Text(c, locales.DetailEdit()).FontSize(12).FontWeight(500)
						})
					} else {
						ui.Text(c, humanSize(f.Size)).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
					}
				})
			})
		}
		add := ui.ButtonBase(c).Padding(theme.Space6, theme.Space8).Gap(theme.Space6).AlignItems(ui.Center).Radius(theme.RadiusSM).Label(locales.DetailAddFormat())
		if add.Hovered() {
			add.Background(pal().Hover)
		}
		add.OnClick(func() { a.addFormat(b.ID) }).Children(func() {
			ui.Icon(c, icons.Plus).FontSize(14).TextColor(pal().TextAccent)
			ui.Text(c, locales.DetailAddFormat()).FontSize(12).FontWeight(500).TextColor(pal().TextAccent)
		})
	})
	if d != nil && strings.TrimSpace(d.Comments) != "" {
		widgets.Hairline(c)
		ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
			widgets.Overline(c, locales.DetailOverlineDescription())
			ui.Text(c, htmlToText(d.Comments)).FontSize(13).LineHeight(1.55).TextColor(pal().TextSecondary).Selectable()
		})
	}
}

var (
	reBlock = regexp.MustCompile(`(?i)</?(p|div|br|li|h[1-6]|blockquote)[^>]*>`)
	reTag   = regexp.MustCompile(`<[^>]*>`)
	reBlank = regexp.MustCompile(`\n{3,}`)
)

// htmlToText turns calibre's HTML comments into paragraphs of text.
func htmlToText(s string) string {
	s = reBlock.ReplaceAllString(s, "\n")
	s = reTag.ReplaceAllString(s, "")
	s = html.UnescapeString(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.Join(strings.Fields(l), " ")
	}
	return strings.TrimSpace(reBlank.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
}
