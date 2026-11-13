// Package ebookmeta reads the title, authors and cover that e-book files
// carry, to fill in a book's metadata when it is added.
package ebookmeta

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding/charmap"
)

// Meta is what a file says about itself; empty fields are unknown.
type Meta struct {
	Title       string
	Authors     []string
	Publisher   string
	Language    string // as the file gives it, e.g. "en" or "zh-CN"
	Description string
	Tags        []string
	ISBN        string
	Pubdate     time.Time
	Series      string
	SeriesIndex float64
	Cover       []byte
	// ASIN and CDEType are a Kindle book's (MOBI EXTH 113 and 501): its
	// Amazon id, or the one calibre gave it, and its kind, as "EBOK".
	ASIN, CDEType string
}

// Read extracts what it can from the file, falling back to its name.
func Read(file string) (Meta, error) {
	var m Meta
	var err error
	switch strings.ToLower(filepath.Ext(file)) {
	case ".epub":
		m, err = readEPUB(file)
	case ".mobi", ".azw3", ".azw", ".prc":
		m, err = readMOBI(file)
	}
	fromName := FromFileName(file)
	if m.Title == "" {
		m.Title = fromName.Title
	}
	if len(m.Authors) == 0 {
		m.Authors = fromName.Authors
	}
	return m, err
}

// FromFileName guesses "Title - Author.ext" as calibre's default.
func FromFileName(file string) Meta {
	base := strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
	base = strings.ReplaceAll(base, "_", " ")
	if title, author, ok := strings.Cut(base, " - "); ok {
		return Meta{Title: strings.TrimSpace(title), Authors: []string{strings.TrimSpace(author)}}
	}
	return Meta{Title: strings.TrimSpace(base)}
}

// ---- EPUB

type opfPackage struct {
	Metadata struct {
		Titles   []string `xml:"title"`
		Creators []struct {
			Name string `xml:",chardata"`
			Role string `xml:"role,attr"`
			ID   string `xml:"id,attr"`
		} `xml:"creator"`
		Publisher   string   `xml:"publisher"`
		Language    []string `xml:"language"`
		Description string   `xml:"description"`
		Subjects    []string `xml:"subject"`
		Date        []string `xml:"date"`
		Identifiers []struct {
			Value  string `xml:",chardata"`
			Scheme string `xml:"scheme,attr"`
		} `xml:"identifier"`
		Metas []struct {
			Name     string `xml:"name,attr"`
			Content  string `xml:"content,attr"`
			Property string `xml:"property,attr"`
			Refines  string `xml:"refines,attr"`
			Value    string `xml:",chardata"`
		} `xml:"meta"`
	} `xml:"metadata"`
	Manifest []struct {
		ID         string `xml:"id,attr"`
		Href       string `xml:"href,attr"`
		MediaType  string `xml:"media-type,attr"`
		Properties string `xml:"properties,attr"`
	} `xml:"manifest>item"`
	Guide []struct {
		Type string `xml:"type,attr"`
		Href string `xml:"href,attr"`
	} `xml:"guide>reference"`
}

var isbnRe = regexp.MustCompile(`(?i)^(?:urn:isbn:|isbn:?)?\s*([0-9Xx-]{10,17})$`)

func readEPUB(file string) (Meta, error) {
	var m Meta
	zr, err := zip.OpenReader(file)
	if err != nil {
		return m, err
	}
	defer zr.Close()
	read := func(name string) ([]byte, error) {
		for _, f := range zr.File {
			if f.Name == name || strings.EqualFold(f.Name, name) {
				rc, err := f.Open()
				if err != nil {
					return nil, err
				}
				defer rc.Close()
				return io.ReadAll(io.LimitReader(rc, 32<<20))
			}
		}
		return nil, os.ErrNotExist
	}
	container, err := read("META-INF/container.xml")
	if err != nil {
		return m, err
	}
	var c struct {
		Rootfiles []struct {
			Path string `xml:"full-path,attr"`
		} `xml:"rootfiles>rootfile"`
	}
	if err := xml.Unmarshal(container, &c); err != nil || len(c.Rootfiles) == 0 {
		return m, errors.New("epub: no rootfile")
	}
	opfPath := c.Rootfiles[0].Path
	data, err := read(opfPath)
	if err != nil {
		return m, err
	}
	var p opfPackage
	if err := xml.Unmarshal(data, &p); err != nil {
		return m, err
	}
	md := p.Metadata
	if len(md.Titles) > 0 {
		m.Title = clean(md.Titles[0])
	}
	// EPUB 3 gives roles by refinement; EPUB 2 by opf:role.
	roles := map[string]string{}
	for _, mt := range md.Metas {
		if mt.Property == "role" && strings.HasPrefix(mt.Refines, "#") {
			roles[mt.Refines[1:]] = strings.TrimSpace(mt.Value)
		}
	}
	for _, cr := range md.Creators {
		role := cr.Role
		if r, ok := roles[cr.ID]; ok {
			role = r
		}
		if role == "" || role == "aut" {
			if n := clean(cr.Name); n != "" {
				m.Authors = append(m.Authors, n)
			}
		}
	}
	m.Publisher = clean(md.Publisher)
	if len(md.Language) > 0 {
		m.Language = clean(md.Language[0])
	}
	m.Description = strings.TrimSpace(md.Description)
	for _, s := range md.Subjects {
		if s = clean(s); s != "" {
			m.Tags = append(m.Tags, s)
		}
	}
	for _, d := range md.Date {
		if t := parseDate(d); !t.IsZero() {
			m.Pubdate = t
			break
		}
	}
	for _, id := range md.Identifiers {
		v := strings.TrimSpace(id.Value)
		if strings.EqualFold(id.Scheme, "isbn") || strings.HasPrefix(strings.ToLower(v), "urn:isbn:") {
			if mm := isbnRe.FindStringSubmatch(v); mm != nil {
				m.ISBN = strings.ReplaceAll(mm[1], "-", "")
			}
		}
	}
	coverID := ""
	for _, mt := range md.Metas {
		switch mt.Name {
		case "calibre:series":
			m.Series = mt.Content
		case "calibre:series_index":
			m.SeriesIndex, _ = strconv.ParseFloat(mt.Content, 64)
		case "cover":
			coverID = mt.Content
		}
	}
	dir := path.Dir(opfPath)
	resolve := func(href string) string {
		if dir == "." {
			return href
		}
		return path.Join(dir, href)
	}
	var coverHref string
	for _, it := range p.Manifest {
		if strings.Contains(it.Properties, "cover-image") {
			coverHref = it.Href
		}
	}
	if coverHref == "" && coverID != "" {
		for _, it := range p.Manifest {
			if it.ID == coverID && strings.HasPrefix(it.MediaType, "image/") {
				coverHref = it.Href
			}
		}
	}
	if coverHref == "" {
		for _, it := range p.Manifest {
			if strings.HasPrefix(it.MediaType, "image/") && strings.Contains(strings.ToLower(it.ID+it.Href), "cover") {
				coverHref = it.Href
				break
			}
		}
	}
	if coverHref != "" {
		if img, err := read(resolve(unescapeHref(coverHref))); err == nil {
			m.Cover = img
		}
	}
	return m, nil
}

func unescapeHref(h string) string { return strings.ReplaceAll(h, "%20", " ") }

func clean(s string) string { return strings.Join(strings.Fields(s), " ") }

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05", "2006-01-02", "2006-01", "2006"} {
		if t, err := time.Parse(layout, s); err == nil && t.Year() > 101 {
			return t
		}
	}
	return time.Time{}
}

// ---- MOBI / AZW3 (MOBI header with EXTH)

func readMOBI(file string) (Meta, error) {
	var m Meta
	data, err := os.ReadFile(file)
	if err != nil {
		return m, err
	}
	if len(data) < 78 || string(data[60:68]) != "BOOKMOBI" {
		return m, errors.New("mobi: not a MOBI file")
	}
	numRecords := int(binary.BigEndian.Uint16(data[76:78]))
	recordOffset := func(i int) int {
		if i < 0 || i >= numRecords || 78+8*i+4 > len(data) {
			return -1
		}
		return int(binary.BigEndian.Uint32(data[78+8*i:]))
	}
	recordBytes := func(i int) []byte {
		start, end := recordOffset(i), recordOffset(i+1)
		if start < 0 || start > len(data) {
			return nil
		}
		if end < 0 || end > len(data) {
			end = len(data)
		}
		return data[start:end]
	}
	r0 := recordBytes(0)
	if len(r0) < 132 || string(r0[16:20]) != "MOBI" {
		return m, errors.New("mobi: no MOBI header")
	}
	headerLen := int(binary.BigEndian.Uint32(r0[20:24]))
	encoding := binary.BigEndian.Uint32(r0[28:32])
	decode := func(b []byte) string {
		if encoding == 1252 || !utf8.Valid(b) {
			if s, err := charmap.Windows1252.NewDecoder().Bytes(b); err == nil {
				return string(s)
			}
		}
		return string(b)
	}
	nameOff, nameLen := int(binary.BigEndian.Uint32(r0[84:88])), int(binary.BigEndian.Uint32(r0[88:92]))
	if nameOff > 0 && nameOff+nameLen <= len(r0) {
		m.Title = clean(decode(r0[nameOff : nameOff+nameLen]))
	}
	firstImage := int(binary.BigEndian.Uint32(r0[108:112]))
	exthFlags := binary.BigEndian.Uint32(r0[128:132])
	coverOffset := -1
	if exthFlags&0x40 != 0 {
		ex := r0[16+headerLen:]
		if len(ex) >= 12 && string(ex[0:4]) == "EXTH" {
			count := int(binary.BigEndian.Uint32(ex[8:12]))
			pos := 12
			for i := 0; i < count && pos+8 <= len(ex); i++ {
				typ := binary.BigEndian.Uint32(ex[pos:])
				size := int(binary.BigEndian.Uint32(ex[pos+4:]))
				if size < 8 || pos+size > len(ex) {
					break
				}
				val := ex[pos+8 : pos+size]
				switch typ {
				case 100:
					if a := clean(decode(val)); a != "" {
						m.Authors = append(m.Authors, a)
					}
				case 101:
					m.Publisher = clean(decode(val))
				case 103:
					m.Description = strings.TrimSpace(decode(val))
				case 104:
					m.ISBN = strings.ReplaceAll(clean(decode(val)), "-", "")
				case 105:
					for _, t := range strings.Split(decode(val), ";") {
						if t = clean(t); t != "" {
							m.Tags = append(m.Tags, t)
						}
					}
				case 106:
					m.Pubdate = parseDate(decode(val))
				case 503:
					m.Title = clean(decode(val))
				case 524:
					m.Language = clean(decode(val))
				case 113:
					m.ASIN = clean(decode(val))
				case 501:
					m.CDEType = clean(decode(val))
				case 201:
					if len(val) >= 4 {
						coverOffset = int(binary.BigEndian.Uint32(val))
					}
				}
				pos += size
			}
		}
	}
	if coverOffset >= 0 && firstImage > 0 && firstImage != 0xffffffff {
		if img := recordBytes(firstImage + coverOffset); isImage(img) {
			m.Cover = bytes.Clone(img)
		}
	}
	return m, nil
}

func isImage(b []byte) bool {
	return bytes.HasPrefix(b, []byte{0xff, 0xd8}) || bytes.HasPrefix(b, []byte("\x89PNG")) || bytes.HasPrefix(b, []byte("GIF8"))
}
