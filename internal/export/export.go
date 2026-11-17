// Package export copies books' files out of a library into a folder the
// user chose, named by a calibre save template, as calibre's Save to
// disk does.
package export

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"dalibre/internal/calibre"
)

// DefaultTemplate is calibre's Save to disk template: a folder per
// author, as calibre sorts them, a folder per book, "Title - Authors" in
// it.
const DefaultTemplate = "{author_sort}/{title}/{title} - {authors}"

// Fields are the template's fields, for the dialog to list.
var Fields = []string{"title", "authors", "author_sort", "series", "series_index", "tags",
	"publisher", "language", "pubdate", "isbn", "format", "id"}

// maxName is the longest a folder or file name may be, in bytes: under
// the 255 every filesystem allows, with room for " (2)" and the
// extension.
const maxName = 200

// Policy is what to do when a file is already where an export puts one.
type Policy int

const (
	// KeepBoth numbers the new file: "Title - Author (2).epub".
	KeepBoth Policy = iota
	Replace
	Skip
)

// Book is a book to export, with what it takes of the library.
type Book struct {
	*calibre.Book
	Details *calibre.Details
	// Cover is the cover's file; "" when the book has none.
	Cover string
	// OPF is its metadata.opf, made when the export writes it.
	OPF func() []byte
}

// Options are what the user chose.
type Options struct {
	Template string
	// Formats are the formats to export, upper case as "EPUB".
	Formats []string
	Policy  Policy
	// ASCII transliterates names to ASCII, as calibre does for readers.
	ASCII bool
	// Extras writes each book's cover and OPF beside its file, named as
	// it is: "Title - Author.jpg", "Title - Author.opf".
	Extras bool
}

// Note is what an export does about a file already there.
type Note int

const (
	New      Note = iota
	Numbered      // there was a file of that name, so the new one is numbered
	Replaces
	Skipped
)

// Item is one file of an export.
type Item struct {
	Book  int64
	Title string
	// Format is the book's format, or "" for its cover and OPF.
	Format string
	// Path is where it goes, relative to the folder, with slashes.
	Path string
	Note Note
	src  string
	data func() []byte
}

// Plan works out where each book's files go in dir, numbering a file
// whose name another one of the export takes, or one already in dir
// when the policy keeps both. Books with none of the formats are left
// out, and returned by title. With dir "", no file is taken to be there.
func Plan(dir string, books []Book, o Options) (items []Item, missing []string) {
	taken := map[string]bool{} // by lower case, as macOS and Windows compare names
	place := func(it Item) {
		ext := path.Ext(it.Path)
		base := strings.TrimSuffix(it.Path, ext)
		exists := func(p string) bool {
			if dir == "" {
				return false
			}
			_, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(p)))
			return err == nil
		}
		free := func(p string) bool { return !taken[strings.ToLower(p)] && (o.Policy != KeepBoth || !exists(p)) }
		if !taken[strings.ToLower(it.Path)] && exists(it.Path) {
			switch o.Policy {
			case Replace:
				it.Note = Replaces
			case Skip:
				it.Note = Skipped
			}
		}
		if it.Note == New && !free(it.Path) {
			it.Note = Numbered
			for n := 2; ; n++ {
				if p := fmt.Sprintf("%s (%d)%s", base, n, ext); free(p) {
					it.Path = p
					break
				}
			}
		}
		taken[strings.ToLower(it.Path)] = true
		items = append(items, it)
	}
	for _, b := range books {
		var files []calibre.FormatFile
		for _, f := range b.Details.Files {
			for _, want := range o.Formats {
				if strings.EqualFold(f.Format, want) {
					files = append(files, f)
				}
			}
		}
		if len(files) == 0 {
			missing = append(missing, b.Title)
			continue
		}
		var first string
		for _, f := range files {
			p := Path(o.Template, b, f.Format, o.ASCII) + strings.ToLower(filepath.Ext(f.Path))
			place(Item{Book: b.ID, Title: b.Title, Format: f.Format, Path: p, src: f.Path})
			if first == "" {
				first = items[len(items)-1].Path
			}
		}
		if o.Extras {
			base := strings.TrimSuffix(first, path.Ext(first))
			if b.Cover != "" {
				place(Item{Book: b.ID, Title: b.Title, Path: base + ".jpg", src: b.Cover})
			}
			if b.OPF != nil {
				place(Item{Book: b.ID, Title: b.Title, Path: base + ".opf", data: b.OPF})
			}
		}
	}
	return items, missing
}

// Path is where template puts b's file of format, without its extension:
// each component filled in and made a safe name; a component that comes
// out empty is left out, as calibre does, and an empty name is "Unknown".
func Path(template string, b Book, format string, ascii bool) string {
	fields := fieldsOf(b, format)
	// As calibre, a slash in a value is not a folder, but one the
	// template writes is, even as a field's suffix: "{series:||/}".
	for k, v := range fields {
		fields[k] = strings.NewReplacer("/", "_", `\`, "_").Replace(v)
	}
	var comps []string
	parts := strings.Split(calibre.ExpandTemplate(strings.ReplaceAll(template, `\`, "/"), fields), "/")
	for i, s := range parts {
		if ascii {
			s = calibre.ASCIIFilename(s)
		}
		s = calibre.SafeFileName(s, maxName)
		if s == "" {
			if i < len(parts)-1 {
				continue
			}
			s = "Unknown"
		}
		comps = append(comps, s)
	}
	return strings.Join(comps, "/")
}

func fieldsOf(b Book, format string) map[string]string {
	authors := b.Authors
	if len(authors) == 0 {
		authors = []string{"Unknown"}
	}
	authorSort := b.AuthorSort
	if authorSort == "" {
		authorSort = calibre.AuthorsSort(authors)
	}
	f := map[string]string{
		"title":       b.Title,
		"title_sort":  b.Sort,
		"authors":     strings.Join(authors, " & "),
		"author_sort": authorSort,
		"series":      b.Series,
		"tags":        strings.Join(b.Tags, ", "),
		"publisher":   b.Publisher,
		"language":    strings.Join(b.Languages, ", "),
		"format":      strings.ToUpper(format),
		"id":          strconv.FormatInt(b.ID, 10),
		"uuid":        b.UUID,
	}
	if b.Series != "" {
		f["series_index"] = strconv.FormatFloat(b.SeriesIndex, 'f', -1, 64)
	}
	if !b.Pubdate.IsZero() {
		f["pubdate"] = b.Pubdate.Format("2006-01-02")
	}
	if b.Details != nil {
		f["isbn"] = b.Details.Identifiers["isbn"]
	}
	return f
}

// Run writes the items into dir, telling progress after each, until ctx
// is done. Each file is written beside its name and moved there once
// whole, so a failed or stopped export never leaves half a file, nor
// loses the one it was replacing. It returns how many it wrote.
func Run(ctx context.Context, dir string, items []Item, progress func(done int)) (int, error) {
	var errs []error
	written := 0
	for i, it := range items {
		if err := ctx.Err(); err != nil {
			return written, err
		}
		if it.Note != Skipped {
			if err := write(ctx, filepath.Join(dir, filepath.FromSlash(it.Path)), it); err != nil {
				if ctx.Err() != nil {
					return written, ctx.Err()
				}
				errs = append(errs, fmt.Errorf("%s: %w", it.Title, err))
			} else if it.Format != "" {
				written++
			}
		}
		if progress != nil {
			progress(i + 1)
		}
	}
	return written, errors.Join(errs...)
}

func write(ctx context.Context, dst string, it Item) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	part := filepath.Join(filepath.Dir(dst), ".dalibre-"+filepath.Base(dst)+".part")
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	if it.data != nil {
		_, err = out.Write(it.data())
	} else {
		var in *os.File
		if in, err = os.Open(it.src); err == nil {
			_, err = io.Copy(out, readerCtx{ctx, in})
			in.Close()
		}
	}
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(part, dst)
	}
	if err != nil {
		os.Remove(part)
	}
	return err
}

// readerCtx stops a copy when ctx is done.
type readerCtx struct {
	ctx context.Context
	r   io.Reader
}

func (r readerCtx) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}
