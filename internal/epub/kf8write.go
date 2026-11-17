package epub

// Writing an AZW3 back. The files, as edited, go back into one KF8 text
// stream: each page becomes a skeleton (the page with its body emptied)
// and fragments (the body's content, cut between elements into pieces of
// about 8 KB), and the flows follow. Links between files turn back into
// kindle: URLs. The skeleton, fragment, table of contents and guide
// indexes are written anew; the record 0 header, its metadata and the
// resource records (images, fonts) are kept as they were, so the images'
// kindle:embed numbers hold. The files are as many as the book had: the
// editor changes them but doesn't add or remove any.

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	textRecordSize = 4096
	chunkSize      = 8192
	eofRecord      = "\xe9\x8e\r\n"
)

var (
	bodyOpenRe = regexp.MustCompile(`(?is)<body\b[^>]*>`)
	bodyAidRe  = regexp.MustCompile(`(?i)\said\s*=\s*["']([^"']+)["']`)
	linkAttrRe = regexp.MustCompile(`(?i)(\s(?:href|src|xlink:href)\s*=\s*)(?:"([^"]*)"|'([^']*)')`)
	cssURLRe   = regexp.MustCompile(`(?i)url\(\s*(?:"([^"]*)"|'([^']*)'|([^)'"\s]*))\s*\)`)
	idPosRe    = regexp.MustCompile(`(?i)\s(?:id|name)\s*=\s*["']([^"']*)["']`)
	posMarkRe  = regexp.MustCompile(`kindle:pos:fid:____:off:([0-9A-V]{10})`)
)

// The layouts of the skeleton and fragment indexes, as Kindle books have
// them: a skeleton's count and geometry are written twice.
var (
	skelDefs = []tagDef{{1, 1, 3, 0}, {6, 2, 12, 0}, {0, 0, 0, 1}}
	fragDefs = []tagDef{{2, 1, 1, 0}, {3, 1, 2, 0}, {4, 1, 4, 0}, {6, 2, 8, 0}, {0, 0, 0, 1}}
)

// The NCX tags that hold CNCX offsets: label, kind, description, author,
// image caption and attribution.
var ncxStrings = map[int]bool{3: true, 5: true, 70: true, 71: true, 72: true, 73: true}

// laidOut is a page as written: its text, with link placeholders of the
// final length, cut into a skeleton and fragments.
type laidOut struct {
	doc        string
	skel       string
	bodyStart  int   // where the fragments go in doc
	chunks     []int // the fragments' lengths
	start      int   // the skeleton's position in the text stream
	firstFrag  int   // the global number of its first fragment
	ids        map[string]int
	selector   string
	fragsTotal int
}

func base32Pad(v, n int) string {
	s := strings.ToUpper(strconv.FormatInt(int64(v), 32))
	if len(s) < n {
		s = strings.Repeat("0", n-len(s)) + s
	}
	return s
}

func (k *kf8) write(b *Book) ([]byte, error) {
	r0 := k.h.r0
	if 16+k.h.hlen < 0x108 || len(r0) < 0x108 {
		return nil, errors.New("kf8: the book's header is too old to write back")
	}
	read := func(name string) (string, error) {
		e := b.byName[name]
		if e == nil {
			return "", fmt.Errorf("kf8: %s is missing", name)
		}
		d, err := b.load(e)
		return string(d), err
	}

	parts := map[string]int{}
	for i, p := range k.parts {
		parts[p.name] = i
	}
	flows := map[string]int{}
	for i, f := range k.flows {
		flows[f.name] = i + 1
	}
	embeds := map[string]int{}
	for n, r := range k.res {
		embeds[r.name] = n
	}

	// kindle turns a reference from a file into a kindle: URL; a link to a
	// page gets a placeholder of the final length, filled in once the
	// pages are laid out.
	var links []target
	kindle := func(from, v string, pages bool) (string, bool) {
		if v == "" || strings.HasPrefix(v, "/") {
			return "", false
		}
		if i := strings.IndexAny(v, ":/#"); i >= 0 && v[i] == ':' {
			return "", false // a URL with a scheme
		}
		name, frag := from, ""
		if i := strings.IndexByte(v, '#'); i >= 0 {
			frag = v[i+1:]
			if i > 0 {
				name = Resolve(path.Dir(from), v[:i])
			}
		} else {
			name = Resolve(path.Dir(from), v)
		}
		if n, ok := embeds[name]; ok {
			s := "kindle:embed:" + base32Pad(n, 4)
			if t := typeByExt(name); IsImage(t) {
				s += "?mime=" + t
			}
			return s, true
		}
		if n, ok := flows[name]; ok {
			return "kindle:flow:" + base32Pad(n, 4) + "?mime=" + typeByExt(name), true
		}
		if _, ok := parts[name]; ok && pages {
			links = append(links, target{name, frag})
			return "kindle:pos:fid:____:off:" + base32Pad(len(links)-1, 10), true
		}
		return "", false
	}
	restore := func(from, data string, pages bool) string {
		data = linkAttrRe.ReplaceAllStringFunc(data, func(m string) string {
			s := linkAttrRe.FindStringSubmatch(m)
			v, q := s[2], `"`
			if v == "" && s[3] != "" {
				v, q = s[3], "'"
			}
			if u, ok := kindle(from, v, pages); ok {
				return s[1] + q + u + q
			}
			return m
		})
		return cssURLRe.ReplaceAllStringFunc(data, func(m string) string {
			s := cssURLRe.FindStringSubmatch(m)
			v := s[1] + s[2] + s[3]
			if u, ok := kindle(from, v, false); ok {
				return "url(" + u + ")"
			}
			return m
		})
	}

	// Lay out the pages, then the flows, into the text stream.
	var text bytes.Buffer
	pages := make([]*laidOut, len(k.parts))
	nfrag := 0
	for i, p := range k.parts {
		doc, err := read(p.name)
		if err != nil {
			return nil, err
		}
		l := layOut(restore(p.name, doc, true))
		l.start, l.firstFrag = text.Len(), nfrag
		nfrag += len(l.chunks)
		text.WriteString(l.skel)
		text.WriteString(l.doc[l.bodyStart : l.bodyStart+l.fragsTotal])
		pages[i] = l
	}
	markupEnd := text.Len()
	fdst := [][2]int{{0, markupEnd}}
	for _, f := range k.flows {
		data, err := read(f.name)
		if err != nil {
			return nil, err
		}
		s := text.Len()
		text.WriteString(restore(f.name, data, false))
		fdst = append(fdst, [2]int{s, text.Len()})
	}

	// locate finds a place's fragment, its offset in it and its position
	// in the text stream; a place outside the fragments is its page's start.
	locate := func(t target) (fid, off, abs int) {
		i, ok := parts[t.file]
		if !ok {
			return 0, 0, 0
		}
		l := pages[i]
		pos := -1
		if t.id != "" {
			if p, ok := l.ids[t.id]; ok {
				pos = p
			}
		}
		if pos >= l.bodyStart && pos < l.bodyStart+l.fragsTotal {
			at := l.bodyStart
			for j, n := range l.chunks {
				if pos < at+n {
					return l.firstFrag + j, pos - at, l.start + pos
				}
				at += n
			}
		}
		if len(l.chunks) == 0 { // a page without a body: the next one's start
			return min(l.firstFrag, max(nfrag-1, 0)), 0, l.start
		}
		return l.firstFrag, 0, l.start + l.bodyStart
	}

	raw := posMarkRe.ReplaceAllFunc(text.Bytes(), func(m []byte) []byte {
		n, _ := strconv.ParseInt(string(posMarkRe.FindSubmatch(m)[1]), 32, 64)
		fid, off, _ := locate(links[n])
		return []byte("kindle:pos:fid:" + base32Pad(fid, 4) + ":off:" + base32Pad(off, 10))
	})

	// The indexes.
	var skelEntries, fragEntries []wEntry
	fragCNCX := &cncxWriter{}
	seq := 0
	for i, l := range pages {
		skelLen := len(l.skel)
		skelEntries = append(skelEntries, wEntry{
			text: fmt.Sprintf("SKEL%010d", i),
			tags: map[int][]int{1: {len(l.chunks), len(l.chunks)}, 6: {l.start, skelLen, l.start, skelLen}},
		})
		at := 0
		for _, n := range l.chunks {
			fragEntries = append(fragEntries, wEntry{
				text: fmt.Sprintf("%010d", l.start+l.bodyStart+at),
				tags: map[int][]int{2: {fragCNCX.add(l.selector)}, 3: {i}, 4: {seq}, 6: {at, n}},
			})
			at += n
			seq++
		}
	}
	var ncxRecords, guideRecords [][]byte
	if k.ncx != nil {
		c := &cncxWriter{}
		entries := make([]wEntry, len(k.ncx.entries))
		abs := make([]int, len(entries))
		for i, e := range k.ncx.entries {
			tags := map[int][]int{}
			for t, v := range e.tags {
				tags[t] = append([]int(nil), v...)
				if ncxStrings[t] && len(v) > 0 {
					tags[t] = []int{c.add(k.decode([]byte(k.ncx.cncx[v[0]])))}
				}
			}
			fid, off, a := locate(k.ncxRefs[i])
			abs[i] = a
			if _, ok := tags[6]; ok {
				tags[6] = []int{fid, off}
			}
			if _, ok := tags[1]; ok {
				tags[1] = []int{a}
			}
			entries[i] = wEntry{text: e.text, tags: tags}
		}
		for i := range entries {
			if _, ok := entries[i].tags[2]; ok {
				end := markupEnd
				if i+1 < len(abs) && abs[i+1] > abs[i] {
					end = abs[i+1]
				}
				entries[i].tags[2] = []int{max(end-abs[i], 0)}
			}
		}
		ncxRecords = writeIndex(k.ncx.defs, k.ncx.ctrl, entries, c)
	}
	if k.guide != nil {
		c := &cncxWriter{}
		entries := make([]wEntry, len(k.guide.entries))
		for i, e := range k.guide.entries {
			tags := map[int][]int{}
			for t, v := range e.tags {
				tags[t] = append([]int(nil), v...)
			}
			if v := e.tags[1]; len(v) > 0 {
				tags[1] = []int{c.add(k.decode([]byte(k.guide.cncx[v[0]])))}
			}
			if _, ok := tags[6]; ok {
				fid, off, _ := locate(k.guideRefs[i])
				tags[6] = []int{fid, off}
			}
			if _, ok := tags[3]; ok {
				if f, ok := parts[k.guideRefs[i].file]; ok {
					tags[3] = []int{f}
				}
			}
			entries[i] = wEntry{text: e.text, tags: tags}
		}
		guideRecords = writeIndex(k.guide.defs, k.guide.ctrl, entries, c)
	}

	// The records.
	records := [][]byte{nil} // record 0, made last
	textRecords := splitText(raw)
	records = append(records, textRecords...)
	firstNonText := len(records)
	add := func(rs [][]byte) int {
		if len(rs) == 0 {
			return int(none)
		}
		at := len(records)
		records = append(records, rs...)
		return at
	}
	skelIdx := add(writeIndex(skelDefs, 1, skelEntries, &cncxWriter{}))
	fragIdx := add(writeIndex(fragDefs, 1, fragEntries, fragCNCX))
	ncxIdx := add(ncxRecords)
	guideIdx := add(guideRecords)
	firstRes := add(k.resources)
	fd := make([]byte, 12+8*len(fdst))
	copy(fd, "FDST")
	binary.BigEndian.PutUint32(fd[4:], 12)
	binary.BigEndian.PutUint32(fd[8:], uint32(len(fdst)))
	for i, s := range fdst {
		binary.BigEndian.PutUint32(fd[12+8*i:], uint32(s[0]))
		binary.BigEndian.PutUint32(fd[16+8*i:], uint32(s[1]))
	}
	fdstIdx := add([][]byte{fd})
	flisIdx := add([][]byte{append([]byte("FLIS\x00\x00\x00\x08\x00\x41\x00\x00\x00\x00\x00\x00\xff\xff\xff\xff\x00\x01\x00\x03\x00\x00\x00\x03\x00\x00\x00\x01"), 0xff, 0xff, 0xff, 0xff)})
	fcis := []byte("FCIS\x00\x00\x00\x14\x00\x00\x00\x10\x00\x00\x00\x02\x00\x00\x00\x00")
	fcis = binary.BigEndian.AppendUint32(fcis, uint32(len(raw)))
	fcis = append(fcis, "\x00\x00\x00\x00\x00\x00\x00\x28\x00\x00\x00\x00\x00\x00\x00\x28\x00\x00\x00\x08\x00\x01\x00\x01\x00\x00\x00\x00"...)
	fcisIdx := add([][]byte{fcis})
	add([][]byte{[]byte(eofRecord)})

	// Record 0: the book's own, with the new text and records.
	h := append([]byte(nil), r0...)
	put16 := func(at, v int) { binary.BigEndian.PutUint16(h[at:], uint16(v)) }
	put32 := func(at, v int) { binary.BigEndian.PutUint32(h[at:], uint32(v)) }
	put16(0, 2) // PalmDOC
	put16(2, 0)
	put32(4, len(raw))
	put16(8, len(textRecords))
	put16(10, textRecordSize)
	put16(12, 0)
	put16(14, 0)
	put32(0x1C, 65001)
	put32(0x50, firstNonText)
	put32(0x6C, firstRes)
	for _, at := range []int{0x70, 0x74, 0x78, 0x7C} {
		put32(at, 0)
	}
	put32(0xC0, fdstIdx)
	put32(0xC4, len(fdst))
	put32(0xC8, fcisIdx)
	put32(0xCC, 1)
	put32(0xD0, flisIdx)
	put32(0xD4, 1)
	put32(0xE0, int(none)) // no SRCS
	put32(0xE4, 0)
	put32(0xF0, 1) // trailing bytes: multibyte characters only
	put32(0xF4, ncxIdx)
	put32(0xF8, fragIdx)
	put32(0xFC, skelIdx)
	put32(0x100, int(none)) // no DATP
	put32(0x104, guideIdx)
	records[0] = h
	if len(records) > 0xFFFF {
		return nil, errors.New("kf8: too many records")
	}

	// The PalmDB: the book's header, with the new record list.
	var out bytes.Buffer
	db := append([]byte(nil), b.raw[:78]...)
	binary.BigEndian.PutUint32(db[40:], uint32(time.Now().Unix()))
	binary.BigEndian.PutUint32(db[72:], uint32(2*len(records)-1))
	binary.BigEndian.PutUint16(db[76:], uint16(len(records)))
	out.Write(db)
	off := 78 + 8*len(records) + 2
	for i, r := range records {
		var e [8]byte
		binary.BigEndian.PutUint32(e[:], uint32(off))
		binary.BigEndian.PutUint32(e[4:], uint32(2*i)&0xFFFFFF)
		out.Write(e[:])
		off += len(r)
	}
	out.Write([]byte{0, 0})
	for _, r := range records {
		out.Write(r)
	}
	return out.Bytes(), nil
}

// layOut cuts a page into a skeleton and fragments: the body's content,
// cut between its top-level nodes into pieces of about chunkSize.
func layOut(doc string) *laidOut {
	l := &laidOut{doc: doc, ids: map[string]int{}, selector: "P-//*[@aid='0']"}
	for _, m := range idPosRe.FindAllStringSubmatchIndex(doc, -1) {
		id := doc[m[2]:m[3]]
		if _, ok := l.ids[id]; !ok {
			l.ids[id] = max(strings.LastIndexByte(doc[:m[0]+1], '<'), 0)
		}
	}
	open := bodyOpenRe.FindStringIndex(doc)
	end := strings.LastIndex(strings.ToLower(doc), "</body")
	if open == nil || end < open[1] {
		l.skel, l.bodyStart = "", 0
		if doc != "" {
			l.chunks, l.fragsTotal = []int{len(doc)}, len(doc)
		}
		return l
	}
	if m := bodyAidRe.FindStringSubmatch(doc[open[0]:open[1]]); m != nil {
		l.selector = "P-//*[@aid='" + m[1] + "']"
	}
	l.bodyStart = open[1]
	inner := doc[open[1]:end]
	l.skel = doc[:open[1]] + doc[end:]
	bounds := topBounds(inner)
	for start := 0; start < len(inner); {
		cut := -1
		for _, b := range bounds {
			if b > start && b <= start+chunkSize {
				cut = b
			} else if b > start+chunkSize {
				if cut < 0 {
					cut = b
				}
				break
			}
		}
		if cut < 0 {
			cut = len(inner)
		}
		l.chunks = append(l.chunks, cut-start)
		start = cut
	}
	l.fragsTotal = len(inner)
	return l
}

// topBounds are the places between a body's top-level nodes, where it can
// be cut, ending with its length.
func topBounds(s string) []int {
	var out []int
	depth := 0
	for i := 0; i < len(s); {
		if s[i] != '<' {
			j := strings.IndexByte(s[i:], '<')
			if j < 0 {
				i = len(s)
			} else {
				i += j
			}
		} else {
			end := func(close string) int {
				if j := strings.Index(s[i:], close); j >= 0 {
					return i + j + len(close)
				}
				return len(s)
			}
			switch {
			case strings.HasPrefix(s[i:], "<!--"):
				i = end("-->")
			case strings.HasPrefix(s[i:], "<![CDATA["):
				i = end("]]>")
			case strings.HasPrefix(s[i:], "<?"):
				i = end("?>")
			case strings.HasPrefix(s[i:], "</"):
				i = end(">")
				depth--
			case strings.HasPrefix(s[i:], "<!"):
				i = end(">")
			default:
				j, quote := i+1, byte(0)
				for ; j < len(s); j++ {
					c := s[j]
					if quote != 0 {
						if c == quote {
							quote = 0
						}
					} else if c == '"' || c == '\'' {
						quote = c
					} else if c == '>' {
						break
					}
				}
				if j < len(s) && s[j-1] != '/' {
					depth++
				}
				i = min(j+1, len(s))
			}
		}
		if depth <= 0 {
			depth = 0
			out = append(out, i)
		}
	}
	return out
}

// splitText cuts the text into compressed records of textRecordSize
// bytes. A character cut at a record's end is completed after its data,
// followed by the count of bytes that does so, as the header's trailing
// flags say.
func splitText(text []byte) [][]byte {
	var out [][]byte
	for start := 0; start < len(text); start += textRecordSize {
		end := min(start+textRecordSize, len(text))
		rec := compressPalmDOC(text[start:end])
		n := 0
		for end+n < len(text) && n < 3 && text[end+n]&0xC0 == 0x80 {
			n++
		}
		rec = append(rec, text[end:end+n]...)
		out = append(out, append(rec, byte(n)))
	}
	return out
}

// compressPalmDOC is PalmDOC's LZ77: back references of 3 to 10 bytes up
// to 2047 back, a space and a letter in one byte, and runs of bytes that
// would read as commands copied with a count before them.
func compressPalmDOC(in []byte) []byte {
	out := make([]byte, 0, len(in))
	heads := map[uint32][]int{}
	key := func(i int) uint32 { return uint32(in[i])<<16 | uint32(in[i+1])<<8 | uint32(in[i+2]) }
	note := func(i int) {
		if i+3 <= len(in) {
			k := key(i)
			heads[k] = append(heads[k], i)
		}
	}
	for i := 0; i < len(in); {
		if i+3 <= len(in) {
			best, dist := 0, 0
			cands := heads[key(i)]
			for c := len(cands) - 1; c >= 0 && c >= len(cands)-64; c-- {
				j := cands[c]
				if i-j > 2047 {
					break
				}
				n := 0
				for n < 10 && i+n < len(in) && in[j+n] == in[i+n] {
					n++
				}
				if n > best {
					best, dist = n, i-j
					if n == 10 {
						break
					}
				}
			}
			if best >= 3 {
				v := 0x8000 | dist<<3 | (best - 3)
				out = append(out, byte(v>>8), byte(v))
				for j := i; j < i+best; j++ {
					note(j)
				}
				i += best
				continue
			}
		}
		c := in[i]
		switch {
		case c == ' ' && i+1 < len(in) && in[i+1] >= 0x40 && in[i+1] < 0x80:
			out = append(out, in[i+1]^0x80)
			note(i)
			note(i + 1)
			i += 2
		case c == 0 || (c >= 9 && c < 0x80):
			out = append(out, c)
			note(i)
			i++
		default:
			n := 1
			for n < 8 && i+n < len(in) && (in[i+n] >= 0x80 || (in[i+n] >= 1 && in[i+n] <= 8)) {
				n++
			}
			out = append(out, byte(n))
			out = append(out, in[i:i+n]...)
			for j := i; j < i+n; j++ {
				note(j)
			}
			i += n
		}
	}
	return out
}

// ---- Writing indexes

type wEntry struct {
	text string
	tags map[int][]int
}

// cncxWriter collects an index's strings into CNCX records.
type cncxWriter struct {
	records [][]byte
	at      map[string]int
}

func (c *cncxWriter) add(s string) int {
	if c.at == nil {
		c.at = map[string]int{}
	}
	if off, ok := c.at[s]; ok {
		return off
	}
	e := append(encVWI(len(s)), s...)
	if len(c.records) == 0 || len(c.records[len(c.records)-1])+len(e) > 0xFBF8 {
		c.records = append(c.records, nil)
	}
	r := &c.records[len(c.records)-1]
	off := (len(c.records)-1)*0x10000 + len(*r)
	*r = append(*r, e...)
	c.at[s] = off
	return off
}

// encVWI writes a forward variable-width integer, as vwi reads it.
func encVWI(v int) []byte {
	b := []byte{byte(v&0x7F) | 0x80}
	for v >>= 7; v > 0; v >>= 7 {
		b = append([]byte{byte(v & 0x7F)}, b...)
	}
	return b
}

func align4(b []byte) []byte {
	for len(b)%4 != 0 {
		b = append(b, 0)
	}
	return b
}

// writeIndex writes an index: its header record, its entry records and
// its CNCX records.
func writeIndex(defs []tagDef, ctrlBytes int, entries []wEntry, cncx *cncxWriter) [][]byte {
	const headerLen = 0xC0
	const limit = 0x10000 - headerLen - 1048
	encode := func(e wEntry) []byte {
		ctrl := make([]byte, ctrlBytes)
		var lens, vals []byte
		ci := 0
		for _, d := range defs {
			if d.end == 1 {
				ci++
				continue
			}
			v := e.tags[d.tag]
			if len(v) == 0 || d.per == 0 || ci >= ctrlBytes {
				continue
			}
			shift := 0
			for d.mask>>shift&1 == 0 {
				shift++
			}
			var enc []byte
			for _, x := range v {
				enc = append(enc, encVWI(x)...)
			}
			if count := len(v) / d.per; count<<shift < d.mask || (bitsSet(d.mask) == 1 && count == 1) {
				ctrl[ci] |= byte(count << shift)
			} else {
				ctrl[ci] |= byte(d.mask)
				lens = append(lens, encVWI(len(enc))...)
			}
			vals = append(vals, enc...)
		}
		out := append([]byte{byte(len(e.text))}, e.text...)
		out = append(out, ctrl...)
		out = append(out, lens...)
		return append(out, vals...)
	}

	type block struct {
		body    []byte
		offsets []int
		last    string
	}
	blocks := []*block{{}}
	for _, e := range entries {
		raw := encode(e)
		bl := blocks[len(blocks)-1]
		if len(bl.offsets) > 0 && len(bl.body)+2*len(bl.offsets)+len(raw)+2 > limit {
			bl = &block{}
			blocks = append(blocks, bl)
		}
		bl.offsets = append(bl.offsets, headerLen+len(bl.body))
		bl.body = append(bl.body, raw...)
		bl.last = e.text
	}
	if len(entries) == 0 {
		blocks = nil
	}
	var recs [][]byte
	for _, bl := range blocks {
		body := align4(bl.body)
		idxt := []byte("IDXT")
		for _, o := range bl.offsets {
			idxt = binary.BigEndian.AppendUint16(idxt, uint16(o))
		}
		idxt = align4(idxt)
		h := make([]byte, headerLen)
		copy(h, "INDX")
		binary.BigEndian.PutUint32(h[4:], headerLen)
		binary.BigEndian.PutUint32(h[12:], 1)
		binary.BigEndian.PutUint32(h[20:], uint32(headerLen+len(body)))
		binary.BigEndian.PutUint32(h[24:], uint32(len(bl.offsets)))
		copy(h[28:36], "\xff\xff\xff\xff\xff\xff\xff\xff")
		recs = append(recs, append(append(h, body...), idxt...))
	}

	// The header record: the tag layout, and where each entry record ends.
	tagx := []byte("TAGX")
	tagx = binary.BigEndian.AppendUint32(tagx, uint32(12+4*len(defs)))
	tagx = binary.BigEndian.AppendUint32(tagx, uint32(ctrlBytes))
	for _, d := range defs {
		tagx = append(tagx, byte(d.tag), byte(d.per), byte(d.mask), byte(d.end))
	}
	var geom []byte
	var geomOffs []int
	for _, bl := range blocks {
		geomOffs = append(geomOffs, headerLen+len(tagx)+len(geom))
		geom = append(geom, byte(len(bl.last)))
		geom = append(geom, bl.last...)
		geom = binary.BigEndian.AppendUint16(geom, uint16(len(bl.offsets)))
	}
	geom = align4(geom)
	idxt := []byte("IDXT")
	for _, o := range geomOffs {
		idxt = binary.BigEndian.AppendUint16(idxt, uint16(o))
	}
	idxt = align4(idxt)
	h := make([]byte, headerLen)
	copy(h, "INDX")
	binary.BigEndian.PutUint32(h[4:], headerLen) // where TAGX is
	binary.BigEndian.PutUint32(h[16:], 2)
	binary.BigEndian.PutUint32(h[20:], uint32(headerLen+len(tagx)+len(geom)))
	binary.BigEndian.PutUint32(h[24:], uint32(len(recs)))
	binary.BigEndian.PutUint32(h[28:], 65001)
	copy(h[32:36], "\xff\xff\xff\xff")
	binary.BigEndian.PutUint32(h[36:], uint32(len(entries)))
	binary.BigEndian.PutUint32(h[52:], uint32(len(cncx.records)))
	binary.BigEndian.PutUint32(h[0xB4:], headerLen) // where TAGX is, as calibre reads it
	head := append(append(append(h, tagx...), geom...), idxt...)
	out := append([][]byte{head}, recs...)
	for _, r := range cncx.records {
		out = append(out, align4(r))
	}
	return out
}
