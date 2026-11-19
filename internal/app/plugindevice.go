package app

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"slices"
	"strings"

	"dalibre/internal/device"
	"dalibre/internal/device/kindle"
	"dalibre/internal/plugin"
)

// The Kindle files an action with permissions.device may reach: a file
// in a book's .sdr folder, or one of Word Wise's dictionaries in
// system/kll. Everything is named by Dalibre, after the book on the
// device; the plugin never gives a device path itself.
const (
	deviceSidecar = "sidecar"
	deviceKLL     = "kll"
)

// deviceHost gives an action the readers plugged in, and reads and
// writes their Word Wise files through the checks in devicePath.
func (a *App) deviceHost() plugin.DeviceHost {
	return plugin.DeviceHost{
		List: func(ctx context.Context) ([]plugin.DeviceInfo, error) {
			devices, err := a.pluginDevices(ctx)
			if err != nil {
				return nil, err
			}
			out := []plugin.DeviceInfo{}
			for _, d := range devices {
				di := d.Info()
				if !pluginKindle(d) {
					continue
				}
				info := plugin.DeviceInfo{
					ID: di.ID, Kind: di.Kind, Name: di.Name,
					Books: []plugin.DeviceBook{},
				}
				for _, b := range d.Books() {
					info.Books = append(info.Books, plugin.DeviceBook{
						Path: b.LPath, Title: b.Title, Authors: slices.Clone(b.Authors),
						Identifiers: maps.Clone(b.Identifiers),
					})
				}
				out = append(out, info)
			}
			return out, ctx.Err()
		},
		Read:  a.deviceFile,
		Write: a.deviceWrite,
	}
}

// pluginDevices snapshots the connected readers on the main thread. No
// callback keeps or accesses the app's mutable device list in the background.
func (a *App) pluginDevices(ctx context.Context) ([]device.Device, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ready := make(chan []device.Device, 1)
	a.post(func() {
		if ctx.Err() == nil {
			ready <- slices.Clone(a.devices())
		}
	})
	select {
	case devices := <-ready:
		return devices, ctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func pluginKindle(d device.Device) bool {
	return d.Info().Kind == "kindle" && d.Storage().USB().Protocol == "usbms"
}

// devicePath checks the named Kindle file and says where it is on the
// reader.
func devicePath(kind, book, name string) (string, error) {
	if kind != deviceSidecar && kind != deviceKLL {
		return "", fmt.Errorf("dalibre.device: kind must be %q or %q", deviceSidecar, deviceKLL)
	}
	if !fs.ValidPath(name) || name == "." || strings.ContainsAny(name, "/\\:") ||
		strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("dalibre.device: %q isn’t a file name", name)
	}
	switch kind {
	case deviceSidecar:
		if !fs.ValidPath(book) || strings.ContainsAny(book, "\\:") || path.Base(book) == book || path.Ext(book) == "" {
			return "", fmt.Errorf("dalibre.device: %q isn’t a book on the device", book)
		}
		if !strings.HasPrefix(book, "documents/") {
			return "", fmt.Errorf("dalibre.device: %q isn’t a book on the device", book)
		}
		return path.Join(kindle.Sidecars(book)[0], name), nil
	case deviceKLL:
		if !strings.HasSuffix(strings.ToLower(name), ".klld") {
			return "", fmt.Errorf("dalibre.device: %q isn’t a Word Wise dictionary", name)
		}
		return path.Join("system", "kll", name), nil
	}
	return "", fmt.Errorf("dalibre.device: kind must be %q or %q", deviceSidecar, deviceKLL)
}

// deviceOf finds reader id among those plugged in.
func deviceOf(devices []device.Device, id string) (device.Device, error) {
	for _, d := range devices {
		if d.Info().ID == id && pluginKindle(d) {
			return d, nil
		}
	}
	return nil, fmt.Errorf("dalibre.device: no reader %q", id)
}

// pluginDeviceFile checks the reader, and for a sidecar, that the book is
// one it knows and still has. String validation alone grants no access.
func (a *App) pluginDeviceFile(ctx context.Context, id, kind, book, name string) (device.Device, string, error) {
	devices, err := a.pluginDevices(ctx)
	if err != nil {
		return nil, "", err
	}
	d, err := deviceOf(devices, id)
	if err != nil {
		return nil, "", err
	}
	at, err := devicePath(kind, book, name)
	if err != nil {
		return nil, "", err
	}
	if kind == deviceSidecar {
		if !slices.ContainsFunc(d.Books(), func(b device.Book) bool { return b.LPath == book }) {
			return nil, "", fmt.Errorf("dalibre.device: %q isn’t a book on the device", book)
		}
		st, err := fs.Stat(d.Storage(), book)
		if err != nil {
			return nil, "", err
		}
		if !st.Mode().IsRegular() {
			return nil, "", fmt.Errorf("dalibre.device: %q isn’t a book file", book)
		}
	}
	return d, at, ctx.Err()
}

// deviceFile streams a named file straight into the plugin's /work.
// Storage.Open confines the read to the volume even through symlinks.
func (a *App) deviceFile(ctx context.Context, id, kind, book, name string, dst io.Writer) error {
	d, at, err := a.pluginDeviceFile(ctx, id, kind, book, name)
	if err != nil {
		return err
	}
	in, err := d.Storage().Open(at)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("dalibre.device: %q isn’t a file", name)
	}
	if _, err := io.Copy(dst, deviceReader{ctx: ctx, Reader: in}); err != nil {
		return err
	}
	return ctx.Err()
}

// deviceWrite copies src to its named place on reader id. The write goes
// beside the book first and is renamed only once whole, so a failure
// leaves what was there.
func (a *App) deviceWrite(ctx context.Context, id, kind, book, name, src string) error {
	d, at, err := a.pluginDeviceFile(ctx, id, kind, book, name)
	if err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	// Each call owns its temporary file, including concurrent calls from
	// different plugins. A failed call cannot truncate another's file.
	part := path.Join(path.Dir(at), ".dalibre-"+rand.Text()+".part")
	out, err := d.Storage().Create(part)
	if err != nil {
		return err
	}
	defer d.Storage().Remove(part)
	_, err = io.Copy(out, deviceReader{ctx: ctx, Reader: in})
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = d.Storage().Rename(part, at)
	}
	if err != nil {
		return err
	}
	a.post(func() { a.deviceWritten(d) })
	return nil
}

// deviceReader checks cancellation between chunks without letting io.Copy
// bypass Read through an underlying file's WriterTo implementation.
type deviceReader struct {
	ctx context.Context
	io.Reader
}

func (r deviceReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
