package device

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
)

// DefaultEvery is how often a Watcher looks for readers.
const DefaultEvery = 2 * time.Second

// Watcher keeps the readers connected: it looks at its sources every so
// often, opens the storages a driver recognises, and closes the devices
// whose storage is gone.
type Watcher struct {
	Sources []Source
	Drivers []Driver
	// OnChange is called, on the watcher's goroutine, when a reader
	// connects, goes, or fails to open.
	OnChange func()

	mu      sync.Mutex
	devices map[string]Device // by storage ID
	// failed are the storages a driver recognised but couldn't open, by
	// storage ID: not tried again until they go and come back.
	failed map[string]error
	// ejected are storages ejected but maybe not gone yet, by storage ID.
	ejected map[string]bool
	// available are the readers plugged in but not connected, by node.
	available map[string]Available
}

// Run looks for readers every so often until ctx is done, then closes the
// devices.
func (w *Watcher) Run(ctx context.Context, every time.Duration) {
	if every <= 0 {
		every = DefaultEvery
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		w.Scan(ctx)
		select {
		case <-ctx.Done():
			w.mu.Lock()
			for id, d := range w.devices {
				d.Close()
				delete(w.devices, id)
			}
			w.mu.Unlock()
			return
		case <-t.C:
		}
	}
}

// Scan looks at the sources once.
func (w *Watcher) Scan(ctx context.Context) {
	present := map[string]Storage{}
	available := map[string]Available{}
	for _, src := range w.Sources {
		sts, err := src.Storages()
		if err != nil {
			continue
		}
		for _, st := range sts {
			present[st.ID()] = st
		}
		if ps, ok := src.(PendingSource); ok {
			pending, _ := ps.Pending()
			for _, p := range pending {
				if kind := w.matchUSB(p.USB); kind != "" {
					available[p.Node] = Available{Pending: p, Kind: kind}
				}
			}
		}
	}
	changed := false
	w.mu.Lock()
	if w.devices == nil {
		w.devices, w.failed, w.ejected = map[string]Device{}, map[string]error{}, map[string]bool{}
	}
	if !sameNodes(w.available, available) {
		changed = true
	}
	w.available = available
	for id, d := range w.devices {
		if _, ok := present[id]; !ok {
			d.Close()
			delete(w.devices, id)
			changed = true
		}
	}
	for id := range w.failed {
		if _, ok := present[id]; !ok {
			delete(w.failed, id)
			changed = true
		}
	}
	for id := range w.ejected {
		if _, ok := present[id]; !ok {
			delete(w.ejected, id)
		}
	}
	var todo []Storage
	for id, st := range present {
		if w.devices[id] == nil && w.failed[id] == nil && !w.ejected[id] {
			todo = append(todo, st)
		}
	}
	w.mu.Unlock()

	for _, st := range todo {
		drv := w.match(st)
		if drv == nil {
			continue
		}
		d, err := drv.Open(ctx, st)
		w.mu.Lock()
		if err != nil {
			oe := &OpenError{Err: err}
			if n, ok := st.(interface{ Node() string }); ok {
				oe.Node = n.Node()
			}
			w.failed[st.ID()] = oe
		} else {
			w.devices[st.ID()] = d
		}
		w.mu.Unlock()
		changed = true
	}
	if changed && w.OnChange != nil {
		w.OnChange()
	}
}

// matchUSB is the kind of the driver that recognises a reader by its USB
// device; "" when none does.
func (w *Watcher) matchUSB(usb USBInfo) string {
	for _, drv := range w.Drivers {
		if m, ok := drv.(USBMatcher); ok && m.MatchUSB(usb) {
			return drv.Kind()
		}
	}
	return ""
}

func sameNodes(a, b map[string]Available) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}

// Available are the readers plugged in but not connected, by label.
func (w *Watcher) Available() []Available {
	w.mu.Lock()
	out := make([]Available, 0, len(w.available))
	for _, a := range w.available {
		out = append(out, a)
	}
	w.mu.Unlock()
	slices.SortFunc(out, func(a, b Available) int {
		if c := strings.Compare(a.Label, b.Label); c != 0 {
			return c
		}
		return strings.Compare(a.Node, b.Node)
	})
	return out
}

// Connect connects the available reader at node (mounts it) and returns
// it opened.
func (w *Watcher) Connect(ctx context.Context, node string) (Device, error) {
	w.mu.Lock()
	a, ok := w.available[node]
	w.mu.Unlock()
	if !ok {
		return nil, ErrGone
	}
	if err := a.Connect(ctx); err != nil {
		return nil, err
	}
	w.Scan(ctx)
	for _, d := range w.Devices() {
		if n, ok := d.Storage().(interface{ Node() string }); ok && n.Node() == node {
			return d, nil
		}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, err := range w.failed {
		var oe *OpenError
		if errors.As(err, &oe) && oe.Node == node {
			return nil, oe.Err
		}
	}
	return nil, ErrNotFound
}

// OpenError is a reader recognised that couldn't be opened.
type OpenError struct {
	Node string
	Err  error
}

func (e *OpenError) Error() string { return e.Err.Error() }
func (e *OpenError) Unwrap() error { return e.Err }

var (
	// ErrGone is a reader unplugged meanwhile.
	ErrGone = errors.New("the reader isn’t plugged in any more")
	// ErrNotFound is a reader that was mounted but isn't seen.
	ErrNotFound = errors.New("the reader was mounted but its library wasn’t found")
)

func (w *Watcher) match(st Storage) Driver {
	for _, drv := range w.Drivers {
		if drv.Match(st) {
			return drv
		}
	}
	return nil
}

// Devices are the readers connected, by name.
func (w *Watcher) Devices() []Device {
	w.mu.Lock()
	out := make([]Device, 0, len(w.devices))
	for _, d := range w.devices {
		out = append(out, d)
	}
	w.mu.Unlock()
	slices.SortFunc(out, func(a, b Device) int {
		if c := strings.Compare(a.Info().Name, b.Info().Name); c != 0 {
			return c
		}
		return strings.Compare(a.Storage().ID(), b.Storage().ID())
	})
	return out
}

// Device is the reader whose Info().ID is id, nil when none is connected.
func (w *Watcher) Device(id string) Device {
	for _, d := range w.Devices() {
		if d.Info().ID == id {
			return d
		}
	}
	return nil
}

// Errors are why readers that were recognised couldn't be opened.
func (w *Watcher) Errors() []error {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]error, 0, len(w.failed))
	for _, err := range w.failed {
		out = append(out, err)
	}
	return out
}

// Eject ejects d's storage and forgets d.
func (w *Watcher) Eject(d Device) error {
	st := d.Storage()
	if err := st.Eject(); err != nil {
		return err
	}
	w.mu.Lock()
	if w.devices[st.ID()] == d {
		delete(w.devices, st.ID())
	}
	if w.ejected == nil {
		w.ejected = map[string]bool{}
	}
	w.ejected[st.ID()] = true
	w.mu.Unlock()
	d.Close()
	if w.OnChange != nil {
		w.OnChange()
	}
	return nil
}
