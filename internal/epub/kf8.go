package epub

// AZW3 (KF8): a PalmDB of records. Record 0 holds the MOBI header; the
// text records after it, compressed, are one stream of markup and flows
// (styles, SVG) that the FDST splits apart. The skeleton and fragment
// indexes cut the markup into the book's files, and resource records hold
// its images and fonts. Links between them are kindle: URLs, which opening
// rewrites to the files' names. kf8write.go writes them back.

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/text/encoding/charmap"
)

// ErrDRM means the book is encrypted, which nothing here can read.
var ErrDRM = errors.New("the book is protected by DRM")

// ErrNotKF8 means a MOBI file has no KF8 part: an old-style MOBI.
var ErrNotKF8 = errors.New("kf8: not an AZW3 (KF8) book")

const none = 0xFFFFFFFF

type palmDB struct {
	data    []byte
	offsets []int
}

func (p *palmDB) record(i int) []byte {
	if i < 0 || i >= len(p.offsets) {
		return nil
	}
	start, end := p.offsets[i], len(p.data)
	if i+1 < len(p.offsets) {
		end = p.offsets[i+1]
	}
	if start > end || end > len(p.data) {
		return nil
	}
	return p.data[start:end]
}

func be32(b []byte, at int) uint32 {
	if at < 0 || at+4 > len(b) {
		return none
	}
	return binary.BigEndian.Uint32(b[at:])
}

func be16(b []byte, at int) int {
	if at < 0 || at+2 > len(b) {
		return 0
	}
	return int(binary.BigEndian.Uint16(b[at:]))
}

// isMOBI reports whether data is a PalmDB e-book: MOBI, AZW or AZW3.
func isMOBI(data []byte) bool { return len(data) >= 78 && string(data[60:68]) == "BOOKMOBI" }

// mobiHeader is the part of a record 0 that reading a KF8 book needs. The
// record numbers in it are absolute.
type mobiHeader struct {
	base           int // record 0's number
	r0             []byte
	textLength     int
	compression    int
	textRecords    int
	encoding       uint32
	version        uint32
	firstResource  int
	huffRecord     int
	huffCount      int
	extraFlags     int
	hlen           int
	fdst           int
	ncx, skel, frg int
	guide          int
	exth           map[uint32][][]byte
}

func readHeader(p *palmDB, base int) (*mobiHeader, error) {
	r0 := p.record(base)
	if len(r0) < 0x84 || string(r0[16:20]) != "MOBI" {
		return nil, errors.New("kf8: no MOBI header")
	}
	if be16(r0, 12) != 0 {
		return nil, ErrDRM
	}
	hlen := int(be32(r0, 20))
	h := &mobiHeader{
		base:        base,
		r0:          r0,
		hlen:        hlen,
		textLength:  int(be32(r0, 4)),
		compression: be16(r0, 0),
		textRecords: be16(r0, 8),
		encoding:    be32(r0, 28),
		version:     be32(r0, 36),
	}
	rel := func(at int) int {
		v := be32(r0, at)
		if v == none || at+4 > 16+hlen {
			return -1
		}
		return base + int(v)
	}
	h.firstResource = rel(0x6C)
	h.huffRecord = rel(0x70)
	h.huffCount = int(be32(r0, 0x74))
	if 16+hlen >= 0xF4 {
		h.extraFlags = be16(r0, 0xF2)
	}
	h.fdst, h.ncx, h.frg, h.skel, h.guide = rel(0xC0), rel(0xF4), rel(0xF8), rel(0xFC), rel(0x104)
	h.exth = map[uint32][][]byte{}
	if be32(r0, 0x80)&0x40 != 0 && 16+hlen+12 <= len(r0) {
		ex := r0[16+hlen:]
		if string(ex[:4]) == "EXTH" {
			n, pos := int(be32(ex, 8)), 12
			for i := 0; i < n && pos+8 <= len(ex); i++ {
				typ, size := be32(ex, pos), int(be32(ex, pos+4))
				if size < 8 || pos+size > len(ex) {
					break
				}
				h.exth[typ] = append(h.exth[typ], ex[pos+8:pos+size])
				pos += size
			}
		}
	}
	return h, nil
}

// openKF8 reads an AZW3 book, or the KF8 part of a joint MOBI, into b.
func (b *Book) openKF8() error {
	p := &palmDB{data: b.raw}
	n := be16(b.raw, 76)
	for i := 0; i < n; i++ {
		off := be32(b.raw, 78+8*i)
		if off == none || int(off) > len(b.raw) {
			return errors.New("kf8: bad record table")
		}
		p.offsets = append(p.offsets, int(off))
	}
	h, err := readHeader(p, 0)
	if err != nil {
		return err
	}
	// Resources sit after the first header's text either way; a joint
	// file's KF8 header comes after them, at the record EXTH 121 names.
	firstResource, cover := h.firstResource, -1
	if v := h.exth[201]; len(v) > 0 && len(v[0]) >= 4 && be32(v[0], 0) != none {
		cover = int(be32(v[0], 0)) + 1
	}
	if h.version < 8 {
		v := h.exth[121]
		if len(v) == 0 || len(v[0]) < 4 || be32(v[0], 0) == none {
			return ErrNotKF8
		}
		if h, err = readHeader(p, int(be32(v[0], 0))); err != nil {
			return err
		}
	}
	if h.skel < 0 || h.frg < 0 || h.fdst < 0 {
		return ErrNotKF8
	}
	text, err := h.text(p)
	if err != nil {
		return err
	}
	k := &kf8{p: p, h: h, text: text, cover: cover}
	if err := k.read(firstResource); err != nil {
		return err
	}
	k.fill(b)
	b.k = k
	return nil
}

// text decompresses the text records into the raw markup stream.
func (h *mobiHeader) text(p *palmDB) ([]byte, error) {
	var huff *huffCDIC
	switch h.compression {
	case 1, 2:
	case 17480:
		huff = &huffCDIC{}
		if err := huff.load(p, h.huffRecord, h.huffCount); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("kf8: unknown compression %d", h.compression)
	}
	var out bytes.Buffer
	for i := 1; i <= h.textRecords; i++ {
		rec := p.record(h.base + i)
		rec = rec[:len(rec)-trailingSize(rec, h.extraFlags)]
		switch h.compression {
		case 1:
			out.Write(rec)
		case 2:
			out.Write(palmDOC(rec))
		default:
			d, err := huff.unpack(rec, 0)
			if err != nil {
				return nil, err
			}
			out.Write(d)
		}
	}
	text := out.Bytes()
	if h.textLength > 0 && h.textLength < len(text) {
		text = text[:h.textLength]
	}
	return text, nil
}

// trailingSize is how many bytes of extra data end a text record.
func trailingSize(rec []byte, flags int) int {
	size := 0
	for f := flags >> 1; f != 0; f >>= 1 {
		if f&1 == 0 {
			continue
		}
		end := len(rec) - size
		v := 0
		for i := max(0, end-4); i < end; i++ {
			if rec[i]&0x80 != 0 {
				v = 0
			}
			v = v<<7 | int(rec[i]&0x7F)
		}
		size += v
	}
	if flags&1 != 0 && len(rec)-size-1 >= 0 {
		size += int(rec[len(rec)-size-1]&3) + 1
	}
	return min(size, len(rec))
}

// palmDOC undoes PalmDOC's LZ77.
func palmDOC(in []byte) []byte {
	out := make([]byte, 0, 4096)
	for i := 0; i < len(in); {
		c := in[i]
		i++
		switch {
		case c >= 1 && c <= 8:
			end := min(i+int(c), len(in))
			out = append(out, in[i:end]...)
			i = end
		case c < 0x80:
			out = append(out, c)
		case c >= 0xC0:
			out = append(out, ' ', c^0x80)
		default:
			if i >= len(in) {
				return out
			}
			v := int(c)<<8 | int(in[i])
			i++
			dist, n := (v>>3)&0x7FF, v&7+3
			if dist == 0 || dist > len(out) {
				continue
			}
			for j := 0; j < n; j++ {
				out = append(out, out[len(out)-dist])
			}
		}
	}
	return out
}

// huffCDIC is the Huffman and dictionary compression of Amazon's books.
type huffCDIC struct {
	dict1   [256]struct{ codelen, term, maxcode uint64 }
	mincode [33]uint64
	maxcode [33]uint64
	phrases []phrase
}

type phrase struct {
	data []byte
	done bool
	busy bool
}

func (h *huffCDIC) load(p *palmDB, first, count int) error {
	huff := p.record(first)
	if len(huff) < 24 || string(huff[:8]) != "HUFF\x00\x00\x00\x18" {
		return errors.New("kf8: bad HUFF record")
	}
	off1, off2 := int(be32(huff, 8)), int(be32(huff, 12))
	if off1+256*4 > len(huff) || off2+64*4 > len(huff) {
		return errors.New("kf8: bad HUFF record")
	}
	for i := 0; i < 256; i++ {
		v := uint64(be32(huff, off1+4*i))
		cl := v & 0x1F
		if cl == 0 {
			return errors.New("kf8: bad HUFF code")
		}
		h.dict1[i].codelen, h.dict1[i].term = cl, v&0x80
		h.dict1[i].maxcode = ((v>>8)+1)<<(32-cl) - 1
	}
	for cl := 1; cl <= 32; cl++ {
		lo, hi := uint64(be32(huff, off2+8*(cl-1))), uint64(be32(huff, off2+8*(cl-1)+4))
		h.mincode[cl] = lo << (32 - cl)
		h.maxcode[cl] = (hi+1)<<(32-cl) - 1
	}
	for i := 1; i < count; i++ {
		cdic := p.record(first + i)
		if len(cdic) < 16 || string(cdic[:8]) != "CDIC\x00\x00\x00\x10" {
			return errors.New("kf8: bad CDIC record")
		}
		total, bits := int(be32(cdic, 8)), int(be32(cdic, 12))
		n := min(1<<bits, total-len(h.phrases))
		for j := 0; j < n; j++ {
			off := be16(cdic, 16+2*j)
			blen := be16(cdic, 16+off)
			start := 18 + off
			end := min(start+(blen&0x7FFF), len(cdic))
			if start > end {
				return errors.New("kf8: bad CDIC record")
			}
			h.phrases = append(h.phrases, phrase{data: cdic[start:end], done: blen&0x8000 != 0})
		}
	}
	return nil
}

func (h *huffCDIC) unpack(data []byte, depth int) ([]byte, error) {
	if depth > 32 {
		return nil, errors.New("kf8: HUFF phrases nest too deep")
	}
	bitsLeft := len(data) * 8
	buf := append(append([]byte(nil), data...), make([]byte, 8)...)
	pos, n := 0, 32
	x := binary.BigEndian.Uint64(buf)
	var out []byte
	for {
		if n <= 0 {
			pos += 4
			if pos+8 > len(buf) {
				break
			}
			x = binary.BigEndian.Uint64(buf[pos:])
			n += 32
		}
		code := (x >> uint(n)) & 0xFFFFFFFF
		d := h.dict1[code>>24]
		cl, maxcode := d.codelen, d.maxcode
		if d.term == 0 {
			for cl < 32 && code < h.mincode[cl] {
				cl++
			}
			maxcode = h.maxcode[cl]
		}
		n -= int(cl)
		bitsLeft -= int(cl)
		if bitsLeft < 0 {
			break
		}
		r := int((maxcode - code) >> (32 - cl))
		if r < 0 || r >= len(h.phrases) {
			return nil, errors.New("kf8: bad HUFF code")
		}
		ph := &h.phrases[r]
		if !ph.done {
			if ph.busy {
				return nil, errors.New("kf8: HUFF phrase refers to itself")
			}
			ph.busy = true
			d, err := h.unpack(ph.data, depth+1)
			ph.busy = false
			if err != nil {
				return nil, err
			}
			ph.data, ph.done = d, true
		}
		out = append(out, ph.data...)
	}
	return out, nil
}

// ---- Indexes (INDX records)

type indexEntry struct {
	text string
	tags map[int][]int
}

type tagDef struct{ tag, per, mask, end int }

// index is an INDX index: its entries, the strings of its CNCX records by
// offset, and the layout of its entries' tags.
type index struct {
	entries []indexEntry
	cncx    map[int]string
	defs    []tagDef
	ctrl    int // control bytes an entry starts with
}

// readIndex reads the index at record idx.
func readIndex(p *palmDB, idx int) (*index, error) {
	hdr := p.record(idx)
	if len(hdr) < 56 || string(hdr[:4]) != "INDX" {
		return nil, errors.New("kf8: bad index")
	}
	count, ncncx := int(be32(hdr, 24)), int(be32(hdr, 52))
	cncx := map[int]string{}
	for j := 0; j < ncncx; j++ {
		rec := p.record(idx + count + 1 + j)
		for off := 0; off < len(rec) && rec[off] != 0; {
			start := off
			n, l := vwi(rec, off)
			if n == 0 {
				break
			}
			off += n
			end := min(off+l, len(rec))
			cncx[start+j*0x10000] = string(rec[off:end])
			off = end
		}
	}
	tagx := int(be32(hdr, 4))
	if tagx+12 > len(hdr) || string(hdr[tagx:tagx+4]) != "TAGX" {
		return nil, errors.New("kf8: index without TAGX")
	}
	var defs []tagDef
	tagEnd, ctrlBytes := tagx+int(be32(hdr, tagx+4)), int(be32(hdr, tagx+8))
	for at := tagx + 12; at+4 <= tagEnd && at+4 <= len(hdr); at += 4 {
		defs = append(defs, tagDef{int(hdr[at]), int(hdr[at+1]), int(hdr[at+2]), int(hdr[at+3])})
	}
	var out []indexEntry
	for i := idx + 1; i <= idx+count; i++ {
		rec := p.record(i)
		if len(rec) < 28 || string(rec[:4]) != "INDX" {
			return nil, errors.New("kf8: bad index record")
		}
		idxt, n := int(be32(rec, 20)), int(be32(rec, 24))
		pos := make([]int, 0, n+1)
		for j := 0; j < n; j++ {
			pos = append(pos, be16(rec, idxt+4+2*j))
		}
		pos = append(pos, idxt)
		for j := 0; j < n; j++ {
			start, end := pos[j], pos[j+1]
			if start >= len(rec) || end > len(rec) || start >= end {
				continue
			}
			tl := int(rec[start])
			if start+1+tl > end {
				continue
			}
			e := indexEntry{text: string(rec[start+1 : start+1+tl]), tags: map[int][]int{}}
			ctrl := start + 1 + tl
			data := ctrl + ctrlBytes
			type found struct{ tag, count, bytes, per int }
			var fs []found
			ci := 0
			for _, d := range defs {
				if d.end == 1 {
					ci++
					continue
				}
				if ctrl+ci >= len(rec) || d.mask == 0 {
					continue
				}
				v := int(rec[ctrl+ci]) & d.mask
				if v == 0 {
					continue
				}
				if v == d.mask && bitsSet(d.mask) > 1 {
					c, l := vwi(rec, data)
					data += c
					fs = append(fs, found{d.tag, -1, l, d.per})
					continue
				}
				m := d.mask
				for m&1 == 0 {
					m >>= 1
					v >>= 1
				}
				fs = append(fs, found{d.tag, v, 0, d.per})
			}
			for _, f := range fs {
				var vals []int
				if f.count >= 0 {
					for k := 0; k < f.count*f.per; k++ {
						c, v := vwi(rec, data)
						data += c
						vals = append(vals, v)
					}
				} else {
					for used := 0; used < f.bytes; {
						c, v := vwi(rec, data)
						if c == 0 {
							break
						}
						data += c
						used += c
						vals = append(vals, v)
					}
				}
				e.tags[f.tag] = vals
			}
			out = append(out, e)
		}
	}
	return &index{entries: out, cncx: cncx, defs: defs, ctrl: ctrlBytes}, nil
}

// vwi reads a forward variable-width integer: 7 bits a byte, the last with
// its high bit set. It returns the bytes read and the value.
func vwi(b []byte, at int) (int, int) {
	v, n := 0, 0
	for at+n < len(b) {
		c := b[at+n]
		n++
		v = v<<7 | int(c&0x7F)
		if c&0x80 != 0 {
			break
		}
	}
	return n, v
}

func bitsSet(v int) int {
	n := 0
	for ; v != 0; v >>= 1 {
		n += v & 1
	}
	return n
}

func tagVal(e indexEntry, tag, i int) int {
	if v := e.tags[tag]; i < len(v) {
		return v[i]
	}
	return -1
}

// ---- Rebuilding the files

type kf8 struct {
	p    *palmDB
	h    *mobiHeader
	text []byte

	cover int // the cover's kindle:embed number, from EXTH 201; -1 without

	// For writing the book back: the resource records, kept as they are so
	// their kindle:embed numbers hold, and the table of contents and guide
	// with the places they point at.
	resources  [][]byte
	ncx, guide *index
	ncxRefs    []target
	guideRefs  []target

	frags []fragment
	parts []part
	flows []flowFile
	res   map[int]resource // by kindle:embed number, from 1
	toc   map[string]string
	aids  map[string]map[string]bool // the aids links point at, by file
}

type fragment struct{ insert, file, length int }

// target is a place in the book: a file, and an id in it or "" for its
// start.
type target struct{ file, id string }

type part struct {
	name       string
	start, end int // in the text stream
	data       []byte
}

type flowFile struct {
	name string
	data []byte
}

type resource struct {
	name string
	data []byte
}

func (k *kf8) read(firstResource int) error {
	// FDST: where each flow starts and ends in the text.
	fd := k.p.record(k.h.fdst)
	if len(fd) < 12 || string(fd[:4]) != "FDST" {
		return errors.New("kf8: bad FDST")
	}
	var bounds [][2]int
	for i, n := 0, int(be32(fd, 8)); i < n; i++ {
		s, e := int(be32(fd, 12+8*i)), int(be32(fd, 16+8*i))
		s, e = min(s, len(k.text)), min(e, len(k.text))
		bounds = append(bounds, [2]int{s, max(s, e)})
	}
	if len(bounds) == 0 {
		bounds = [][2]int{{0, len(k.text)}}
	}
	markup := k.text[bounds[0][0]:bounds[0][1]]

	k.aids = map[string]map[string]bool{}
	skels, err := readIndex(k.p, k.h.skel)
	if err != nil {
		return err
	}
	frags, err := readIndex(k.p, k.h.frg)
	if err != nil {
		return err
	}
	for _, f := range frags.entries {
		ins, _ := strconv.Atoi(f.text)
		k.frags = append(k.frags, fragment{insert: ins, file: tagVal(f, 3, 0), length: tagVal(f, 6, 1)})
	}
	fp := 0
	for i, s := range skels.entries {
		count, start, length := tagVal(s, 1, 0), tagVal(s, 6, 0), tagVal(s, 6, 1)
		if start < 0 || length < 0 || start+length > len(markup) {
			return errors.New("kf8: bad skeleton")
		}
		doc := append([]byte(nil), markup[start:start+length]...)
		at := start + length
		for j := 0; j < count && fp < len(k.frags); j++ {
			f := k.frags[fp]
			fp++
			if f.length < 0 || at+f.length > len(markup) {
				return errors.New("kf8: bad fragment")
			}
			ins := min(max(f.insert-start, 0), len(doc))
			doc = append(doc[:ins], append(append([]byte(nil), markup[at:at+f.length]...), doc[ins:]...)...)
			at += f.length
		}
		k.parts = append(k.parts, part{name: fmt.Sprintf("Text/part%04d.xhtml", i), start: start, end: at, data: doc})
	}

	for i := 1; i < len(bounds); i++ {
		data := k.text[bounds[i][0]:bounds[i][1]]
		name := fmt.Sprintf("Styles/flow%04d.css", i)
		if bytes.Contains(data[:min(len(data), 512)], []byte("<svg")) {
			name = fmt.Sprintf("Images/flow%04d.svg", i)
		}
		k.flows = append(k.flows, flowFile{name: name, data: data})
	}

	k.res = map[int]resource{}
	if firstResource > 0 {
		for i := firstResource; i < len(k.p.offsets); i++ {
			rec := k.p.record(i)
			if !isResource(rec) {
				break
			}
			k.resources = append(k.resources, rec)
			n := i - firstResource + 1
			if ext := imageExt(rec); ext != "" {
				name := fmt.Sprintf("Images/image%05d.%s", n, ext)
				if n == k.cover {
					name = "Images/cover." + ext
				}
				k.res[n] = resource{name: name, data: rec}
			} else if bytes.HasPrefix(rec, []byte("FONT")) {
				if data, ext := fontRecord(rec); data != nil {
					k.res[n] = resource{name: fmt.Sprintf("Fonts/font%05d.%s", n, ext), data: data}
				}
			}
		}
	}

	k.toc = map[string]string{}
	if k.h.ncx >= 0 {
		if k.ncx, err = readIndex(k.p, k.h.ncx); err != nil {
			return err
		}
		for _, e := range k.ncx.entries {
			t := k.target(tagVal(e, 6, 0), tagVal(e, 6, 1))
			k.ncxRefs = append(k.ncxRefs, t)
			label := clean(k.decode([]byte(k.ncx.cncx[tagVal(e, 3, 0)])))
			if _, ok := k.toc[t.file]; !ok && label != "" && t.file != "" {
				k.toc[t.file] = label
			}
		}
	}
	if k.h.guide >= 0 {
		if k.guide, err = readIndex(k.p, k.h.guide); err != nil {
			return err
		}
		for _, e := range k.guide.entries {
			t := target{}
			if fid := tagVal(e, 6, 0); fid >= 0 {
				t = k.target(fid, tagVal(e, 6, 1))
			} else if f := tagVal(e, 3, 0); f >= 0 && f < len(k.parts) {
				t.file = k.parts[f].name
			}
			k.guideRefs = append(k.guideRefs, t)
		}
	}
	return nil
}

// isResource reports whether a record after the first resource is one:
// the resources end at the records that close the book.
func isResource(rec []byte) bool {
	for _, end := range []string{"\xe9\x8e\r\n", "BOUNDARY", "FDST", "FLIS", "FCIS", "DATP", "SRCS", "CMET", "INDX"} {
		if bytes.HasPrefix(rec, []byte(end)) {
			return false
		}
	}
	return true
}

func (k *kf8) decode(b []byte) string {
	if k.h.encoding == 1252 {
		if s, err := charmap.Windows1252.NewDecoder().Bytes(b); err == nil {
			return string(s)
		}
	}
	return string(b)
}

func (k *kf8) partAt(pos int) *part {
	for i := range k.parts {
		if pos >= k.parts[i].start && pos < k.parts[i].end {
			return &k.parts[i]
		}
	}
	return nil
}

var (
	kindlePosRe   = regexp.MustCompile(`kindle:pos:fid:([0-9A-Va-v]{4}):off:([0-9A-Va-v]{10})`)
	kindleEmbedRe = regexp.MustCompile(`kindle:embed:([0-9A-Va-v]{4})(\?mime=[^"')\s]*)?`)
	kindleFlowRe  = regexp.MustCompile(`kindle:flow:([0-9A-Va-v]{4})(\?mime=[^"')\s]*)?`)
	idAttrRe      = regexp.MustCompile(`(?i)^<[^>]*\s(?:id|name)\s*=\s*['"]([^'"]*)['"]`)
	aidAttrRe     = regexp.MustCompile(`^<[^>]+\s(?:aid|AID)\s*=\s*['"]([^'"]+)['"]`)
	coverLinkRe   = regexp.MustCompile(`(?i)<link\b[^>]*>`)
	hrefRe        = regexp.MustCompile(`(?i)(\bhref\s*=\s*["'])([^"']*)(["'])`)
)

func base32(s string) int {
	v, _ := strconv.ParseInt(s, 32, 64)
	return int(v)
}

// target is the file and anchor a kindle:pos link points at: the nearest
// id (or aid) at or before the position in its file.
func (k *kf8) target(fid, off int) target {
	if fid < 0 || fid >= len(k.frags) {
		return target{}
	}
	pos := k.frags[fid].insert + off
	p := k.partAt(pos)
	if p == nil {
		if f := k.frags[fid].file; f >= 0 && f < len(k.parts) {
			return target{file: k.parts[f].name}
		}
		return target{}
	}
	doc, at := p.data, min(pos-p.start, len(p.data))
	gt, lt := bytes.IndexByte(doc[at:], '>'), bytes.IndexByte(doc[at:], '<')
	if gt >= 0 && (lt == 0 || lt < 0 || gt < lt) {
		at += gt + 1
	}
	doc = doc[:at]
	for end := len(doc); end > 0; {
		s := bytes.LastIndexByte(doc[:end], '<')
		if s < 0 {
			break
		}
		tag := doc[s:end]
		end = s
		if bytes.HasPrefix(tag, []byte("<body")) {
			return target{file: p.name}
		}
		if bytes.HasPrefix(tag, []byte("<meta")) {
			continue
		}
		if m := idAttrRe.FindSubmatch(tag); m != nil {
			return target{p.name, string(m[1])}
		}
		if m := aidAttrRe.FindSubmatch(tag); m != nil {
			if k.aids[p.name] == nil {
				k.aids[p.name] = map[string]bool{}
			}
			k.aids[p.name][string(m[1])] = true
			return target{p.name, "aid-" + string(m[1])}
		}
	}
	return target{file: p.name}
}

// rewrite turns the kindle: URLs of a file into paths from its folder.
func (k *kf8) rewrite(from string, data []byte) []byte {
	dir := path.Dir(from)
	rel := func(to string) string {
		r, err := relPath(dir, to)
		if err != nil {
			return to
		}
		return r
	}
	data = kindlePosRe.ReplaceAllFunc(data, func(m []byte) []byte {
		s := kindlePosRe.FindSubmatch(m)
		t := k.target(base32(string(s[1])), base32(string(s[2])))
		name, id := t.file, t.id
		if name == "" {
			return m
		}
		if name == from && id != "" {
			return []byte("#" + id) // within the page
		}
		href := rel(name)
		if id != "" {
			href += "#" + id
		}
		return []byte(href)
	})
	data = kindleEmbedRe.ReplaceAllFunc(data, func(m []byte) []byte {
		s := kindleEmbedRe.FindSubmatch(m)
		if r, ok := k.res[base32(string(s[1]))]; ok {
			return []byte(rel(r.name))
		}
		return m
	})
	data = kindleFlowRe.ReplaceAllFunc(data, func(m []byte) []byte {
		s := kindleFlowRe.FindSubmatch(m)
		if i := base32(string(s[1])) - 1; i >= 0 && i < len(k.flows) {
			return []byte(rel(k.flows[i].name))
		}
		return m
	})
	// A <link type="image/x-cover"> keeps the name the cover had before the
	// book was made, as Project Gutenberg's books do: point it at the cover.
	if r, ok := k.res[k.cover]; ok {
		data = coverLinkRe.ReplaceAllFunc(data, func(m []byte) []byte {
			if !bytes.Contains(m, []byte("image/x-cover")) {
				return m
			}
			return hrefRe.ReplaceAll(m, []byte(`${1}`+rel(r.name)+`${3}`))
		})
	}
	return data
}

func relPath(dir, to string) (string, error) {
	if dir == "." {
		return to, nil
	}
	if path.Dir(to) == dir {
		return path.Base(to), nil
	}
	return strings.Repeat("../", strings.Count(dir, "/")+1) + to, nil
}

// fill gives b the rebuilt files, with their links rewritten.
func (k *kf8) fill(b *Book) {
	add := func(name, media string, data []byte) {
		e := &entry{name: name, data: data, read: true, media: media, size: int64(len(data))}
		b.files = append(b.files, e)
		b.byName[name] = e
	}
	// Rewrite every file before adding ids: targets read the parts as built.
	texts := make([][]byte, len(k.parts))
	for i, p := range k.parts {
		texts[i] = k.rewrite(p.name, p.data)
	}
	var names []string
	for i, p := range k.parts {
		data := texts[i]
		for aid := range k.aids[p.name] {
			re := regexp.MustCompile(`(<[^>]+\s)(aid\s*=\s*['"]` + regexp.QuoteMeta(aid) + `['"])`)
			data = re.ReplaceAll(data, []byte(`${1}id="aid-`+aid+`" ${2}`))
		}
		add(p.name, "application/xhtml+xml", []byte(k.decode(data)))
		names = append(names, p.name)
	}
	b.setSpine(names, k.toc)
	for _, f := range k.flows {
		add(f.name, typeByExt(f.name), []byte(k.decode(k.rewrite(f.name, f.data))))
	}
	for n := 1; n <= len(k.p.offsets); n++ {
		if r, ok := k.res[n]; ok {
			add(r.name, typeByExt(r.name), r.data)
		}
	}
}

func imageExt(b []byte) string {
	switch {
	case bytes.HasPrefix(b, []byte{0xFF, 0xD8, 0xFF}):
		return "jpg"
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		return "png"
	case bytes.HasPrefix(b, []byte("GIF8")):
		return "gif"
	case bytes.HasPrefix(b, []byte("BM")) && len(b) > 14:
		return "bmp"
	}
	return ""
}

// fontRecord undoes a FONT record's obfuscation and compression.
func fontRecord(rec []byte) ([]byte, string) {
	if len(rec) < 24 {
		return nil, ""
	}
	flags, start := be32(rec, 8), int(be32(rec, 12))
	xorLen, xorStart := int(be32(rec, 16)), int(be32(rec, 20))
	if start > len(rec) {
		return nil, ""
	}
	data := append([]byte(nil), rec[start:]...)
	if flags&2 != 0 && xorLen > 0 && xorStart+xorLen <= len(rec) {
		key := rec[xorStart : xorStart+xorLen]
		for i := 0; i < min(1040, len(data)); i++ {
			data[i] ^= key[i%xorLen]
		}
	}
	if flags&1 != 0 {
		zr, err := zlib.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, ""
		}
		if data, err = io.ReadAll(zr); err != nil {
			return nil, ""
		}
	}
	switch {
	case bytes.HasPrefix(data, []byte("OTTO")):
		return data, "otf"
	case bytes.HasPrefix(data, []byte("wOFF")):
		return data, "woff"
	case bytes.HasPrefix(data, []byte("wOF2")):
		return data, "woff2"
	}
	return data, "ttf"
}
