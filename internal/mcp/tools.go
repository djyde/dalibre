package mcp

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
	"dalibre/internal/plugin"
)

var (
	errNoLibrary = errors.New("no library is open in Dalibre")
	errReadOnly  = errors.New("Dalibre allows reading only; the user can allow changes in Settings")
)

// MaxFileSize is the largest e-book add_books and add_format take.
const MaxFileSize = 2 << 30

// maxCoverSize is the largest image set_cover takes.
const maxCoverSize = 20 << 20

var writeTools = []string{"update_books", "set_cover", "add_books", "remove_books", "add_format", "remove_format"}

func boolp(b bool) *bool { return &b }

func readTool(name, desc string) *sdk.Tool {
	return &sdk.Tool{Name: name, Description: desc, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, OpenWorldHint: boolp(false)}}
}

func writeTool(name, desc string, destructive, idempotent bool) *sdk.Tool {
	return &sdk.Tool{Name: name, Description: desc, Annotations: &sdk.ToolAnnotations{
		DestructiveHint: boolp(destructive), IdempotentHint: idempotent, OpenWorldHint: boolp(false)}}
}

// library is the open library.
func (s *Server) library() (*calibre.Library, error) {
	if lib := s.host.Library(); lib != nil {
		return lib, nil
	}
	return nil, errNoLibrary
}

// writable is the open library, when changes are allowed.
func (s *Server) changeable() (*calibre.Library, error) {
	if !s.allowsChanges() {
		return nil, errReadOnly
	}
	return s.library()
}

// ---- Results

// BookSummary is a book as lists show it.
type BookSummary struct {
	ID          int64    `json:"id"`
	Title       string   `json:"title"`
	Authors     []string `json:"authors"`
	Series      string   `json:"series,omitempty"`
	SeriesIndex float64  `json:"series_index,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Formats     []string `json:"formats,omitempty"`
	Rating      float64  `json:"rating,omitempty" jsonschema:"stars, 0 to 5"`
	Added       string   `json:"added"`
}

func summary(b *calibre.Book) BookSummary {
	s := BookSummary{
		ID: b.ID, Title: b.Title, Authors: nonNil(b.Authors), Tags: b.Tags, Formats: b.Formats,
		Rating: float64(b.Rating) / 2, Added: b.Timestamp.Format(time.RFC3339),
	}
	if b.Series != "" {
		s.Series, s.SeriesIndex = b.Series, b.SeriesIndex
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Book is all of a book's metadata and where its files are.
type Book struct {
	BookSummary
	TitleSort   string            `json:"title_sort"`
	AuthorSort  string            `json:"author_sort"`
	Publisher   string            `json:"publisher,omitempty"`
	Languages   []string          `json:"languages,omitempty" jsonschema:"ISO 639-2 codes, as eng"`
	Published   string            `json:"published,omitempty"`
	Modified    string            `json:"modified"`
	Identifiers map[string]string `json:"identifiers,omitempty"`
	Comments    string            `json:"comments,omitempty" jsonschema:"plain text"`
	Files       []File            `json:"files"`
	Cover       string            `json:"cover,omitempty" jsonschema:"the cover image's path"`
	Folder      string            `json:"folder"`
}

// File is one format of a book.
type File struct {
	Format string `json:"format"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
}

func fullBook(lib *calibre.Library, b *calibre.Book) (*Book, error) {
	d, err := lib.Details(b)
	if err != nil {
		return nil, err
	}
	out := &Book{
		BookSummary: summary(b), TitleSort: b.Sort, AuthorSort: b.AuthorSort, Publisher: b.Publisher,
		Languages: b.Languages, Modified: b.LastModified.Format(time.RFC3339), Identifiers: d.Identifiers,
		Comments: htmlToText(d.Comments), Files: []File{}, Folder: lib.Dir(b),
	}
	if !b.Pubdate.IsZero() {
		out.Published = b.Pubdate.Format("2006-01-02")
	}
	if len(out.Identifiers) == 0 {
		out.Identifiers = nil
	}
	for _, f := range d.Files {
		size := f.Size
		if fi, err := os.Stat(f.Path); err == nil {
			size = fi.Size()
		}
		out.Files = append(out.Files, File{Format: f.Format, Path: f.Path, Size: size})
	}
	if b.HasCover {
		out.Cover = lib.CoverPath(b)
	}
	return out, nil
}

var (
	reBlock = regexp.MustCompile(`(?i)</?(p|div|br|li|h[1-6]|blockquote)[^>]*>`)
	reTag   = regexp.MustCompile(`<[^>]*>`)
	reBlank = regexp.MustCompile(`\n{3,}`)
)

// htmlToText is a book's comments as plain text, as the book view shows
// them.
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

// textToHTML is plain text as comments keep it: paragraphs, as the
// editor writes them.
func textToHTML(s string) string {
	var paras []string
	for _, p := range strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n\n") {
		if p = strings.TrimSpace(p); p != "" {
			paras = append(paras, "<p>"+strings.ReplaceAll(html.EscapeString(p), "\n", "<br>")+"</p>")
		}
	}
	return strings.Join(paras, "\n")
}

// ---- Read tools

type searchIn struct {
	Query     string `json:"query,omitempty" jsonschema:"words that must all appear in the title, authors, series, tags, publisher or formats"`
	Author    string `json:"author,omitempty" jsonschema:"an author's name, exactly"`
	Series    string `json:"series,omitempty" jsonschema:"a series' name, exactly"`
	Tag       string `json:"tag,omitempty" jsonschema:"a tag, exactly"`
	Publisher string `json:"publisher,omitempty" jsonschema:"a publisher, exactly"`
	Language  string `json:"language,omitempty" jsonschema:"a language code, as eng or en"`
	Format    string `json:"format,omitempty" jsonschema:"a format, as EPUB"`
	Recent    bool   `json:"recent,omitempty" jsonschema:"only books added in the last 30 days"`
	Sort      string `json:"sort,omitempty" jsonschema:"added (the default), title, author, series, rating, published, size or modified"`
	Desc      *bool  `json:"desc,omitempty" jsonschema:"descending order; the default when sorting by added"`
	Limit     int    `json:"limit,omitempty" jsonschema:"at most this many books; 50 when not given"`
	Offset    int    `json:"offset,omitempty" jsonschema:"books to skip, for the next page"`
}

type searchOut struct {
	Total int           `json:"total" jsonschema:"how many books match, of which books is a page"`
	Books []BookSummary `json:"books"`
}

var sortKeys = map[string]catalog.SortKey{
	"": catalog.SortDateAdded, "added": catalog.SortDateAdded, "title": catalog.SortTitle, "author": catalog.SortAuthor,
	"series": catalog.SortSeries, "rating": catalog.SortRating, "published": catalog.SortPublished,
	"size": catalog.SortSize, "modified": catalog.SortModified,
}

type idIn struct {
	ID int64 `json:"id" jsonschema:"the book's id"`
}

type facetsIn struct {
	Kind string `json:"kind" jsonschema:"authors, series, tags, publishers, languages or formats"`
}

// Facet is a value and how many books have it.
type Facet struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

type facetsOut struct {
	Kind   string  `json:"kind"`
	Values []Facet `json:"values"`
}

func (s *Server) addReadTools() {
	sdk.AddTool(s.srv, readTool("search_books", "Searches and filters the library's books. Each book comes with its id, for get_book and the other tools."), s.searchBooks)
	sdk.AddTool(s.srv, readTool("get_book", "All of a book's metadata, and the path and size of each of its files and of its cover."), s.getBook)
	sdk.AddTool(s.srv, readTool("list_facets", "The library's authors, series, tags, publishers, languages or formats, with how many books have each."), s.listFacets)
}

func (s *Server) searchBooks(ctx context.Context, _ *sdk.CallToolRequest, in searchIn) (*sdk.CallToolResult, searchOut, error) {
	lib, err := s.library()
	if err != nil {
		return nil, searchOut{}, err
	}
	key, ok := sortKeys[strings.ToLower(in.Sort)]
	if !ok {
		return nil, searchOut{}, fmt.Errorf("unknown sort %q", in.Sort)
	}
	books, err := lib.Books()
	if err != nil {
		return nil, searchOut{}, err
	}
	now := time.Now()
	lang := in.Language
	if c := calibre.LanguageCode(lang); c != "" {
		lang = c
	}
	for _, f := range []catalog.Filter{
		{Kind: catalog.ByAuthor, Value: in.Author}, {Kind: catalog.BySeries, Value: in.Series},
		{Kind: catalog.ByTag, Value: in.Tag}, {Kind: catalog.ByPublisher, Value: in.Publisher},
		{Kind: catalog.ByLanguage, Value: lang}, {Kind: catalog.ByFormat, Value: in.Format},
	} {
		if f.Value != "" {
			books = catalog.Apply(books, catalog.Query{Filter: f}, now)
		}
	}
	q := catalog.Query{Search: in.Query, Sort: key, Descending: key == catalog.SortDateAdded}
	if in.Desc != nil {
		q.Descending = *in.Desc
	}
	if in.Recent {
		q.Shelf = catalog.RecentlyAdded
	}
	books = catalog.Apply(books, q, now)
	limit := in.Limit
	if limit <= 0 {
		limit = 50
	}
	out := searchOut{Total: len(books), Books: []BookSummary{}}
	for _, b := range books[min(max(in.Offset, 0), len(books)):] {
		if len(out.Books) == limit {
			break
		}
		out.Books = append(out.Books, summary(b))
	}
	s.noteBooks(ctx, len(out.Books))
	return nil, out, nil
}

// ref is b as the call log names it.
func ref(b *calibre.Book) BookRef { return BookRef{ID: b.ID, Title: b.Title} }

func (s *Server) book(lib *calibre.Library, id int64) (*calibre.Book, error) {
	b, err := lib.Book(id)
	if err != nil {
		return nil, fmt.Errorf("no book %d", id)
	}
	return b, nil
}

func (s *Server) getBook(ctx context.Context, _ *sdk.CallToolRequest, in idIn) (*sdk.CallToolResult, *Book, error) {
	lib, err := s.library()
	if err != nil {
		return nil, nil, err
	}
	b, err := s.book(lib, in.ID)
	if err != nil {
		return nil, nil, err
	}
	s.noteBooks(ctx, 1, ref(b))
	out, err := fullBook(lib, b)
	return nil, out, err
}

func (s *Server) listFacets(ctx context.Context, _ *sdk.CallToolRequest, in facetsIn) (*sdk.CallToolResult, facetsOut, error) {
	lib, err := s.library()
	if err != nil {
		return nil, facetsOut{}, err
	}
	books, err := lib.Books()
	if err != nil {
		return nil, facetsOut{}, err
	}
	c := catalog.Tally(books, time.Now())
	var counts []catalog.Count
	switch strings.ToLower(in.Kind) {
	case "authors":
		counts = c.Authors
	case "series":
		counts = c.Series
	case "tags":
		counts = c.Tags
	case "publishers":
		counts = c.Publishers
	case "languages":
		counts = c.Languages
	case "formats":
		counts = c.Formats
	default:
		return nil, facetsOut{}, fmt.Errorf("unknown kind %q", in.Kind)
	}
	out := facetsOut{Kind: strings.ToLower(in.Kind), Values: []Facet{}}
	for _, v := range counts {
		out.Values = append(out.Values, Facet{Value: v.Value, Count: v.N})
	}
	return nil, out, nil
}

// ---- Write tools

// Patch is what update_books changes; fields left out keep their value.
type Patch struct {
	Title       *string           `json:"title,omitempty"`
	Authors     []string          `json:"authors,omitempty"`
	Series      *string           `json:"series,omitempty" jsonschema:"empty removes the book from its series"`
	SeriesIndex *float64          `json:"series_index,omitempty"`
	Tags        []string          `json:"tags,omitempty" jsonschema:"replaces all of the book's tags; an empty list removes them"`
	AddTags     []string          `json:"add_tags,omitempty"`
	RemoveTags  []string          `json:"remove_tags,omitempty"`
	Publisher   *string           `json:"publisher,omitempty"`
	Rating      *float64          `json:"rating,omitempty" jsonschema:"stars, 0 to 5 in halves; 0 removes the rating"`
	Languages   []string          `json:"languages,omitempty" jsonschema:"language codes, as eng or en"`
	Published   *string           `json:"published,omitempty" jsonschema:"as 2006-01-02, 2006-01 or 2006; empty clears it"`
	Identifiers map[string]string `json:"identifiers,omitempty" jsonschema:"merged into the book's, as isbn; an empty value removes one"`
	Comments    *string           `json:"comments,omitempty" jsonschema:"plain text, paragraphs separated by blank lines"`
}

func (p *Patch) apply(m *calibre.Metadata) {
	if p.Title != nil {
		m.Title = *p.Title
	}
	if p.Authors != nil {
		m.Authors, m.AuthorSort = p.Authors, ""
	}
	if p.Series != nil {
		m.Series = *p.Series
	}
	if p.SeriesIndex != nil {
		m.SeriesIndex = *p.SeriesIndex
	}
	if p.Tags != nil {
		m.Tags = slices.Clone(p.Tags)
	}
	m.Tags = append(m.Tags, p.AddTags...)
	m.Tags = slices.DeleteFunc(m.Tags, func(t string) bool {
		return slices.ContainsFunc(p.RemoveTags, func(r string) bool { return strings.EqualFold(strings.TrimSpace(r), strings.TrimSpace(t)) })
	})
	if p.Publisher != nil {
		m.Publisher = *p.Publisher
	}
	if p.Rating != nil {
		m.Rating = int(math.Round(*p.Rating * 2))
	}
	if p.Languages != nil {
		m.Languages = nil
		for _, l := range p.Languages {
			if c := calibre.LanguageCode(l); c != "" {
				m.Languages = append(m.Languages, c)
			}
		}
	}
	if p.Published != nil {
		m.Pubdate = plugin.ParseDate(*p.Published)
	}
	for k, v := range p.Identifiers {
		k = strings.ToLower(strings.TrimSpace(k))
		if strings.TrimSpace(v) == "" {
			delete(m.Identifiers, k)
		} else {
			m.Identifiers[k] = v
		}
	}
	if p.Comments != nil {
		m.Comments = textToHTML(*p.Comments)
	}
}

type updateIn struct {
	IDs   []int64 `json:"ids" jsonschema:"the books to change"`
	Patch Patch   `json:"patch"`
}

// Failure is a book or file a tool couldn't do, and why.
type Failure struct {
	ID    int64  `json:"id,omitempty"`
	File  string `json:"file,omitempty"`
	Error string `json:"error"`
}

type updateOut struct {
	Updated []BookSummary `json:"updated"`
	Failed  []Failure     `json:"failed,omitempty"`
}

type coverIn struct {
	ID   int64  `json:"id"`
	Path string `json:"path,omitempty" jsonschema:"an image file on this computer"`
	URL  string `json:"url,omitempty" jsonschema:"an image's http or https address; give path or url"`
}

type coverOut struct {
	ID    int64  `json:"id"`
	Cover string `json:"cover" jsonschema:"the cover image's path"`
}

type addBooksIn struct {
	Paths []string `json:"paths" jsonschema:"e-book files, or folders whose e-books are all added"`
}

// Added is a book add_books added.
type Added struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	File  string `json:"file"`
}

// Held is a file add_books didn't add because the library has its book.
type Held struct {
	File      string `json:"file"`
	ID        int64  `json:"id" jsonschema:"the book in the library"`
	Title     string `json:"title"`
	HasFormat bool   `json:"has_format" jsonschema:"the book has a file of this format; add_format replaces it"`
	SameFile  bool   `json:"same_file" jsonschema:"and that file is this one, byte for byte"`
}

type addBooksOut struct {
	Added      []Added   `json:"added"`
	Duplicates []Held    `json:"duplicates,omitempty" jsonschema:"not added: add_format adds a file to the book the library has"`
	Failed     []Failure `json:"failed,omitempty"`
	Warnings   []string  `json:"warnings,omitempty" jsonschema:"file type plugins that failed on a file, which was added as it was"`
}

type idsIn struct {
	IDs []int64 `json:"ids"`
}

type removeOut struct {
	Removed []int64 `json:"removed"`
}

type addFormatIn struct {
	ID   int64  `json:"id"`
	Path string `json:"path" jsonschema:"the e-book file, outside the library folder; its extension is the format"`
}

type formatOut struct {
	ID       int64    `json:"id"`
	Format   string   `json:"format"`
	Path     string   `json:"path,omitempty" jsonschema:"the file in the library"`
	Formats  []string `json:"formats" jsonschema:"the formats the book has now"`
	Warnings []string `json:"warnings,omitempty" jsonschema:"file type plugins that failed on the file, which was added as it was"`
}

type removeFormatIn struct {
	ID     int64  `json:"id"`
	Format string `json:"format" jsonschema:"as EPUB"`
}

func (s *Server) addWriteTools() {
	sdk.AddTool(s.srv, writeTool("update_books", "Changes books' metadata: only the fields in patch change, on every book in ids. add_tags and remove_tags tag many books at once.", true, true), s.updateBooks)
	sdk.AddTool(s.srv, writeTool("set_cover", "Replaces a book's cover with an image file or the image at a URL.", true, true), s.setCover)
	sdk.AddTool(s.srv, writeTool("add_books", "Adds e-book files to the library as new books, reading their metadata from the files. Files of books the library has are reported, not added.", false, false), s.addBooks)
	sdk.AddTool(s.srv, writeTool("remove_books", "Removes books from the library; their folders go to the Recycle Bin.", true, false), s.removeBooks)
	sdk.AddTool(s.srv, writeTool("add_format", "Adds a file to a book as a format, or replaces the file of that format the book has. The file is copied, so it must be outside the library folder.", true, true), s.addFormat)
	sdk.AddTool(s.srv, writeTool("remove_format", "Deletes one format of a book: its file and its record.", true, false), s.removeFormat)
}

func (s *Server) updateBooks(ctx context.Context, _ *sdk.CallToolRequest, in updateIn) (*sdk.CallToolResult, updateOut, error) {
	lib, err := s.changeable()
	if err != nil {
		return nil, updateOut{}, err
	}
	if len(in.IDs) == 0 {
		return nil, updateOut{}, errors.New("no ids")
	}
	out := updateOut{Updated: []BookSummary{}}
	var changed []int64
	for _, id := range in.IDs {
		err := func() error {
			b, err := s.book(lib, id)
			if err != nil {
				return err
			}
			d, err := lib.Details(b)
			if err != nil {
				return err
			}
			m := b.Metadata(d)
			in.Patch.apply(&m)
			if err := lib.UpdateMetadata(id, m); err != nil {
				return err
			}
			changed = append(changed, id)
			if b, err = lib.Book(id); err != nil {
				return err
			}
			out.Updated = append(out.Updated, summary(b))
			return nil
		}()
		if err != nil {
			out.Failed = append(out.Failed, Failure{ID: id, Error: err.Error()})
		}
	}
	if len(changed) > 0 {
		s.host.Changed(changed...)
	}
	refs := make([]BookRef, len(out.Updated))
	for i, b := range out.Updated {
		refs[i] = BookRef{b.ID, b.Title}
	}
	s.noteBooks(ctx, len(refs), refs...)
	return nil, out, nil
}

func (s *Server) setCover(ctx context.Context, _ *sdk.CallToolRequest, in coverIn) (*sdk.CallToolResult, coverOut, error) {
	lib, err := s.changeable()
	if err != nil {
		return nil, coverOut{}, err
	}
	b, err := s.book(lib, in.ID)
	if err != nil {
		return nil, coverOut{}, err
	}
	var img []byte
	switch {
	case in.Path != "" && in.URL != "":
		return nil, coverOut{}, errors.New("give path or url, not both")
	case in.Path != "":
		img, err = readCover(in.Path)
	case in.URL != "":
		img, err = fetchCover(ctx, in.URL)
	default:
		return nil, coverOut{}, errors.New("give path or url")
	}
	if err != nil {
		return nil, coverOut{}, err
	}
	if err := lib.SetCover(in.ID, img); err != nil {
		return nil, coverOut{}, err
	}
	s.host.Changed(in.ID)
	s.noteBooks(ctx, 1, ref(b))
	return nil, coverOut{ID: in.ID, Cover: lib.CoverPath(b)}, nil
}

func readCover(path string) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s isn't a file", path)
	}
	if fi.Size() > maxCoverSize {
		return nil, fmt.Errorf("%s is over %d MB", path, maxCoverSize>>20)
	}
	return os.ReadFile(path)
}

func fetchCover(ctx context.Context, u string) ([]byte, error) {
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		return nil, errors.New("url must be http or https")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", u, resp.Status)
	}
	img, err := io.ReadAll(io.LimitReader(resp.Body, maxCoverSize+1))
	if err == nil && len(img) > maxCoverSize {
		err = fmt.Errorf("the image is over %d MB", maxCoverSize>>20)
	}
	return img, err
}

// ebookExts are the formats Dalibre adds, as the window's import does.
var ebookExts = []string{".epub", ".pdf", ".mobi", ".azw3", ".azw", ".fb2", ".txt", ".cbz", ".djvu", ".docx", ".rtf", ".prc"}

func isEbook(path string) bool {
	return slices.Contains(ebookExts, strings.ToLower(filepath.Ext(path)))
}

// checkFile says why path can't go into lib, if it can't: it must be a
// regular e-book file under MaxFileSize, outside the library folder.
func checkFile(lib *calibre.Library, path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("%s isn't an absolute path", path)
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err
	}
	fi, err := os.Stat(real)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("%s isn't a file", path)
	}
	if !isEbook(real) {
		return fmt.Errorf("%s isn't an e-book Dalibre knows (%s)", filepath.Base(path), strings.ToUpper(strings.Join(ebookExts, " ")))
	}
	if fi.Size() > MaxFileSize {
		return fmt.Errorf("%s is over %d GB", filepath.Base(path), MaxFileSize>>30)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("%s is empty", filepath.Base(path))
	}
	root, err := filepath.EvalSymlinks(lib.Root)
	if err != nil {
		root = lib.Root
	}
	if rel, err := filepath.Rel(root, real); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s is inside the library folder; convert or copy it to a file outside it first", path)
	}
	return nil
}

func (s *Server) addBooks(ctx context.Context, _ *sdk.CallToolRequest, in addBooksIn) (*sdk.CallToolResult, addBooksOut, error) {
	lib, err := s.changeable()
	if err != nil {
		return nil, addBooksOut{}, err
	}
	out := addBooksOut{Added: []Added{}}
	var files []string
	for _, p := range in.Paths {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() && filepath.IsAbs(p) {
			filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
				if err == nil && !d.IsDir() && isEbook(path) {
					files = append(files, path)
				}
				return nil
			})
			continue
		}
		files = append(files, p)
	}
	dups, _ := lib.Duplicates() // without it, every file is added
	for _, f := range files {
		if err := ctx.Err(); err != nil {
			break
		}
		if err := checkFile(lib, f); err != nil {
			out.Failed = append(out.Failed, Failure{File: f, Error: err.Error()})
			continue
		}
		file, done, warned := s.processImport(ctx, f)
		out.Warnings = append(out.Warnings, warned...)
		p := calibre.Prepare(file, calibre.Metadata{}, nil)
		if dups != nil {
			if d, _ := dups.Find(p); d != nil {
				done()
				out.Duplicates = append(out.Duplicates, Held{File: f, ID: d.Book.ID, Title: d.Book.Title, HasFormat: d.HasFormat, SameFile: d.SameFile})
				continue
			}
		}
		id, err := lib.Add(p)
		done()
		if err != nil {
			out.Failed = append(out.Failed, Failure{File: f, Error: err.Error()})
			continue
		}
		s.added(ctx, id, p.Format, true)
		if dups != nil {
			dups.Added(id, p.Metadata)
		}
		title := p.Metadata.Title
		if b, err := lib.Book(id); err == nil {
			title = b.Title
		}
		out.Added = append(out.Added, Added{ID: id, Title: title, File: f})
	}
	if len(out.Added) > 0 {
		s.host.Changed()
	}
	refs := make([]BookRef, len(out.Added))
	for i, b := range out.Added {
		refs[i] = BookRef{b.ID, b.Title}
	}
	s.noteBooks(ctx, len(refs), refs...)
	return nil, out, nil
}

func (s *Server) removeBooks(ctx context.Context, _ *sdk.CallToolRequest, in idsIn) (*sdk.CallToolResult, removeOut, error) {
	lib, err := s.changeable()
	if err != nil {
		return nil, removeOut{}, err
	}
	if len(in.IDs) == 0 {
		return nil, removeOut{}, errors.New("no ids")
	}
	var refs []BookRef
	for _, id := range in.IDs {
		b, err := s.book(lib, id)
		if err != nil {
			return nil, removeOut{}, err
		}
		refs = append(refs, ref(b))
	}
	err = lib.RemoveBooks(in.IDs, s.host.Trash())
	s.host.Changed()
	s.noteBooks(ctx, len(refs), refs...)
	if err != nil {
		// The books are gone from the library; some folders stayed.
		return nil, removeOut{}, fmt.Errorf("removed, but: %w", err)
	}
	return nil, removeOut{Removed: in.IDs}, nil
}

func (s *Server) addFormat(ctx context.Context, _ *sdk.CallToolRequest, in addFormatIn) (*sdk.CallToolResult, formatOut, error) {
	lib, err := s.changeable()
	if err != nil {
		return nil, formatOut{}, err
	}
	b, err := s.book(lib, in.ID)
	if err != nil {
		return nil, formatOut{}, err
	}
	s.noteBooks(ctx, 1, ref(b))
	if err := checkFile(lib, in.Path); err != nil {
		return nil, formatOut{}, err
	}
	file, done, warned := s.processImport(ctx, in.Path)
	defer done()
	if err := lib.AddFormat(in.ID, file); err != nil {
		return nil, formatOut{}, err
	}
	s.added(ctx, in.ID, strings.ToUpper(strings.TrimPrefix(filepath.Ext(file), ".")), false)
	s.host.Changed()
	format := strings.ToUpper(strings.TrimPrefix(filepath.Ext(file), "."))
	out := formatOut{ID: in.ID, Format: format, Formats: []string{}, Warnings: warned}
	if b, err := lib.Book(in.ID); err == nil {
		out.Formats = nonNil(b.Formats)
		if d, err := lib.Details(b); err == nil {
			for _, f := range d.Files {
				if f.Format == format {
					out.Path = f.Path
				}
			}
		}
	}
	return nil, out, nil
}

func (s *Server) removeFormat(ctx context.Context, _ *sdk.CallToolRequest, in removeFormatIn) (*sdk.CallToolResult, formatOut, error) {
	lib, err := s.changeable()
	if err != nil {
		return nil, formatOut{}, err
	}
	b, err := s.book(lib, in.ID)
	if err != nil {
		return nil, formatOut{}, err
	}
	s.noteBooks(ctx, 1, ref(b))
	format := strings.ToUpper(strings.TrimPrefix(strings.TrimSpace(in.Format), "."))
	if !slices.Contains(b.Formats, format) {
		return nil, formatOut{}, fmt.Errorf("book %d has no %s", in.ID, format)
	}
	if err := lib.RemoveFormat(in.ID, format); err != nil {
		return nil, formatOut{}, err
	}
	if ev, ok := s.host.(Events); ok {
		ev.FormatRemoved(ctx, in.ID, format)
	}
	s.host.Changed()
	out := formatOut{ID: in.ID, Format: format, Formats: []string{}}
	if b, err := lib.Book(in.ID); err == nil {
		out.Formats = nonNil(b.Formats)
	}
	return nil, out, nil
}
