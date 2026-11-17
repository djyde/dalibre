package device

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
type fakeDriver struct {
	openErr error
	delay   time.Duration // reading the library takes time, as a real one does
	opens   *atomic.Int32 // counts the opens, when set
}

func (fakeDriver) Kind() string              { return "fake" }
func (fakeDriver) Match(st Storage) bool     { return st.Label() == "Reader" }
func (fakeDriver) MatchUSB(usb USBInfo) bool { return usb.Vendor == 1 }
func (d fakeDriver) Open(_ context.Context, st Storage) (Device, error) {
	if d.opens != nil {
		d.opens.Add(1)
	}
	time.Sleep(d.delay)
	if d.openErr != nil {
		return nil, d.openErr
	}
	return &fakeDevice{st: st}, nil
}

// pluggedSource has one reader on USB, not mounted until connected.
type pluggedSource struct {
	mu      sync.Mutex
	dir     string
	mounted bool
}

func (s *pluggedSource) isMounted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mounted
}

func (s *pluggedSource) Storages() ([]Storage, error) {
	if !s.isMounted() {
		return nil, nil
	}
	return []Storage{NewVolume(s.dir, "/dev/sdx1", "Reader", USBInfo{Vendor: 1})}, nil
}

func (s *pluggedSource) Pending() ([]Pending, error) {
	if s.isMounted() {
		return nil, nil
	}
	return []Pending{
		{Node: "/dev/sdx1", Label: "Reader", USB: USBInfo{Vendor: 1}, Connect: func(context.Context) error {
			s.mu.Lock()
			s.mounted = true
			s.mu.Unlock()
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

// While the watcher scans by itself, the reader Connect returns is the one
// the watcher keeps: a scan running meanwhile must not open it a second
// time and replace it.
func TestWatcherConnectWhileRunning(t *testing.T) {
	for range 20 {
		src := &pluggedSource{dir: t.TempDir()}
		var opens atomic.Int32
		w := &Watcher{Sources: []Source{src}, Drivers: []Driver{fakeDriver{delay: 5 * time.Millisecond, opens: &opens}}}
		w.Scan(context.Background())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() { w.Run(ctx, time.Millisecond); close(done) }()
		d, err := w.Connect(context.Background(), "/dev/sdx1")
		if err != nil {
			t.Fatal(err)
		}
		time.Sleep(30 * time.Millisecond) // scans go on
		devs := w.Devices()
		cancel()
		<-done
		if len(devs) != 1 || devs[0] != d {
			t.Fatalf("Connect returned %p, the watcher keeps %v", d, devs)
		}
		if n := opens.Load(); n != 1 {
			t.Fatalf("the reader was opened %d times", n)
		}
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
