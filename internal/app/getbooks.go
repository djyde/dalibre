package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"slices"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"dalibre/internal/plugin"
)

// The Get books page (Figma "07 · Get books"): one page where the user
// picks a book source, searches or browses it, and adds books to the
// library. Every book source plugin gets the same page; plugins only give
// data.

// allSources is the source picker's "All sources".
const allSources = "*"

// allPreview is how many results each source shows in All sources.
const allPreview = 3

// shelfPreview is how many books a collection shows on the browse page
// until its width is known.
const shelfPreview = 5

type page int

const (
	pageLibrary page = iota
	pageGetBooks
	pageCatalog // an OPDS catalog's feeds
	pageDevice  // a reader plugged in: its library (devices.go)
)

type getBooksState struct {
	source string // a plugin id, or allSources
	text   string // the search box
	// query is what was last searched; "" shows the browse page.
	query string
	// collection is the collection opened with See all, on the source
	// picked; "" when none.
	collection string
	// filters are the values chosen per source, by filter key; language
	// is All sources' language.
	filters  map[string]map[string]string
	language string
	// lists are the searches or collections shown, by listKey.
	lists map[listKey]*resultList
	// browse is each source's collections.
	browse map[string]*browseState
	// selected is the result in the detail panel; full what get gave.
	selected *resultRef
	full     map[resultKey]*fullResult
	format   map[resultKey]string // the format chosen in the detail panel
	covers   map[string]*remoteCover
	focus    ui.Handle
	shelfW   float32 // a browse shelf's width in the last frame
}

// listKey names a list of results: a source's search for text, or one of
// its collections, with the filters it was asked with.
type listKey struct {
	source, text, collection, filters string
}

type resultList struct {
	items   []plugin.Result
	page    int
	cursor  string
	hasMore bool
	loading bool
	err     error
}

type browseState struct {
	loading     bool
	err         error
	collections []plugin.Collection
}

type resultKey struct{ source, id string }

type resultRef struct {
	source string
	result plugin.Result
}

type fullResult struct {
	loading bool
	result  plugin.Result
	err     error
}

type remoteCover struct {
	loading bool
	bmp     *ui.Bitmap
	// data and size are the image file and its pixels, kept by Fetch
	// metadata, which may save it as the book's cover.
	data []byte
	size image.Point
}

func newGetBooks() getBooksState {
	return getBooksState{
		filters: map[string]map[string]string{},
		lists:   map[listKey]*resultList{},
		browse:  map[string]*browseState{},
		full:    map[resultKey]*fullResult{},
		format:  map[resultKey]string{},
		covers:  map[string]*remoteCover{},
	}
}

// pluginByID is the loaded plugin with id.
func (a *App) pluginByID(id string) *plugin.Plugin {
	for _, p := range a.plugins {
		if p.Manifest.ID == id {
			return p
		}
	}
	for _, p := range a.catalogs {
		if p.Manifest.ID == id {
			return p
		}
	}
	return nil
}

// openGetBooks shows the Get books page, on the source used last.
func (a *App) openGetBooks() {
	a.page = pageGetBooks
	g := &a.gb
	if g.source == "" || g.source != allSources && a.pluginByID(g.source) == nil {
		g.source = a.settings.GetBooksSource
		if g.source != allSources && a.pluginByID(g.source) == nil {
			g.source = ""
			if s := a.sources(); len(s) > 0 {
				g.source = s[0].Manifest.ID
			}
		}
	}
	a.gbRefresh()
}

// gbSetSource picks the source to search.
func (a *App) gbSetSource(id string) {
	g := &a.gb
	g.source, g.collection = id, ""
	a.settings.GetBooksSource = id
	a.saveSettings()
	a.gbRefresh()
}

// gbSearch searches the source picked for what the search box holds.
func (a *App) gbSearch() {
	g := &a.gb
	g.query, g.collection = strings.TrimSpace(g.text), ""
	a.gbRefresh()
}

// gbOpenCollection shows all of a collection of source.
func (a *App) gbOpenCollection(source, id string) {
	g := &a.gb
	g.source, g.collection, g.query, g.text = source, id, "", ""
	a.gbRefresh()
}

// gbRefresh loads what the page shows now, as far as it isn't loaded.
func (a *App) gbRefresh() {
	g := &a.gb
	for _, p := range a.gbSources() {
		switch {
		case p.MissingSettings() != nil:
		case g.collection != "":
			a.gbLoad(p, g.collection, false)
		case g.query != "":
			a.gbLoad(p, "", false)
		case p.Manifest.HasCollections():
			a.gbBrowse(p)
		}
	}
}

// gbSources are the sources the page shows: the one picked, or all.
func (a *App) gbSources() []*plugin.Plugin {
	if a.gb.source == allSources {
		return a.sources()
	}
	if p := a.pluginByID(a.gb.source); p != nil && p.Manifest.IsBookSource() {
		return []*plugin.Plugin{p}
	}
	return nil
}

// gbFilters are the filter values for p, as the page shows them.
func (a *App) gbFilters(p *plugin.Plugin) map[string]string {
	g := &a.gb
	values := map[string]string{}
	for k, v := range g.filters[p.Manifest.ID] {
		values[k] = v
	}
	if g.source == allSources {
		// Only the language is shared, and only with sources that have it.
		values = map[string]string{}
		for _, f := range p.Manifest.Filters() {
			if f.Type == plugin.FilterLanguage {
				values[f.Key] = g.language
			}
		}
	}
	return p.FilterValues(values)
}

// gbSetFilter sets a filter of the source picked, and searches again.
func (a *App) gbSetFilter(p *plugin.Plugin, key, value string) {
	g := &a.gb
	if g.filters[p.Manifest.ID] == nil {
		g.filters[p.Manifest.ID] = map[string]string{}
	}
	g.filters[p.Manifest.ID][key] = value
	a.gbRefresh()
}

// gbSetLanguage sets All sources' language, and searches again.
func (a *App) gbSetLanguage(code string) {
	a.gb.language = code
	a.gbRefresh()
}

func filtersKey(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k + "=" + values[k] + "&")
	}
	return b.String()
}

// gbKey is the list p shows now: its search, or the collection.
func (a *App) gbKey(p *plugin.Plugin, collection string) listKey {
	k := listKey{source: p.Manifest.ID, collection: collection, filters: filtersKey(a.gbFilters(p))}
	if collection == "" {
		k.text = a.gb.query
	}
	return k
}

// gbList is the list p shows now, nil until it is asked for.
func (a *App) gbList(p *plugin.Plugin, collection string) *resultList {
	return a.gb.lists[a.gbKey(p, collection)]
}

// gbLoad asks p for the first page of its search (collection "") or
// collection, unless it has it; more asks for the next page.
func (a *App) gbLoad(p *plugin.Plugin, collection string, more bool) {
	k := a.gbKey(p, collection)
	l := a.gb.lists[k]
	if l == nil {
		l = &resultList{}
		a.gb.lists[k] = l
	}
	switch {
	case l.loading:
		return
	case more && !l.hasMore:
		return
	case !more && (l.page > 0 || l.err != nil):
		return // loaded, or failed: gbRetry asks again
	}
	l.loading, l.err = true, nil
	q := plugin.Query{Text: k.text, Page: l.page + 1, Cursor: l.cursor, Filters: a.gbFilters(p), Collection: collection}
	a.run(func() {
		var pg plugin.Page
		var err error
		if collection != "" {
			pg, err = p.List(context.Background(), q)
		} else {
			pg, err = p.Search(context.Background(), q)
		}
		a.post(func() {
			l.loading = false
			if err != nil {
				l.err = err
				return
			}
			l.page = q.Page
			// A source paging by number may return books already shown.
			seen := map[string]bool{}
			for _, r := range l.items {
				seen[r.ID] = true
			}
			for _, r := range pg.Items {
				if !seen[r.ID] {
					l.items = append(l.items, r)
				}
			}
			l.cursor, l.hasMore = pg.Cursor, pg.HasMore
		})
	})
}

// gbRetry asks again for a list that failed.
func (a *App) gbRetry(p *plugin.Plugin, collection string) {
	if l := a.gbList(p, collection); l != nil && !l.loading {
		delete(a.gb.lists, a.gbKey(p, collection))
	}
	a.gbLoad(p, collection, false)
}

// gbBrowse loads p's collections and the first page of each.
func (a *App) gbBrowse(p *plugin.Plugin) {
	id := p.Manifest.ID
	b := a.gb.browse[id]
	if b == nil {
		b = &browseState{loading: true}
		a.gb.browse[id] = b
		a.run(func() {
			cs, err := p.Collections(context.Background())
			a.post(func() {
				b.loading, b.collections, b.err = false, cs, err
				a.gbBrowse(p)
			})
		})
		return
	}
	for i, c := range b.collections {
		if a.gb.source == allSources && i > 0 {
			break // All sources shows each source's first collection
		}
		a.gbLoad(p, c.ID, false)
	}
}

// gbSelect shows r of source in the detail panel, asking the source for
// all it knows about it.
func (a *App) gbSelect(source string, r plugin.Result) {
	a.gb.selected = &resultRef{source: source, result: r}
	k := resultKey{source, r.ID}
	if f := a.gb.full[k]; f != nil && (f.loading || f.err == nil) {
		return
	}
	p := a.pluginByID(source)
	if p == nil {
		return
	}
	f := &fullResult{loading: true, result: r}
	a.gb.full[k] = f
	a.run(func() {
		full, err := p.Get(context.Background(), r)
		a.post(func() { f.loading, f.result, f.err = false, full, err })
	})
}

// gbFull is the most the page knows about r: what get returned, once it
// has.
func (a *App) gbFull(source string, r plugin.Result) plugin.Result {
	if f := a.gb.full[resultKey{source, r.ID}]; f != nil && !f.loading && f.err == nil {
		return f.result
	}
	return r
}

// inLibrary is the book in the library that r is, by identifier: the
// source's own id or an ISBN. 0 when none.
func (a *App) inLibrary(r plugin.Result) int64 {
	for k, v := range r.Identifiers {
		if id := a.idIndex[strings.ToLower(k)+":"+strings.ToLower(v)]; id != 0 && a.byID[id] != nil {
			return id
		}
	}
	return 0
}

// downloadOf is the download of r in progress or failed last, if any.
func (a *App) downloadOf(source string, r plugin.Result) *download {
	for i := len(a.downloads) - 1; i >= 0; i-- {
		d := a.downloads[i]
		if d.plugin.Manifest.ID == source && d.result.ID == r.ID {
			return d
		}
	}
	return nil
}

// resultCover is r's cover image, nil while it loads or when there is
// none. Covers come through the plugin, from its own hosts.
func (a *App) resultCover(source string, r plugin.Result) *ui.Bitmap {
	if r.Cover == "" {
		return nil
	}
	rc := a.gb.covers[r.Cover]
	if rc != nil {
		return rc.bmp
	}
	p := a.pluginByID(source)
	if p == nil {
		return nil
	}
	rc = &remoteCover{loading: true}
	a.gb.covers[r.Cover] = rc
	a.run(func() {
		coverSlots <- struct{}{}
		data, err := p.Cover(context.Background(), r)
		<-coverSlots
		var bmp *ui.Bitmap
		if err == nil && len(data) > 0 {
			if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
				bmp = ui.NewBitmap(img)
			}
		}
		a.post(func() { rc.loading, rc.bmp = false, bmp })
	})
	return nil
}

// coverSlots bounds the covers fetched at once, to be polite to sources.
var coverSlots = make(chan struct{}, 4)

// sourceState says whether p can be searched: "" when it can, else why
// not, as the error kind that decides what the page offers.
func sourceState(p *plugin.Plugin, err error) plugin.ErrorKind {
	if p.MissingSettings() != nil {
		return plugin.KindSettings
	}
	var pe *plugin.Error
	if errors.As(err, &pe) {
		return pe.Kind
	}
	if err != nil {
		return plugin.KindUnavailable
	}
	return ""
}

// openURL opens a web page in the user's browser.
func openURL(url string) {
	go mygo.Shell.OpenExternal(url)
}
