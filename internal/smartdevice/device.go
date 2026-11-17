package smartdevice

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"maps"
	"net"
	"os"
	"path"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"dalibre/internal/device/usbms"
)

// The calibre version Dalibre says it is. KOReader answers a book it
// can't take with an ERROR only from calibre 4.18 on; before, it shows the
// error itself and says nothing.
var claimedVersion = []int{8, 0, 0}

const (
	replyTimeout = 30 * time.Second
	writeTimeout = 60 * time.Second
	// noopEvery is how long a quiet reader waits for a NOOP. KOReader
	// lets go after five minutes without a word.
	noopEvery = time.Minute
	// thumbnailHeight is the height of the cover calibre sends with a
	// book, for the reader to show while it arrives.
	thumbnailHeight = 160
)

var (
	// ErrPassword is a reader that was given the wrong password.
	ErrPassword = errors.New("smartdevice: wrong password")
	// ErrTooOld is a reader that can't stream books or their metadata.
	ErrTooOld = errors.New("smartdevice: the reader's app is too old")
	// ErrGone is a reader that went away.
	ErrGone = errors.New("smartdevice: the reader went away")
	// ErrTimeout is a reader that stopped answering.
	ErrTimeout  = errors.New("smartdevice: the reader stopped answering")
	errProtocol = errors.New("smartdevice: the reader answered what it shouldn't")
)

// Info is what a reader said of itself.
type Info struct {
	Name string // as "KOReader (Kobo_spaBW)"
	App  string // as "KOReader"
	// Extensions are the formats it reads, lower case, the one it likes
	// best first.
	Extensions []string
	FreeSpace  int64 // bytes
}

// Device is a reader connected.
type Device struct {
	s      *Server
	conn   net.Conn
	frames chan frame
	busy   atomic.Bool // an exchange waits for answers
	mu     sync.Mutex  // one exchange at a time
	done   chan struct{}
	once   sync.Once

	state     sync.Mutex
	info      Info
	books     map[string]onDevice // the books on the reader, by uuid
	canSendOK bool
	lastUsed  time.Time
}

// onDevice is a book on the reader: where it is, and its metadata's
// last change as the reader has it.
type onDevice struct {
	lpath    string
	modified time.Time
}

func newDevice(s *Server, conn net.Conn) *Device {
	return &Device{s: s, conn: conn, frames: make(chan frame, 64), done: make(chan struct{}), books: map[string]onDevice{}}
}

// Info is what the reader said of itself, and its free space.
func (d *Device) Info() Info {
	d.state.Lock()
	defer d.state.Unlock()
	return d.info
}

// Has reports whether the reader has the book of uuid.
func (d *Device) Has(uuid string) bool {
	d.state.Lock()
	defer d.state.Unlock()
	_, ok := d.books[uuid]
	return uuid != "" && ok
}

// Count is how many books the reader has from calibre or Dalibre.
func (d *Device) Count() int {
	d.state.Lock()
	defer d.state.Unlock()
	return len(d.books)
}

// Format is which of formats ("EPUB", "PDF") the reader likes best, ""
// when it reads none of them.
func (d *Device) Format(formats []string) string {
	d.state.Lock()
	defer d.state.Unlock()
	for _, ext := range d.info.Extensions {
		for _, f := range formats {
			if strings.EqualFold(f, ext) {
				return f
			}
		}
	}
	return ""
}

// Done is closed when the reader has gone.
func (d *Device) Done() <-chan struct{} { return d.done }

// close drops the connection.
func (d *Device) close() {
	d.once.Do(func() {
		close(d.done)
		d.conn.Close()
	})
}

// Disconnect tells the reader goodbye and lets it go.
func (d *Device) Disconnect() {
	if d.mu.TryLock() {
		d.busy.Store(true)
		if d.send(opNoop, map[string]any{"ejecting": true}) == nil {
			d.recvWithin(5*time.Second, nil)
		}
		d.busy.Store(false)
		d.mu.Unlock()
	}
	d.close()
}

// read reads the reader's messages for the exchange waiting; those that
// come when none waits (a parting NOOP) are dropped.
func (d *Device) read() {
	br := bufio.NewReader(d.conn)
	defer close(d.frames)
	for {
		f, err := readFrame(br)
		if err != nil {
			d.close()
			return
		}
		if !d.busy.Load() {
			continue
		}
		select {
		case d.frames <- f:
		case <-d.done:
			return
		}
	}
}

func (d *Device) begin() {
	d.mu.Lock()
	d.busy.Store(true)
}

func (d *Device) end() {
	d.state.Lock()
	d.lastUsed = time.Now()
	d.state.Unlock()
	d.busy.Store(false)
	d.mu.Unlock()
}

func (d *Device) send(op int, arg any) error {
	select {
	case <-d.done:
		return ErrGone
	default:
	}
	d.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	if err := writeFrame(d.conn, op, arg); err != nil {
		d.close()
		return ErrGone
	}
	return nil
}

func (d *Device) recv(into any) (int, error) { return d.recvWithin(replyTimeout, into) }

// recvWithin waits for the reader's next message, decoding its argument
// into into. A reader that takes longer than timeout is dropped: what it
// says late would answer the wrong question.
func (d *Device) recvWithin(timeout time.Duration, into any) (int, error) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case f, ok := <-d.frames:
		if !ok {
			return 0, ErrGone
		}
		if into != nil && f.op != opError {
			if err := json.Unmarshal(f.arg, into); err != nil {
				d.close()
				return 0, fmt.Errorf("%w: %v", errProtocol, err)
			}
		}
		if f.op == opError {
			var e struct {
				Message string `json:"message"`
			}
			json.Unmarshal(f.arg, &e)
			return opError, &RefusedError{Message: e.Message}
		}
		return f.op, nil
	case <-t.C:
		d.close()
		return 0, ErrTimeout
	}
}

// ask sends op and waits for the reader's OK.
func (d *Device) ask(op int, arg, into any) error {
	if err := d.send(op, arg); err != nil {
		return err
	}
	got, err := d.recv(into)
	if err != nil {
		return err
	}
	if got != opOK {
		d.close()
		return fmt.Errorf("%w: opcode %d", errProtocol, got)
	}
	return nil
}

// initInfo is a reader's answer to GET_INITIALIZATION_INFO.
type initInfo struct {
	VersionOK               bool     `json:"versionOK"`
	MaxBookContentPacketLen int      `json:"maxBookContentPacketLen"`
	AcceptedExtensions      []string `json:"acceptedExtensions"`
	CanStreamBooks          bool     `json:"canStreamBooks"`
	CanStreamMetadata       bool     `json:"canStreamMetadata"`
	CanReceiveBookBinary    bool     `json:"canReceiveBookBinary"`
	CanDeleteMultipleBooks  bool     `json:"canDeleteMultipleBooks"`
	CanSendOkToSendbook     bool     `json:"canSendOkToSendbook"`
	CanUseCachedMetadata    bool     `json:"canUseCachedMetadata"`
	PasswordHash            *string  `json:"passwordHash"`
	DeviceName              string   `json:"deviceName"`
	DeviceKind              string   `json:"deviceKind"`
	AppName                 string   `json:"appName"`
}

// handshake greets a reader that connected, as calibre does, and reads
// which books it has.
func (d *Device) handshake() error {
	d.begin()
	defer d.end()
	go d.read()
	cfg := d.s.cfg
	libName, libUUID := "", ""
	if cfg.Library != nil {
		libName, libUUID = cfg.Library()
	}

	challenge, hash := "", ""
	if cfg.Password != "" {
		challenge = time.Now().UTC().Format("2006-01-02T15:04:05.000000-07:00")
		sum := sha1.Sum([]byte(cfg.Password + challenge))
		hash = hex.EncodeToString(sum[:])
	}
	var in initInfo
	err := d.ask(opGetInitializationInfo, map[string]any{
		"serverProtocolVersion":  1,
		"validExtensions":        validExtensions,
		"passwordChallenge":      challenge,
		"currentLibraryName":     libName,
		"currentLibraryUUID":     libUUID,
		"pubdateFormat":          "MMM yyyy",
		"timestampFormat":        "dd MMM yyyy",
		"lastModifiedFormat":     "dd MMM yyyy",
		"calibre_version":        claimedVersion,
		"canSupportUpdateBooks":  true,
		"canSupportLpathChanges": true,
	}, &in)
	if err != nil {
		return err
	}
	if !in.VersionOK || in.MaxBookContentPacketLen <= 0 || len(in.AcceptedExtensions) == 0 {
		return errProtocol
	}
	if !in.CanStreamBooks || !in.CanStreamMetadata || !in.CanReceiveBookBinary || !in.CanDeleteMultipleBooks {
		return ErrTooOld
	}
	if cfg.Password != "" && (in.PasswordHash == nil || *in.PasswordHash != hash) {
		d.send(opDisplayMessage, map[string]any{
			"messageKind":        messagePasswordError,
			"currentLibraryName": libName,
			"currentLibraryUUID": libUUID,
		})
		return ErrPassword
	}
	exts := make([]string, 0, len(in.AcceptedExtensions))
	for _, e := range in.AcceptedExtensions {
		exts = append(exts, strings.ToLower(e))
	}
	name := in.DeviceName
	if name == "" {
		name = in.DeviceKind
	}
	d.state.Lock()
	d.info = Info{Name: name, App: in.AppName, Extensions: exts}
	d.canSendOK = in.CanSendOkToSendbook
	d.state.Unlock()

	// At once: KOReader gives up a second after its answer.
	var dev struct {
		DeviceInfo map[string]any `json:"device_info"`
	}
	if err := d.ask(opGetDeviceInformation, nil, &dev); err != nil {
		return err
	}
	// The reader keeps this as driveinfo.calibre; fill it as calibre does.
	di := dev.DeviceInfo
	if di == nil {
		di = map[string]any{}
	}
	if s, _ := di["device_store_uuid"].(string); s == "" {
		di["device_store_uuid"] = newUUID()
	}
	if di["device_name"] == nil {
		di["device_name"] = name
	}
	di["location_code"] = "main"
	di["last_library_uuid"] = libUUID
	di["calibre_version"] = fmt.Sprintf("%d.%d.%d", claimedVersion[0], claimedVersion[1], claimedVersion[2])
	di["date_last_connected"] = isoTime(time.Now())
	di["prefix"] = ""
	if err := d.ask(opSetCalibreDeviceInfo, di, nil); err != nil {
		return err
	}
	if err := d.readFreeSpace(); err != nil {
		return err
	}

	// The books on the reader: a count, then one message a book. A reader
	// with a metadata cache sends only ids, which is all Dalibre needs.
	var count struct {
		Count int `json:"count"`
	}
	if err := d.ask(opGetBookCount, map[string]any{
		"canStream":                true,
		"canScan":                  true,
		"willUseCachedMetadata":    in.CanUseCachedMetadata,
		"supportsSync":             false,
		"canSupportBookFormatSync": true,
	}, &count); err != nil {
		return err
	}
	books := map[string]onDevice{}
	for range count.Count {
		var b struct {
			UUID         string `json:"uuid"`
			LPath        string `json:"lpath"`
			LastModified any    `json:"last_modified"`
		}
		op, err := d.recv(&b)
		if err != nil {
			return err
		}
		if op != opOK {
			d.close()
			return errProtocol
		}
		if b.UUID != "" {
			books[b.UUID] = onDevice{b.LPath, parseTime(b.LastModified)}
		}
	}
	d.state.Lock()
	d.books = books
	d.state.Unlock()
	return nil
}

func (d *Device) readFreeSpace() error {
	var fs struct {
		Free int64 `json:"free_space_on_device"`
	}
	if err := d.ask(opFreeSpace, nil, &fs); err != nil {
		return err
	}
	d.state.Lock()
	d.info.FreeSpace = fs.Free
	d.state.Unlock()
	return nil
}

// keepAlive sends a quiet reader a NOOP now and then, so it stays.
func (d *Device) keepAlive() {
	t := time.NewTicker(noopEvery / 4)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case <-t.C:
		}
		if !d.mu.TryLock() {
			continue
		}
		d.busy.Store(true)
		d.state.Lock()
		due := time.Since(d.lastUsed) >= noopEvery
		d.state.Unlock()
		if due {
			d.ask(opNoop, nil, nil)
		}
		d.end()
	}
}

// Book is a book to send.
type Book struct {
	Path   string // its file
	Format string // the file's format, as "EPUB"
	UUID   string
	Title  string
	// Authors, AuthorSort, Tags, Series and SeriesIndex are shown and
	// searched on the reader.
	Authors      []string
	AuthorSort   string
	Tags         []string
	Series       string
	SeriesIndex  float64
	LastModified time.Time
	// Cover is the book's cover image file, "" when it has none.
	Cover string
}

// RefusedError is a book the reader wouldn't take, saying why: no space
// left, a name it can't write.
type RefusedError struct {
	Title   string
	Message string
}

func (e *RefusedError) Error() string {
	if e.Title == "" {
		return e.Message
	}
	return e.Title + ": " + e.Message
}

// Send sends books to the reader, one after the other, calling sent with
// how many are done after each. A book that can't be read or that the
// reader refuses is skipped and its error returned with the others'; when
// the reader goes, Send stops.
func (d *Device) Send(books []Book, sent func(n int)) error {
	// Told after the exchange ends, so whoever is told can start another.
	defer d.s.changed()
	d.begin()
	defer d.end()
	var errs []error
	for i, b := range books {
		err := d.sendBook(b, i, len(books))
		var refused *RefusedError
		var pathErr *os.PathError
		switch {
		case errors.As(err, &refused):
			refused.Title = b.Title
			errs = append(errs, err)
		case errors.As(err, &pathErr):
			errs = append(errs, err)
		case err != nil:
			return errors.Join(append(errs, err)...)
		}
		if sent != nil {
			sent(i + 1)
		}
	}
	if err := d.readFreeSpace(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

func (d *Device) sendBook(b Book, i, total int) error {
	f, err := os.Open(b.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	length := st.Size()
	lpath := LPath(b)
	d.state.Lock()
	wantsOK := d.canSendOK
	d.state.Unlock()
	if err := d.send(opSendBook, map[string]any{
		"lpath":                  lpath,
		"length":                 length,
		"metadata":               b.metadata(lpath, length),
		"thisBook":               i,
		"totalBooks":             total,
		"willStreamBooks":        true,
		"willStreamBinary":       true,
		"wantsSendOkToSendbook":  wantsOK,
		"canSupportLpathChanges": true,
	}); err != nil {
		return err
	}
	if wantsOK {
		var ok struct {
			LPath string `json:"lpath"`
		}
		op, err := d.recv(&ok)
		if err != nil {
			return err
		}
		if op != opOK {
			d.close()
			return errProtocol
		}
		if ok.LPath != "" {
			lpath = ok.LPath
		}
	}
	// Then the file, as it is; the reader knows its length.
	buf := make([]byte, 64<<10)
	var n int64
	for {
		m, rerr := f.Read(buf)
		if m > 0 {
			d.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
			if _, err := d.conn.Write(buf[:m]); err != nil {
				d.close()
				return ErrGone
			}
			n += int64(m)
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil || n > length {
			// The reader still waits for the rest; nothing can follow.
			d.close()
			return fmt.Errorf("smartdevice: reading %s: %v", b.Path, rerr)
		}
	}
	if n != length {
		d.close()
		return fmt.Errorf("smartdevice: %s changed while it was sent", b.Path)
	}
	if b.UUID != "" {
		d.state.Lock()
		d.books[b.UUID] = onDevice{lpath, b.LastModified.UTC().Truncate(time.Second)}
		d.state.Unlock()
	}
	return nil
}

// metadata is the book as calibre describes it to the reader; a size
// below 0 is left out, for the reader to keep its own. KOReader
// keeps uuid, lpath, last_modified, size, title, authors, author_sort,
// tags, series and series_index.
func (b Book) metadata(lpath string, size int64) map[string]any {
	m := map[string]any{
		"uuid":          b.UUID,
		"lpath":         lpath,
		"title":         b.Title,
		"title_sort":    b.Title,
		"authors":       nonNil(b.Authors),
		"author_sort":   b.AuthorSort,
		"tags":          nonNil(b.Tags),
		"series":        nil,
		"series_index":  nil,
		"last_modified": isoTime(b.LastModified),
	}
	if b.Series != "" {
		m["series"], m["series_index"] = b.Series, b.SeriesIndex
	}
	if size >= 0 {
		m["size"] = size
	}
	m["thumbnail"] = thumbnail(b.Cover)
	return m
}

// thumbnail is the cover in file as calibre sends it: width, height and
// the JPEG in base64; nil when there is none.
func thumbnail(file string) any {
	if file == "" {
		return nil
	}
	img, err := os.ReadFile(file)
	if err != nil {
		return nil
	}
	jpg := usbms.Thumbnail(img, thumbnailHeight)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(jpg))
	if err != nil {
		return nil
	}
	return []any{cfg.Width, cfg.Height, base64.StdEncoding.EncodeToString(jpg)}
}

// Delete deletes the books of uuids from the reader; those it doesn't
// have are left out.
func (d *Device) Delete(uuids []string) error {
	d.state.Lock()
	var lpaths []string
	for _, u := range uuids {
		if b, ok := d.books[u]; ok && b.lpath != "" {
			lpaths = append(lpaths, b.lpath)
		}
	}
	d.state.Unlock()
	if len(lpaths) == 0 {
		return nil
	}
	defer d.s.changed()
	d.begin()
	defer d.end()
	// An OK, then an OK with its uuid for each book deleted.
	if err := d.ask(opDeleteBook, map[string]any{"lpaths": lpaths}, nil); err != nil {
		return err
	}
	for range lpaths {
		var r struct {
			UUID string `json:"uuid"`
		}
		if _, err := d.recv(&r); err != nil {
			return err
		}
	}
	d.state.Lock()
	for _, u := range uuids {
		delete(d.books, u)
	}
	d.state.Unlock()
	return d.readFreeSpace()
}

// FormatOf is the format, as "EPUB", of the book of uuid on the reader;
// "" when it doesn't have it.
func (d *Device) FormatOf(uuid string) string {
	d.state.Lock()
	defer d.state.Unlock()
	b, ok := d.books[uuid]
	if !ok {
		return ""
	}
	return strings.ToUpper(strings.TrimPrefix(path.Ext(b.lpath), "."))
}

// Stale picks, of books, those on the reader whose metadata changed
// since it was sent.
func (d *Device) Stale(books []Book) []Book {
	d.state.Lock()
	defer d.state.Unlock()
	var stale []Book
	for _, b := range books {
		on, ok := d.books[b.UUID]
		if ok && b.UUID != "" && !b.LastModified.IsZero() && !on.modified.Equal(b.LastModified.UTC().Truncate(time.Second)) {
			stale = append(stale, b)
		}
	}
	return stale
}

// UpdateMetadata sends the reader the metadata of books it has, as
// calibre does when their metadata changed; the reader doesn't answer.
// A book's Path is the file the reader has, in the format FormatOf says,
// for its size: the reader replaces what it kept of the book.
func (d *Device) UpdateMetadata(books []Book) error {
	d.state.Lock()
	on := maps.Clone(d.books)
	d.state.Unlock()
	var send []map[string]any
	for _, b := range books {
		if o, ok := on[b.UUID]; ok && b.UUID != "" {
			size := int64(-1)
			if st, err := os.Stat(b.Path); err == nil {
				size = st.Size()
			}
			send = append(send, b.metadata(o.lpath, size))
		}
	}
	if len(send) == 0 {
		return nil
	}
	d.begin()
	defer d.end()
	if err := d.send(opSendBooklists, map[string]any{
		"count": len(send), "collections": map[string]any{}, "willStreamMetadata": true, "supportsSync": false,
	}); err != nil {
		return err
	}
	for i, m := range send {
		if err := d.send(opSendBookMetadata, map[string]any{"index": i, "count": len(send), "data": m, "supportsSync": false}); err != nil {
			return err
		}
	}
	d.state.Lock()
	for _, b := range books {
		if on, ok := d.books[b.UUID]; ok {
			on.modified = b.LastModified.UTC().Truncate(time.Second)
			d.books[b.UUID] = on
		}
	}
	d.state.Unlock()
	return nil
}

// parseTime reads a date as calibre's JSON has it: an ISO string, or
// {"__class__": "datetime.datetime", "__value__": "..."}; zero when it
// can't.
func parseTime(v any) time.Time {
	if m, ok := v.(map[string]any); ok {
		v = m["__value__"]
	}
	s, _ := v.(string)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999-07:00", "2006-01-02 15:04:05.999999-07:00"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC().Truncate(time.Second)
		}
	}
	return time.Time{}
}

// LPath is where on the reader b goes, relative to its folder for books:
// "author sort/title - authors.ext", as calibre's default template has it.
func LPath(b Book) string {
	author := b.AuthorSort
	if author == "" {
		author = strings.Join(b.Authors, " & ")
	}
	title := b.Title
	if len(b.Authors) > 0 {
		title += " - " + strings.Join(b.Authors, " & ")
	}
	return pathPart(author, 60) + "/" + pathPart(title, 120) + "." + strings.ToLower(b.Format)
}

// pathPart makes s a file name every file system takes, of at most n
// bytes.
func pathPart(s string, n int) string {
	s = strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`/\:*?"<>|`, r) {
			return '_'
		}
		return r
	}, s)
	if len(s) > n {
		cut := n
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	s = strings.Trim(s, " .")
	if s == "" {
		return "Unknown"
	}
	return s
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// isoTime is t as calibre's JSON has dates.
func isoTime(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.UTC().Format("2006-01-02T15:04:05-07:00")
}

func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// validExtensions are the formats Dalibre can send, as calibre lists them
// to the reader.
var validExtensions = []string{
	"epub", "kepub", "azw3", "azw", "mobi", "pdf", "fb2", "cbz", "cbr", "cb7", "djvu", "djv",
	"docx", "doc", "rtf", "txt", "md", "htm", "html", "xhtml", "pdb", "prc", "chm", "lit", "lrf", "odt", "zip",
}
