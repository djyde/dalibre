package device

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Volume is a storage mounted as a folder of this computer: a reader in
// USB mass storage mode. Every access stays inside the folder (os.Root),
// whatever names the reader's own records give. The folder is opened for
// each access rather than held, so the user can still eject the reader
// from the system.
type Volume struct {
	// Mount is the folder the volume is mounted at.
	Mount string
	// Dev is the device it is mounted from, as "/dev/sde1"; "" when
	// unknown.
	Dev   string
	label string
	usb   USBInfo
}

// NewVolume is the volume mounted at mount.
func NewVolume(mount, dev, label string, usb USBInfo) *Volume {
	if label == "" {
		label = filepath.Base(mount)
	}
	if usb.Protocol == "" {
		usb.Protocol = "usbms"
	}
	return &Volume{Mount: mount, Dev: dev, label: label, usb: usb}
}

func (v *Volume) with(fn func(r *os.Root) error) error {
	r, err := os.OpenRoot(v.Mount)
	if err != nil {
		return err
	}
	defer r.Close()
	return fn(r)
}

func (v *Volume) Open(name string) (fs.File, error) {
	var f fs.File
	err := v.with(func(r *os.Root) error {
		var err error
		f, err = r.FS().Open(name)
		return err
	})
	return f, err
}

// ReadDir and Stat make fs.ReadDir and fs.Stat direct.
func (v *Volume) ReadDir(name string) ([]fs.DirEntry, error) {
	var ents []fs.DirEntry
	err := v.with(func(r *os.Root) error {
		var err error
		ents, err = fs.ReadDir(r.FS(), name)
		return err
	})
	return ents, err
}

func (v *Volume) Stat(name string) (fs.FileInfo, error) {
	var fi fs.FileInfo
	err := v.with(func(r *os.Root) error {
		var err error
		fi, err = fs.Stat(r.FS(), name)
		return err
	})
	return fi, err
}

func (v *Volume) ID() string    { return v.Mount }
func (v *Volume) Label() string { return v.label }
func (v *Volume) USB() USBInfo  { return v.usb }

func (v *Volume) Create(name string) (io.WriteCloser, error) {
	if !fs.ValidPath(name) || name == "." {
		return nil, &fs.PathError{Op: "create", Path: name, Err: fs.ErrInvalid}
	}
	var f *os.File
	err := v.with(func(r *os.Root) error {
		if dir := path.Dir(name); dir != "." {
			if err := r.MkdirAll(dir, 0o755); err != nil {
				return err
			}
		}
		var err error
		f, err = r.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		return err
	})
	return f, err
}

func (v *Volume) Remove(name string) error {
	return v.with(func(r *os.Root) error { return r.Remove(name) })
}

func (v *Volume) RemoveAll(name string) error {
	if !fs.ValidPath(name) || name == "." {
		return &fs.PathError{Op: "removeall", Path: name, Err: fs.ErrInvalid}
	}
	err := v.with(func(r *os.Root) error { return r.RemoveAll(name) })
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

func (v *Volume) Usage() (free, total int64, err error) { return diskUsage(v.Mount) }

func (v *Volume) LocalPath(name string) string {
	if !fs.ValidPath(name) {
		return ""
	}
	return filepath.Join(v.Mount, filepath.FromSlash(name))
}

func (v *Volume) Eject() error { return eject(v) }

// firstLine is what a command that failed said, for its error.
func firstLine(out []byte, err error) string {
	if s, _, _ := strings.Cut(strings.TrimSpace(string(out)), "\n"); s != "" {
		return s
	}
	return err.Error()
}
