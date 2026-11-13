package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
)

// The grid is a wall of floating shelves (Figma "01b · Library — Shelf"):
// each cell is a slot of a shelf, its book standing on a thin board under
// which its title sits.
const (
	shelfBayH   = 228 // the tallest cover, lifted, with air above it
	shelfPlankH = 12  // a board: lit top, front face, bottom lip
	shelfLabelH = 76
	// shelfReach is how far a slot's board and shadows reach past its cell,
	// over the cell's border and half the gap, so the boards of a row join.
	shelfReach = 6
	gridCellH  = shelfBayH + shelfPlankH + shelfLabelH + 4 // + the cell's border

	coverMinH, coverMaxH = 160, 210
)

// shelf is a list of books the grid and the list show: the library's, or
// a reader's. It holds where their state is and what a book does.
type shelf struct {
	grid      *ui.GridState
	table     *ui.ListState
	tableSort *ui.SortOrder
	chosen    *ui.Selection[int64]
	cur       *int
	query     *catalog.Query
	// sorted is told when the list's header changed the order.
	sorted func()
	menu   func(b *calibre.Book) func(m *ui.Menu)
	cover  func(b *calibre.Book, height int) *ui.Bitmap
	open   func(b *calibre.Book)
}

// libraryShelf is the library's books.
func (a *App) libraryShelf() shelf {
	return shelf{
		grid: &a.grid, table: &a.table, tableSort: &a.tableSort, chosen: &a.chosen, cur: &a.cur, query: &a.query,
		sorted: a.saveSettings, menu: a.bookMenu, cover: a.cover,
		open: func(*calibre.Book) { a.openBook(a.current(), "") },
	}
}

func (a *App) gridView(c *ui.Context, s shelf, shown []*calibre.Book) {
	s.grid.Key = func(i int) any { return shown[i].ID }
	s.grid.Selection = s.chosen
	s.grid.Selected = s.cur
	s.grid.Label = func(i int) string { return shown[i].Title }
	// The grid tints and outlines its chosen cells; on the shelf a book
	// shows it by being pulled out instead, so its cells stay clear.
	base := c.Theme()
	tt := *base
	tt.Accent, tt.InactiveAccent, tt.SurfaceHover, tt.SurfacePressed, tt.Border = ui.Transparent, ui.Transparent, ui.Transparent, ui.Transparent, ui.Transparent
	tt.Radius = 0
	var g ui.Element
	ui.Themed(c, &tt).Grow(1).MinHeight(0).Background(ui.Transparent).AlignItems(ui.Stretch).Children(func() {
		g = ui.GridView(c, s.grid, len(shown), theme.CardW, gridCellH, func(i int) {
			a.shelfSlot(c, s, base, shown[i], i == len(shown)-1)
		}).Grow(1).Margin(theme.Space8, theme.Space32, 0, theme.Space32).Label(locales.BooksLabel())
	})
	a.gridBounds = g.Bounds()
	if g.Submitted() {
		if i := *s.cur; i >= 0 && i < len(shown) {
			s.open(shown[i])
		}
	}
}

// shelfSlot builds a book standing on its board, with its title below; the
// last one's board runs on to the end of the shelf.
func (a *App) shelfSlot(c *ui.Context, s shelf, base *ui.Theme, b *calibre.Book, last bool) {
	p := pal()
	ink := ui.RGBA(26, 23, 18, 1)
	if p.Dark {
		ink = ui.RGBA(0, 0, 0, 1)
	}
	chosen := s.chosen.Has(b.ID)
	// The menu takes the theme current where ContextMenu is called, so it
	// goes on an element built inside the base theme, not the grid's
	// (whose clear Accent would wash out the highlighted item).
	slot := ui.Themed(c, base).Fill().Background(ui.Transparent).AlignItems(ui.Stretch)
	// Where the slot stands in its row, from where it and the grid stood
	// in the last frame: the shelf's ends fade its shadow out.
	var ends shelfEnds
	ends.right = -shelfReach
	if r, g := slot.Bounds(), a.gridBounds; g.W > 0 && r.W > 0 {
		ends.first = r.X-g.X < theme.CardW/2
		ends.last = last || g.X+g.W-r.X-r.W < theme.CardW/2
		if last {
			// The board runs on to the end of the shelf: the grid's
			// right edge.
			ends.right = min(ends.right, -(g.X + g.W - r.X - r.W))
		}
		ends.width = r.W + shelfReach - ends.right
	}
	slot.Children(func() {
		a.shelfSlotBody(c, s, b, chosen, ends, ink)
	})
}

// shelfEnds is where a slot stands on its shelf: whether it begins or
// ends the row, how far its board reaches past it on the right, and the
// board's width (0 while unknown).
type shelfEnds struct {
	first, last  bool
	right, width float32
}

// shelfShadowFade is how far in from a shelf's ends its shadow fades in.
const shelfShadowFade = 28

func (a *App) shelfSlotBody(c *ui.Context, s shelf, b *calibre.Book, chosen bool, ends shelfEnds, ink ui.Color) {
	right := ends.right
	p := pal()
	slot := ui.Column(c).Grow(1).ContextMenu(s.menu(b))
	var lift float32
	switch {
	case chosen:
		lift = theme.Space12
	case slot.Hovered():
		lift = theme.Space4
	}
	slot.Children(func() {
		ui.Column(c).Height(shelfBayH).Justify(ui.End).AlignItems(ui.Center).Padding(0, 0, lift, 0).Children(func() {
			bmp := s.cover(b, theme.CoverH*2)
			cov := coverOf(c, b, bmp, theme.CoverW, shelfCoverH(bmp), false)
			if lift > 0 {
				cov.Shadow(0, 14, 22, -6, ink.Alpha(0.26))
			}
		})
		ui.Box(c).Height(shelfPlankH).Children(func() {
			ui.Column(c).Absolute().Top(0).Bottom(0).Left(-shelfReach).Right(right).PassThrough().Children(func() {
				ui.Box(c).Height(3).Background(p.PlankTop)
				ui.Box(c).Grow(1).Background(p.PlankFace)
				ui.Box(c).Height(2).Background(p.PlankEdge)
			})
			if chosen {
				ui.Row(c).Absolute().Top(5).Left(0).Right(0).Justify(ui.Center).PassThrough().Children(func() {
					ui.Box(c).Size(24, 2).Radius(1).Background(p.Accent)
				})
			}
		})
		ui.Column(c).Grow(1).Padding(theme.Space16, 0, 0, 0).AlignItems(ui.Center).Children(func() {
			// The soft shadow the board casts on the wall below it, faded
			// out at the shelf's ends rather than cut off in a line.
			shadow := ui.Box(c).Absolute().Top(0).Height(18).Left(-shelfReach).Right(right).PassThrough().LinearGradient(ui.LinearGradient{
				Angle: 180, Stops: []ui.GradientStop{{Color: ink.Alpha(0.10), At: 0}, {Color: ink.Alpha(0.03), At: 0.4}, {Color: ink.Alpha(0), At: 1}},
			})
			if ends.width > 0 && (ends.first || ends.last) {
				f := min(shelfShadowFade/ends.width, 0.5)
				black := ui.RGB(0, 0, 0)
				stops := []ui.GradientStop{{Color: black, At: 0}, {Color: black, At: 1}}
				if ends.first {
					stops = append([]ui.GradientStop{{Color: ui.Transparent, At: 0}, {Color: black, At: f}}, stops[1:]...)
				}
				if ends.last {
					stops = append(stops[:len(stops)-1], ui.GradientStop{Color: black, At: 1 - f}, ui.GradientStop{Color: ui.Transparent, At: 1})
				}
				shadow.MaskLinearGradient(ui.LinearGradient{Angle: 90, Stops: stops})
			}
			// As wide as the cover, under it.
			ui.Column(c).Width(theme.CoverW).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
				title := ui.Text(c, b.Title).FontWeight(500).FontSize(13).FixedLineHeight(18).SingleLine()
				if chosen {
					title.TextColor(p.TextAccent)
				}
				ui.Text(c, strings.Join(b.Authors, ", ")).FontSize(12).TextColor(p.TextTertiary).SingleLine()
			})
		})
	})
}

// shelfCoverH is how high a cover stands at the shelf's width: as its
// image is, within bounds, so books differ in height as real ones do.
func shelfCoverH(bmp *ui.Bitmap) float32 {
	if bmp == nil {
		return theme.CoverH
	}
	w, h := bmp.Size()
	if w <= 0 {
		return theme.CoverH
	}
	return min(max(theme.CoverW*float32(h)/float32(w), coverMinH), coverMaxH)
}

// tableColumns are the list view's columns, titled in the current language.
func tableColumns() []ui.TableColumn {
	return []ui.TableColumn{
		{ID: "cover", Width: 44, Fixed: true},
		{ID: "title", Title: locales.CommonTitle(), Sortable: true, MinWidth: 160, RowHeader: true},
		{ID: "author", Title: locales.CommonAuthor(), Width: 130, Sortable: true},
		{ID: "series", Title: locales.CommonSeries(), Width: 110, Sortable: true},
		{ID: "rating", Title: locales.CommonRating(), Width: 84, Sortable: true},
		{ID: "formats", Title: locales.BooksColFormats(), Width: 92},
		{ID: "size", Title: locales.BooksColSize(), Width: 76, Align: ui.End, Sortable: true},
		{ID: "added", Title: locales.BooksColAdded(), Width: 96, Align: ui.End, Sortable: true},
	}
}

var columnSort = map[string]catalog.SortKey{
	"title": catalog.SortTitle, "author": catalog.SortAuthor, "series": catalog.SortSeries, "rating": catalog.SortRating,
	"size": catalog.SortSize, "added": catalog.SortDateAdded,
}

func (a *App) listView(c *ui.Context, s shelf, shown []*calibre.Book) {
	// The header shows the order the toolbar's menu chose, and sets it.
	*s.tableSort = ui.SortOrder{Descending: s.query.Descending}
	for id, k := range columnSort {
		if k == s.query.Sort {
			s.tableSort.Column = id
		}
	}
	before := *s.tableSort
	s.table.Key = func(i int) any { return shown[i].ID }
	s.table.Selection = s.chosen
	s.table.Selected = s.cur
	s.table.Sort = s.tableSort
	s.table.RoundRows = true
	s.table.Label = func(i int) string { return shown[i].Title }
	s.table.CellMenu = func(m *ui.Menu, row, col int) { s.menu(shown[row])(m) }
	// Chosen rows in the design's tint (bg/selected) with the usual text,
	// rather than the accent with white text, so every column stays legible.
	tt := *c.Theme()
	tt.Accent, tt.InactiveAccent, tt.AccentText = pal().Selected, pal().Selected, pal().Text
	var t ui.Element
	ui.Themed(c, &tt).Grow(1).MinHeight(0).Background(ui.Transparent).Margin(0, theme.Space24, theme.Space24, theme.Space24).AlignItems(ui.Stretch).Children(func() {
		t = ui.Table(c, s.table, tableColumns(), len(shown), func(row, col int) {
			b := shown[row]
			switch col {
			case 0:
				ui.Box(c).Padding(theme.Space6, 0).Children(func() { coverOf(c, b, s.cover(b, 80), 28, 40, true) })
			case 1:
				ui.Column(c).Gap(theme.Space2).MinWidth(0).Children(func() {
					ui.Text(c, b.Title).FontWeight(500).SingleLine()
					sub := b.Sort
					if !b.Pubdate.IsZero() {
						sub = fmt.Sprint(b.Pubdate.Year())
					} else {
						sub = ""
					}
					if b.Publisher != "" {
						sub = strings.TrimPrefix(sub+" · "+b.Publisher, " · ")
					}
					if sub != "" {
						ui.Text(c, sub).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
					}
				})
			case 2:
				ui.Text(c, strings.Join(b.Authors, ", ")).SingleLine()
			case 3:
				if b.Series != "" {
					ui.Text(c, locales.BooksSeriesIndex(b.Series, seriesIndex(b.SeriesIndex))).TextColor(pal().TextSecondary).SingleLine()
				} else {
					ui.Text(c, "—").TextColor(pal().TextTertiary)
				}
			case 4:
				widgets.Stars(c, b.Rating, 12)
			case 5:
				ui.Row(c).Gap(theme.Space4).Children(func() {
					for _, f := range b.Formats {
						widgets.FormatBadge(c.Key(f), f, false)
					}
				})
			case 6:
				ui.Text(c, humanSize(b.Size)).Font(theme.FontMono).FontSize(12).TextColor(pal().TextSecondary).NoWrap()
			case 7:
				ui.Text(c, isoDate(b.Timestamp)).Font(theme.FontMono).FontSize(12).TextColor(pal().TextSecondary).NoWrap()
			}
		}).Grow(1).Label(locales.BooksLabel())
	})
	if t.Submitted() {
		if i := *s.cur; i >= 0 && i < len(shown) {
			s.open(shown[i])
		}
	}
	if *s.tableSort != before {
		if k, ok := columnSort[s.tableSort.Column]; ok {
			s.query.Sort, s.query.Descending = k, s.tableSort.Descending
			if s.sorted != nil {
				s.sorted()
			}
		}
	}
}

// bookMenu is the context menu of a book, in the grid and the list.
func (a *App) bookMenu(b *calibre.Book) func(m *ui.Menu) {
	return func(m *ui.Menu) {
		ids := a.selectedIDs()
		if !containsID(ids, b.ID) {
			ids = []int64{b.ID}
		}
		many := len(ids) > 1
		if !many {
			if m.Item(locales.CommonOpen()).Icon(icons.Menu(icons.BookOpen)).Trailing("Enter").Chosen() {
				a.openBook(b, "")
			}
			if m.Item(locales.BooksMenuEditMetadata()).Icon(icons.Menu(icons.Pencil)).Trailing("Ctrl+I").Chosen() {
				a.openEditor(b)
			}
			if editorFormat(b) != "" && m.Item(locales.EditorMenuEditBook()).Icon(icons.Menu(icons.File)).Trailing("Ctrl+E").Chosen() {
				a.editBook(b, "")
			}
		}
		// The separator only parts Remove from what is above it: a
		// selection may have nothing there.
		above := !many
		if a.pluginActions(m, ids) {
			above = true
		}
		if dev := a.connectedDevice(); dev != nil {
			above = true
			item := m.Item(locales.WirelessSendTo(dev.Info().Name)).Icon(icons.Menu(icons.Wifi))
			if a.device.sending > 0 {
				item.Disabled(true)
			}
			if item.Chosen() {
				a.sendToDevice(ids)
			}
			if on := a.onDevice(ids); len(on) > 0 {
				item := m.Item(locales.WirelessRemoveFrom(dev.Info().Name)).Icon(icons.Menu(icons.Trash))
				if a.device.sending > 0 {
					item.Disabled(true)
				}
				if item.Chosen() {
					a.removeFromDevice(on)
				}
			}
		}
		if a.readerMenuItems(m, ids) {
			above = true
		}
		if !many {
			a.shareMenu(m, b)
			if m.Item(locales.CommonShowInFolder()).Icon(icons.Menu(icons.Folder)).Chosen() {
				a.revealBook(b)
			}
		}
		if above {
			m.Separator()
		}
		del := locales.CommonRemoveFromLibrary()
		if many {
			del = locales.BooksMenuRemoveMany(len(ids))
		}
		if m.Item(del).Icon(icons.Menu(icons.Trash)).Danger().Trailing("Del").Chosen() {
			a.askDelete(ids)
		}
	}
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func seriesIndex(f float64) string {
	if f == float64(int64(f)) {
		return fmt.Sprint(int64(f))
	}
	return fmt.Sprintf("%.1f", f)
}

func humanSize(n int64) string {
	switch {
	case n <= 0:
		return "—"
	case n < 1<<20:
		return fmt.Sprintf("%d KB", (n+1023)/1024)
	case n < 1<<30:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
}

func isoDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("2006-01-02")
}
