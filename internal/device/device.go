// Package device finds the e-readers connected to the computer and gives
// each one as a library of its own: the books on it, sending books to it
// and removing them.
//
// It is in three layers, so that new readers and new connections slot in:
//
//   - A Storage is how a reader's files are reached: a volume mounted over
//     USB mass storage today (Volume), an MTP storage later. Names are
//     slash-separated paths from the storage's root, as calibre's lpaths.
//   - A Source lists the storages connected now: the mounted volumes; an
//     MTP source later.
//   - A Driver recognises a reader on a storage and opens it as a Device:
//     a Kindle (package kindle) today, a Kobo later. Drivers that keep
//     calibre's on-device library build on package usbms.
//
// A Watcher polls the sources, matches drivers and keeps the Devices.
package device

import (
	"context"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

// Storage is a reader's storage, as Dalibre reaches it. Its methods may be
// called from any goroutine.
type Storage interface {
	// FS reads the storage's files.
	fs.FS
	// ID tells this storage from others while it is connected: the mount
	// point of a volume.
	ID() string
	// Label is the storage's name, as its volume label ("Kindle").
	Label() string
	// USB is the USB device the storage is on, when known.
	USB() USBInfo
	// Create makes or replaces the file name, and the folders it needs.
	Create(name string) (io.WriteCloser, error)
	// Remove deletes the file or empty folder name.
	Remove(name string) error
	// RemoveAll deletes name and what it holds; a name that doesn't exist
	// is no error.
	RemoveAll(name string) error
	// Usage is the storage's free and total space, in bytes.
	Usage() (free, total int64, err error)
	// LocalPath is where name is on this computer, for a mounted volume;
	// "" when the storage has no path (MTP), whose files are read with
	// Open.
	LocalPath(name string) string
	// Eject unmounts the storage so that it can be unplugged.
	Eject() error
}

// USBInfo identifies the USB device a storage is on. Zero values are
// unknown.
type USBInfo struct {
	Vendor, Product uint16
	Manufacturer    string
	Name            string // the product string, as "Amazon Kindle"
	Serial          string
	// Protocol is how the storage is reached: "usbms" (mass storage) or,
	// later, "mtp".
	Protocol string
}

// Source lists the storages connected now.
type Source interface {
	Storages() ([]Storage, error)
}

// Pending is a reader plugged in whose storage can't be read yet: a drive
// that isn't mounted. Connect makes it readable (mounts it); the next scan
// then finds its storage.
type Pending struct {
	// Node tells it from others while it is plugged in: the device it is,
	// as "/dev/sde1".
	Node  string
	Label string
	USB   USBInfo
	// Connect mounts it.
	Connect func(ctx context.Context) error
}

// PendingSource is a Source that also finds the readers plugged in that
// it can't read yet.
type PendingSource interface {
	Source
	Pending() ([]Pending, error)
}

// USBMatcher is a Driver that recognises its readers by their USB device
// alone, before their storage can be read, to offer them while they
// aren't mounted.
type USBMatcher interface {
	MatchUSB(usb USBInfo) bool
}

// Available is a reader plugged in but not connected, that a driver
// recognised by its USB device.
type Available struct {
	Pending
	// Kind is the driver's, as "kindle".
	Kind string
}

// Driver recognises one kind of reader.
type Driver interface {
	// Kind names the kind of reader, as "kindle".
	Kind() string
	// Match reports whether st is a reader of this kind. It must be quick
	// and must not change the storage.
	Match(st Storage) bool
	// Open reads the reader's library. It may write the reader's own
	// records of its library when they are out of date, as calibre does.
	Open(ctx context.Context, st Storage) (Device, error)
}

// Device is a reader connected, with its library. Its methods may be
// called from any goroutine.
type Device interface {
	Info() Info
	// Books are the books on the reader, as last read.
	Books() []Book
	// Formats are the formats the reader takes, best first, in upper case
	// as "AZW3".
	Formats() []string
	// Usage is the reader's free and total space.
	Usage() (free, total int64, err error)
	// Refresh reads the reader's library again.
	Refresh(ctx context.Context) error
	// Send copies files to the reader. progress, when not nil, is told how
	// many are done. It returns the books sent; an error for each file it
	// couldn't send is joined in err.
	Send(ctx context.Context, uploads []Upload, progress func(done int)) ([]Book, error)
	// Delete removes books from the reader, by LPath.
	Delete(ctx context.Context, lpaths []string) error
	// Storage is where the reader's files are.
	Storage() Storage
	// Close forgets the reader, as when it is unplugged.
	Close()
}

// Info describes a connected reader.
type Info struct {
	// ID tells the reader from others, across connections: calibre's
	// device_store_uuid.
	ID string
	// Kind is the driver's, as "kindle".
	Kind string
	// Name is what the reader is called, as "Kindle".
	Name string
	// Model and Firmware, when known, as "Amazon Kindle" and "5.16.2.1.1".
	Model, Firmware string
}

// Book is a book on a reader, with the metadata calibre keeps of it.
type Book struct {
	// LPath is the book's file on the reader, as calibre gives it:
	// "documents/Author/Title - Author.azw3".
	LPath       string
	UUID        string
	Title       string
	TitleSort   string
	Authors     []string
	AuthorSort  string
	Tags        []string
	Series      string
	SeriesIndex float64
	Publisher   string
	Languages   []string
	Comments    string
	// Rating is 0–10, as calibre's.
	Rating       int
	Identifiers  map[string]string
	Pubdate      time.Time // zero when unknown
	Timestamp    time.Time // when it was added
	LastModified time.Time
	Size         int64
	Mime         string
	// ApplicationID is the id of the book in the calibre library it was
	// sent from, 0 when unknown.
	ApplicationID int64
	// Thumbnail is a small JPEG cover, nil when there is none.
	Thumbnail []byte
}

// Format is the book's file format, its extension in upper case.
func (b *Book) Format() string {
	return strings.ToUpper(strings.TrimPrefix(path.Ext(b.LPath), "."))
}

// Upload is a file to send to a reader, with the metadata it goes with.
type Upload struct {
	// File is the file on this computer.
	File string
	// Book is its metadata; LPath, Size and Mime are the device's to set.
	Book Book
	// Cover is the book's cover image, nil for none.
	Cover []byte
}
