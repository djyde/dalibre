package device

import (
	"context"
	"errors"
	"testing"
)

// fakeDevice is a Device on a storage, with no books.
type fakeDevice struct{ st Storage }

func (d *fakeDevice) Info() Info                             { return Info{ID: d.st.ID(), Kind: "fake", Name: "Reader"} }
func (d *fakeDevice) Books() []Book                          { return nil }
func (d *fakeDevice) Formats() []string                      { return nil }
func (d *fakeDevice) Usage() (int64, int64, error)           { return 0, 0, nil }
func (d *fakeDevice) Refresh(context.Context) error          { return nil }
func (d *fakeDevice) Delete(context.Context, []string) error { return nil }
func (d *fakeDevice) Storage() Storage                       { return d.st }
func (d *fakeDevice) Close()                                 {}
func (d *fakeDevice) Send(context.Context, []Upload, func(int)) ([]Book, error) {
	return nil, nil
}

// fakeDriver takes the storages labelled "Reader", and USB vendor 1.
type fakeDriver struct{ openErr error }

func (fakeDriver) Kind() string              { return "fake" }
func (fakeDriver) Match(st Storage) bool     { return st.Label() == "Reader" }
func (fakeDriver) MatchUSB(usb USBInfo) bool { return usb.Vendor == 1 }
func (d fakeDriver) Open(_ context.Context, st Storage) (Device, error) {
	if d.openErr != nil {
		return nil, d.openErr
	}
	return &fakeDevice{st: st}, nil
}

// pluggedSource has one reader on USB, not mounted until connected.
type pluggedSource struct {
	dir     string
	mounted bool
}

func (s *pluggedSource) Storages() ([]Storage, error) {
	if !s.mounted {
		return nil, nil
	}
	return []Storage{NewVolume(s.dir, "/dev/sdx1", "Reader", USBInfo{Vendor: 1})}, nil
}

func (s *pluggedSource) Pending() ([]Pending, error) {
	if s.mounted {
		return nil, nil
	}
	return []Pending{
		{Node: "/dev/sdx1", Label: "Reader", USB: USBInfo{Vendor: 1}, Connect: func(context.Context) error {
			s.mounted = true
			return nil
		}},
		{Node: "/dev/sdy1", Label: "Stick", USB: USBInfo{Vendor: 2}},
	}, nil
}

func TestWatcherConnectsAvailable(t *testing.T) {
	src := &pluggedSource{dir: t.TempDir()}
	changes := 0
	w := &Watcher{Sources: []Source{src}, Drivers: []Driver{fakeDriver{}}, OnChange: func() { changes++ }}
	w.Scan(context.Background())
	av := w.Available()
	if len(av) != 1 || av[0].Node != "/dev/sdx1" || av[0].Kind != "fake" {
		t.Fatalf("Available = %+v (only the drive a driver knows)", av)
	}
	if changes != 1 || len(w.Devices()) != 0 {
		t.Errorf("changes %d, devices %d", changes, len(w.Devices()))
	}
	d, err := w.Connect(context.Background(), "/dev/sdx1")
	if err != nil || d == nil || d.Storage().ID() != src.dir {
		t.Fatalf("Connect = %v, %v", d, err)
	}
	if len(w.Available()) != 0 || len(w.Devices()) != 1 {
		t.Errorf("after connecting: available %d, devices %d", len(w.Available()), len(w.Devices()))
	}
	if _, err := w.Connect(context.Background(), "/dev/sdx1"); !errors.Is(err, ErrGone) {
		t.Errorf("connecting again = %v, want ErrGone", err)
	}
}

func TestWatcherConnectSaysWhyOpenFailed(t *testing.T) {
	src := &pluggedSource{dir: t.TempDir()}
	broken := errors.New("broken library")
	w := &Watcher{Sources: []Source{src}, Drivers: []Driver{fakeDriver{openErr: broken}}}
	w.Scan(context.Background())
	if _, err := w.Connect(context.Background(), "/dev/sdx1"); !errors.Is(err, broken) {
		t.Errorf("Connect = %v, want the open error", err)
	}
}
