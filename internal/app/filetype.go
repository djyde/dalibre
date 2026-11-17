package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/plugin"
)

// importPlugins are the file type plugins that process the files Dalibre
// adds, in name order (as loaded). Main thread.
func (a *App) importPlugins() []*plugin.Plugin {
	var out []*plugin.Plugin
	for _, p := range a.plugins {
		if p.Manifest.RunsOnImport() {
			out = append(out, p)
		}
	}
	return out
}

// fileTypePlugins are the file type plugins the book menu can run: those
// that export process. Main thread.
func (a *App) fileTypePlugins() []*plugin.Plugin {
	var out []*plugin.Plugin
	for _, p := range a.plugins {
		if p.Manifest.IsFileType() && p.Defines("process") {
			out = append(out, p)
		}
	}
	return out
}

// processImport runs plugins on file, one after the other, each on what
// the one before made, as calibre runs its file type plugins on import.
// It returns the file to add and done, which removes what the plugins
// made once the file is in the library. A plugin that fails is passed
// over: the file goes on as it was, and the failure is returned to tell
// the user. Any goroutine.
func processImport(ctx context.Context, plugins []*plugin.Plugin, file string) (string, func(), []error) {
	var made []*plugin.Processed
	var errs []error
	cur := file
	for _, p := range plugins {
		r, err := p.Process(ctx, cur, plugin.ReasonImport, nil)
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("%s: %v", p.Manifest.ID, err)
			errs = append(errs, processError(p, filepath.Base(file), err))
			continue
		}
		if r.File == "" {
			continue
		}
		made = append(made, r)
		cur = r.File
	}
	return cur, func() {
		for _, r := range made {
			r.Done()
		}
	}, errs
}

// processError is what the user is told when plugin p failed on a file.
func processError(p *plugin.Plugin, name string, err error) error {
	msg := err.Error()
	var pe *plugin.Error
	if errors.As(err, &pe) {
		msg = pe.Msg
	}
	return fmt.Errorf("%s", locales.PluginsProcessFailed(p.Manifest.Name, name, msg))
}

// hookPlugins are the file type plugins with hooks. Main thread.
func (a *App) hookPlugins() []*plugin.Plugin {
	var out []*plugin.Plugin
	for _, p := range a.plugins {
		if len(p.Manifest.Hooks()) > 0 {
			out = append(out, p)
		}
	}
	return out
}

// afterAdd calls the hooks for a file of format added to book id:
// postimport, then postadd when the book is new, as calibre calls them.
// Any goroutine.
func afterAdd(ctx context.Context, lib *calibre.Library, plugins []*plugin.Plugin, id int64, format string, newBook bool) {
	runHooks(ctx, lib, plugins, plugin.HookPostImport, id, format)
	if newBook {
		runHooks(ctx, lib, plugins, plugin.HookPostAdd, id, "")
	}
}

// runHooks calls hook on every plugin that asks for it, for book id
// (and format: the one added or removed), and writes the changes they
// return into the book, one plugin after the other. A hook that fails is
// logged, as calibre does: what happened to the book stands. Any
// goroutine.
func runHooks(ctx context.Context, lib *calibre.Library, plugins []*plugin.Plugin, hook string, id int64, format string) {
	for _, p := range plugins {
		if !p.Manifest.HasHook(hook) {
			continue
		}
		b, err := lib.Book(id)
		if err != nil {
			return
		}
		d, err := lib.Details(b)
		if err != nil {
			return
		}
		files := map[string]string{}
		for _, f := range d.Files {
			if hook == plugin.HookPostImport && f.Format == format ||
				hook == plugin.HookPostAdd && !strings.HasPrefix(f.Format, "ORIGINAL_") {
				files[f.Format] = f.Path
			}
		}
		ch, err := p.Hook(ctx, hook, plugin.HookEvent{Book: bookInfo(b, d), Format: format}, files)
		if err != nil {
			log.Printf("%s.%s: %v", p.Manifest.ID, hook, err)
			continue
		}
		if ch == nil {
			continue
		}
		if err := lib.UpdateMetadata(id, applyChanges(b.Metadata(d), ch)); err != nil {
			log.Printf("%s.%s: writing its changes: %v", p.Manifest.ID, hook, err)
		}
	}
}

// bookInfo is b as a hook sees it.
func bookInfo(b *calibre.Book, d *calibre.Details) plugin.BookInfo {
	info := plugin.BookInfo{
		ID: b.ID, Title: b.Title, Authors: nonNilStrings(b.Authors), Series: b.Series, SeriesIndex: b.SeriesIndex,
		Tags: nonNilStrings(b.Tags), Publisher: b.Publisher, Languages: b.Languages, Rating: b.Rating,
		Identifiers: d.Identifiers, Comments: d.Comments, Formats: nonNilStrings(b.Formats), Size: b.Size,
	}
	if info.Identifiers == nil {
		info.Identifiers = map[string]string{}
	}
	if !b.Pubdate.IsZero() {
		info.Pubdate = b.Pubdate.Format("2006-01-02")
	}
	info.UUID, info.TitleSort, info.AuthorSort, info.HasCover = b.UUID, b.Sort, b.AuthorSort, b.HasCover
	if !b.Timestamp.IsZero() {
		info.Added = b.Timestamp.Format(time.RFC3339)
	}
	if !b.LastModified.IsZero() {
		info.Modified = b.LastModified.Format(time.RFC3339)
	}
	return info
}

// applyChanges is m with what a hook asked to change.
func applyChanges(m calibre.Metadata, ch *plugin.Changes) calibre.Metadata {
	if ch.Title != nil {
		m.Title = strings.TrimSpace(*ch.Title)
	}
	if ch.Authors != nil {
		m.Authors, m.AuthorSort = *ch.Authors, ""
	}
	if ch.Series != nil {
		m.Series = *ch.Series
	}
	if ch.SeriesIndex != nil {
		m.SeriesIndex = *ch.SeriesIndex
	}
	if ch.Tags != nil {
		m.Tags = *ch.Tags
	}
	if ch.Publisher != nil {
		m.Publisher = *ch.Publisher
	}
	if ch.Pubdate != nil {
		m.Pubdate = plugin.ParseDate(*ch.Pubdate)
	}
	if ch.Languages != nil {
		m.Languages = nil
		for _, l := range *ch.Languages {
			if code := calibre.LanguageCode(l); code != "" {
				m.Languages = append(m.Languages, code)
			}
		}
	}
	if ch.Rating != nil {
		m.Rating = *ch.Rating
	}
	if ch.Comments != nil {
		m.Comments = *ch.Comments
	}
	if len(ch.Identifiers) > 0 && m.Identifiers == nil {
		m.Identifiers = map[string]string{}
	}
	for k, v := range ch.Identifiers {
		if k = strings.ToLower(strings.TrimSpace(k)); v == "" {
			delete(m.Identifiers, k)
		} else if k != "" {
			m.Identifiers[k] = v
		}
	}
	return m
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
