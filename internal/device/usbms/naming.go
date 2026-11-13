package usbms

import (
	"math"
	"mime"
	"path"
	"strconv"
	"strings"

	"dalibre/internal/calibre"
	"dalibre/internal/device"
)

// DefaultTemplate is calibre's save template for readers: a folder per
// author, as calibre sorts them, and "Title - Authors" in it.
const DefaultTemplate = "{author_sort}/{title} - {authors}"

// maxPathLen is calibre's limit on a book's path on a reader, from the
// reader's mount point (USBMS.MAX_PATH_LEN).
const maxPathLen = 250

// uploadPath is where calibre puts book b, of extension ext (".azw3"), on
// a reader: the template's fields, each made a safe ASCII file name
// (ascii_filename), the whole shortened to fit maxPathLen less prefix,
// under dir.
func uploadPath(template, dir string, subDirs bool, b device.Book, ext string, prefix int) string {
	authors := b.Authors
	if len(authors) == 0 {
		authors = []string{"Unknown"}
	}
	authorSort := b.AuthorSort
	if authorSort == "" {
		authorSort = calibre.AuthorsSort(authors)
	}
	fields := map[string]string{
		"title":        b.Title,
		"authors":      strings.Join(authors, " & "),
		"author_sort":  authorSort,
		"series":       b.Series,
		"publisher":    b.Publisher,
		"tags":         strings.Join(b.Tags, ", "),
		"uuid":         b.UUID,
		"title_sort":   b.TitleSort,
		"series_index": seriesIndex(b),
	}
	var comps []string
	for _, part := range strings.Split(template, "/") {
		s := expand(part, fields)
		s = calibre.ASCIIFilename(s)
		if s == "" {
			s = "Unknown"
		}
		comps = append(comps, s)
	}
	if !subDirs {
		comps = comps[len(comps)-1:]
	}
	comps[len(comps)-1] += strings.ToLower(ext)
	limit := maxPathLen - prefix
	if dir != "" {
		limit -= len(dir) + 1
	}
	comps = shortenComponents(limit, comps)
	return path.Join(append([]string{dir}, comps...)...)
}

func seriesIndex(b device.Book) string {
	if b.Series == "" {
		return ""
	}
	return strconv.FormatFloat(b.SeriesIndex, 'f', -1, 64)
}

// expand fills a template component's {field}s. A field the book doesn't
// have leaves nothing, as calibre's templates do.
func expand(s string, fields map[string]string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '{')
		if i < 0 {
			b.WriteString(s)
			break
		}
		j := strings.IndexByte(s[i:], '}')
		if j < 0 {
			b.WriteString(s)
			break
		}
		b.WriteString(s[:i])
		b.WriteString(fields[strings.TrimSpace(s[i+1:i+j])])
		s = s[i+j+1:]
	}
	return strings.TrimSpace(b.String())
}

// shortenComponents is calibre's shorten_components_to: when the
// components joined are longer than length, each loses its share of the
// excess from its middle, and the last keeps its extension.
func shortenComponents(length int, comps []string) []string {
	joined := strings.Join(comps, "/")
	extra := len(joined) - length
	if extra < 1 || length <= 0 {
		return comps
	}
	out := make([]string, len(comps))
	for i, x := range comps {
		delta := int(math.Ceil(float64(len(x)) / float64(len(joined)) * float64(extra)))
		last := i == len(comps)-1
		var r string
		switch {
		case delta > len(x):
			if last {
				r = x[:1]
			}
		case last:
			ext := path.Ext(x)
			base := strings.TrimSuffix(x, ext)
			r = shortenComponent(base, delta) + ext
			if strings.HasPrefix(r, ".") {
				r = x[:1] + r
			}
		default:
			r = shortenComponent(x, delta)
		}
		r = strings.TrimSpace(r)
		if r == "" {
			if t := strings.TrimSpace(x); t != "" {
				r = t[:1]
			} else {
				r = "x"
			}
		}
		out[i] = r
	}
	return out
}

// shortenComponent is calibre's shorten_component: s without byWhat bytes
// from its middle.
func shortenComponent(s string, byWhat int) string {
	l := len(s)
	if l < byWhat {
		return s
	}
	l = (l - byWhat) / 2
	if l <= 0 {
		return s
	}
	return s[:l] + s[len(s)-l:]
}

// mimeTypes are calibre's MIME types of the formats readers take (its
// mime.types), which metadata.calibre records.
var mimeTypes = map[string]string{
	".azw":   "application/x-mobipocket-ebook",
	".mobi":  "application/x-mobipocket-ebook",
	".prc":   "application/x-mobipocket-ebook",
	".pobi":  "application/x-mobipocket-ebook",
	".azw3":  "application/x-mobi8-ebook",
	".azw4":  "application/x-mobipocket-ebook",
	".azw1":  "application/x-topaz-ebook",
	".tpz":   "application/x-topaz-ebook",
	".kfx":   "application/x-mobi8-ebook",
	".epub":  "application/epub+zip",
	".kepub": "application/epub+zip",
	".pdf":   "application/pdf",
	".txt":   "text/plain",
	".fb2":   "application/x-fictionbook+xml",
	".cbz":   "application/x-cbz",
	".cbr":   "application/x-cbr",
	".djvu":  "image/vnd.djvu",
	".rtf":   "application/rtf",
	".html":  "text/html",
}

func mimeOf(lpath string) string {
	ext := strings.ToLower(path.Ext(lpath))
	if m, ok := mimeTypes[ext]; ok {
		return m
	}
	if m := mime.TypeByExtension(ext); m != "" {
		return m
	}
	return "application/octet-stream"
}
