package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"golang.org/x/image/draw"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/catalog"
	"dalibre/internal/device"
	"dalibre/internal/device/kindle"
)

// Readers plugged in (USB mass storage today; MTP and other kinds later,
// see package device): each is a library of its own in the switcher. Its
// page shows its books on the same shelf and list as the library's, and
// books go to it from the library's book menu. The library stays open
// behind it, to send from and to tell which of the reader's books it has.

// devicesState is the readers connected.
type devicesState struct {
	watcher *device.Watcher
	stop    func()
	// list is the readers connected, as last told (main thread).
	list []device.Device
	// failed are the errors already said, so each is said once.
	failed map[string]bool
	// view is the reader whose page shows.
	view *deviceView
	// busy is the reader being written to, by Info().ID; "" for none.
	busy string
}

// deviceView is a reader's page: its books, as the shelf shows them, and
// what is chosen and searched.
type deviceView struct {
	dev  device.Device
	info device.Info

	books   []*calibre.Book
	byID    map[int64]*calibre.Book
	items   map[int64]device.Book
	ids     map[string]int64 // by LPath, kept across reads so the selection holds
	nextID  int64
	counts  catalog.Counts
	version int
	at      time.Time
	// inLib maps the reader's books to the library's, by id.
	inLib map[int64]int64

	query     catalog.Query
	shown     []*calibre.Book
	shownKey  shownKey
	grid      ui.GridState
	table     ui.ListState
	tableSort ui.SortOrder
	chosen    ui.Selection[int64]
	cur       int
	curID     int64

	covers  map[coverKey]*ui.Bitmap
	loading map[coverKey]bool

	free, total int64

	deleteOpen bool
	deleteIDs  []int64
}

// deviceDrivers are the kinds of readers Dalibre knows.
func (a *App) deviceDrivers() []device.Driver {
	return []device.Driver{
		kindle.Driver{LibraryUUID: a.deviceLibraryUUID},
	}
}

// deviceLibraryUUID is the open library's id, recorded on the readers it
// sends to as calibre does; any goroutine.
func (a *App) deviceLibraryUUID() string {
	if lib := a.libShared.Load(); lib != nil {
		return lib.UUID()
	}
	return ""
}

// startDevices watches for readers.
func (a *App) startDevices() {
	ctx, cancel := context.WithCancel(context.Background())
	w := &device.Watcher{
		Sources:  []device.Source{device.Volumes{}},
		Drivers:  a.deviceDrivers(),
		OnChange: func() { a.post(a.devicesChanged) },
	}
	a.dev = &devicesState{watcher: w, stop: cancel, failed: map[string]bool{}}
	go w.Run(ctx, device.DefaultEvery)
}

// devicesChanged follows the readers connected: one that comes is said,
// one that goes closes its page.
func (a *App) devicesChanged() {
	ds := a.dev
	if ds == nil {
		return
	}
	before := ds.list
	ds.list = ds.watcher.Devices()
	for _, d := range ds.list {
		if !slices.Contains(before, d) {
			name := d.Info().Name
			a.toasts = append(a.toasts, toastMsg{text: locales.DeviceConnected(name), action: locales.DeviceShow(), onAction: func() {
				a.post(func() { a.openDevice(d) })
			}})
		}
	}
	for _, err := range ds.watcher.Errors() {
		if !ds.failed[err.Error()] {
			ds.failed[err.Error()] = true
			a.toastError(errors.New(locales.DeviceOpenFailed("Kindle", err.Error())))
		}
	}
	if v := ds.view; v != nil {
		if !slices.Contains(ds.list, v.dev) {
			ds.view = nil
			if a.page == pageDevice {
				a.page = pageLibrary
			}
			a.toastError(errors.New(locales.WirelessLost(v.info.Name)))
		}
	}
}

// devices are the readers connected.
func (a *App) devices() []device.Device {
	if a.dev == nil {
		return nil
	}
	return a.dev.list
}

// openDevice shows the reader's page.
func (a *App) openDevice(d device.Device) {
	if a.dev == nil || !slices.Contains(a.dev.list, d) {
		return
	}
	a.closeResults()
	if v := a.dev.view; v == nil || v.dev != d {
		v = &deviceView{dev: d, info: d.Info(), ids: map[string]int64{}, cur: -1,
			covers: map[coverKey]*ui.Bitmap{}, loading: map[coverKey]bool{}}
		v.query.Sort, v.query.Descending = a.query.Sort, a.query.Descending
		a.dev.view = v
		a.loadDeviceBooks(v)
	}
	a.page = pageDevice
}

// deviceView is the reader whose page shows, nil when none does.
func (a *App) deviceView() *deviceView {
	if a.dev == nil || a.page != pageDevice {
		return nil
	}
	return a.dev.view
}

// loadDeviceBooks takes the reader's books as it last read them.
func (a *App) loadDeviceBooks(v *deviceView) {
	items := v.dev.Books()
	v.books = make([]*calibre.Book, 0, len(items))
	v.byID = make(map[int64]*calibre.Book, len(items))
	v.items = make(map[int64]device.Book, len(items))
	for _, it := range items {
		id, ok := v.ids[it.LPath]
		if !ok {
			v.nextID++
			id = v.nextID
			v.ids[it.LPath] = id
		}
		b := shelfBook(id, it)
		v.books = append(v.books, b)
		v.byID[id] = b
		v.items[id] = it
	}
	v.at = time.Now()
	v.counts = catalog.Tally(v.books, v.at)
	v.version++
	for id := range v.chosen.All() {
		if v.byID[id] == nil {
			v.chosen.Remove(id)
		}
	}
	if v.byID[v.curID] == nil {
		v.curID = 0
	}
	a.matchDeviceBooks(v)
	dev := v.dev
	a.run(func() {
		free, total, err := dev.Usage()
		if err == nil {
			a.post(func() { v.free, v.total = free, total })
		}
	})
}

// shelfBook is a reader's book as the shelf shows books.
func shelfBook(id int64, it device.Book) *calibre.Book {
	ts := it.Timestamp
	if ts.IsZero() {
		ts = it.LastModified
	}
	return &calibre.Book{
		ID: id, UUID: it.UUID, Title: it.Title, Sort: it.TitleSort,
		Authors: it.Authors, AuthorSort: it.AuthorSort, Series: it.Series, SeriesIndex: it.SeriesIndex,
		Rating: it.Rating, Tags: it.Tags, Publisher: it.Publisher, Languages: it.Languages,
		Formats: []string{it.Format()}, Size: it.Size, Timestamp: ts, Pubdate: it.Pubdate,
		LastModified: it.LastModified, HasCover: len(it.Thumbnail) > 0, Path: it.LPath,
	}
}

// matchKey tells books apart by title and first author, as calibre
// matches a reader's books without a uuid it knows.
func matchKey(title string, authors []string) string {
	k := strings.ToLower(strings.Join(strings.Fields(title), " "))
	if len(authors) > 0 {
		k += "\x00" + strings.ToLower(strings.Join(strings.Fields(authors[0]), " "))
	}
	return k
}

// matchDeviceBooks finds the library's book of each of the reader's: by
// uuid, else by title and author.
func (a *App) matchDeviceBooks(v *deviceView) {
	byUUID := make(map[string]int64, len(a.books))
	byKey := make(map[string]int64, len(a.books))
	for _, b := range a.books {
		byUUID[b.UUID] = b.ID
		byKey[matchKey(b.Title, b.Authors)] = b.ID
	}
	v.inLib = map[int64]int64{}
	for id, b := range v.byID {
		if lid, ok := byUUID[b.UUID]; ok && b.UUID != "" {
			v.inLib[id] = lid
		} else if lid, ok := byKey[matchKey(b.Title, b.Authors)]; ok {
			v.inLib[id] = lid
		}
	}
}

// visible are the reader's books the page's query selects.
func (v *deviceView) visible() []*calibre.Book {
	k := shownKey{v.query, v.version}
	if k != v.shownKey || v.shown == nil {
		v.shown = catalog.Apply(v.books, v.query, v.at)
		v.shownKey = k
	}
	return v.shown
}

// current is the book the page's detail shows.
func (v *deviceView) current() *calibre.Book { return v.byID[v.curID] }

// selectedIDs are the books chosen, or the current one.
func (v *deviceView) selectedIDs() []int64 {
	var ids []int64
	for _, b := range v.visible() {
		if v.chosen.Has(b.ID) {
			ids = append(ids, b.ID)
		}
	}
	if len(ids) == 0 && v.current() != nil {
		ids = []int64{v.curID}
	}
	return ids
}

// deviceCover is a reader's book's cover, from the thumbnail calibre
// keeps, at about height pixels; nil while it loads or when there is none.
func (a *App) deviceCover(v *deviceView, b *calibre.Book, height int) *ui.Bitmap {
	it, ok := v.items[b.ID]
	if !ok || len(it.Thumbnail) == 0 {
		return nil
	}
	k := coverKey{b.ID, it.LastModified.UnixNano() ^ it.Size, height}
	if bmp := v.covers[k]; bmp != nil {
		return bmp
	}
	if v.loading[k] {
		return nil
	}
	v.loading[k] = true
	data := it.Thumbnail
	a.run(func() {
		var bmp *ui.Bitmap
		if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
			if r := img.Bounds(); r.Dy() > height && height > 0 {
				small := image.NewRGBA(image.Rect(0, 0, max(1, r.Dx()*height/r.Dy()), height))
				draw.CatmullRom.Scale(small, small.Bounds(), img, r, draw.Src, nil)
				img = small
			}
			bmp = ui.NewBitmap(img)
		}
		a.post(func() {
			if bmp != nil {
				v.covers[k] = bmp
			}
		})
	})
	return nil
}

// deviceFormat is the format of b to send to d: the first d takes of
// those b has; "" when it takes none.
func deviceFormat(d device.Device, formats []string) string {
	for _, f := range d.Formats() {
		if slices.Contains(formats, f) {
			return f
		}
	}
	return ""
}

// sendToReader sends the library's books of ids to d, each in the format
// it takes best, with their metadata and cover, as calibre does.
func (a *App) sendToReader(d device.Device, ids []int64) {
	lib := a.lib
	if lib == nil || a.dev == nil || a.dev.busy != "" || a.busy != "" {
		return
	}
	var books []*calibre.Book
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			books = append(books, b)
		}
	}
	if len(books) == 0 {
		return
	}
	name := d.Info().Name
	a.dev.busy = d.Info().ID
	a.busy, a.busyDone, a.busyTotal = locales.DeviceSending(name), 0, len(books)
	a.run(func() {
		var ups []device.Upload
		var unreadable []string
		for _, b := range books {
			up, ok := readerUpload(lib, d, b)
			if !ok {
				unreadable = append(unreadable, b.Title)
				continue
			}
			ups = append(ups, up)
		}
		a.post(func() { a.busyTotal = len(ups) })
		sent, err := d.Send(context.Background(), ups, func(n int) { a.post(func() { a.busyDone = n }) })
		a.post(func() {
			a.dev.busy, a.busy = "", ""
			a.sayDeviceErrors(err)
			if len(unreadable) > 0 {
				a.toastError(errors.New(locales.WirelessNoFormat(strings.Join(unreadable, ", "), name)))
			}
			if len(sent) > 0 {
				a.toast("%s", locales.WirelessSent(len(sent), name))
			}
			a.deviceWritten(d)
		})
	})
}

// readerUpload is what sending b to d is: its file in the format d takes
// best, its metadata and its cover. Any goroutine.
func readerUpload(lib *calibre.Library, d device.Device, b *calibre.Book) (device.Upload, bool) {
	format := deviceFormat(d, b.Formats)
	if format == "" {
		return device.Upload{}, false
	}
	det, err := lib.Details(b)
	if err != nil {
		return device.Upload{}, false
	}
	file := ""
	for _, f := range det.Files {
		if f.Format == format {
			file = f.Path
		}
	}
	if file == "" {
		return device.Upload{}, false
	}
	up := device.Upload{File: file, Book: device.Book{
		UUID: b.UUID, Title: b.Title, TitleSort: b.Sort, Authors: b.Authors, AuthorSort: b.AuthorSort,
		Tags: b.Tags, Series: b.Series, SeriesIndex: b.SeriesIndex, Publisher: b.Publisher,
		Languages: b.Languages, Comments: det.Comments, Rating: b.Rating, Identifiers: det.Identifiers,
		Pubdate: b.Pubdate, Timestamp: b.Timestamp, LastModified: b.LastModified, ApplicationID: b.ID,
	}}
	if b.HasCover {
		up.Cover, _ = os.ReadFile(lib.CoverPath(b))
	}
	return up, true
}

// sendFilesToReader sends files dropped on a reader's page, with the
// metadata they carry.
func (a *App) sendFilesToReader(d device.Device, paths []string) {
	if a.dev == nil || a.dev.busy != "" || a.busy != "" {
		return
	}
	name := d.Info().Name
	a.dev.busy = d.Info().ID
	a.busy, a.busyDone, a.busyTotal = locales.DeviceSending(name), 0, 0
	a.run(func() {
		var ups []device.Upload
		var unreadable []string
		for _, p := range expandFiles(paths) {
			pr := calibre.Prepare(p, calibre.Metadata{}, nil)
			if !slices.Contains(d.Formats(), pr.Format) {
				unreadable = append(unreadable, strings.TrimSuffix(fileBase(p), "."+strings.ToLower(pr.Format)))
				continue
			}
			m := pr.Metadata
			ups = append(ups, device.Upload{File: p, Cover: pr.Cover, Book: device.Book{
				Title: m.Title, Authors: m.Authors, AuthorSort: m.AuthorSort, Tags: m.Tags, Series: m.Series,
				SeriesIndex: m.SeriesIndex, Publisher: m.Publisher, Languages: m.Languages, Comments: m.Comments,
				Identifiers: m.Identifiers, Pubdate: m.Pubdate,
			}})
		}
		a.post(func() { a.busyTotal = len(ups) })
		sent, err := d.Send(context.Background(), ups, func(n int) { a.post(func() { a.busyDone = n }) })
		a.post(func() {
			a.dev.busy, a.busy = "", ""
			a.sayDeviceErrors(err)
			if len(unreadable) > 0 {
				a.toastError(errors.New(locales.WirelessNoFormat(strings.Join(unreadable, ", "), name)))
			}
			if len(sent) > 0 {
				a.toast("%s", locales.WirelessSent(len(sent), name))
			}
			a.deviceWritten(d)
		})
	})
}

// expandFiles is paths with the e-books in the folders among them.
func expandFiles(paths []string) []string {
	var out []string
	for _, p := range paths {
		filepath.WalkDir(p, func(path string, d os.DirEntry, err error) error {
			if err == nil && !d.IsDir() && (path == p || isEbook(path)) {
				out = append(out, path)
			}
			return nil
		})
	}
	return out
}

func fileBase(p string) string { return filepath.Base(p) }

// sayDeviceErrors toasts each file a reader didn't take.
func (a *App) sayDeviceErrors(err error) {
	if err == nil {
		return
	}
	errs := []error{err}
	if j, ok := err.(interface{ Unwrap() []error }); ok {
		errs = j.Unwrap()
	}
	for _, e := range errs {
		a.toastError(e)
	}
}

// deviceWritten shows a reader's books again after it changed.
func (a *App) deviceWritten(d device.Device) {
	if a.dev == nil {
		return
	}
	if v := a.dev.view; v != nil && v.dev == d {
		a.loadDeviceBooks(v)
	}
}

// readerBooksOf are the reader's books that are the library's books of
// ids, by LPath.
func (a *App) readerBooksOf(d device.Device, ids []int64) []string {
	byUUID, byKey := map[string]bool{}, map[string]bool{}
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			byUUID[b.UUID] = true
			byKey[matchKey(b.Title, b.Authors)] = true
		}
	}
	var out []string
	for _, it := range d.Books() {
		if (it.UUID != "" && byUUID[it.UUID]) || byKey[matchKey(it.Title, it.Authors)] {
			out = append(out, it.LPath)
		}
	}
	return out
}

// removeFromReader deletes the reader's books at lpaths.
func (a *App) removeFromReader(d device.Device, lpaths []string) {
	if a.dev == nil || a.dev.busy != "" || len(lpaths) == 0 {
		return
	}
	name := d.Info().Name
	a.dev.busy = d.Info().ID
	a.run(func() {
		before := len(d.Books())
		err := d.Delete(context.Background(), lpaths)
		gone := before - len(d.Books())
		a.post(func() {
			a.dev.busy = ""
			a.sayDeviceErrors(err)
			if gone > 0 {
				a.toast("%s", locales.WirelessRemoved(gone, name))
			}
			a.deviceWritten(d)
		})
	})
}

// addReaderBooksToLibrary adds the reader's books of ids to the library,
// with the metadata the reader has of them, as calibre's Add books to
// library does.
func (a *App) addReaderBooksToLibrary(v *deviceView, ids []int64) {
	lib := a.lib
	if lib == nil || a.busy != "" {
		return
	}
	var items []device.Book
	for _, id := range ids {
		if it, ok := v.items[id]; ok {
			items = append(items, it)
		}
	}
	if len(items) == 0 {
		return
	}
	st := v.dev.Storage()
	a.busy, a.busyDone, a.busyTotal = locales.DeviceAdding(), 0, len(items)
	a.run(func() {
		added := 0
		var errs []error
		for i, it := range items {
			file, done, err := readerFile(st, it.LPath)
			if err == nil {
				over := calibre.Metadata{
					Title: it.Title, Authors: it.Authors, AuthorSort: it.AuthorSort, Tags: it.Tags,
					Series: it.Series, SeriesIndex: it.SeriesIndex, Publisher: it.Publisher, Languages: it.Languages,
					Comments: it.Comments, Pubdate: it.Pubdate, Rating: it.Rating, Identifiers: it.Identifiers,
				}
				_, err = lib.ImportWith(file, over, it.Thumbnail)
				done()
			}
			if err != nil {
				errs = append(errs, errors.New(it.Title+": "+err.Error()))
			} else {
				added++
			}
			a.post(func() { a.busyDone = i + 1 })
		}
		a.post(func() {
			a.busy = ""
			for _, e := range errs {
				a.toastError(e)
			}
			a.reload(func() {
				if added > 0 {
					a.toast("%s", locales.DeviceAdded(added))
				}
				if v := a.dev.view; v != nil {
					a.matchDeviceBooks(v)
				}
			})
		})
	})
}

// readerFile is the reader's file at lpath on this computer: where it is
// for a mounted reader, else a copy that done removes.
func readerFile(st device.Storage, lpath string) (string, func(), error) {
	if p := st.LocalPath(lpath); p != "" {
		return p, func() {}, nil
	}
	src, err := st.Open(lpath)
	if err != nil {
		return "", func() {}, err
	}
	defer src.Close()
	dir, err := os.MkdirTemp("", "dalibre-reader-")
	if err != nil {
		return "", func() {}, err
	}
	done := func() { os.RemoveAll(dir) }
	dst := filepath.Join(dir, path.Base(lpath))
	out, err := os.Create(dst)
	if err == nil {
		_, err = io.Copy(out, src)
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}
	if err != nil {
		done()
		return "", func() {}, err
	}
	return dst, done, nil
}

// ejectReader ejects d, so it can be unplugged.
func (a *App) ejectReader(d device.Device) {
	if a.dev == nil || a.dev.busy != "" {
		return
	}
	name := d.Info().Name
	w := a.dev.watcher
	a.dev.busy = d.Info().ID
	a.run(func() {
		err := w.Eject(d)
		a.post(func() {
			a.dev.busy = ""
			if err != nil {
				a.toastError(err)
				return
			}
			a.dev.list = w.Devices()
			if v := a.dev.view; v != nil && v.dev == d {
				a.dev.view = nil
				if a.page == pageDevice {
					a.page = pageLibrary
				}
			}
			a.toast("%s", locales.DeviceEjected(name))
		})
	})
}
