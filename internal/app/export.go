package app

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/egoist/mygo"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/export"
	"dalibre/internal/settings"
)

// exportState is the Export to folder dialog: the books, read with what
// exporting takes of them, and the user's answers.
type exportState struct {
	open          bool
	ids           []int64
	books         []export.Book // nil while they are read
	formats       []string      // the formats the books have
	on            []bool        // by formats
	folder        string
	template      string
	policy        string // the Select's option
	ascii, extras bool
	// plan is the preview, for the answers in planFor.
	plan    []export.Item
	missing []string
	planFor string
}

func (a *App) policies() []string {
	return []string{locales.ExportKeepBoth(), locales.ExportReplace(), locales.ExportSkip()}
}

// openExport opens the Export to folder dialog for the books of ids,
// with the answers the user gave last time.
func (a *App) openExport(ids []int64) {
	lib := a.lib
	if lib == nil || a.busy != "" || len(ids) == 0 {
		return
	}
	s := a.settings.Export
	tmpl := s.Template
	if tmpl == "" {
		tmpl = export.DefaultTemplate
	}
	ps := a.policies()
	a.ex = exportState{open: true, ids: ids, folder: s.Folder, template: tmpl,
		policy: ps[min(max(s.Policy, 0), len(ps)-1)], ascii: s.ASCII, extras: s.Extras}
	var books []*calibre.Book
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			books = append(books, b)
		}
	}
	a.run(func() {
		out := make([]export.Book, 0, len(books))
		var formats []string
		for _, b := range books {
			d, err := lib.Details(b)
			if err != nil {
				continue
			}
			eb := export.Book{Book: b, Details: d, OPF: func() []byte { return lib.OPF(b, d) }}
			if b.HasCover {
				eb.Cover = lib.CoverPath(b)
			}
			out = append(out, eb)
			for _, f := range d.Files {
				if !slices.Contains(formats, f.Format) {
					formats = append(formats, f.Format)
				}
			}
		}
		slices.Sort(formats)
		a.post(func() {
			if !a.ex.open || !slices.Equal(a.ex.ids, ids) {
				return
			}
			a.ex.books, a.ex.formats = out, formats
			a.ex.on = make([]bool, len(formats))
			for i, f := range formats {
				a.ex.on[i] = !slices.Contains(s.Unchecked, f)
			}
		})
	})
}

// options are the dialog's answers.
func (ex *exportState) options(policies []string) export.Options {
	o := export.Options{Template: ex.template, ASCII: ex.ascii, Extras: ex.extras,
		Policy: export.Policy(max(slices.Index(policies, ex.policy), 0))}
	for i, f := range ex.formats {
		if ex.on[i] {
			o.Formats = append(o.Formats, f)
		}
	}
	return o
}

// preview plans the export for the dialog, again only when an answer
// changed.
func (a *App) exportPreview() ([]export.Item, []string) {
	ex := &a.ex
	o := ex.options(a.policies())
	key := fmt.Sprintf("%s\x00%+v", ex.folder, o)
	if key != ex.planFor {
		ex.plan, ex.missing = export.Plan(ex.folder, ex.books, o)
		ex.planFor = key
	}
	return ex.plan, ex.missing
}

func (a *App) chooseExportFolder() {
	a.run(func() {
		dirs, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.win, Title: locales.ExportChooseFolder(), Directory: true, CreateDirectories: true})
		if err != nil || len(dirs) == 0 {
			return
		}
		a.post(func() { a.ex.folder = dirs[0] })
	})
}

// startExport closes the dialog and exports in the background, in the
// sidebar's busy row, which can stop it.
func (a *App) startExport() {
	ex := a.ex
	if a.busy != "" || ex.folder == "" || strings.TrimSpace(ex.template) == "" {
		return
	}
	o := ex.options(a.policies())
	a.settings.Export = settings.Export{Folder: ex.folder, Template: ex.template, Policy: int(o.Policy), ASCII: ex.ascii, Extras: ex.extras}
	for i, f := range ex.formats {
		if !ex.on[i] {
			a.settings.Export.Unchecked = append(a.settings.Export.Unchecked, f)
		}
	}
	a.saveSettings()
	a.ex = exportState{}
	ctx, cancel := context.WithCancel(context.Background())
	a.busy, a.busyDone, a.busyTotal, a.busyStop = locales.ExportBusy(), 0, 0, cancel
	a.run(func() {
		// Planned again: the folder may have changed since the preview.
		items, _ := export.Plan(ex.folder, ex.books, o)
		a.post(func() { a.busyTotal = len(items) })
		n, err := export.Run(ctx, ex.folder, items, func(done int) { a.post(func() { a.busyDone = done }) })
		cancel()
		a.post(func() {
			a.busy, a.busyStop = "", nil
			if err != nil && !errors.Is(err, context.Canceled) {
				a.toastError(err)
			}
			if n > 0 {
				folder := ex.folder
				a.toasts = append(a.toasts, toastMsg{text: locales.ExportDone(n, folder), action: locales.CommonShowInFolder(),
					onAction: func() { mygo.Shell.OpenPath(folder) }})
			}
		})
	})
}
