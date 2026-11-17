package app

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/app/icons"
	"dalibre/internal/app/theme"
	"dalibre/internal/app/widgets"
	"dalibre/internal/export"
)

// previewRows is how many of the export's paths the preview lists.
const previewRows = 6

// exportDialog is the Export to folder dialog, as in Figma (25 · Export
// to folder).
func (a *App) exportDialog(c *ui.Context) {
	ex := &a.ex
	if !ex.open {
		return
	}
	loading := ex.books == nil
	var items []export.Item
	var missing []string
	if !loading {
		items, missing = a.exportPreview()
	}
	files, there := 0, 0
	for _, it := range items {
		if it.Format != "" {
			files++
			if it.Note != export.New {
				there++
			}
		}
	}
	widgets.DialogFrame(c, &ex.open, 600, func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(theme.Space2).Children(func() {
			ui.Text(c, locales.ExportTitle(len(ex.ids))).FontSize(16).FontWeight(600).SingleLine()
			ui.Text(c, locales.ExportSubtitle()).FontSize(12).TextColor(pal().TextTertiary).SingleLine()
		})
	}, func() {
		ui.Column(c).Padding(theme.Space24).Gap(theme.Space16).AlignItems(ui.Stretch).Children(func() {
			labeled(c, locales.ExportFolder(), func() {
				ui.Row(c).Gap(theme.Space8).AlignItems(ui.Center).Children(func() {
					ui.TextInput(c, &ex.folder).Label(locales.ExportFolder()).Placeholder(locales.ExportNoFolder()).Grow(1)
					if widgets.Button(c, widgets.Secondary, nil, locales.ExportChooseFolder()).Clicked() {
						a.chooseExportFolder()
					}
				})
			})
			labeled(c, locales.ExportFileNames(), func() {
				ui.TextInput(c, &ex.template).Label(locales.ExportFileNames()).Font(theme.FontMono).FontSize(12)
				fields := "{" + strings.Join(export.Fields, "} {") + "}"
				ui.Text(c, locales.ExportTemplateHint("{series:||/}", fields)).FontSize(12).TextColor(pal().TextTertiary)
			})
			ui.Row(c).Gap(theme.Space16).AlignItems(ui.Start).Children(func() {
				widgets.Field(c, locales.ExportFormats(), "", func() {
					ui.Row(c).Height(32).Gap(theme.Space16).AlignItems(ui.Center).Wrap().Children(func() {
						if loading {
							ui.Spinner(c).Size(14, 14).Label(locales.ExportLoading())
						}
						for i, f := range ex.formats {
							ui.Checkbox(c.Key(f), &ex.on[i], f)
						}
					})
				})
				widgets.Field(c, locales.ExportWhenThere(), "", func() {
					ui.Select(c, &ex.policy, a.policies()).Label(locales.ExportWhenThere())
				})
			})
			ui.Column(c).Gap(theme.Space10).AlignItems(ui.Start).Children(func() {
				ui.Checkbox(c, &ex.extras, locales.ExportExtras())
				ui.Checkbox(c, &ex.ascii, locales.ExportASCII())
			})
			if !loading {
				labeled(c, locales.ExportPreview(files), func() { exportPreviewList(c, items, missing) })
			}
		})
	}, func() {
		if there > 0 {
			ui.Text(c, locales.ExportAlreadyThere(there)).FontSize(12).TextColor(pal().TextTertiary).SingleLine().Shrink(1).MinWidth(0)
		}
		ui.Spacer(c)
		if widgets.Button(c, widgets.Secondary, nil, locales.CommonCancel()).Clicked() {
			ex.open = false
		}
		ok := files > 0 && ex.folder != "" && strings.TrimSpace(ex.template) != "" && a.busy == ""
		if widgets.Button(c, widgets.Primary, icons.Download, locales.ExportSubmit(files)).Disabled(!ok).Clicked() {
			a.startExport()
		}
	})
	if !a.ex.open {
		a.ex = exportState{}
	}
}

// exportPreviewList lists the first of the export's book files where
// they will go, marking those a file already there changes.
func exportPreviewList(c *ui.Context, items []export.Item, missing []string) {
	var shown []export.Item
	more := 0
	for _, it := range items {
		switch {
		case it.Format == "": // covers and OPFs go with their book
		case len(shown) < previewRows:
			shown = append(shown, it)
		default:
			more++
		}
	}
	if len(shown) > 0 {
		ui.Column(c).Radius(theme.RadiusMD).Border(1, pal().BorderSubtle).Background(pal().Subtle).Clip().AlignItems(ui.Stretch).Children(func() {
			for i, it := range shown {
				row := ui.Row(c.Key(i)).Padding(7, theme.Space12).Gap(theme.Space8).AlignItems(ui.Center)
				if i < len(shown)-1 || more > 0 {
					row.BorderWidth(0, 0, 1, 0).BorderColor(pal().BorderSubtle)
				}
				row.Children(func() {
					ui.Text(c, it.Path).Font(theme.FontMono).FontSize(12).SingleLine().Grow(1).Shrink(1).MinWidth(0)
					note := ""
					switch it.Note {
					case export.Numbered:
						note = locales.ExportNumbered()
					case export.Replaces:
						note = locales.ExportReplaces()
					case export.Skipped:
						note = locales.ExportSkipped()
					}
					if note != "" {
						ui.Box(c).Padding(theme.Space2, theme.Space6).Radius(theme.RadiusXS).Background(pal().Warning.BG).Shrink(0).Children(func() {
							ui.Text(c, note).FontSize(12).TextColor(pal().Warning.FG).NoWrap()
						})
					}
				})
			}
			if more > 0 {
				ui.Text(c, locales.ExportMore(more)).FontSize(12).TextColor(pal().TextTertiary).Padding(7, theme.Space12)
			}
		})
	}
	if len(missing) > 0 {
		ui.Text(c, locales.ExportMissing(strings.Join(missing, ", "))).FontSize(12).TextColor(pal().TextTertiary)
	}
}
