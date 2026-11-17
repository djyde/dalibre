package epub

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// testdata/alice.azw3 is Project Gutenberg's AZW3 of Alice's Adventures
// in Wonderland (ebook #11), as downloaded.
func TestKF8(t *testing.T) {
	b, err := Open("testdata/alice.azw3")
	if err != nil {
		t.Fatal(err)
	}
	if !b.Packaged() {
		t.Fatal("an AZW3 opens as a package")
	}
	sp := b.Spine()
	if len(sp) != 19 || sp[0].Title != "Cover" || sp[3].Title != "Contents" || sp[4].Title != "CHAPTER I. Down the Rabbit-Hole" {
		t.Fatalf("spine: %+v", sp)
	}
	ch1, _ := b.Read(sp[4].Name)
	if !bytes.Contains(ch1, []byte("Alice was beginning to get very tired")) || sp[4].Words < 2000 {
		t.Errorf("chapter one: %d words", sp[4].Words)
	}
	// The contents page links to the chapters' ids, rewritten from kindle:pos.
	toc, _ := b.Read(sp[3].Name)
	for _, want := range []string{`href="part0004.xhtml#chap01"`, `href="part0005.xhtml#chap02"`, `href="../Styles/flow0002.css"`} {
		if !bytes.Contains(toc, []byte(want)) {
			t.Errorf("contents lacks %s", want)
		}
	}
	// Its <link type="image/x-cover"> names the cover as it was before the
	// book was made; it points at the cover EXTH 201 names.
	if !bytes.Contains(toc, []byte(`href="../Images/cover.jpg"`)) || b.Type("Images/cover.jpg") != "image/jpeg" {
		t.Error("the cover link points nowhere")
	}
	ch2, _ := b.Read("Text/part0005.xhtml")
	if !bytes.Contains(ch2, []byte(`id="chap02"`)) {
		t.Error("chapter two lacks the id the contents link to")
	}
	for _, f := range b.Files() {
		if d, _ := b.Read(f.Name); IsTextType(f.Type) && bytes.Contains(d, []byte("kindle:")) {
			t.Errorf("%s keeps a kindle: URL", f.Name)
		}
	}
	if b.Type("Images/image00002.jpg") != "image/jpeg" || b.Type("Styles/flow0002.css") != "text/css" {
		t.Errorf("files: %+v", b.Files())
	}
}

func TestKF8DRM(t *testing.T) {
	raw, _ := Open("testdata/alice.azw3")
	data := append([]byte(nil), raw.raw...)
	r0 := int(binary.BigEndian.Uint32(data[78:]))
	binary.BigEndian.PutUint16(data[r0+12:], 2)
	b := &Book{raw: data, byName: map[string]*entry{}}
	if err := b.openKF8(); !errors.Is(err, ErrDRM) {
		t.Errorf("DRM: %v", err)
	}
}

func TestPalmDOC(t *testing.T) {
	// "ab", " c" as one byte, 3 bytes back from distance 4 ("ab "), and
	// two bytes taken literally.
	in := []byte{'a', 'b', 0xC0 | 'c', 0x80, 4 << 3, 2, 0x80, 0x81}
	if got := string(palmDOC(in)); got != "ab cab \x80\x81" {
		t.Errorf("palmDOC: %q", got)
	}
}

func TestTrailing(t *testing.T) {
	// flags 0b11: a multibyte byte, under one trailing entry of 3 bytes.
	rec := append([]byte("text"), 'x', 0x01) // multibyte: 2 bytes
	rec = append(rec, 0x00, 0x00, 0x83)      // entry of size 3
	if n := trailingSize(rec, 3); n != 5 {
		t.Errorf("trailing: %d", n)
	}
}

// TestHuffCDIC decodes with a table where every byte is an 8-bit code for
// phrase 255-b, phrase 0x41 'A' itself compressed to "BC".
func TestHuffCDIC(t *testing.T) {
	huff := make([]byte, 24+256*4+64*4)
	copy(huff, "HUFF\x00\x00\x00\x18")
	binary.BigEndian.PutUint32(huff[8:], 24)
	binary.BigEndian.PutUint32(huff[12:], 24+256*4)
	for i := 0; i < 256; i++ {
		binary.BigEndian.PutUint32(huff[24+4*i:], 255<<8|0x80|8)
	}
	var cdic bytes.Buffer
	cdic.WriteString("CDIC\x00\x00\x00\x10")
	binary.Write(&cdic, binary.BigEndian, uint32(256))
	binary.Write(&cdic, binary.BigEndian, uint32(8))
	offs, body := make([]byte, 512), new(bytes.Buffer)
	for r := 0; r < 256; r++ {
		binary.BigEndian.PutUint16(offs[2*r:], uint16(512+body.Len()))
		b := byte(255 - r)
		if b == 'A' {
			binary.Write(body, binary.BigEndian, uint16(2))
			body.Write([]byte{'B', 'C'})
			continue
		}
		binary.Write(body, binary.BigEndian, uint16(0x8001))
		body.WriteByte(b)
	}
	cdic.Write(offs)
	cdic.Write(body.Bytes())
	p := &palmDB{data: append(huff, cdic.Bytes()...), offsets: []int{0, len(huff)}}
	h := &huffCDIC{}
	if err := h.load(p, 0, 2); err != nil {
		t.Fatal(err)
	}
	got, err := h.unpack([]byte("xAyA"), 0)
	if err != nil || string(got) != "xBCyBC" {
		t.Errorf("huff: %q %v", got, err)
	}
}

func TestBase32(t *testing.T) {
	if base32("000V") != 31 || base32("0010") != 32 || base32(strings.ToLower("0010")) != 32 {
		t.Error("base32")
	}
}

// A book split by size has files without headings that all carry the
// book's <title>; they're titled by their opening words instead.
func TestSpineTitles(t *testing.T) {
	b := &Book{byName: map[string]*entry{}}
	add := func(name, data string) {
		e := &entry{name: name, data: []byte(data), read: true}
		b.files, b.byName[name] = append(b.files, e), e
	}
	add("a.xhtml", page("紅樓夢", "<p>　　只是賈珍雖然此時心意滿足，但里面尤氏又犯了舊疾，不能料理事務</p>"))
	add("b.xhtml", page("紅樓夢", "<h2>第二回</h2><p>text</p>"))
	add("c.xhtml", page("Preface", "<p>short</p>"))
	add("d.xhtml", page(`"Cover"`, `<img src="cover.jpg"/>`))
	b.setSpine([]string{"a.xhtml", "b.xhtml", "c.xhtml", "d.xhtml"}, map[string]string{})
	var got []string
	for _, it := range b.Spine() {
		got = append(got, it.Title)
	}
	want := []string{"只是賈珍雖然此時心意滿足，但里面尤氏又犯了舊疾，…", "第二回", "Preface", "Cover"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("titles %q, want %q", got, want)
	}
}

// openCopy opens a copy of testdata/alice.azw3 that can be written.
func openCopy(t *testing.T) (*Book, string) {
	t.Helper()
	raw, err := os.ReadFile("testdata/alice.azw3")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "alice.azw3")
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	return b, p
}

// Saved without edits, the book reads back as it was opened.
func TestKF8SaveUnchanged(t *testing.T) {
	a, p := openCopy(t)
	before := map[string][]byte{}
	for _, f := range a.Files() {
		before[f.Name], _ = a.Read(f.Name)
	}
	if _, err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Files()) != len(before) {
		t.Fatalf("%d files, was %d", len(b.Files()), len(before))
	}
	for _, f := range b.Files() {
		if d, _ := b.Read(f.Name); !bytes.Equal(d, before[f.Name]) {
			t.Errorf("%s changed", f.Name)
		}
	}
	for i, it := range b.Spine() {
		if it.Title != a.Spine()[i].Title {
			t.Errorf("title %q, was %q", it.Title, a.Spine()[i].Title)
		}
	}
}

// An edited chapter and stylesheet are saved; links into the chapter after
// the edit still land on their anchors.
func TestKF8SaveEdits(t *testing.T) {
	a, p := openCopy(t)
	ch1 := "Text/part0004.xhtml"
	d, _ := a.Read(ch1)
	long := strings.Repeat("<p>爱丽丝 sat by her sister on the bank, and of having nothing to do.</p>", 400)
	d = bytes.Replace(d, []byte("Alice was beginning"), []byte("ALICE, edited, was beginning"), 1)
	d = bytes.Replace(d, []byte("</body>"), []byte(long+`<p id="new-end"><a href="part0005.xhtml#chap02">on</a> <a href="#new-end">here</a></p></body>`), 1)
	if err := a.Set(ch1, d); err != nil {
		t.Fatal(err)
	}
	css, _ := a.Read("Styles/flow0002.css")
	if err := a.Set("Styles/flow0002.css", append(css, "\n.edited { color: red }"...)); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Save(); err != nil {
		t.Fatal(err)
	}
	b, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := b.Read(ch1)
	if !bytes.Equal(got, d) {
		i := 0
		for i < len(got) && i < len(d) && got[i] == d[i] {
			i++
		}
		t.Errorf("chapter one reads back changed at %d:\n%q\n%q", i, d[max(0, i-80):min(len(d), i+80)], got[max(0, i-80):min(len(got), i+80)])
	}
	if got, _ := b.Read("Styles/flow0002.css"); !bytes.HasSuffix(got, []byte(".edited { color: red }")) {
		t.Error("the stylesheet edit is lost")
	}
	toc, _ := b.Read("Text/part0003.xhtml")
	for _, want := range []string{`href="part0004.xhtml#chap01"`, `href="part0005.xhtml#chap02"`, `href="../Images/cover.jpg"`} {
		if !bytes.Contains(toc, []byte(want)) {
			t.Errorf("contents lacks %s", want)
		}
	}
	if sp := b.Spine(); sp[4].Title != "CHAPTER I. Down the Rabbit-Hole" || sp[5].Title != "CHAPTER II. The Pool of Tears" {
		t.Errorf("spine: %+v", sp[3:6])
	}
}

func TestPalmDOCRoundTrip(t *testing.T) {
	samples := [][]byte{
		[]byte("It is a truth universally acknowledged, that a single man in possession of a good fortune, must be in want of a wife. " +
			"It is a truth universally acknowledged."),
		[]byte("三体 is two words and one. 三体三体三体 \x00\x01\x02\x08\x09 \xff\x80 end"),
	}
	r := uint32(1)
	noise := make([]byte, 4096)
	for i := range noise {
		r = r*1103515245 + 12345
		noise[i] = byte(r >> 16)
		if i%7 == 0 {
			noise[i] = ' '
		}
	}
	samples = append(samples, noise)
	for _, s := range samples {
		if got := palmDOC(compressPalmDOC(s)); !bytes.Equal(got, s) {
			t.Errorf("round trip of %d bytes differs", len(s))
		}
	}
}

func TestSplitText(t *testing.T) {
	text := []byte(strings.Repeat("a", textRecordSize-1) + "三" + "tail")
	recs := splitText(text)
	var got []byte
	for _, r := range recs {
		got = append(got, palmDOC(r[:len(r)-trailingSize(r, 1)])...)
	}
	if !bytes.Equal(got, text) || len(recs) != 2 || recs[0][len(recs[0])-1] != 2 {
		t.Errorf("records: %d, last byte %d", len(recs), recs[0][len(recs[0])-1])
	}
}
