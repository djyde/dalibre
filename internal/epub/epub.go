// Package epub opens an e-book's files for the book editor: the files of
// an EPUB (or KEPUB) zip, its spine in reading order with titles and word
// counts, and edits kept in memory until Save writes the book back. A TXT
// file opens as a book of one file; an AZW3 opens as the files its KF8
// records hold, and saves back to KF8.
package epub

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"mime"
	"net/url"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode"
)

// Item is a file of the spine, in reading order.
type Item struct {
	Name  string // the file's path in the book, as "OEBPS/Text/ch1.xhtml"
	Title string // from the table of contents, the file's own title or heading, or its name
	Words int
}

// File is a file of the book.
type File struct {
	Name string
	Size int64
	Type string // media type, as "application/xhtml+xml"
}

// Book is an opened e-book. Its methods are safe from any goroutine: the
// editor's web view reads it while the window edits it.
type Book struct {
	mu      sync.Mutex
	path    string
	zipped  bool
	k       *kf8   // an AZW3's KF8 records, which its files are rebuilt from
	raw     []byte // the file as opened, for copying unchanged entries
	files   []*entry
	byName  map[string]*entry
	opf     string
	spine   []Item
	changed bool
}

type entry struct {
	name    string
	zf      *zip.File // nil for a TXT book's one file
	data    []byte    // read lazily; set by Set
	read    bool
	edited  bool
	media   string
	size    int64
	deflate bool
}

// ErrNotEPUB means the file is not a zip with an OPF package.
var ErrNotEPUB = errors.New("epub: not an EPUB")

// Open opens the book at file: an EPUB or KEPUB zip, an AZW3, or a .txt
// file.
func Open(file string) (*Book, error) {
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	b := &Book{path: file, raw: raw, byName: map[string]*entry{}}
	if strings.EqualFold(path.Ext(file), ".txt") {
		name := path.Base(strings.ReplaceAll(file, "\\", "/"))
		e := &entry{name: name, data: raw, read: true, media: "text/plain", size: int64(len(raw))}
		b.files, b.byName[name] = []*entry{e}, e
		b.spine = []Item{{Name: name, Title: strings.TrimSuffix(name, path.Ext(name)), Words: CountWords(string(raw))}}
		return b, nil
	}
	if isMOBI(raw) {
		if err := b.openKF8(); err != nil {
			return nil, err
		}
		return b, nil
	}
	zr, err := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrNotEPUB, err)
	}
	b.zipped = true
	for _, f := range zr.File {
		if strings.HasSuffix(f.Name, "/") {
			continue // a directory entry
		}
		e := &entry{name: f.Name, zf: f, size: int64(f.UncompressedSize64), deflate: f.Method != zip.Store}
		b.files = append(b.files, e)
		b.byName[f.Name] = e
	}
	if err := b.readPackage(); err != nil {
		return nil, err
	}
	return b, nil
}

// PackageDir is the folder of the book's OPF, where its hrefs start.
func (b *Book) PackageDir() string { return path.Dir(b.opf) }

// Size is the size of the book's file, as last opened or saved.
func (b *Book) Size() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return int64(len(b.raw))
}

// Path is the file the book was opened from.
func (b *Book) Path() string { return b.path }

// Packaged reports whether the book is a package of files (an EPUB or an
// AZW3) rather than one text file.
func (b *Book) Packaged() bool { return b.zipped || b.k != nil }

// Spine returns the book's reading order.
func (b *Book) Spine() []Item {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Item(nil), b.spine...)
}

// Files returns every file of the book, sorted by name.
func (b *Book) Files() []File {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]File, 0, len(b.files))
	for _, e := range b.files {
		out = append(out, File{Name: e.name, Size: e.size, Type: e.media})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Type returns a file's media type: the package's, else its extension's.
func (b *Book) Type(name string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	if e := b.byName[name]; e != nil {
		return e.media
	}
	return typeByExt(name)
}

// Has reports whether the book has a file of that name.
func (b *Book) Has(name string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.byName[name] != nil
}

// Read returns a file's contents, with the edits made to it.
func (b *Book) Read(name string) ([]byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.byName[name]
	if e == nil {
		return nil, os.ErrNotExist
	}
	return b.load(e)
}

func (b *Book) load(e *entry) ([]byte, error) {
	if e.read {
		return e.data, nil
	}
	rc, err := e.zf.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	e.data, e.read = data, true
	return data, nil
}

// Set replaces a file's contents; Save writes them.
func (b *Book) Set(name string, data []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.byName[name]
	if e == nil {
		return os.ErrNotExist
	}
	old, err := b.load(e)
	if err != nil {
		return err
	}
	if bytes.Equal(old, data) {
		return nil
	}
	e.data, e.edited, e.size = data, true, int64(len(data))
	b.changed = true
	for i, it := range b.spine {
		if it.Name == name {
			b.spine[i].Words = countMarkupWords(data)
		}
	}
	return nil
}

// Changed reports whether Set changed a file since the book was opened or
// saved.
func (b *Book) Changed() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.changed
}

// Save writes the book back to its file, atomically: to a file beside it,
// then over it. An EPUB keeps its entries' order, with mimetype first and
// stored, and copies the files not edited byte for byte. It returns the
// new size.
func (b *Book) Save() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	var out bytes.Buffer
	switch {
	case b.k != nil:
		data, err := b.k.write(b)
		if err != nil {
			return 0, err
		}
		out.Write(data)
	case !b.zipped:
		out.Write(b.files[0].data)
	default:
		zw := zip.NewWriter(&out)
		order := make([]*entry, 0, len(b.files))
		if m := b.byName["mimetype"]; m != nil {
			order = append(order, m)
		}
		for _, e := range b.files {
			if e.name != "mimetype" {
				order = append(order, e)
			}
		}
		for _, e := range order {
			if !e.edited && e.zf != nil && e.name != "mimetype" {
				if err := zw.Copy(e.zf); err != nil {
					return 0, err
				}
				continue
			}
			data, err := b.load(e)
			if err != nil {
				return 0, err
			}
			h := &zip.FileHeader{Name: e.name, Method: zip.Deflate, Modified: time.Now()}
			if e.zf != nil {
				h.Modified = e.zf.Modified
				if e.edited {
					h.Modified = time.Now()
				}
				h.Comment, h.Extra = e.zf.Comment, nil
			}
			if e.name == "mimetype" || !e.deflate {
				h.Method = zip.Store
			}
			w, err := zw.CreateHeader(h)
			if err != nil {
				return 0, err
			}
			if _, err := w.Write(data); err != nil {
				return 0, err
			}
		}
		if err := zw.Close(); err != nil {
			return 0, err
		}
	}
	tmp := b.path + ".dalibre-part"
	if err := writeSynced(tmp, out.Bytes()); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	if err := os.Rename(tmp, b.path); err != nil {
		os.Remove(tmp)
		return 0, err
	}
	// The saved file is the new original: unchanged entries copy from it.
	b.raw = out.Bytes()
	if b.zipped {
		zr, err := zip.NewReader(bytes.NewReader(b.raw), int64(len(b.raw)))
		if err != nil {
			return 0, err
		}
		for _, f := range zr.File {
			if e := b.byName[f.Name]; e != nil {
				e.zf, e.edited = f, false
			}
		}
	}
	b.changed = false
	return int64(out.Len()), nil
}

func writeSynced(name string, data []byte) error {
	f, err := os.Create(name)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// ---- The package: container.xml, the OPF, the table of contents.

type opfPackage struct {
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Spine struct {
		Toc   string `xml:"toc,attr"`
		Items []struct {
			IDRef string `xml:"idref,attr"`
		} `xml:"itemref"`
	} `xml:"spine"`
}

func (b *Book) readPackage() error {
	c := b.byName["META-INF/container.xml"]
	if c == nil {
		return ErrNotEPUB
	}
	data, err := b.load(c)
	if err != nil {
		return err
	}
	var container struct {
		Rootfiles []struct {
			FullPath string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := decodeXML(data, &container); err != nil || len(container.Rootfiles) == 0 {
		return ErrNotEPUB
	}
	b.opf = container.Rootfiles[0].FullPath
	oe := b.byName[b.opf]
	if oe == nil {
		return ErrNotEPUB
	}
	oe.media = "application/oebps-package+xml"
	data, err = b.load(oe)
	if err != nil {
		return err
	}
	var pkg opfPackage
	if err := decodeXML(data, &pkg); err != nil {
		return fmt.Errorf("%w: %v", ErrNotEPUB, err)
	}
	dir := path.Dir(b.opf)
	byID := map[string]string{}
	var nav, ncx string
	for _, it := range pkg.Manifest {
		name := Resolve(dir, it.Href)
		byID[it.ID] = name
		if e := b.byName[name]; e != nil && it.MediaType != "" {
			e.media = it.MediaType
		}
		if strings.Contains(" "+it.Properties+" ", " nav ") {
			nav = name
		}
		if it.MediaType == "application/x-dtbncx+xml" && (ncx == "" || it.ID == pkg.Spine.Toc) {
			ncx = name
		}
	}
	for _, e := range b.files {
		if e.media == "" {
			e.media = typeByExt(e.name)
		}
	}
	titles := map[string]string{}
	if ncx != "" {
		b.ncxTitles(ncx, titles)
	}
	if nav != "" {
		b.navTitles(nav, titles)
	}
	var names []string
	for _, ref := range pkg.Spine.Items {
		if name := byID[ref.IDRef]; b.byName[name] != nil {
			names = append(names, name)
		}
	}
	b.setSpine(names, titles)
	return nil
}

func decodeXML(data []byte, v any) error {
	d := xml.NewDecoder(bytes.NewReader(data))
	d.Strict = false
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	d.Entity = xml.HTMLEntity
	return d.Decode(v)
}

// ncxTitles maps each file to the label of its first entry in the NCX.
func (b *Book) ncxTitles(name string, titles map[string]string) {
	e := b.byName[name]
	if e == nil {
		return
	}
	data, err := b.load(e)
	if err != nil {
		return
	}
	type navPoint struct {
		Label   string `xml:"navLabel>text"`
		Content struct {
			Src string `xml:"src,attr"`
		} `xml:"content"`
		Points []navPoint `xml:"navPoint"`
	}
	var ncx struct {
		Points []navPoint `xml:"navMap>navPoint"`
	}
	if decodeXML(data, &ncx) != nil {
		return
	}
	dir := path.Dir(name)
	var walk func([]navPoint)
	walk = func(ps []navPoint) {
		for _, p := range ps {
			f := Resolve(dir, p.Content.Src)
			if _, ok := titles[f]; !ok && strings.TrimSpace(p.Label) != "" {
				titles[f] = clean(p.Label)
			}
			walk(p.Points)
		}
	}
	walk(ncx.Points)
}

var (
	navTocRe = regexp.MustCompile(`(?is)<nav\b[^>]*epub:type\s*=\s*["'][^"']*\btoc\b[^"']*["'][^>]*>(.*?)</nav>`)
	anchorRe = regexp.MustCompile(`(?is)<a\b[^>]*\bhref\s*=\s*["']([^"']*)["'][^>]*>(.*?)</a>`)
	tagRe    = regexp.MustCompile(`(?s)<[^>]*>`)
	titleRe  = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
	headRe   = regexp.MustCompile(`(?is)<h[1-3][^>]*>(.*?)</h[1-3]>`)
	bodyRe   = regexp.MustCompile(`(?is)<body[^>]*>(.*)</body>`)
	skipRe   = regexp.MustCompile(`(?is)<(script|style)[^>]*>.*?</(script|style)>`)
)

// navTitles maps each file to the text of its first link in the EPUB 3
// navigation document's toc, which wins over the NCX's.
func (b *Book) navTitles(name string, titles map[string]string) {
	e := b.byName[name]
	if e == nil {
		return
	}
	data, err := b.load(e)
	if err != nil {
		return
	}
	m := navTocRe.FindSubmatch(data)
	if m == nil {
		return
	}
	dir := path.Dir(name)
	seen := map[string]bool{}
	for _, a := range anchorRe.FindAllSubmatch(m[1], -1) {
		f := Resolve(dir, string(a[1]))
		label := clean(tagRe.ReplaceAllString(string(a[2]), " "))
		if !seen[f] && label != "" {
			titles[f] = label
			seen[f] = true
		}
	}
}

// setSpine makes the spine of these files, titled by toc's entry, else
// their first heading, else their <title> unless another file has the
// same one (as in a book split by size, where every file's is the book's),
// else their opening words, else their name.
func (b *Book) setSpine(names []string, toc map[string]string) {
	type page struct {
		name, title, doc string
		data             []byte
	}
	pages := make([]page, 0, len(names))
	docs := map[string]int{}
	for _, name := range names {
		data, _ := b.load(b.byName[name])
		p := page{name: name, title: toc[name], data: data}
		if m := titleRe.FindSubmatch(data); m != nil {
			p.doc = clean(string(m[1]))
			docs[p.doc]++
		}
		pages = append(pages, p)
	}
	for _, p := range pages {
		t := p.title
		if t == "" {
			t = firstHeading(p.data)
		}
		if t == "" && p.doc != "" && docs[p.doc] == 1 {
			t = p.doc
		}
		if t == "" {
			t = openingWords(p.data)
		}
		if t == "" {
			t = strings.TrimSuffix(path.Base(p.name), path.Ext(p.name))
		}
		b.spine = append(b.spine, Item{Name: p.name, Title: unquote(t), Words: countMarkupWords(p.data)})
	}
}

// unquote drops quotes around a whole title, as a cover page's "Cover".
func unquote(t string) string {
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"'", "'"}} {
		if len(t) > len(q[0])+len(q[1]) && strings.HasPrefix(t, q[0]) && strings.HasSuffix(t, q[1]) &&
			!strings.Contains(t[len(q[0]):len(t)-len(q[1])], q[0]) {
			return t[len(q[0]) : len(t)-len(q[1])]
		}
	}
	return t
}

func firstHeading(data []byte) string {
	if m := headRe.FindSubmatch(data); m != nil {
		return clean(tagRe.ReplaceAllString(string(m[1]), " "))
	}
	return ""
}

// openingWords is the start of a page's text, cut to about 24 characters.
func openingWords(data []byte) string {
	s := string(data)
	if m := bodyRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = clean(tagRe.ReplaceAllString(skipRe.ReplaceAllString(s, " "), " "))
	if r := []rune(s); len(r) > 24 {
		cut := 24
		for i := 24; i > 12; i-- { // end at a space where there is one
			if r[i] == ' ' {
				cut = i
				break
			}
		}
		s = strings.TrimSpace(string(r[:cut])) + "…"
	}
	return s
}

func clean(s string) string { return strings.Join(strings.Fields(html.UnescapeString(s)), " ") }

// Resolve turns an href in a file of dir into a path in the book, without
// its fragment: "../Text/ch1.xhtml#p3" from "OEBPS/Styles".
func Resolve(dir, href string) string {
	if i := strings.IndexByte(href, '#'); i >= 0 {
		href = href[:i]
	}
	if u, err := url.PathUnescape(href); err == nil {
		href = u
	}
	if href == "" {
		return ""
	}
	return strings.TrimPrefix(path.Clean(path.Join(dir, href)), "/")
}

func typeByExt(name string) string {
	switch strings.ToLower(path.Ext(name)) {
	case ".xhtml", ".xht":
		return "application/xhtml+xml"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".opf":
		return "application/oebps-package+xml"
	case ".ncx":
		return "application/x-dtbncx+xml"
	case ".xml":
		return "application/xml"
	case ".svg":
		return "image/svg+xml"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".ttf":
		return "font/ttf"
	case ".otf":
		return "font/otf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".txt":
		return "text/plain"
	case "":
		return "application/octet-stream"
	}
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return strings.SplitN(t, ";", 2)[0]
	}
	return "application/octet-stream"
}

// ---- Kinds of files

// IsPage reports whether a media type is a document the web view renders:
// XHTML, HTML, SVG or an image.
func IsPage(media string) bool { return IsMarkup(media) || IsImage(media) }

// IsMarkup reports whether a media type is an (X)HTML page.
func IsMarkup(media string) bool {
	return media == "application/xhtml+xml" || media == "text/html"
}

// IsImage reports whether a media type is an image.
func IsImage(media string) bool { return strings.HasPrefix(media, "image/") }

// IsTextType reports whether a file of this media type is text the
// editor edits: markup, styles, the package files, XML and plain text.
func IsTextType(media string) bool {
	return IsMarkup(media) || media == "text/css" || media == "text/plain" ||
		strings.HasSuffix(media, "+xml") || strings.HasSuffix(media, "/xml")
}

// PageWords counts the words of a page's body.
func PageWords(data []byte) int { return countMarkupWords(data) }

// ---- Words

// countMarkupWords counts the words of a page's body.
func countMarkupWords(data []byte) int {
	s := string(data)
	if m := bodyRe.FindStringSubmatch(s); m != nil {
		s = m[1]
	}
	s = skipRe.ReplaceAllString(s, " ")
	s = tagRe.ReplaceAllString(s, " ")
	return CountWords(html.UnescapeString(s))
}

// CountWords counts words as word processors do: runs of letters and
// digits, and each Chinese or Japanese character on its own. Korean
// separates its words with spaces, so they count as runs.
func CountWords(s string) int {
	n, in := 0, false
	for _, r := range s {
		switch {
		case unicode.Is(unicode.Han, r) || unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r):
			n++
			in = false
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' || r == '’' || r == '-':
			if !in && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
				n++
				in = true
			}
		default:
			in = false
		}
	}
	return n
}
