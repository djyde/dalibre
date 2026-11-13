package app

import (
	"errors"
	"fmt"
	"html"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
)

func (a *App) dialogs(c *ui.Context) {
	a.editDialog(c)
	a.deleteDialog(c)
	a.deviceDialogs(c)
	a.settingsDialog(c)
	a.pluginsDialog(c)
	a.consentDialog(c)
	a.toolInstallDialog(c)
	a.notLibrarySheet(c)
	a.createLibrarySheet(c)
	a.importReviewSheet(c)
	a.appUpdateDialog(c)
	a.formDialog(c)
	a.reviewDialog(c)
}

// ---- Edit metadata

type editState struct {
	id                  int64
	order               []int64 // the books Previous and Next go through
	title, titleSort    string
	authors             []string
	authorSort          string
	series, seriesIndex string
	rating              int // widgets.Stars, 0–5
	tags                []string
	publisher           string
	pubdate             time.Time
	languages           []string
	identifiers         string
	comments            string
	origComments        string // HTML, kept when the text is not edited
	commentsText        string
	cover               []byte
	coverBmp            *ui.Bitmap
	err                 string
}

func (a *App) openEditor(b *calibre.Book) {
	if b == nil || a.lib == nil {
		return
	}
	d, err := a.lib.Details(b)
	if err != nil {
		a.toastError(err)
		return
	}
	ids := make([]int64, 0)
	for _, x := range a.visible() {
		ids = append(ids, x.ID)
	}
	m := b.Metadata(d)
	var idents []string
	keys := make([]string, 0, len(m.Identifiers))
	for k := range m.Identifiers {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		idents = append(idents, k+":"+m.Identifiers[k])
	}
	text := htmlToText(d.Comments)
	a.edit = editState{
		id: b.ID, order: ids, title: m.Title, titleSort: b.Sort, authors: m.Authors, authorSort: m.AuthorSort,
		series: m.Series, seriesIndex: seriesIndex(m.SeriesIndex), rating: (m.Rating + 1) / 2, tags: m.Tags,
		publisher: m.Publisher, pubdate: m.Pubdate, languages: m.Languages, identifiers: strings.Join(idents, ", "),
		comments: text, commentsText: text, origComments: d.Comments,
	}
	a.editOpen = true
	a.fetch.open = false
}

// metadata returns what the editor holds as calibre metadata.
func (e *editState) metadata() calibre.Metadata {
	idx, _ := strconv.ParseFloat(strings.TrimSpace(e.seriesIndex), 64)
	m := calibre.Metadata{
		Title: e.title, Authors: e.authors, AuthorSort: e.authorSort, Series: e.series, SeriesIndex: idx,
		Tags: e.tags, Publisher: e.publisher, Rating: e.rating * 2, Pubdate: e.pubdate,
		Identifiers: map[string]string{}, Comments: e.origComments,
	}
	for _, l := range e.languages {
		if code := calibre.LanguageCode(l); code != "" {
			m.Languages = append(m.Languages, code)
		} else {
			m.Languages = append(m.Languages, l)
		}
	}
	for _, part := range strings.Split(e.identifiers, ",") {
		if k, v, ok := strings.Cut(strings.TrimSpace(part), ":"); ok {
			m.Identifiers[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	if e.comments != e.commentsText {
		var paras []string
		for _, p := range strings.Split(e.comments, "\n\n") {
			if p = strings.TrimSpace(p); p != "" {
				paras = append(paras, "<p>"+strings.ReplaceAll(html.EscapeString(p), "\n", "<br>")+"</p>")
			}
		}
		m.Comments = strings.Join(paras, "\n")
	}
	return m
}

func (a *App) editDialog(c *ui.Context) {
	if !a.editOpen {
		return
	}
	e := &a.edit
	b := a.byID[e.id]
	if b == nil {
		a.editOpen = false
		return
	}
	if a.fetch.open {
		a.fetchDialog(c, b)
		return
	}
	pos := slices.Index(e.order, e.id)
	step := func(delta int) {
		a.saveMetadata(e.id, e.metadata(), e.cover)
		if p := pos + delta; p >= 0 && p < len(e.order) {
			if nb := a.byID[e.order[p]]; nb != nil {
				order := e.order
				a.openEditor(nb)
				a.edit.order = order
			}
		}
	}
	widgets.DialogFrame(c, &a.editOpen, 880, func() {
		ui.Text(c, locales.DialogsEditMetadataTitle()).FontSize(16).FontWeight(600)
		if len(e.order) > 1 && pos >= 0 {
			ui.Text(c, locales.DialogsPosition(pos+1, len(e.order))).Font(theme.FontMono).FontSize(12).TextColor(pal().TextTertiary)
		}
		ui.Spacer(c)
		if widgets.ButtonSized(c, widgets.Secondary, icons.Search, locales.CommonFetchMetadata(), theme.ControlS).
			Tooltip(locales.DialogsFetchTooltip()).Clicked() {
			a.openFetch()
		}
		if len(e.order) > 1 {
			if widgets.IconButton(c, icons.ChevronLeft, locales.DialogsSaveEditPrevious(), false).Disabled(pos <= 0).Clicked() {
				step(-1)
			}
			if widgets.IconButton(c, icons.ChevronRight, locales.DialogsSaveEditNext(), false).Disabled(pos >= len(e.order)-1).Clicked() {
				step(1)
			}
		}
	}, func() {
		ui.Row(c).Padding(theme.Space24).Gap(theme.Space32).AlignItems(ui.Start).Children(func() {
			// Cover column.
			ui.Column(c).Width(184).Gap(theme.Space12).AlignItems(ui.Stretch).Children(func() {
				ui.Row(c).Justify(ui.Center).Padding(theme.Space20, 0).Radius(theme.RadiusLG).Background(pal().Subtle).Children(func() {
					if e.coverBmp != nil {
						ui.Image(c, e.coverBmp).Size(136, 204).Fit(ui.Cover).Radius(2, 4, 4, 2)
					} else {
						a.coverView(c, b, 136, 204, false)
					}
				})
				ui.Row(c).Gap(theme.Space8).Children(func() {
					if widgets.ButtonSized(c, widgets.Secondary, icons.Image, locales.DialogsChangeCover(), theme.ControlS).Grow(1).Clicked() {
						a.chooseCover()
					}
				})
				if e.cover != nil {
					ui.Text(c, locales.DialogsNewCoverNote()).FontSize(11).TextColor(pal().TextTertiary).TextAlign(ui.Center)
				}
			})
			// Form.
			ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
				row := func(fn func()) { ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(fn) }
				// Title and Authors take the full width, as names run long
				// (CJK titles, several authors with translators); the sort
				// fields, rarely edited, share the row below.
				row(func() {
					widgets.Field(c, locales.CommonTitle(), "", func() { ui.TextInput(c, &e.title).Label(locales.CommonTitle()).AutoFocus() })
				})
				row(func() {
					widgets.Field(c, locales.CommonAuthors(), "", func() {
						// A chip doesn't shrink below its name: keep a very
						// long one inside the field.
						ui.Box(c).Clip().AlignItems(ui.Stretch).Children(func() {
							if ui.TokenField(c, &e.authors, values(a.counts.Authors)).Placeholder(locales.DialogsAddAuthor()).Label(locales.CommonAuthors()).Changed() {
								e.authorSort = ""
							}
						})
					})
				})
				row(func() {
					widgets.Field(c, locales.CommonTitleSort(), locales.DialogsAutoHint(), func() {
						v := calibre.TitleSort(e.title)
						ui.TextInput(c, &v).Label(locales.CommonTitleSort()).ReadOnly(true).TextColor(pal().TextSecondary)
					})
					widgets.Field(c, locales.DialogsAuthorSort(), locales.DialogsAutoHint(), func() {
						if e.authorSort == "" {
							e.authorSort = calibre.AuthorsSort(e.authors)
						}
						ui.TextInput(c, &e.authorSort).Label(locales.DialogsAuthorSort())
					})
				})
				row(func() {
					widgets.Field(c, locales.CommonSeries(), "", func() {
						ui.Combobox(c, &e.series, values(a.counts.Series)).Label(locales.CommonSeries())
					})
					ui.Column(c).Width(72).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
						ui.Text(c, "#").FontSize(12).FontWeight(500).TextColor(pal().TextSecondary)
						ui.TextInput(c, &e.seriesIndex).Label(locales.DialogsSeriesNumber())
					})
					ui.Column(c).Width(132).Gap(theme.Space6).AlignItems(ui.Stretch).Children(func() {
						ui.Text(c, locales.CommonRating()).FontSize(12).FontWeight(500).TextColor(pal().TextSecondary)
						ui.Row(c).Height(theme.ControlM).AlignItems(ui.Center).Children(func() {
							ui.Rating(c, &e.rating, 5).Label(locales.CommonRating()).TextColor(pal().WarningSolid)
						})
					})
				})
				row(func() {
					widgets.Field(c, locales.CommonTags(), fmt.Sprint(len(e.tags)), func() {
						ui.TokenField(c, &e.tags, values(a.counts.Tags)).Placeholder(locales.DialogsAddTag()).Label(locales.CommonTags())
					})
				})
				row(func() {
					widgets.Field(c, locales.CommonPublisher(), "", func() { ui.Combobox(c, &e.publisher, values(a.counts.Publishers)).Label(locales.CommonPublisher()) })
					widgets.Field(c, locales.CommonPublished(), "", func() { ui.DateInput(c, &e.pubdate).Label(locales.CommonPublished()) })
					widgets.Field(c, locales.DialogsLanguages(), "", func() {
						ui.TokenField(c, &e.languages, values(a.counts.Languages)).Placeholder("eng").Label(locales.DialogsLanguages())
					})
				})
				row(func() {
					widgets.Field(c, locales.CommonIdentifiers(), "type:value, …", func() {
						ui.TextInput(c, &e.identifiers).Placeholder("isbn:9780765382030, goodreads:20518872").Label(locales.CommonIdentifiers()).Font(theme.FontMono)
					})
				})
				row(func() {
					widgets.Field(c, locales.CommonDescription(), "", func() { ui.TextArea(c, &e.comments).Lines(4, 10).Label(locales.CommonDescription()) })
				})
			})
		})
	}, func() {
		ui.Row(c).Grow(1).Gap(theme.Space6).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icons.Database).FontSize(12).TextColor(pal().IconMuted)
			ui.Text(c, locales.DialogsWritesNote()).FontSize(12).TextColor(pal().TextTertiary)
		})
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			a.editOpen = false
		}
		if widgets.Button(c, widgets.Primary, nil, locales.CommonSave()).Clicked() {
			a.saveMetadata(e.id, e.metadata(), e.cover)
			a.editOpen = false
		}
	})
	c.OnWindowShortcut(ui.Cmd, ui.KeyS, func() {
		a.saveMetadata(e.id, e.metadata(), e.cover)
		a.editOpen = false
	})
}

func (a *App) chooseCover() {
	a.run(func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: locales.DialogsChooseCover(),
			Filters: []mygo.FileFilter{{Name: locales.DialogsImages(), Extensions: []string{"jpg", "jpeg", "png", "gif", "webp"}}}})
		if err != nil || len(paths) == 0 {
			return
		}
		data, err := os.ReadFile(paths[0])
		var bmp *ui.Bitmap
		if err == nil {
			bmp, err = ui.DecodeBitmap(data)
		}
		a.post(func() {
			if err != nil {
				a.toastError(errors.New(locales.DialogsCoverReadFailed(err.Error())))
				return
			}
			a.edit.cover, a.edit.coverBmp = data, bmp
		})
	})
}

func values(cs []catalog.Count) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Value
	}
	return out
}

// ---- Delete

func (a *App) askDelete(ids []int64) {
	if len(ids) == 0 {
		return
	}
	a.deleteIDs, a.deleteOpen = ids, true
}

func (a *App) deleteDialog(c *ui.Context) {
	if !a.deleteOpen {
		return
	}
	title := locales.DialogsRemoveBooksTitle(len(a.deleteIDs))
	if len(a.deleteIDs) == 1 {
		if b := a.byID[a.deleteIDs[0]]; b != nil {
			title = locales.DialogsRemoveBookTitle(b.Title)
		}
	}
	switch ui.AlertDialog(c, &a.deleteOpen, title,
		locales.DialogsRemoveBody(), locales.CommonCancel(), locales.CommonRemove()) {
	case 1:
		a.removeBooks(a.deleteIDs)
	}
}

func revealPath(p string) { mygo.Shell.ShowItemInFolder(p) }
