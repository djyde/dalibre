// Package kindle is the driver of Amazon's Kindle readers that connect as
// a USB drive (USB mass storage: the Kindles before 2024), as calibre's
// KINDLE2 driver sees them: the books in documents/, calibre's records of
// them (package usbms), the reader's notes beside each book in a .sdr
// folder, and the cover thumbnails the home screen shows in
// system/thumbnails.
//
// Kindles since 2024 connect over MTP instead; they will come in as
// another device.Source, and this driver will take them as long as their
// storage looks the same.
package kindle

import (
	"context"
	"io"
	"io/fs"
	"path"
	"strings"

	"dalibre/internal/device"
	"dalibre/internal/device/usbms"
	"dalibre/internal/ebookmeta"
)

// VendorID is Lab126's USB vendor id, which every Kindle has.
const VendorID = 0x1949

// Formats are the formats a Kindle reads, best first to send (calibre's
// KINDLE2.FORMATS, AZW3 first as it keeps the book's layout best).
var Formats = []string{"AZW3", "MOBI", "AZW", "PRC", "AZW1", "TPZ", "AZW4", "KFX", "POBI", "PDF", "TXT"}

// KOReaderFormats are those KOReader adds, on a Kindle that has it.
var KOReaderFormats = []string{"EPUB", "FB2", "CBZ", "DJVU"}

// sidecarExts are the files a Kindle keeps beside a book, removed with it
// (calibre's KINDLE.DELETE_EXTS), besides its .sdr folder.
var sidecarExts = []string{".mbp", ".tan", ".pdr", ".ea", ".apnx", ".phl"}

// thumbnailHeight is the height of the covers on a Kindle's home screen,
// and of those calibre keeps for it.
const thumbnailHeight = 500

// Driver recognises Kindles.
type Driver struct {
	// LibraryUUID is the library sending books, recorded on the Kindle
	// as calibre does; nil records none.
	LibraryUUID func() string
}

func (Driver) Kind() string { return "kindle" }

// Match recognises a Kindle by its documents/ and system/ folders, on a
// USB device of Lab126's when the device is known, else on a volume
// called Kindle or whose system/version.txt says Kindle.
func (Driver) Match(st device.Storage) bool {
	usb := st.USB()
	if usb.Vendor != 0 && usb.Vendor != VendorID {
		return false
	}
	if !isDir(st, "documents") || !isDir(st, "system") {
		return false
	}
	if usb.Vendor == VendorID {
		return true
	}
	return strings.EqualFold(st.Label(), "Kindle") || strings.HasPrefix(versionLine(st), "Kindle")
}

// MatchUSB recognises a Kindle by its USB device alone, to offer it while
// its drive isn't mounted: Lab126's, as a USB drive (an MTP Kindle has no
// drive to mount).
func (Driver) MatchUSB(usb device.USBInfo) bool {
	return usb.Vendor == VendorID && usb.Protocol == "usbms"
}

func isDir(st device.Storage, name string) bool {
	fi, err := fs.Stat(st, name)
	return err == nil && fi.IsDir()
}

// versionLine is system/version.txt: "Kindle 5.16.2.1.1 (409747 002)".
func versionLine(st device.Storage) string {
	f, err := st.Open("system/version.txt")
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, 256))
	return strings.TrimSpace(string(b))
}

// Firmware is the firmware version in a version.txt line.
func Firmware(line string) string {
	f := strings.Fields(line)
	if len(f) >= 2 && f[0] == "Kindle" {
		return f[1]
	}
	return ""
}

func (drv Driver) Open(ctx context.Context, st device.Storage) (device.Device, error) {
	usb := st.USB()
	model := usb.Name
	if model == "" {
		model = "Kindle"
	}
	formats := append([]string(nil), Formats...)
	if isDir(st, "koreader") {
		formats = append(formats, KOReaderFormats...)
	}
	return usbms.Open(ctx, st, usbms.Config{
		Info: device.Info{
			Kind: drv.Kind(), Name: "Kindle", Model: model,
			Firmware: Firmware(versionLine(st)),
		},
		EbookDir:        "documents",
		SubDirs:         true,
		Formats:         formats,
		ThumbnailHeight: thumbnailHeight,
		Sidecars:        Sidecars,
		AfterSend:       writeThumbnail,
		LibraryUUID:     drv.LibraryUUID,
	})
}

// Sidecars are what a Kindle keeps beside the book at lpath: its .sdr
// folder (notes, reading position; KOReader's too) and older files.
func Sidecars(lpath string) []string {
	base := strings.TrimSuffix(lpath, path.Ext(lpath))
	out := []string{base + ".sdr"}
	for _, ext := range sidecarExts {
		out = append(out, base+ext)
	}
	return out
}

// ThumbnailPath is where a Kindle looks for the cover of a book whose
// file says asin and cdeType (EXTH 113 and 501), as calibre writes it.
func ThumbnailPath(asin, cdeType string) string {
	return "system/thumbnails/thumbnail_" + asin + "_" + cdeType + "_portrait.jpg"
}

// writeThumbnail puts the cover of the book just sent where the Kindle's
// home screen looks for it, as calibre does: only for a file that names
// itself (ASIN and type), which the Kindle matches the thumbnail by.
func writeThumbnail(st device.Storage, b device.Book, file string, cover []byte) {
	switch b.Format() {
	case "AZW3", "MOBI", "AZW", "PRC", "AZW4", "POBI":
	default:
		return
	}
	if !isDir(st, "system/thumbnails") {
		return
	}
	m, err := ebookmeta.Read(file)
	if err != nil || m.ASIN == "" || m.CDEType == "" || strings.ContainsAny(m.ASIN+m.CDEType, "/\\") {
		return
	}
	if len(cover) == 0 {
		cover = m.Cover
	}
	thumb := usbms.Thumbnail(cover, thumbnailHeight)
	if thumb == nil {
		return
	}
	w, err := st.Create(ThumbnailPath(m.ASIN, m.CDEType))
	if err != nil {
		return
	}
	w.Write(thumb)
	w.Close()
}
