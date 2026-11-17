package app

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/plugin"
)

// toolsNav is the sidebar's Tools: the plugins' actions on the whole
// library, as their plugins name them.
func (a *App) toolsNav(c *ui.Context) {
	tools := a.toolActions()
	if len(tools) == 0 {
		return
	}
	title := locales.MainNavTools()
	ui.Column(c.Key("tools")).Gap(theme.Space2).AlignItems(ui.Stretch).Children(func() {
		ui.Row(c).Height(24).Padding(0, theme.Space8).AlignItems(ui.Center).Children(func() {
			widgets.Overline(c, strings.ToUpper(title))
		})
		for _, t := range tools {
			r := a.act.results
			showing := r != nil && r.plugin == t.p.Manifest.ID && r.action == t.act.ID
			a.navItem(c, "tool:"+t.p.Manifest.ID+":"+t.act.ID, actionIcon(t.act), t.act.Title, 0, showing, nil, func() {
				a.runAction(t.p, t.act, nil)
			})
		}
	})
}

// selectionActions are the plugins' actions for the books chosen, in the
// detail panel: what the plugin whose results show does with them as
// buttons, two at most, and the rest behind one Plugins button.
func (a *App) selectionActions(c *ui.Context, ids []int64) {
	results, others := a.bookActions()
	const shown = 2
	for i, pa := range results {
		if i == shown {
			break
		}
		v := widgets.Secondary
		if i == 0 {
			v = widgets.Primary
		}
		b := widgets.Button(c.Key("act:"+pa.p.Manifest.ID+":"+pa.act.ID), v, actionIcon(pa.act), pa.act.Title).
			Disabled(a.busy != "" || !a.takes(pa.act, ids))
		if b.Clicked() {
			a.runAction(pa.p, pa.act, ids)
		}
	}
	var rest []pluginAction
	if len(results) > shown {
		rest = results[shown:]
	}
	ps := a.fileTypePlugins()
	if len(rest)+len(others)+len(ps) == 0 {
		return
	}
	widgets.Button(c.Key("plugins"), widgets.Secondary, icons.Puzzle, locales.BooksMenuPlugins()).PopoverMenuButton(func(m *ui.Menu) {
		for _, pa := range rest {
			a.actionItem(m, pa, ids)
		}
		for _, p := range ps {
			if m.Item(p.Manifest.ActionTitle()).ID("run:" + p.Manifest.ID).Icon(icons.Menu(icons.Sparkle)).Disabled(a.busy != "").Chosen() {
				a.runPlugin(p, ids)
			}
		}
		for _, pa := range others {
			a.actionItem(m, pa, ids)
		}
	})
}

// ---- The form an action asks

func (a *App) formDialog(c *ui.Context) {
	fa := a.act.form
	if fa == nil {
		return
	}
	open := true
	submit := fa.form.Submit
	if submit == "" {
		submit = fa.act.Label()
	}
	widgets.DialogFrame(c, &open, 480, func() {
		pluginTile(c, fa.p.Manifest, 32)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, fa.act.Label()).FontSize(16).FontWeight(600).SingleLine()
			ui.Text(c, fa.p.Manifest.Name).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		})
	}, func() {
		ui.Column(c).Padding(theme.Space24).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
			for i, fd := range fa.form.Fields {
				a.formField(c.Key(fd.Key), fa, i, fd)
			}
		})
	}, func() {
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			open = false
		}
		if widgets.Button(c, widgets.Primary, nil, submit).Clicked() {
			a.answerForm(true)
		}
	})
	if !open {
		a.answerForm(false)
	}
}

// formField is one field of the form, in Dalibre's own controls.
func (a *App) formField(c *ui.Context, fa *formAsk, i int, fd plugin.FormField) {
	if fd.Type == plugin.FieldToggle {
		ui.Checkbox(c, &fa.bools[i], fd.Title)
		return
	}
	// A Field shares its row's width; here each has a row to itself.
	ui.Row(c).AlignItems(ui.Start).Children(func() { a.formControl(c, fa, i, fd) })
}

func (a *App) formControl(c *ui.Context, fa *formAsk, i int, fd plugin.FormField) {
	switch fd.Type {
	case plugin.FieldText:
		widgets.Field(c, fd.Title, "", func() {
			in := ui.TextInput(c, &fa.texts[i]).Label(fd.Title)
			if i == 0 {
				in.AutoFocus()
			}
		})
	case plugin.FieldSelect:
		widgets.Field(c, fd.Title, "", func() {
			names := make([]string, len(fd.Options))
			for j, o := range fd.Options {
				names[j] = o.Title
			}
			ui.Select(c, &fa.texts[i], names).Label(fd.Title)
		})
	case plugin.FieldMultiselect:
		widgets.Field(c, fd.Title, "", func() {
			ui.Scroll(c).Padding(theme.Space10, theme.Space12).Gap(theme.Space8).Radius(theme.RadiusMD).Border(1, pal().BorderSubtle).
				MaxHeight(280).AlignItems(ui.Stretch).Children(func() {
				for j, o := range fd.Options {
					ui.Checkbox(c.Key(o.Value), &fa.sets[i][j], o.Title)
				}
			})
		})
	}
}

// ---- The changes an action asks for

func (a *App) reviewDialog(c *ui.Context) {
	rv := a.act.review
	if rv == nil {
		return
	}
	open := true
	books, fields := 0, 0
	for _, it := range rv.items {
		if it.on {
			books++
			fields += len(it.lines)
		}
	}
	widgets.DialogFrame(c, &open, 760, func() {
		ui.Text(c, rv.title).FontSize(16).FontWeight(600)
		ui.Text(c, locales.CommonBookCount(len(rv.items))).FontSize(13).TextColor(pal().TextTertiary)
		ui.Spacer(c)
	}, func() {
		ui.Column(c).Padding(theme.Space24).AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Radius(theme.RadiusLG).Border(1, pal().BorderSubtle).Background(pal().Surface).Clip().AlignItems(ui.Stretch).Children(func() {
				for i, it := range rv.items {
					row := ui.Row(c.Key(it.id)).Padding(theme.Space12).Gap(theme.Space12).AlignItems(ui.Start)
					if i < len(rv.items)-1 {
						row.BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle)
					}
					row.Children(func() {
						ui.Checkbox(c, &it.on, "").Label(it.title)
						if b := a.byID[it.id]; b != nil {
							a.coverView(c, b, 28, 40, true)
						}
						ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
							ui.Text(c, it.title).FontWeight(500).FontSize(13).SingleLine()
							for j, l := range it.lines {
								ui.Row(c.Key(j)).Gap(theme.Space8).AlignItems(ui.Baseline).Children(func() {
									ui.Text(c, l.field()).Width(84).Shrink(0).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
									old := l.old
									if old == "" {
										old = "—"
									}
									was := ui.Text(c, old).FontSize(13).TextColor(pal().TextTertiary).Shrink(1).MinWidth(0)
									if l.old != "" {
										was.Strikethrough()
									}
									ui.Icon(c, icons.ArrowRight).FontSize(12).TextColor(pal().IconMuted).Shrink(0)
									now := l.now
									if now == "" {
										now = "—"
									}
									ui.Text(c, now).FontSize(13).Shrink(1).MinWidth(0)
								})
							}
						})
					})
				}
			})
		})
	}, func() {
		ui.Text(c, locales.ActionReviewNote(books, fields)).FontSize(12).TextColor(pal().TextTertiary).Grow(1)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			open = false
		}
		if widgets.Button(c, widgets.Primary, nil, locales.ActionApply()).Disabled(books == 0).Clicked() {
			a.applyChangeReview()
		}
	})
	if !open {
		a.act.review = nil
	}
}

// ---- The books an action found

// resultsView is the list of the books an action found, under the
// action's groups: a book list like any other, so the book menu, the
// detail panel and the keyboard work in it as in the library.
func (a *App) resultsView(c *ui.Context) {
	r := a.act.results
	shown := a.visible() // makes the rows
	if len(shown) == 0 {
		a.closeResults() // every book it found is gone
		return
	}
	ui.Row(c).Padding(0, theme.Space32, theme.Space12, theme.Space32).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
		if r.message != "" {
			ui.Text(c, r.message).FontSize(12).TextColor(pal().TextSecondary).SingleLine().Shrink(1)
		}
		again := ui.ButtonBase(c).Padding(theme.Space2, theme.Space4).Radius(theme.RadiusXS).Label(locales.ActionRunAgain()).Disabled(a.busy != "")
		again.OnClick(a.rerunResults).Children(func() {
			t := ui.Text(c, locales.ActionRunAgain()).FontSize(12).FontWeight(500).TextColor(pal().TextAccent)
			if again.Hovered() {
				t.Underline()
			}
		})
	})

	a.syncCur(shown)
	rows := r.rows
	r.cur = -1
	for i, row := range rows {
		if row.book != nil && row.book.ID == a.curID {
			r.cur = i
		}
	}
	was := r.cur
	t := &r.table
	// A header's key is no book's: headers are never chosen.
	t.Key = func(i int) any {
		if b := rows[i].book; b != nil {
			return b.ID
		}
		return int64(-i - 1)
	}
	t.Header = func(i int) bool { return rows[i].book == nil }
	t.Selection = &a.chosen
	t.Selected = &r.cur
	t.RoundRows = true
	t.Label = func(i int) string {
		if b := rows[i].book; b != nil {
			return b.Title
		}
		return rows[i].header
	}
	t.CellMenu = func(m *ui.Menu, row, col int) {
		if b := rows[row].book; b != nil {
			a.bookMenu(b)(m)
		}
	}
	cols := tableColumns()
	for i := range cols {
		cols[i].Sortable = false
	}
	tt := *c.Theme()
	tt.Accent, tt.InactiveAccent, tt.AccentText = pal().Selected, pal().Selected, pal().Text
	tt.Surface = pal().Subtle // the headers'
	var table ui.Element
	ui.Themed(c, &tt).Grow(1).MinHeight(0).Background(ui.Transparent).Margin(0, theme.Space24, theme.Space24, theme.Space24).AlignItems(ui.Stretch).Children(func() {
		table = ui.Table(c, t, cols, len(rows), func(row, col int) {
			rw := rows[row]
			b := rw.book
			if b == nil {
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Baseline).Children(func() {
					ui.Text(c, rw.header).FontSize(12).FontWeight(600).SingleLine().Shrink(1)
					ui.Text(c, locales.CommonBookCount(rw.count)).FontSize(11).FontWeight(400).TextColor(pal().TextTertiary).NoWrap()
				})
				return
			}
			switch col {
			case 0:
				ui.Box(c).Padding(theme.Space6, 0).Children(func() { a.coverView(c, b, 28, 40, true) })
			case 1:
				ui.Column(c).Gap(theme.Space2).MinWidth(0).Children(func() {
					ui.Text(c, b.Title).FontWeight(500).SingleLine()
					sub := rw.note // what the action says of the book, else as the library's list
					if sub == "" {
						if !b.Pubdate.IsZero() {
							sub = fmt.Sprint(b.Pubdate.Year())
						}
						if b.Publisher != "" {
							sub = strings.TrimPrefix(sub+" · "+b.Publisher, " · ")
						}
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
		}).Grow(1).Label(r.title)
	})
	if r.cur != was {
		// The table moved its row: that row's book is the current one.
		a.curID = 0
		if r.cur >= 0 && r.cur < len(rows) && rows[r.cur].book != nil {
			a.curID = rows[r.cur].book.ID
		}
		a.syncCur(shown)
	}
	if table.Submitted() {
		a.openBook(a.current(), "")
	}
}
