package catalog

import (
	"testing"
	"time"

	"dalibre/internal/calibre"
)

var now = time.Date(2026, 11, 8, 0, 0, 0, 0, time.UTC)

func books() []*calibre.Book {
	return []*calibre.Book{
		{ID: 1, Title: "The Three-Body Problem", Sort: "Three-Body Problem, The", Authors: []string{"Liu Cixin"}, AuthorSort: "Cixin, Liu",
			Series: "Remembrance", SeriesIndex: 1, Tags: []string{"SF"}, Formats: []string{"EPUB", "MOBI"}, Rating: 8, Timestamp: now.AddDate(0, 0, -2)},
		{ID: 2, Title: "The Dark Forest", Sort: "Dark Forest, The", Authors: []string{"Liu Cixin"}, AuthorSort: "Cixin, Liu",
			Series: "Remembrance", SeriesIndex: 2, Tags: []string{"SF"}, Formats: []string{"EPUB"}, Rating: 10, Timestamp: now.AddDate(0, -3, 0)},
		{ID: 3, Title: "Dune", Sort: "Dune", Authors: []string{"Frank Herbert"}, AuthorSort: "Herbert, Frank",
			Formats: []string{"PDF"}, Timestamp: now.AddDate(-1, 0, 0)},
	}
}

func ids(bs []*calibre.Book) []int64 {
	var out []int64
	for _, b := range bs {
		out = append(out, b.ID)
	}
	return out
}

func TestApply(t *testing.T) {
	cases := []struct {
		q    Query
		want []int64
	}{
		{Query{}, []int64{3, 2, 1}},
		{Query{Descending: true}, []int64{1, 2, 3}},
		{Query{Sort: SortTitle}, []int64{2, 3, 1}},
		{Query{Sort: SortSeries}, []int64{3, 1, 2}},
		{Query{Sort: SortFormats}, []int64{2, 1, 3}},
		{Query{Sort: SortFormats, Descending: true}, []int64{3, 1, 2}},
		{Query{Shelf: RecentlyAdded}, []int64{1}},
		{Query{Filter: Filter{ByFormat, "epub"}, Sort: SortTitle}, []int64{2, 1}},
		{Query{Filter: Filter{ByAuthor, "Frank Herbert"}}, []int64{3}},
		{Query{Search: "liu forest"}, []int64{2}},
		{Query{Search: "pdf"}, []int64{3}},
	}
	for _, c := range cases {
		if got := ids(Apply(books(), c.q, now)); !equal(got, c.want) {
			t.Errorf("%+v: got %v want %v", c.q, got, c.want)
		}
	}
}

func equal(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTally(t *testing.T) {
	c := Tally(books(), now)
	if c.All != 3 || c.Recent != 1 {
		t.Errorf("all %d recent %d", c.All, c.Recent)
	}
	if len(c.Formats) != 3 || c.Formats[0].Value != "EPUB" || c.Formats[0].N != 2 || c.Formats[1].Value != "PDF" {
		t.Errorf("formats %v", c.Formats)
	}
	if len(c.Authors) != 2 || c.Authors[1].Value != "Liu Cixin" || c.Authors[1].N != 2 {
		t.Errorf("authors %v", c.Authors)
	}
}
