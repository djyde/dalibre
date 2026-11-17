package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"dalibre/cmd/dalibre/locales"
	"dalibre/internal/calibre"
	"dalibre/internal/smartdevice"
)

// Wireless devices (Figma "13 · Sharing & AI agents"): Dalibre serves
// calibre's wireless device connection, so reading apps that connect to
// calibre over Wi‑Fi, KOReader's Calibre plugin first, connect to it, and
// books are sent to them from the book menu. Unlike OPDS and MCP its name
// is translated: it is no protocol people know by name.

// wirelessSecret names the password readers give, in the credential store.
const wirelessSecret = "wireless-device"

// deviceState is the wireless connection, while it is on.
type deviceState struct {
	srv      *smartdevice.Server
	port     int  // as set; 0 for calibre's
	password bool // as set
	err      error
	// synced is the reader whose books' metadata was last brought up to
	// date, to do it again for one that connects.
	synced *smartdevice.Device
	// sending is how many books are being sent, 0 when none are; sent is
	// how many of them are done.
	sending, sent int
}

// applyDevice starts, stops or restarts the wireless connection to match
// the settings.
func (a *App) applyDevice() {
	w := a.settings.Wireless
	if cur := a.device; cur != nil {
		if w.On && cur.port == w.Port && cur.password == w.Password && cur.err == nil {
			return
		}
		if cur.srv != nil {
			go cur.srv.Close()
		}
	}
	a.device = nil
	if !w.On {
		return
	}
	st := &deviceState{port: w.Port, password: w.Password}
	a.device = st
	content := 0
	if sh := a.settings.OPDS.Share; sh.On {
		content = sh.SharePort()
	}
	name, _ := os.Hostname()
	name, _, _ = strings.Cut(name, ".")
	post, secrets := a.post, a.secrets
	a.run(func() {
		pw := ""
		if w.Password && secrets != nil {
			pw, _ = secrets.Get(wirelessSecret)
		}
		if w.Password && pw == "" {
			// A password asked for and none kept would let anyone in;
			// refuse all.
			pw = newCatalogID() + newCatalogID()
		}
		srv, err := smartdevice.Listen(smartdevice.Config{
			Port: w.Port, Name: name, ContentPort: content, Password: pw,
			Library:  a.deviceLibrary,
			OnChange: func() { post(a.deviceChanged) },
		})
		a.post(func() {
			if a.device != st {
				if srv != nil {
					srv.Close()
				}
				return
			}
			if err != nil {
				port := w.Port
				if port == 0 {
					port = smartdevice.DefaultPort
				}
				st.err = errors.New(locales.WirelessPortError(strconv.Itoa(port), err.Error()))
				return
			}
			st.srv = srv
		})
	})
}

// deviceLibrary is the open library's name and id, for readers that
// connect; it may run on any goroutine.
func (a *App) deviceLibrary() (string, string) {
	lib := a.libShared.Load()
	if lib == nil {
		return "", ""
	}
	return filepath.Base(lib.Root), lib.UUID()
}

// deviceChanged follows the reader: one that has just connected gets
// the metadata it lacks.
func (a *App) deviceChanged() {
	st := a.device
	if st == nil || st.srv == nil {
		return
	}
	dev := st.srv.Device()
	if dev != nil && st.synced != dev {
		st.synced = dev
		a.syncDevice()
	}
}

// syncDevice sends the reader connected the metadata of its books that
// changed in the library since it got them, as calibre does.
func (a *App) syncDevice() {
	dev, lib := a.connectedDevice(), a.lib
	if dev == nil || lib == nil {
		return
	}
	stale := dev.Stale(deviceBooks(a.books))
	if len(stale) == 0 {
		return
	}
	byUUID := map[string]*calibre.Book{}
	for _, b := range a.books {
		byUUID[b.UUID] = b
	}
	a.run(func() {
		for i, s := range stale {
			// The size of the file the reader has, which it keeps.
			if d, err := lib.Details(byUUID[s.UUID]); err == nil {
				for _, f := range d.Files {
					if f.Format == dev.FormatOf(s.UUID) {
						stale[i].Path = f.Path
					}
				}
			}
			stale[i].Cover = coverFile(lib, byUUID[s.UUID])
		}
		dev.UpdateMetadata(stale)
	})
}

// deviceBooks are books as the reader is sent them, without their files.
func deviceBooks(books []*calibre.Book) []smartdevice.Book {
	out := make([]smartdevice.Book, 0, len(books))
	for _, b := range books {
		out = append(out, deviceBook(b, "", ""))
	}
	return out
}

func deviceBook(b *calibre.Book, path, format string) smartdevice.Book {
	return smartdevice.Book{
		Path: path, Format: format, UUID: b.UUID, Title: b.Title,
		Authors: b.Authors, AuthorSort: b.AuthorSort, Tags: b.Tags,
		Series: b.Series, SeriesIndex: b.SeriesIndex, LastModified: b.LastModified,
	}
}

// coverFile is b's cover image, "" when it has none.
func coverFile(lib *calibre.Library, b *calibre.Book) string {
	if b == nil || !b.HasCover {
		return ""
	}
	return lib.CoverPath(b)
}

// connectedDevice is the reader connected, nil when none is.
func (a *App) connectedDevice() *smartdevice.Device {
	if a.device == nil || a.device.srv == nil {
		return nil
	}
	return a.device.srv.Device()
}

// deviceAddress is what a reader that can't find Dalibre by itself is
// given: this computer's address and the port.
func (a *App) deviceAddress() string {
	if a.device == nil || a.device.srv == nil {
		return ""
	}
	return fmt.Sprintf("%s:%d", lanAddress(), a.device.srv.Port())
}

// deviceStatus is how the wireless connection is, in a line, and whether
// that is trouble.
func (a *App) deviceStatus() (string, bool) {
	st := a.device
	switch {
	case !a.settings.Wireless.On:
		return locales.SharingOff(), false
	case st != nil && st.err != nil:
		return st.err.Error(), true
	case st == nil || st.srv == nil:
		return locales.SettingsStarting(), false
	case st.sending > 0:
		return locales.WirelessSending(st.sent+1, st.sending), false
	}
	if d := st.srv.Device(); d != nil {
		return locales.WirelessConnected(d.Count(), d.Info().Name), false
	}
	switch err := st.srv.Err(); {
	case errors.Is(err, smartdevice.ErrPassword):
		return locales.WirelessWrongPassword(), true
	case errors.Is(err, smartdevice.ErrTooOld):
		return locales.WirelessTooOld(), true
	}
	return locales.WirelessWaiting(), false
}

// sendToDevice sends the books of ids to the reader connected, each in
// the format it likes best of those the book has.
func (a *App) sendToDevice(ids []int64) {
	st, dev, lib := a.device, a.connectedDevice(), a.lib
	if dev == nil || lib == nil || st.sending > 0 {
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
	st.sending, st.sent = len(books), 0
	device := dev.Info().Name
	a.run(func() {
		var send []smartdevice.Book
		var unreadable []string
		for _, b := range books {
			format := dev.Format(b.Formats)
			path := ""
			if format != "" {
				if d, err := lib.Details(b); err == nil {
					for _, f := range d.Files {
						if f.Format == format {
							path = f.Path
						}
					}
				}
			}
			if path == "" {
				unreadable = append(unreadable, b.Title)
				continue
			}
			db := deviceBook(b, path, format)
			db.Cover = coverFile(lib, b)
			send = append(send, db)
		}
		a.post(func() { st.sending = len(send) })
		var err error
		if len(send) > 0 {
			err = dev.Send(send, func(n int) { a.post(func() { st.sent = n }) })
		}
		a.post(func() {
			st.sending, st.sent = 0, 0
			if errors.Is(err, smartdevice.ErrGone) || errors.Is(err, smartdevice.ErrTimeout) {
				a.toastError(errors.New(locales.WirelessLost(device)))
				return
			}
			// Send skips the books it couldn't send and says why for each.
			errs := []error{}
			if j, ok := err.(interface{ Unwrap() []error }); ok {
				errs = j.Unwrap()
			} else if err != nil {
				errs = append(errs, err)
			}
			for _, e := range errs {
				var refused *smartdevice.RefusedError
				if errors.As(e, &refused) {
					e = errors.New(locales.WirelessRefused(refused.Error()))
				}
				a.toastError(e)
			}
			if len(unreadable) > 0 {
				a.toastError(errors.New(locales.WirelessNoFormat(strings.Join(unreadable, ", "), device)))
			}
			if n := len(send) - len(errs); n > 0 {
				a.toast("%s", locales.WirelessSent(n, device))
			}
		})
	})
}

// onDevice are those of ids the reader connected has.
func (a *App) onDevice(ids []int64) []int64 {
	dev := a.connectedDevice()
	if dev == nil {
		return nil
	}
	var on []int64
	for _, id := range ids {
		if b := a.byID[id]; b != nil && dev.Has(b.UUID) {
			on = append(on, id)
		}
	}
	return on
}

// removeFromDevice deletes the books of ids from the reader connected.
func (a *App) removeFromDevice(ids []int64) {
	st, dev := a.device, a.connectedDevice()
	if dev == nil || st.sending > 0 {
		return
	}
	var uuids []string
	for _, id := range ids {
		if b := a.byID[id]; b != nil {
			uuids = append(uuids, b.UUID)
		}
	}
	device := dev.Info().Name
	a.run(func() {
		err := dev.Delete(uuids)
		a.post(func() {
			if err != nil {
				a.toastError(errors.New(locales.WirelessLost(device)))
				return
			}
			a.toast("%s", locales.WirelessRemoved(len(uuids), device))
		})
	})
}
