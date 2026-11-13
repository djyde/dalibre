package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"dalibre/internal/device"
	"dalibre/internal/device/kindle"
)

// fixedSource gives the same storages every time.
type fixedSource []device.Storage

func (s fixedSource) Storages() ([]device.Storage, error) { return s, nil }

// fakeKindle makes a folder that looks like a Kindle's drive, with
// KOReader when koreader is set (so it takes EPUB, as the sample books
// are).
func fakeKindle(t *testing.T, koreader bool) string {
	t.Helper()
	root := t.TempDir()
	dirs := []string{"documents", "system/thumbnails"}
	if koreader {
		dirs = append(dirs, "koreader")
	}
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(root, "system", "version.txt"), []byte("Kindle 5.16.2.1.1 (409747 002)\n"), 0o644)
	return root
}

// plugIn connects the reader in root to a, as the watcher would.
func plugIn(t *testing.T, a *App, root string) device.Device {
	t.Helper()
	vol := device.NewVolume(root, "", "Kindle", device.USBInfo{Vendor: kindle.VendorID, Name: "Amazon Kindle"})
	w := &device.Watcher{Sources: []device.Source{fixedSource{vol}}, Drivers: a.deviceDrivers()}
	a.dev = &devicesState{watcher: w, failed: map[string]bool{}}
	w.Scan(context.Background())
	a.devicesChanged()
	if len(a.devices()) != 1 {
		t.Fatalf("reader not found: %v", w.Errors())
	}
	return a.devices()[0]
}

func TestReaderInSwitcher(t *testing.T) {
	a := newTestApp(t)
	d := plugIn(t, a, fakeKindle(t, true))
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if !tt.HasText("Kindle connected") {
		t.Errorf("no toast for the reader: %q", tt.Texts())
	}
	if err := tt.Click("Library"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Kindle") || !tt.HasText("Amazon Kindle · 0 books") {
		t.Errorf("switcher misses the reader: %q", tt.Texts())
	}
	shot(t, tt, "device-switcher")
	if err := tt.Click("Kindle"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.page != pageDevice || a.deviceView() == nil || a.deviceView().dev != d {
		t.Fatalf("page = %v", a.page)
	}
	if !tt.HasText("No books on Kindle") || !tt.HasText("Eject") {
		t.Errorf("empty reader page: %q", tt.Texts())
	}
	shot(t, tt, "device-empty")
}

func TestSendToReaderAndBack(t *testing.T) {
	a := newTestApp(t)
	root := fakeKindle(t, true)
	d := plugIn(t, a, root)

	// Send two books from the library's book menu.
	var ids []int64
	for _, b := range a.books {
		if b.Title == "Dune" || b.Title == "Piranesi" {
			ids = append(ids, b.ID)
		}
	}
	a.sendToReader(d, ids)
	if len(d.Books()) != 2 {
		t.Fatalf("reader has %d books", len(d.Books()))
	}
	if _, err := os.Stat(filepath.Join(root, "documents", "Herbert, Frank", "Dune - Frank Herbert.epub")); err != nil {
		t.Errorf("Dune not where calibre puts it: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, "metadata.calibre"))
	if err != nil || !strings.Contains(string(data), `"title": "Dune"`) {
		t.Errorf("metadata.calibre: %v\n%s", err, data)
	}

	// The reader's page shows them, as books the library has.
	a.openDevice(d)
	v := a.deviceView()
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	if !tt.HasText("Dune") || !tt.HasText("Piranesi") {
		t.Fatalf("reader page: %q", tt.Texts())
	}
	if len(v.inLib) != 2 {
		t.Errorf("matched %d books to the library", len(v.inLib))
	}
	if err := tt.Click("Dune"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("In your library") || !tt.HasText("Show in library") {
		t.Errorf("detail: %q", tt.Texts())
	}
	shot(t, tt, "device-books")

	// Remove one from the reader, after asking.
	a.askRemoveFromReader(v, []int64{v.curID})
	tt.Frame()
	if !tt.HasText("Remove “Dune” from Kindle?") {
		t.Fatalf("no confirmation: %q", tt.Texts())
	}
	shot(t, tt, "device-remove")
	if err := tt.Click("Remove"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(d.Books()) != 1 || tt.HasText("Dune") {
		t.Errorf("after removing, reader has %v", d.Books())
	}
	if _, err := os.Stat(filepath.Join(root, "documents", "Herbert, Frank")); !os.IsNotExist(err) {
		t.Errorf("Dune's folder left on the reader: %v", err)
	}
}

func TestReaderRefusesFormat(t *testing.T) {
	a := newTestApp(t)
	d := plugIn(t, a, fakeKindle(t, false)) // no KOReader: no EPUB
	a.toasts = nil
	a.sendToReader(d, []int64{a.books[0].ID})
	if len(d.Books()) != 0 {
		t.Errorf("an EPUB went to a Kindle without KOReader")
	}
	found := false
	for _, m := range a.toasts {
		if m.error && strings.Contains(m.text, "can’t read") {
			found = true
		}
	}
	if !found {
		t.Errorf("toasts = %+v", a.toasts)
	}
}

func TestAddReaderBookToLibrary(t *testing.T) {
	a := newTestApp(t)
	root := fakeKindle(t, false)
	os.WriteFile(filepath.Join(root, "documents", "Nous - Evgueni Zamiatine.txt"), []byte("a text"), 0o644)
	d := plugIn(t, a, root)
	a.openDevice(d)
	v := a.deviceView()
	if len(v.books) != 1 || len(v.inLib) != 0 {
		t.Fatalf("reader books %d, in library %d", len(v.books), len(v.inLib))
	}
	n := len(a.books)
	a.addReaderBooksToLibrary(v, []int64{v.books[0].ID})
	if len(a.books) != n+1 {
		t.Fatalf("library has %d books, want %d", len(a.books), n+1)
	}
	if len(v.inLib) != 1 {
		t.Errorf("the added book isn't matched to the reader's")
	}
}

func TestReaderUnplugged(t *testing.T) {
	a := newTestApp(t)
	d := plugIn(t, a, fakeKindle(t, false))
	a.openDevice(d)
	a.dev.watcher.Sources = nil
	a.dev.watcher.Scan(context.Background())
	a.devicesChanged()
	if a.page != pageLibrary || a.dev.view != nil {
		t.Errorf("page %v after unplugging", a.page)
	}
}

// TestRealKindle shows the reader whose files are in DALIBRE_KINDLE (a
// copy: the test writes calibre's records there).
func TestRealKindle(t *testing.T) {
	root := os.Getenv("DALIBRE_KINDLE")
	if root == "" {
		t.Skip("DALIBRE_KINDLE not set")
	}
	a := newTestApp(t)
	d := plugIn(t, a, root)
	a.openDevice(d)
	tt := ui.NewTester(a.View, 1440, 900)
	tt.Frame()
	tt.Frame()
	shot(t, tt, "kindle-grid")
	a.settings.ListView = true
	tt.Frame()
	tt.Frame()
	shot(t, tt, "kindle-list")
}
