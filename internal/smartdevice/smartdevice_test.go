package smartdevice

import (
	"bufio"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"encoding/base64"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := writeFrame(&buf, opOK, map[string]any{"title": "三体"}); err != nil {
		t.Fatal(err)
	}
	// The length counts bytes, from the "[".
	s := buf.String()
	i := bytes.IndexByte(buf.Bytes(), '[')
	n, _ := strconv.Atoi(s[:i])
	if n != len(s)-i {
		t.Fatalf("length %d, JSON is %d bytes", n, len(s)-i)
	}
	buf.WriteString(`2[]`) // too short to be a message
	br := bufio.NewReader(&buf)
	f, err := readFrame(br)
	if err != nil || f.op != opOK {
		t.Fatalf("readFrame = %+v, %v", f, err)
	}
	var arg map[string]string
	json.Unmarshal(f.arg, &arg)
	if arg["title"] != "三体" {
		t.Fatalf("arg = %v", arg)
	}
	if _, err := readFrame(br); err == nil {
		t.Fatal("a message without opcode was read")
	}
}

func TestLPath(t *testing.T) {
	got := LPath(Book{Title: "What If? / Again", Authors: []string{"Randall Munroe"}, AuthorSort: "Munroe, Randall", Format: "EPUB"})
	if want := "Munroe, Randall/What If_ _ Again - Randall Munroe.epub"; got != want {
		t.Fatalf("LPath = %q, want %q", got, want)
	}
	if got := LPath(Book{Title: "...", Format: "PDF"}); got != "Unknown/Unknown.pdf" {
		t.Fatalf("LPath = %q", got)
	}
}

// fakeReader answers as KOReader's calibre plugin does (wireless.lua).
type fakeReader struct {
	conn     net.Conn
	br       *bufio.Reader
	password string
	books    []map[string]any // what it has, as metadata.calibre
	refuse   string           // an lpath it answers ERROR to

	mu        sync.Mutex
	inbox     map[string][]byte
	received  []map[string]any
	messages  []int // DISPLAY_MESSAGE kinds
	busy      bool  // was told CALIBRE_BUSY
	deleted   []string
	updated   []map[string]any // SEND_BOOK_METADATA's data
	driveinfo map[string]any
}

func dialReader(t *testing.T, port int, r *fakeReader) *fakeReader {
	t.Helper()
	conn, err := net.Dial("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	r.conn, r.br, r.inbox = conn, bufio.NewReader(conn), map[string][]byte{}
	t.Cleanup(func() { conn.Close() })
	go r.serve()
	return r
}

func (r *fakeReader) reply(op int, arg any) { writeFrame(r.conn, op, arg) }

func (r *fakeReader) serve() {
	for {
		f, err := readFrame(r.br)
		if err != nil {
			return
		}
		var arg map[string]any
		json.Unmarshal(f.arg, &arg)
		switch f.op {
		case opGetInitializationInfo:
			hash := ""
			if c, _ := arg["passwordChallenge"].(string); r.password != "" && c != "" {
				sum := sha1.Sum([]byte(r.password + c))
				hash = hex.EncodeToString(sum[:])
			}
			r.reply(opOK, map[string]any{
				"appName": "KOReader", "acceptedExtensions": []string{"epub", "pdf"}, "cacheUsesLpaths": true,
				"canAcceptLibraryInfo": true, "canDeleteMultipleBooks": true, "canReceiveBookBinary": true,
				"canSendOkToSendbook": true, "canStreamBooks": true, "canStreamMetadata": true,
				"canUseCachedMetadata": true, "deviceKind": "Kobo", "deviceName": "KOReader (Kobo)",
				"passwordHash": hash, "maxBookContentPacketLen": 4096, "versionOK": true,
			})
		case opGetDeviceInformation:
			r.reply(opOK, map[string]any{"device_info": map[string]any{"device_name": "KOReader (Kobo)"}, "version": "v1", "device_version": "v1"})
		case opSetCalibreDeviceInfo:
			r.mu.Lock()
			r.driveinfo = arg
			r.mu.Unlock()
			r.reply(opOK, nil)
		case opFreeSpace:
			r.reply(opOK, map[string]any{"free_space_on_device": 1 << 30})
		case opGetBookCount:
			r.reply(opOK, map[string]any{"willStream": true, "willScan": true, "count": len(r.books)})
			for i, b := range r.books {
				r.reply(opOK, map[string]any{"priKey": i + 1, "uuid": b["uuid"], "lpath": b["lpath"], "last_modified": b["last_modified"]})
			}
		case opSendBook:
			lpath, _ := arg["lpath"].(string)
			if lpath == r.refuse {
				r.reply(opError, map[string]any{"message": "No space left on device"})
				continue
			}
			n := int(arg["length"].(float64))
			r.reply(opOK, nil)
			data := make([]byte, n)
			if _, err := io.ReadFull(r.br, data); err != nil {
				return
			}
			r.mu.Lock()
			r.inbox[lpath] = data
			r.received = append(r.received, arg["metadata"].(map[string]any))
			r.mu.Unlock()
		case opNoop:
			r.reply(opOK, nil)
		case opDeleteBook:
			r.reply(opOK, nil)
			for _, l := range arg["lpaths"].([]any) {
				r.mu.Lock()
				r.deleted = append(r.deleted, l.(string))
				r.mu.Unlock()
				r.reply(opOK, map[string]any{"uuid": "x"})
			}
		case opSendBooklists:
			// KOReader doesn't answer, nor look at it.
		case opSendBookMetadata:
			r.mu.Lock()
			r.updated = append(r.updated, arg["data"].(map[string]any))
			r.mu.Unlock()
		case opDisplayMessage:
			r.mu.Lock()
			r.messages = append(r.messages, int(arg["messageKind"].(float64)))
			r.mu.Unlock()
		case opCalibreBusy:
			r.mu.Lock()
			r.busy = true
			r.mu.Unlock()
		}
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func listen(t *testing.T, cfg Config) (*Server, chan struct{}) {
	t.Helper()
	changed := make(chan struct{}, 16)
	cfg.Port = freePort(t)
	cfg.OnChange = func() { changed <- struct{}{} }
	s, err := Listen(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s, changed
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for range 200 {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestSendBooks(t *testing.T) {
	s, _ := listen(t, Config{Name: "studio.local", Library: func() (string, string) { return "Books", "lib-1" }})
	r := dialReader(t, s.Port(), &fakeReader{
		books:  []map[string]any{{"uuid": "u-old", "lpath": "A/Old.epub", "last_modified": "2020-01-01T00:00:00+00:00"}},
		refuse: "Unknown/Too Big.pdf",
	})
	waitFor(t, "the reader to connect", func() bool { return s.Device() != nil })
	d := s.Device()
	if info := d.Info(); info.Name != "KOReader (Kobo)" || info.App != "KOReader" || info.FreeSpace != 1<<30 {
		t.Fatalf("Info = %+v", info)
	}
	if !d.Has("u-old") || d.Has("u-new") || d.Count() != 1 {
		t.Fatal("the reader's books weren't read")
	}
	if f := d.Format([]string{"PDF", "EPUB"}); f != "EPUB" {
		t.Fatalf("Format = %q, want the reader's first choice", f)
	}
	if f := d.Format([]string{"MOBI"}); f != "" {
		t.Fatalf("Format = %q for a format it doesn't read", f)
	}
	r.mu.Lock()
	di := r.driveinfo
	r.mu.Unlock()
	if di["device_store_uuid"] == "" || di["last_library_uuid"] != "lib-1" || di["location_code"] != "main" {
		t.Fatalf("driveinfo = %v", di)
	}

	dir := t.TempDir()
	content := bytes.Repeat([]byte("dalibre "), 20000) // more than a write
	epub := filepath.Join(dir, "book.epub")
	os.WriteFile(epub, content, 0o644)
	pdf := filepath.Join(dir, "big.pdf")
	os.WriteFile(pdf, []byte("pdf"), 0o644)
	var cover bytes.Buffer
	png.Encode(&cover, image.NewRGBA(image.Rect(0, 0, 300, 480)))
	coverFile := filepath.Join(dir, "cover.png")
	os.WriteFile(coverFile, cover.Bytes(), 0o644)
	var sent []int
	err := d.Send([]Book{
		{Path: pdf, Format: "PDF", UUID: "u-big", Title: "Too Big"},
		{Path: filepath.Join(dir, "missing.epub"), Format: "EPUB", UUID: "u-gone", Title: "Gone"},
		{Path: epub, Format: "EPUB", UUID: "u-new", Title: "三体", Authors: []string{"刘慈欣"}, AuthorSort: "Liu, Cixin",
			Tags: []string{"SF"}, Series: "Remembrance", SeriesIndex: 1, LastModified: time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC),
			Cover: coverFile},
	}, func(n int) { sent = append(sent, n) })
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Title != "Too Big" || refused.Message != "No space left on device" {
		t.Fatalf("Send error = %v, want the refused book's", err)
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Send error = %v, want the missing file's too", err)
	}
	if len(sent) != 3 || sent[2] != 3 {
		t.Fatalf("progress = %v", sent)
	}
	r.mu.Lock()
	got := r.inbox["Liu, Cixin/三体 - 刘慈欣.epub"]
	meta := r.received[0]
	r.mu.Unlock()
	if !bytes.Equal(got, content) {
		t.Fatalf("the reader got %d bytes, want %d", len(got), len(content))
	}
	if meta["uuid"] != "u-new" || meta["series"] != "Remembrance" || meta["series_index"] != 1.0 ||
		meta["last_modified"] != "2024-05-01T12:00:00+00:00" || meta["size"] != float64(len(content)) {
		t.Fatalf("metadata = %v", meta)
	}
	// The cover goes along as calibre sends it: a JPEG 160 pixels high.
	thumb, _ := meta["thumbnail"].([]any)
	if len(thumb) != 3 || thumb[0] != 100.0 || thumb[1] != 160.0 {
		t.Fatalf("thumbnail = %.40v", meta["thumbnail"])
	}
	jpg, _ := base64.StdEncoding.DecodeString(thumb[2].(string))
	if cfg, err := jpeg.DecodeConfig(bytes.NewReader(jpg)); err != nil || cfg.Width != 100 || cfg.Height != 160 {
		t.Fatalf("thumbnail JPEG = %+v, %v", cfg, err)
	}
	if !d.Has("u-new") {
		t.Fatal("a book sent isn't known to be on the reader")
	}

	// The connection still works after a refusal and a missing file.
	if err := d.Send([]Book{{Path: pdf, Format: "PDF", UUID: "u-pdf", Title: "Small"}}, nil); err != nil {
		t.Fatal(err)
	}

	// Another reader is told calibre is busy.
	other := dialReader(t, s.Port(), &fakeReader{})
	waitFor(t, "the busy answer", func() bool { other.mu.Lock(); defer other.mu.Unlock(); return other.busy })

	// The reader going away is noticed.
	r.conn.Close()
	waitFor(t, "the reader to go", func() bool { return s.Device() == nil })
}

func TestPassword(t *testing.T) {
	s, _ := listen(t, Config{Password: "secret"})
	r := dialReader(t, s.Port(), &fakeReader{password: "wrong"})
	waitFor(t, "the password error", func() bool { return errors.Is(s.Err(), ErrPassword) })
	waitFor(t, "the reader to be told", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return len(r.messages) == 1 })
	if s.Device() != nil || r.messages[0] != messagePasswordError {
		t.Fatal("a reader with the wrong password connected")
	}

	dialReader(t, s.Port(), &fakeReader{password: "secret"})
	waitFor(t, "the reader to connect", func() bool { return s.Device() != nil })
	if s.Err() != nil {
		t.Fatalf("Err = %v after a reader connected", s.Err())
	}
}

// koreaderDiscovery is how KOReader reads the discovery answer.
var koreaderDiscovery = regexp.MustCompile(`^calibre wireless device client \(on (.*?)\);(\d+),(\d+)$`)

func TestDiscovery(t *testing.T) {
	s, _ := listen(t, Config{Name: "my (mac);x"})
	if !s.Discoverable() {
		t.Skip("no broadcast port free here")
	}
	conn, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	to := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: s.udp.LocalAddr().(*net.UDPAddr).Port}
	conn.WriteTo([]byte("hello"), to)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 512)
	n, _, err := conn.ReadFrom(buf)
	if err != nil {
		t.Fatal(err)
	}
	m := koreaderDiscovery.FindStringSubmatch(string(buf[:n]))
	if m == nil || m[1] != "my (macx" || m[3] != strconv.Itoa(s.Port()) {
		t.Fatalf("answer %q isn't what KOReader reads", buf[:n])
	}
}

func TestDeleteAndUpdateMetadata(t *testing.T) {
	s, _ := listen(t, Config{})
	r := dialReader(t, s.Port(), &fakeReader{books: []map[string]any{
		{"uuid": "u-1", "lpath": "A/One.epub", "last_modified": "2024-05-01T12:00:00+00:00"},
		{"uuid": "u-2", "lpath": "B/Two.pdf", "last_modified": map[string]any{"__class__": "datetime.datetime", "__value__": "2024-05-01T12:00:00.250000+00:00"}},
		{"uuid": "u-3", "lpath": "C/Three.epub", "last_modified": "2024-05-01T12:00:00+00:00"},
	}})
	waitFor(t, "the reader to connect", func() bool { return s.Device() != nil })
	d := s.Device()
	if d.FormatOf("u-2") != "PDF" || d.FormatOf("u-9") != "" {
		t.Fatalf("FormatOf = %q", d.FormatOf("u-2"))
	}
	same := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	books := []Book{
		{UUID: "u-1", Title: "One", LastModified: same},
		{UUID: "u-2", Title: "Two", LastModified: same.Add(400 * time.Millisecond)}, // the same second
		{UUID: "u-3", Title: "Three, renamed", LastModified: same.Add(time.Hour)},
		{UUID: "u-9", Title: "Not there", LastModified: same.Add(time.Hour)},
	}
	stale := d.Stale(books)
	if len(stale) != 1 || stale[0].UUID != "u-3" {
		t.Fatalf("Stale = %+v", stale)
	}
	file := filepath.Join(t.TempDir(), "three.epub")
	os.WriteFile(file, []byte("12345"), 0o644)
	stale[0].Path = file
	if err := d.UpdateMetadata(stale); err != nil {
		t.Fatal(err)
	}
	if len(d.Stale(books)) != 0 {
		t.Fatal("a book updated is still stale")
	}
	// Delete waits for answers, so the metadata sent before has arrived.
	if err := d.Delete([]string{"u-1", "u-9"}); err != nil {
		t.Fatal(err)
	}
	r.mu.Lock()
	updated, deleted := r.updated, r.deleted
	r.mu.Unlock()
	if len(updated) != 1 || updated[0]["title"] != "Three, renamed" || updated[0]["lpath"] != "C/Three.epub" || updated[0]["size"] != 5.0 {
		t.Fatalf("updated = %v", updated)
	}
	if len(deleted) != 1 || deleted[0] != "A/One.epub" || d.Has("u-1") || !d.Has("u-2") {
		t.Fatalf("deleted = %v", deleted)
	}
	// The connection is still in step.
	if err := d.Delete([]string{"u-2"}); err != nil || d.Count() != 1 {
		t.Fatalf("Delete = %v, %d left", err, d.Count())
	}
}
