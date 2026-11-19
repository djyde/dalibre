// Package catalog filters, sorts and counts a library's books for the
// views, independently of how they are drawn.
package catalog

import (
	"cmp"
	"slices"
	"strings"
	"time"

	"dalibre/internal/calibre"
)

// Shelf is a fixed view in the sidebar.
type Shelf int

const (
	AllBooks Shelf = iota
	RecentlyAdded
)

// FilterKind is the kind of a sidebar filter.
type FilterKind int

const (
	NoFilter FilterKind = iota
	ByFormat
	ByAuthor
	BySeries
	ByTag
	ByPublisher
	ByLanguage
)

// Filter narrows the books to those with a value.
type Filter struct {
	Kind  FilterKind
	Value string
}

// SortKey orders the books.
type SortKey int

const (
	SortDateAdded SortKey = iota
	SortTitle
	SortAuthor
	SortSeries
	SortRating
	SortPublished
	SortSize
	SortModified
	SortFormats
)

// SortKeys lists the orders with their labels, for menus.
var SortKeys = []struct {
	Key   SortKey
	Label string
}{
	{SortDateAdded, "Date added"}, {SortTitle, "Title"}, {SortAuthor, "Author"}, {SortSeries, "Series"},
	{SortRating, "Rating"}, {SortFormats, "Formats"}, {SortPublished, "Published"}, {SortSize, "Size"}, {SortModified, "Modified"},
}

// Label returns the sort key's label.
func (k SortKey) Label() string {
	for _, s := range SortKeys {
		if s.Key == k {
			return s.Label
		}
	}
	return ""
}

// Query is everything that decides which books show, in which order.
type Query struct {
	Shelf      Shelf
	Filter     Filter
	Search     string
	Sort       SortKey
	Descending bool
}

// RecentDays is how far back "Recently added" reaches.
const RecentDays = 30

// Apply returns the books the query selects, ordered.
func Apply(books []*calibre.Book, q Query, now time.Time) []*calibre.Book {
	terms := strings.Fields(strings.ToLower(q.Search))
	out := make([]*calibre.Book, 0, len(books))
	for _, b := range books {
		if q.Shelf == RecentlyAdded && now.Sub(b.Timestamp) > RecentDays*24*time.Hour {
			continue
		}
		if !matchFilter(b, q.Filter) || !matchSearch(b, terms) {
			continue
		}
		out = append(out, b)
	}
	slices.SortStableFunc(out, func(a, b *calibre.Book) int {
		c := compare(a, b, q.Sort)
		if c == 0 {
			c = cmp.Compare(strings.ToLower(a.Sort), strings.ToLower(b.Sort))
		}
		if q.Descending {
			return -c
		}
		return c
	})
	return out
}

func containsFold(list []string, v string) bool {
	return slices.ContainsFunc(list, func(s string) bool { return strings.EqualFold(s, v) })
}

func matchFilter(b *calibre.Book, f Filter) bool {
	switch f.Kind {
	case ByFormat:
		return containsFold(b.Formats, f.Value)
	case ByAuthor:
		return containsFold(b.Authors, f.Value)
	case BySeries:
		return strings.EqualFold(b.Series, f.Value)
	case ByTag:
		return containsFold(b.Tags, f.Value)
	case ByPublisher:
		return strings.EqualFold(b.Publisher, f.Value)
	case ByLanguage:
		return containsFold(b.Languages, f.Value)
	}
	return true
}

// matchSearch requires every term in the title, authors, series, tags or
// publisher.
func matchSearch(b *calibre.Book, terms []string) bool {
	if len(terms) == 0 {
		return true
	}
	hay := strings.ToLower(strings.Join([]string{
		b.Title, strings.Join(b.Authors, " "), b.Series, strings.Join(b.Tags, " "), b.Publisher, strings.Join(b.Formats, " "),
	}, "\x00"))
	for _, t := range terms {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

func compare(a, b *calibre.Book, k SortKey) int {
	switch k {
	case SortTitle:
		return cmp.Compare(strings.ToLower(a.Sort), strings.ToLower(b.Sort))
	case SortAuthor:
		return cmp.Compare(strings.ToLower(a.AuthorSort), strings.ToLower(b.AuthorSort))
	case SortSeries:
		if c := cmp.Compare(strings.ToLower(a.Series), strings.ToLower(b.Series)); c != 0 {
			return c
		}
		return cmp.Compare(a.SeriesIndex, b.SeriesIndex)
	case SortRating:
		return cmp.Compare(a.Rating, b.Rating)
	case SortFormats:
		return cmp.Compare(strings.ToLower(strings.Join(a.Formats, ", ")), strings.ToLower(strings.Join(b.Formats, ", ")))
	case SortPublished:
		return a.Pubdate.Compare(b.Pubdate)
	case SortSize:
		return cmp.Compare(a.Size, b.Size)
	case SortModified:
		return a.LastModified.Compare(b.LastModified)
	default:
		return a.Timestamp.Compare(b.Timestamp)
	}
}

// Count is a value and how many books have it.
type Count struct {
	Value string
	N     int
}

// Counts are the sidebar's numbers.
type Counts struct {
	All, Recent                 int
	Formats, Authors, Series    []Count
	Tags, Publishers, Languages []Count
}

// Tally counts the books by format, author, series, tag, publisher and
// language. Formats are ordered EPUB, PDF, MOBI, AZW3 first.
func Tally(books []*calibre.Book, now time.Time) Counts {
	c := Counts{All: len(books)}
	maps := map[string]map[string]int{}
	add := func(kind, v string) {
		if v == "" {
			return
		}
		m := maps[kind]
		if m == nil {
			m = map[string]int{}
			maps[kind] = m
		}
		m[v]++
	}
	for _, b := range books {
		if now.Sub(b.Timestamp) <= RecentDays*24*time.Hour {
			c.Recent++
		}
		for _, f := range b.Formats {
			add("format", f)
		}
		for _, a := range b.Authors {
			add("author", a)
		}
		add("series", b.Series)
		for _, t := range b.Tags {
			add("tag", t)
		}
		add("publisher", b.Publisher)
		for _, l := range b.Languages {
			add("language", l)
		}
	}
	sorted := func(kind string) []Count {
		var out []Count
		for v, n := range maps[kind] {
			out = append(out, Count{v, n})
		}
		slices.SortFunc(out, func(a, b Count) int { return cmp.Compare(strings.ToLower(a.Value), strings.ToLower(b.Value)) })
		return out
	}
	c.Formats = sorted("format")
	rank := map[string]int{"EPUB": 0, "PDF": 1, "MOBI": 2, "AZW3": 3}
	slices.SortStableFunc(c.Formats, func(a, b Count) int {
		ra, oka := rank[a.Value]
		rb, okb := rank[b.Value]
		if !oka {
			ra = 99
		}
		if !okb {
			rb = 99
		}
		return cmp.Compare(ra, rb)
	})
	c.Authors, c.Series, c.Tags = sorted("author"), sorted("series"), sorted("tag")
	c.Publishers, c.Languages = sorted("publisher"), sorted("language")
	return c
}
