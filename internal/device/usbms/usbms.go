// Package usbms is calibre's library on a reader that is a storage of
// files (calibre's USBMS driver, which its Kindle and Kobo drivers build
// on): the book files in the reader's folder for them, and calibre's own
// record of them, metadata.calibre and driveinfo.calibre (cache.go).
// Drivers give a Config for their reader; Open reads the library.
package usbms

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"dalibre/internal/calibre"
	"dalibre/internal/device"
	"dalibre/internal/ebookmeta"
)

// Config is what a driver says of its reader.
type Config struct {
	// Info is the reader's kind, name, model and firmware; its ID comes
	// from driveinfo.calibre.
	Info device.Info
	// EbookDir is the folder books go in, as "documents"; "" for the
	// storage's root.
	EbookDir string
	// SubDirs lets books be in folders under EbookDir, as the template
	// makes them.
	SubDirs bool
	// Formats are the formats the reader takes, best first, upper case.
	// Files of other formats aren't the library's.
	Formats []string
	// Template places a sent book under EbookDir; DefaultTemplate when "".
	Template string
	// ThumbnailHeight is the height of the covers kept in
	// metadata.calibre.
	ThumbnailHeight int
	// Sidecars are the files and folders the reader keeps beside the book
	// at lpath (its notes, its page numbers), removed with it.
	Sidecars func(lpath string) []string
	// SkipDir reports whether the folder name (a path from the storage's
	// root) holds no books, to leave it out of the scan.
	SkipDir func(name string) bool
	// AfterSend is called once a book's file is on the reader, for what
	// else the reader needs, as a Kindle's cover thumbnail. file is the
	// file sent, on this computer.
	AfterSend func(st device.Storage, b device.Book, file string, cover []byte)
	// LibraryUUID, when set, is called on sending to know the library
	// sending, recorded in driveinfo.calibre as calibre does.
	LibraryUUID func() string
}

// Device is a reader's library.
type Device struct {
	cfg  Config
	st   device.Storage
	info device.Info

	mu      sync.Mutex
	entries []entry // as metadata.calibre lists them, after a scan
	books   []device.Book
	di      driveInfo
}

// Open reads the library on st: driveinfo.calibre (made when missing, as
// calibre makes it), metadata.calibre, and the book files, whose new ones
// it reads the metadata of. It writes metadata.calibre again when the
// files changed since.
func Open(ctx context.Context, st device.Storage, cfg Config) (*Device, error) {
	if cfg.Template == "" {
		cfg.Template = DefaultTemplate
	}
	if cfg.ThumbnailHeight == 0 {
		cfg.ThumbnailHeight = 500
	}
	d := &Device{cfg: cfg, st: st, info: cfg.Info}
	d.di = readDriveInfo(st)
	if d.di.str("device_store_uuid") == "" {
		d.di.set("device_store_uuid", newUUID())
	}
	if d.di.str("device_name") == "" {
		name := cfg.Info.Model
		if name == "" {
			name = cfg.Info.Name
		}
		d.di.set("device_name", name)
	}
	if _, ok := d.di["location_code"]; !ok {
		d.di.set("location_code", "main")
	}
	if _, ok := d.di["last_library_uuid"]; !ok {
		d.di.set("last_library_uuid", nil)
	}
	prefix := ""
	if p := st.LocalPath("."); p != "" {
		prefix = strings.TrimSuffix(filepath.ToSlash(p), "/") + "/"
	}
	d.di.set("prefix", prefix)
	d.di.set("date_last_connected", time.Now().UTC().Format(dateLayout))
	if err := d.di.write(st); err != nil {
		return nil, err
	}
	d.info.ID = d.di.str("device_store_uuid")
	if err := d.Refresh(ctx); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *Device) Info() device.Info       { return d.info }
func (d *Device) Storage() device.Storage { return d.st }
func (d *Device) Formats() []string       { return slices.Clone(d.cfg.Formats) }
func (d *Device) Close()                  {}

func (d *Device) Usage() (free, total int64, err error) { return d.st.Usage() }

func (d *Device) Books() []device.Book {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.books)
}

// takes reports whether the reader takes the file at lpath.
func (d *Device) takes(lpath string) bool {
	ext := strings.ToUpper(strings.TrimPrefix(path.Ext(lpath), "."))
	return ext != "" && slices.Contains(d.cfg.Formats, ext)
}

// Refresh scans the reader's books, as calibre's USBMS books() does: the
// files of the formats it takes under EbookDir; those metadata.calibre
// has keep its metadata, the new ones are read; the entries whose file is
// gone are dropped.
func (d *Device) Refresh(ctx context.Context) error {
	cached, err := readCache(d.st)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		// A broken cache is calibre's to mend too: start afresh, as it does.
		cached = nil
	}
	files, err := d.scan(ctx)
	if err != nil {
		return err
	}
	byPath := map[string]entry{}
	for _, e := range cached {
		if lp := e.str("lpath"); lp != "" {
			byPath[lp] = e
		}
	}
	changed := false
	var entries []entry
	seen := map[string]bool{}
	// Cached entries first, in their order, then the new files.
	for _, e := range cached {
		lp := e.str("lpath")
		size, ok := files[lp]
		if !ok || seen[lp] {
			changed = true
			continue
		}
		seen[lp] = true
		if int64(e.num("size")) != size {
			e.set("size", size)
			changed = true
		}
		entries = append(entries, e)
	}
	var fresh []string
	for lp := range files {
		if !seen[lp] {
			fresh = append(fresh, lp)
		}
	}
	slices.Sort(fresh)
	for _, lp := range fresh {
		if err := ctx.Err(); err != nil {
			return err
		}
		b := d.readBook(lp, files[lp])
		entries = append(entries, newEntry(b))
		changed = true
	}
	if changed {
		if err := writeCache(d.st, entries); err != nil {
			return err
		}
	}
	d.mu.Lock()
	d.entries = entries
	d.books = booksOf(entries)
	d.mu.Unlock()
	return nil
}

func booksOf(es []entry) []device.Book {
	out := make([]device.Book, len(es))
	for i, e := range es {
		out[i] = e.book()
	}
	return out
}

// scan lists the book files under EbookDir with their sizes, by lpath.
func (d *Device) scan(ctx context.Context) (map[string]int64, error) {
	root := d.cfg.EbookDir
	if root == "" {
		root = "."
	}
	files := map[string]int64{}
	err := fs.WalkDir(d.st, root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			if p == root && errors.Is(err, fs.ErrNotExist) {
				return fs.SkipAll // no books yet
			}
			return nil // an unreadable folder is skipped, as calibre does
		}
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if e.IsDir() {
			if p == root {
				return nil
			}
			name := e.Name()
			if !d.cfg.SubDirs || strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".sdr") ||
				(d.cfg.SkipDir != nil && d.cfg.SkipDir(p)) {
				return fs.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(e.Name(), ".") || !e.Type().IsRegular() || !d.takes(p) {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return nil
		}
		files[p] = fi.Size()
		return nil
	})
	return files, err
}

// readBook reads the metadata of the file at lpath, which calibre hasn't
// seen: what the file says, else its name.
func (d *Device) readBook(lpath string, size int64) device.Book {
	file, done := d.localFile(lpath)
	defer done()
	m := ebookmeta.FromFileName(lpath)
	if file != "" {
		m, _ = ebookmeta.Read(file)
	}
	b := device.Book{
		LPath: lpath, UUID: newUUID(), Title: m.Title, Authors: m.Authors,
		Tags: m.Tags, Series: m.Series, SeriesIndex: m.SeriesIndex, Publisher: m.Publisher,
		Comments: m.Description, Pubdate: m.Pubdate, Size: size, Mime: mimeOf(lpath),
		Identifiers: map[string]string{},
	}
	if m.ISBN != "" {
		b.Identifiers["isbn"] = m.ISBN
	}
	if m.ASIN != "" {
		b.Identifiers["mobi-asin"] = m.ASIN
	}
	if code := calibre.LanguageCode(m.Language); code != "" {
		b.Languages = []string{code}
	}
	if b.Title == "" {
		b.Title = "Unknown"
	}
	if len(b.Authors) == 0 {
		b.Authors = []string{"Unknown"}
	}
	b.TitleSort = calibre.TitleSort(b.Title)
	b.AuthorSort = calibre.AuthorsSort(b.Authors)
	if fi, err := fs.Stat(d.st, lpath); err == nil {
		b.Timestamp, b.LastModified = fi.ModTime(), fi.ModTime()
	}
	if len(m.Cover) > 0 {
		b.Thumbnail = Thumbnail(m.Cover, d.cfg.ThumbnailHeight)
	}
	return b
}

// localFile is the file at lpath on this computer, copied there when the
// storage has no path for it; done removes the copy.
func (d *Device) localFile(lpath string) (string, func()) {
	if p := d.st.LocalPath(lpath); p != "" {
		return p, func() {}
	}
	src, err := d.st.Open(lpath)
	if err != nil {
		return "", func() {}
	}
	defer src.Close()
	tmp, err := os.CreateTemp("", "dalibre-device-*"+path.Ext(lpath))
	if err != nil {
		return "", func() {}
	}
	_, err = io.Copy(tmp, src)
	tmp.Close()
	if err != nil {
		os.Remove(tmp.Name())
		return "", func() {}
	}
	return tmp.Name(), func() { os.Remove(tmp.Name()) }
}

// Send copies the files to the reader where calibre would put them, and
// records them in metadata.calibre with their metadata and a thumbnail of
// their cover. A book already at that place is replaced.
func (d *Device) Send(ctx context.Context, uploads []device.Upload, progress func(done int)) ([]device.Book, error) {
	prefix := 0
	if p := d.st.LocalPath("."); p != "" {
		prefix = len(strings.TrimSuffix(filepath.ToSlash(p), "/")) + 1
	}
	var sent []device.Book
	var errs []error
	for i, up := range uploads {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		b, err := d.sendOne(ctx, up, prefix)
		if err != nil {
			errs = append(errs, &SendError{Title: up.Book.Title, Err: err})
		} else {
			sent = append(sent, b)
		}
		if progress != nil {
			progress(i + 1)
		}
	}
	if len(sent) > 0 {
		d.mu.Lock()
		for _, b := range sent {
			at := slices.IndexFunc(d.entries, func(e entry) bool { return e.str("lpath") == b.LPath })
			if at >= 0 {
				d.entries[at].update(b)
			} else {
				d.entries = append(d.entries, newEntry(b))
			}
		}
		err := writeCache(d.st, d.entries)
		d.books = booksOf(d.entries)
		d.mu.Unlock()
		if err != nil {
			errs = append(errs, err)
		}
		if d.cfg.LibraryUUID != nil {
			if id := d.cfg.LibraryUUID(); id != "" {
				d.mu.Lock()
				d.di.set("last_library_uuid", id)
				d.di.write(d.st)
				d.mu.Unlock()
			}
		}
	}
	return sent, errors.Join(errs...)
}

// SendError is a file that couldn't be sent.
type SendError struct {
	Title string
	Err   error
}

func (e *SendError) Error() string { return e.Title + ": " + e.Err.Error() }
func (e *SendError) Unwrap() error { return e.Err }

func (d *Device) sendOne(ctx context.Context, up device.Upload, prefix int) (device.Book, error) {
	ext := strings.ToLower(filepath.Ext(up.File))
	b := up.Book
	if !d.takes("x" + ext) {
		return b, ErrFormat
	}
	if b.UUID == "" {
		b.UUID = newUUID()
	}
	if len(b.Authors) == 0 {
		b.Authors = []string{"Unknown"}
	}
	if b.AuthorSort == "" {
		b.AuthorSort = calibre.AuthorsSort(b.Authors)
	}
	if b.TitleSort == "" {
		b.TitleSort = calibre.TitleSort(b.Title)
	}
	b.LPath = uploadPath(d.cfg.Template, d.cfg.EbookDir, d.cfg.SubDirs, b, ext, prefix)
	src, err := os.Open(up.File)
	if err != nil {
		return b, err
	}
	defer src.Close()
	dst, err := d.st.Create(b.LPath)
	if err != nil {
		return b, err
	}
	n, err := io.Copy(dst, readerCtx{ctx, src})
	if cerr := dst.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		d.st.Remove(b.LPath)
		return b, err
	}
	b.Size = n
	b.Mime = mimeOf(b.LPath)
	now := time.Now()
	if b.Timestamp.IsZero() {
		b.Timestamp = now
	}
	if b.LastModified.IsZero() {
		b.LastModified = now
	}
	if len(up.Cover) > 0 {
		b.Thumbnail = Thumbnail(up.Cover, d.cfg.ThumbnailHeight)
	} else if m, err := ebookmeta.Read(up.File); err == nil && len(m.Cover) > 0 {
		b.Thumbnail = Thumbnail(m.Cover, d.cfg.ThumbnailHeight)
	}
	if d.cfg.AfterSend != nil {
		d.cfg.AfterSend(d.st, b, up.File, up.Cover)
	}
	return b, nil
}

// ErrFormat is a file of a format the reader doesn't take.
var ErrFormat = errors.New("the reader doesn’t take this format")

// readerCtx stops a copy when ctx is done.
type readerCtx struct {
	ctx context.Context
	r   io.Reader
}

func (r readerCtx) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Delete removes the books' files from the reader, with what the reader
// keeps beside them, and the folders left empty, as calibre does; then
// their entries in metadata.calibre.
func (d *Device) Delete(ctx context.Context, lpaths []string) error {
	var errs []error
	gone := map[string]bool{}
	for _, lp := range lpaths {
		if err := ctx.Err(); err != nil {
			errs = append(errs, err)
			break
		}
		if !fs.ValidPath(lp) || !d.takes(lp) {
			continue
		}
		if err := d.st.Remove(lp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
			continue
		}
		gone[lp] = true
		if d.cfg.Sidecars != nil {
			for _, s := range d.cfg.Sidecars(lp) {
				d.st.RemoveAll(s)
			}
		}
		d.removeEmptyDirs(path.Dir(lp))
	}
	if len(gone) > 0 {
		d.mu.Lock()
		d.entries = slices.DeleteFunc(d.entries, func(e entry) bool { return gone[e.str("lpath")] })
		err := writeCache(d.st, d.entries)
		d.books = booksOf(d.entries)
		d.mu.Unlock()
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// removeEmptyDirs removes dir and its parents while they are empty, up to
// EbookDir, which stays (os.removedirs in calibre).
func (d *Device) removeEmptyDirs(dir string) {
	inside := func(dir string) bool {
		if d.cfg.EbookDir == "" {
			return dir != "." && dir != "/"
		}
		return strings.HasPrefix(dir, d.cfg.EbookDir+"/")
	}
	for inside(dir) {
		ents, err := fs.ReadDir(d.st, dir)
		if err != nil || len(ents) > 0 {
			return
		}
		if d.st.Remove(dir) != nil {
			return
		}
		dir = path.Dir(dir)
	}
}
