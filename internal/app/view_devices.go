package app

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
	"dalibre/internal/device"
)

// deviceSubtitle is a reader's line in the switcher: its books and free
// space.
func (a *App) deviceSubtitle(d device.Device) string {
	n := len(d.Books())
	s := locales.CommonBookCount(n)
	if v := a.dev.view; v != nil && v.dev == d && v.total > 0 {
		s += " · " + locales.DeviceFree(humanSize(v.free))
	}
	return s
}

// deviceSwitcherItems lists the readers plugged in, in the switcher's
// menu: choosing one shows its library.
func (a *App) deviceSwitcherItems(m *ui.Menu) {
	devs := a.devices()
	if len(devs) == 0 {
		return
	}
	m.Separator()
	cur := a.deviceView()
	for _, d := range devs {
		info := d.Info()
		sub := info.Model
		if sub == "" || sub == info.Name {
			sub = a.deviceSubtitle(d)
		} else {
			sub += " · " + a.deviceSubtitle(d)
		}
		if m.Item(info.Name).ID("device:" + info.ID).Icon(icons.Menu(icons.Tablet)).Subtitle(sub).Checked(cur != nil && cur.dev == d).Chosen() {
			a.openDevice(d)
		}
	}
}

// deviceNav is the sidebar of a reader's page: all its books, and its
// formats.
func (a *App) deviceNav(c *ui.Context, v *deviceView) {
	ui.Column(c).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		a.navItem(c, "device:all", icons.Library, locales.MainNavAllBooks(), v.counts.All, v.query.Filter.Kind == catalog.NoFilter, nil, func() {
			v.query.Filter, v.curID = catalog.Filter{}, 0
			v.chosen.Clear()
		})
	})
	if len(v.counts.Formats) == 0 {
		return
	}
	key := "device:formats"
	if _, ok := a.sectionsOn[key]; !ok {
		a.sectionsOn[key] = true
	}
	open := a.sectionsOn[key]
	ui.Column(c.Key(key)).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		title := locales.MainNavFormats()
		h := ui.ButtonBase(c).Height(24).Padding(0, theme.Space8).AlignItems(ui.Center).Radius(theme.RadiusXS).Label(title)
		h.OnClick(func() { a.sectionsOn[key] = !a.sectionsOn[key] }).Children(func() {
			widgets.Overline(c, strings.ToUpper(title)).Grow(1)
			chev := icons.ChevronDown
			if !open {
				chev = icons.ChevronRight
			}
			ui.Icon(c, chev).FontSize(12).TextColor(pal().IconMuted)
		})
		if !open {
			return
		}
		for _, it := range v.counts.Formats {
			value := it.Value
			fg := pal().Format(value).FG
			f := catalog.Filter{Kind: catalog.ByFormat, Value: value}
			a.navItem(c, key+":"+value, icons.File, value, it.N, v.query.Filter == f, &fg, func() {
				v.query.Filter, v.curID = f, 0
				v.chosen.Clear()
			})
		}
	})
}

// deviceToolbar heads a reader's page: its name, the list's controls,
// and Eject.
func (a *App) deviceToolbar(c *ui.Context, v *deviceView) {
	right := float32(theme.Space24)
	if !a.detailShown() {
		right += c.TitleBar().Right
	}
	ui.Row(c).Height(theme.ToolbarHeight).Padding(0, right, 0, theme.Space32).Gap(theme.Space8).AlignItems(ui.Center).DragWindow().Children(func() {
		w := a.mainBox.Bounds(c).W
		noTitle := w > 0 && w < 640
		ui.Row(c).Grow(1).MinWidth(0).Gap(theme.Space8).AlignItems(ui.Baseline).Children(func() {
			if noTitle {
				return
			}
			title := v.info.Name
			if f := v.query.Filter; f.Kind != catalog.NoFilter {
				title = f.Value
			}
			ui.Text(c, title).FontWeight(600).FontSize(22).LetterSpacing(-0.4).SingleLine().Shrink(1)
			ui.Text(c, groupDigits(len(v.visible()))).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
		})
		a.queryControls(c, &v.query, func() {}, func() { v.curID = 0 })
		if widgets.IconButton(c, icons.PanelRight, locales.MainDetailsTooltip(), !a.settings.HideDetail).Clicked() {
			a.settings.HideDetail = !a.settings.HideDetail
			a.saveSettings()
		}
		ui.Box(c).Size(1, 20).Background(pal().Border).Margin(0, theme.Space4)
		busy := a.dev.busy != "" || a.busy != ""
		if widgets.Button(c, widgets.Secondary, icons.Eject, locales.DeviceEject()).Tooltip(locales.DeviceEjectTooltip(v.info.Name)).Disabled(busy).Clicked() {
			a.ejectReader(v.dev)
		}
	})
}

// deviceShelf is a reader's books, for the grid and the list.
func (a *App) deviceShelf(v *deviceView) shelf {
	return shelf{
		grid: &v.grid, table: &v.table, tableSort: &v.tableSort, chosen: &v.chosen, cur: &v.cur, query: &v.query,
		menu:  func(b *calibre.Book) func(*ui.Menu) { return a.deviceBookMenu(v, b) },
		cover: func(b *calibre.Book, h int) *ui.Bitmap { return a.deviceCover(v, b, h) },
		open:  func(b *calibre.Book) { a.openReaderBook(v, b) },
	}
}

// deviceContent is a reader's books.
func (a *App) deviceContent(c *ui.Context, v *deviceView) {
	shown := v.visible()
	if len(shown) == 0 {
		ui.Column(c).Grow(1).Center().Gap(theme.Space12).Padding(theme.Space48).Children(func() {
			ui.Box(c).Size(56, 56).Radius(theme.RadiusFull).Background(pal().Subtle).Center().Children(func() {
				ui.Icon(c, icons.Tablet).FontSize(24).TextColor(pal().IconMuted)
			})
			if len(v.books) == 0 {
				ui.Text(c, locales.DeviceEmptyTitle(v.info.Name)).FontWeight(600).FontSize(18)
				ui.Text(c, locales.DeviceEmptyHint()).TextColor(pal().TextSecondary).TextAlign(ui.Center)
				return
			}
			ui.Text(c, locales.MainNoMatchTitle()).FontWeight(600).FontSize(18)
			ui.Text(c, locales.MainNoMatchHint()).TextColor(pal().TextSecondary)
			if widgets.Button(c, widgets.Secondary, nil, locales.MainClearFilters()).Margin(theme.Space8, 0, 0, 0).Clicked() {
				v.query.Search, v.query.Filter = "", catalog.Filter{}
			}
		})
		return
	}
	// The row the grid or list moved to is the book the detail shows.
	v.cur = slices.IndexFunc(shown, func(b *calibre.Book) bool { return b.ID == v.curID })
	if a.settings.ListView {
		a.listView(c, a.deviceShelf(v), shown)
	} else {
		a.gridView(c, a.deviceShelf(v), shown)
	}
	if v.cur >= 0 && v.cur < len(shown) {
		v.curID = shown[v.cur].ID
	} else {
		v.curID = 0
	}
}

// deviceBookMenu is the context menu of a reader's book.
func (a *App) deviceBookMenu(v *deviceView, b *calibre.Book) func(m *ui.Menu) {
	return func(m *ui.Menu) {
		ids := v.selectedIDs()
		if !containsID(ids, b.ID) {
			ids = []int64{b.ID}
		}
		many := len(ids) > 1
		busy := a.dev.busy != "" || a.busy != ""
		if !many {
			if v.dev.Storage().LocalPath(b.Path) != "" && m.Item(locales.CommonOpen()).Icon(icons.Menu(icons.BookOpen)).Trailing("Enter").Chosen() {
				a.openReaderBook(v, b)
			}
			if lid, ok := v.inLib[b.ID]; ok && a.byID[lid] != nil {
				if m.Item(locales.DeviceShowInLibrary()).Icon(icons.Menu(icons.Library)).Chosen() {
					a.showInLibrary(lid)
				}
			}
		}
		var notIn []int64
		for _, id := range ids {
			if _, ok := v.inLib[id]; !ok {
				notIn = append(notIn, id)
			}
		}
		if len(notIn) > 0 && a.lib != nil {
			if m.Item(locales.DeviceAddToLibrary()).Icon(icons.Menu(icons.Download)).Disabled(busy).Chosen() {
				a.addReaderBooksToLibrary(v, notIn)
			}
		}
		if !many {
			if p := v.dev.Storage().LocalPath(b.Path); p != "" && m.Item(locales.CommonShowInFolder()).Icon(icons.Menu(icons.Folder)).Chosen() {
				revealPath(p)
			}
		}
		m.Separator()
		label := locales.WirelessRemoveFrom(v.info.Name)
		if m.Item(label).Icon(icons.Menu(icons.Trash)).Danger().Trailing("Del").Disabled(busy).Chosen() {
			a.askRemoveFromReader(v, ids)
		}
	}
}

// openReaderBook opens a reader's book in the system's app for it.
func (a *App) openReaderBook(v *deviceView, b *calibre.Book) {
	if b == nil {
		return
	}
	if p := v.dev.Storage().LocalPath(b.Path); p != "" {
		a.run(func() {
			if err := mygo.Shell.OpenPath(p); err != nil {
				a.post(func() { a.toastError(err) })
			}
		})
	}
}

func (a *App) askRemoveFromReader(v *deviceView, ids []int64) {
	if len(ids) == 0 {
		return
	}
	v.deleteIDs, v.deleteOpen = ids, true
}

// deviceDialogs asks before removing books from a reader.
func (a *App) deviceDialogs(c *ui.Context) {
	if a.dev == nil || a.dev.view == nil {
		return
	}
	v := a.dev.view
	if !v.deleteOpen {
		return
	}
	title := locales.DeviceRemoveBooksTitle(len(v.deleteIDs), v.info.Name)
	if len(v.deleteIDs) == 1 {
		if b := v.byID[v.deleteIDs[0]]; b != nil {
			title = locales.DeviceRemoveBookTitle(b.Title, v.info.Name)
		}
	}
	if ui.AlertDialog(c, &v.deleteOpen, title, locales.DeviceRemoveBody(), locales.CommonCancel(), locales.CommonRemove()) == 1 {
		var lpaths []string
		for _, id := range v.deleteIDs {
			if b := v.byID[id]; b != nil {
				lpaths = append(lpaths, b.Path)
			}
		}
		v.chosen.Clear()
		v.curID = 0
		a.removeFromReader(v.dev, lpaths)
	}
}

// deviceDetail is the detail panel of a reader's page: the book chosen,
// or the books chosen.
func (a *App) deviceDetail(c *ui.Context, v *deviceView) {
	ids := v.selectedIDs()
	busy := a.dev.busy != "" || a.busy != ""
	if len(ids) > 1 {
		ui.Column(c).Grow(1).Center().Gap(theme.Space16).Padding(theme.Space32).Children(func() {
			ui.Box(c).Size(140, 120).Children(func() {
				for i, id := range ids[:min(3, len(ids))] {
					if b := v.byID[id]; b != nil {
						ui.Box(c.Key(id)).Absolute().Left(float32(i) * 28).Top(float32(i) * 8).Children(func() {
							coverOf(c, b, a.deviceCover(v, b, 192), 64, 96, false)
						})
					}
				}
			})
			ui.Text(c, locales.DetailBooksSelected(len(ids))).FontWeight(600).FontSize(18)
			ui.Column(c).Gap(theme.Space8).Width(220).AlignItems(ui.Stretch).Children(func() {
				var notIn []int64
				for _, id := range ids {
					if _, ok := v.inLib[id]; !ok {
						notIn = append(notIn, id)
					}
				}
				if len(notIn) > 0 && a.lib != nil {
					if widgets.Button(c, widgets.Secondary, icons.Download, locales.DeviceAddToLibrary()).Disabled(busy).Clicked() {
						a.addReaderBooksToLibrary(v, notIn)
					}
				}
				if widgets.Button(c, widgets.Secondary, icons.Trash, locales.WirelessRemoveFrom(v.info.Name)).Disabled(busy).Clicked() {
					a.askRemoveFromReader(v, ids)
				}
			})
		})
		return
	}
	b := v.current()
	if b == nil {
		return
	}
	it := v.items[b.ID]
	ui.Scroll(c).Grow(1).Padding(theme.Space24).Gap(theme.Space24).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Gap(theme.Space20).AlignItems(ui.Center).Children(func() {
			ui.Row(c).FillWidth().Justify(ui.Center).Padding(theme.Space24, 0).Radius(theme.RadiusLG).Background(pal().Subtle).Children(func() {
				coverOf(c, b, a.deviceCover(v, b, 456), 152, 228, false)
			})
			ui.Column(c).FillWidth().Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, b.Title).FontWeight(600).FontSize(20).FixedLineHeight(26).LetterSpacing(-0.3).TextAlign(ui.Center).Selectable()
				ui.Text(c, strings.Join(b.Authors, " · ")).FontSize(14).TextColor(pal().TextSecondary).TextAlign(ui.Center)
				if b.Rating > 0 {
					widgets.Stars(c, b.Rating, 13)
				}
			})
			lid, inLib := v.inLib[b.ID]
			ui.Row(c).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
				ic, label, col := icons.Check, locales.DeviceInLibrary(), pal().TextAccent
				if !inLib {
					ic, label, col = icons.X, locales.DeviceNotInLibrary(), pal().TextTertiary
				}
				ui.Icon(c, ic).FontSize(13).TextColor(col)
				ui.Text(c, label).FontSize(12).TextColor(col)
			})
			ui.Column(c).FillWidth().Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
				if inLib && a.byID[lid] != nil {
					if widgets.Button(c.Key("show"), widgets.Secondary, icons.Library, locales.DeviceShowInLibrary()).FillWidth().Clicked() {
						a.showInLibrary(lid)
					}
				} else if a.lib != nil {
					if widgets.Button(c.Key("add"), widgets.Primary, icons.Download, locales.DeviceAddToLibrary()).FillWidth().Disabled(busy).Clicked() {
						a.addReaderBooksToLibrary(v, []int64{b.ID})
					}
				}
			})
		})
		widgets.Hairline(c)
		ui.Column(c).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
			widgets.Overline(c, locales.DetailOverlineDetails())
			row := func(k, label, val string, mono bool) {
				if val == "" {
					return
				}
				ui.Row(c.Key(k)).Gap(theme.Space12).AlignItems(ui.Start).Children(func() {
					ui.Text(c, label).FontSize(13).TextColor(pal().TextTertiary).Width(84)
					t := ui.Text(c, val).FontSize(13).Grow(1).Selectable()
					if mono {
						t.Font(theme.FontMono).FontSize(12)
					}
				})
			}
			if b.Series != "" {
				row("Series", locales.CommonSeries(), fmt.Sprintf("%s · %s", b.Series, seriesIndex(b.SeriesIndex)), false)
			}
			row("Publisher", locales.CommonPublisher(), b.Publisher, false)
			if !b.Pubdate.IsZero() {
				row("Published", locales.CommonPublished(), b.Pubdate.Format("2006-01-02"), true)
			}
			langs := make([]string, len(b.Languages))
			for i, l := range b.Languages {
				langs[i] = languageName(l)
			}
			row("Language", locales.CommonLanguage(), strings.Join(langs, ", "), false)
			if !b.Timestamp.IsZero() {
				row("Added", locales.DetailAdded(), b.Timestamp.Local().Format("2006-01-02 15:04"), true)
			}
			row("File", locales.DeviceFile(), it.LPath, true)
			if len(b.Tags) > 0 {
				ui.Row(c).Gap(theme.Space6).Wrap().Padding(theme.Space4, 0, 0, 0).Children(func() {
					for _, tg := range b.Tags {
						widgets.Tag(c.Key(tg), tg, false, nil)
					}
				})
			}
		})
		widgets.Hairline(c)
		ui.Column(c).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
			widgets.Overline(c, locales.DetailOverlineFormats()).Margin(0, 0, theme.Space6, 0)
			r := ui.ButtonBase(c.Key("file")).Padding(theme.Space8).Gap(theme.Space10).AlignItems(ui.Center).Radius(theme.RadiusSM).Border(1, pal().BorderSubtle).Label(locales.DetailOpenFormat(it.Format()))
			if r.Hovered() {
				r.Background(pal().Hover)
			}
			r.OnClick(func() { a.openReaderBook(v, b) }).Children(func() {
				widgets.FormatBadge(c, it.Format(), false)
				ui.Text(c, path.Base(it.LPath)).FontSize(12).TextColor(pal().TextSecondary).Grow(1).SingleLine()
				ui.Text(c, humanSize(it.Size)).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
			})
		})
		if strings.TrimSpace(it.Comments) != "" {
			widgets.Hairline(c)
			ui.Column(c).Gap(theme.Space8).AlignItems(ui.Stretch).Children(func() {
				widgets.Overline(c, locales.DetailOverlineDescription())
				ui.Text(c, htmlToText(it.Comments)).FontSize(13).LineHeight(1.55).TextColor(pal().TextSecondary).Selectable()
			})
		}
		widgets.Hairline(c)
		if widgets.Button(c.Key("remove"), widgets.Secondary, icons.Trash, locales.WirelessRemoveFrom(v.info.Name)).FillWidth().Disabled(busy).Clicked() {
			a.askRemoveFromReader(v, []int64{b.ID})
		}
	})
}

// readerMenuItems are the library's book menu's items for the readers
// plugged in: Send to, and Remove from those that have the books. It
// reports whether it added any.
func (a *App) readerMenuItems(m *ui.Menu, ids []int64) bool {
	devs := a.devices()
	busy := a.dev != nil && a.dev.busy != "" || a.busy != ""
	for _, d := range devs {
		name := d.Info().Name
		if m.Item(locales.WirelessSendTo(name)).Icon(icons.Menu(icons.Tablet)).Disabled(busy).Chosen() {
			a.sendToReader(d, ids)
		}
		if on := a.readerBooksOf(d, ids); len(on) > 0 {
			if m.Item(locales.WirelessRemoveFrom(name)).Icon(icons.Menu(icons.Trash)).Disabled(busy).Chosen() {
				a.removeFromReader(d, on)
			}
		}
	}
	return len(devs) > 0
}

// deviceDropOverlay says where dropped files go on a reader's page.
func (a *App) deviceDropOverlay(c *ui.Context, v *deviceView) {
	ui.Box(c).Absolute().Top(theme.Space12).Left(theme.Space12).Right(theme.Space12).Bottom(theme.Space12).PassThrough().
		Radius(theme.RadiusXL).Border(2, pal().Accent).BorderStyle(ui.BorderDashed).Background(pal().Accent.Alpha(0.06)).Center().Children(func() {
		ui.Column(c).Width(400).Center().Gap(theme.Space12).Padding(theme.Space24, theme.Space32).Radius(theme.RadiusLG).
			Background(pal().SurfaceRaised).Border(1, pal().BorderSubtle).Shadow(0, 2, 6, 0, ui.RGBA(26, 23, 18, 0.08)).Shadow(0, 24, 48, -8, ui.RGBA(26, 23, 18, 0.18)).Children(func() {
			ui.Box(c).Size(48, 48).Radius(theme.RadiusFull).Background(pal().AccentSubtle).Center().Children(func() {
				ui.Icon(c, icons.Tablet).FontSize(22).TextColor(pal().TextAccent)
			})
			ui.Text(c, locales.DeviceDropTitle(v.info.Name)).FontSize(18).FontWeight(600)
			ui.Text(c, locales.DeviceDropHint()).FontSize(12).TextColor(pal().TextTertiary).TextAlign(ui.Center)
		})
	})
}
