package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"dalibre/internal/device"
	"dalibre/internal/device/kindle"
	"dalibre/internal/plugin"
)

func TestDevicePath(t *testing.T) {
	cases := []struct {
		kind, book, name, want string
	}{
		{deviceSidecar, "documents/Frank Herbert/Dune.azw3", "LanguageLayer.en.B123.kll",
			"documents/Frank Herbert/Dune.sdr/LanguageLayer.en.B123.kll"},
		{deviceSidecar, "documents/Dune.azw3", "XRAY.entities.B123.asc",
			"documents/Dune.sdr/XRAY.entities.B123.asc"},
		{deviceKLL, "", "kll.en.zh.klld", "system/kll/kll.en.zh.klld"},
	}
	for _, c := range cases {
		got, err := devicePath(c.kind, c.book, c.name)
		if err != nil || got != c.want {
			t.Fatalf("devicePath(%q, %q) = %q, %v; want %q", c.kind, c.book, got, err, c.want)
		}
		if c.kind == deviceSidecar && filepath.ToSlash(filepath.Dir(got)) != kindle.Sidecars(c.book)[0] {
			t.Errorf("sidecar disagrees with the Kindle driver: %s", got)
		}
	}
}

func TestDevicePathRefused(t *testing.T) {
	bad := [][3]string{
		{deviceSidecar, "documents/Dune.azw3", "../LanguageLayer.en.kll"},
		{deviceSidecar, "../documents/Dune.azw3", "LanguageLayer.en.kll"},
		{deviceSidecar, "system/version.txt", "version.txt"},
		{deviceSidecar, "documents/Dune.azw3", ".hidden"},
		{deviceKLL, "", "not-a-dictionary"},
		{deviceKLL, "", "kll.en.zh.klld:stream"},
		{deviceSidecar, `documents/..\..\Dune.azw3`, "LanguageLayer.en.kll"},
		{deviceSidecar, "documents/Dune.azw3", `folder\LanguageLayer.en.kll`},
		{"other", "documents/Dune.azw3", "file"},
	}
	for _, c := range bad {
		if got, err := devicePath(c[0], c[1], c[2]); err == nil {
			t.Errorf("devicePath(%q, %q, %q) = %q; want an error", c[0], c[1], c[2], got)
		}
	}
}

func TestDeviceWrite(t *testing.T) {
	root := t.TempDir()
	storage := device.NewVolume(root, "", "Kindle", device.USBInfo{})
	reader := &pluginTestDevice{st: storage}

	src := filepath.Join(root, "source.klld")
	if err := os.WriteFile(src, []byte("dictionary"), 0o600); err != nil {
		t.Fatal(err)
	}
	app := pluginTestApp(t, reader)
	if err := app.deviceWrite(context.Background(), storage.ID(), deviceKLL, "", "kll.en.zh.klld", src); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, "system/kll/kll.en.zh.klld"))
	if err != nil || string(data) != "dictionary" {
		t.Fatalf("dictionary = %q, %v", data, err)
	}
}

// pluginTestApp dispatches posts on a single goroutine, like the UI, so
// device tests also catch accidental background access under -race.
func pluginTestApp(t *testing.T, readers ...device.Device) *App {
	t.Helper()
	posts := make(chan func(), 64)
	done := make(chan struct{})
	a := &App{dev: &devicesState{list: readers}, post: func(fn func()) { posts <- fn }}
	go func() {
		defer close(done)
		for fn := range posts {
			fn()
		}
	}()
	t.Cleanup(func() { close(posts); <-done })
	return a
}

type pluginTestDevice struct {
	st    device.Storage
	books []device.Book
	kind  string
}

func (d *pluginTestDevice) Info() device.Info {
	kind := d.kind
	if kind == "" {
		kind = "kindle"
	}
	return device.Info{ID: d.st.ID(), Kind: kind, Name: "Kindle"}
}
func (d *pluginTestDevice) Books() []device.Book                   { return slices.Clone(d.books) }
func (d *pluginTestDevice) Formats() []string                      { return nil }
func (d *pluginTestDevice) Usage() (int64, int64, error)           { return 0, 0, nil }
func (d *pluginTestDevice) Refresh(context.Context) error          { return nil }
func (d *pluginTestDevice) Delete(context.Context, []string) error { return nil }
func (d *pluginTestDevice) Storage() device.Storage                { return d.st }
func (d *pluginTestDevice) Close()                                 {}
func (d *pluginTestDevice) Send(context.Context, []device.Upload, func(int)) ([]device.Book, error) {
	return nil, nil
}

func TestDeviceSidecarBook(t *testing.T) {
	st := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
	book := "documents/Frank Herbert/Dune.azw3"
	reader := &pluginTestDevice{st: st, books: []device.Book{{LPath: book}}}
	a := pluginTestApp(t, reader)
	ctx := context.Background()
	src := filepath.Join(t.TempDir(), "sidecar.kll")
	if err := os.WriteFile(src, []byte("sidecar"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Neither a fabricated book nor a stale entry in the device's cache
	// grants access to a sidecar directory.
	for _, missing := range []string{"documents/Other/Fake.azw3", book} {
		if err := a.deviceWrite(ctx, st.ID(), deviceSidecar, missing, "LanguageLayer.en.B123.kll", src); err == nil {
			t.Errorf("wrote sidecar for missing book %q", missing)
		}
		if err := a.deviceFile(ctx, st.ID(), deviceSidecar, missing, "LanguageLayer.en.B123.kll", io.Discard); err == nil {
			t.Errorf("read sidecar for missing book %q", missing)
		}
	}
	if err := os.MkdirAll(filepath.Dir(st.LocalPath(book)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.LocalPath(book), []byte("book"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.deviceWrite(ctx, st.ID(), deviceSidecar, book, "LanguageLayer.en.B123.kll", src); err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	if err := a.deviceFile(ctx, st.ID(), deviceSidecar, book, "LanguageLayer.en.B123.kll", &data); err != nil || data.String() != "sidecar" {
		t.Fatalf("sidecar = %q, %v", data.String(), err)
	}
}

func TestDeviceReadConfined(t *testing.T) {
	st := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
	a := pluginTestApp(t, &pluginTestDevice{st: st})
	outside := filepath.Join(t.TempDir(), "private.klld")
	if err := os.WriteFile(outside, []byte("outside-volume-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(st.LocalPath("system/kll"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, st.LocalPath("system/kll/kll.en.zh.klld")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	var data bytes.Buffer
	if err := a.deviceFile(context.Background(), st.ID(), deviceKLL, "", "kll.en.zh.klld", &data); err == nil || data.Len() != 0 {
		t.Fatalf("read escaped the volume: %q, %v", data.String(), err)
	}
}

func TestDeviceHostReaders(t *testing.T) {
	kindle := &pluginTestDevice{st: device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{}), books: []device.Book{
		{LPath: "documents/Dune.azw3", Authors: []string{"Frank Herbert"}, Identifiers: map[string]string{"asin": "B123"}},
	}}
	other := &pluginTestDevice{st: device.NewVolume(t.TempDir(), "", "Kobo", device.USBInfo{}), kind: "kobo"}
	mtp := &pluginTestDevice{st: device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{Protocol: "mtp"})}
	a := pluginTestApp(t, kindle, other, mtp)
	ctx := context.Background()
	host := a.deviceHost()
	readers, err := host.List(ctx)
	if err != nil || len(readers) != 1 || readers[0].ID != kindle.Info().ID {
		t.Fatalf("readers = %+v, %v", readers, err)
	}
	readers[0].Books[0].Authors[0] = "changed"
	readers[0].Books[0].Identifiers["asin"] = "changed"
	if kindle.books[0].Authors[0] != "Frank Herbert" || kindle.books[0].Identifiers["asin"] != "B123" {
		t.Fatal("device list shared mutable book metadata")
	}
	for _, unsupported := range []device.Device{other, mtp} {
		if err := host.Read(ctx, unsupported.Info().ID, deviceKLL, "", "kll.en.zh.klld", io.Discard); err == nil {
			t.Error("read from unsupported reader")
		}
		if err := host.Write(ctx, unsupported.Info().ID, deviceKLL, "", "kll.en.zh.klld", "missing"); err == nil {
			t.Error("wrote to unsupported reader")
		}
	}
	// Plugging and unplugging readers is serialized with the snapshot,
	// while List itself runs in an action's background goroutine.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for range 100 {
			a.post(func() { a.dev.list = nil })
			a.post(func() { a.dev.list = []device.Device{kindle} })
		}
	}()
	for range 100 {
		if _, err := host.List(ctx); err != nil {
			t.Error(err)
		}
	}
	wg.Wait()
	a.post(func() { a.dev.list = nil })
	if readers, err := host.List(ctx); err != nil || len(readers) != 0 {
		t.Fatalf("unplugged readers = %+v, %v", readers, err)
	}
	if err := host.Read(ctx, kindle.Info().ID, deviceKLL, "", "kll.en.zh.klld", io.Discard); err == nil {
		t.Error("read from disconnected reader")
	}
}

func TestDeviceReadActionCleansFiles(t *testing.T) {
	st := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
	a := pluginTestApp(t, &pluginTestDevice{st: st})
	file, err := st.Create("system/kll/kll.en.zh.klld")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(file, "dictionary"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	dir, data, hostTemps := t.TempDir(), t.TempDir(), t.TempDir()
	t.Setenv("TMPDIR", hostTemps)
	t.Setenv("TMP", hostTemps)
	t.Setenv("TEMP", hostTemps)
	manifest := `{"id":"test.device","name":"Device","version":"1.0.0","api":1,"dalibre":">=0.7.0","capabilities":{"actions":[{"id":"read","title":"Read","scope":"library"}]},"permissions":{"library":"read","device":true}}`
	script := `import { readFile } from "node:fs/promises";
		export async function run() {
			const [d] = await dalibre.device.list();
			const file = await dalibre.device.read(d.id, "kll", "", "kll.en.zh.klld");
			return { message: await readFile(file, "utf8") };
		}`
	for name, content := range map[string]string{"plugin.json": manifest, "main.js": script} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p, err := plugin.Load(dir, plugin.Options{DataDir: data})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	res, err := p.RunAction(context.Background(), "read", nil, nil, &plugin.ActionHost{Device: a.deviceHost()})
	if err != nil {
		t.Fatal(err)
	}
	defer res.Done()
	if res.Message != "dictionary" {
		t.Fatalf("read = %q", res.Message)
	}
	for _, dir := range []string{hostTemps, filepath.Join(data, "work")} {
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Errorf("read left files in %s: %v %v", dir, entries, err)
		}
	}
}

func TestMCPDeviceApply(t *testing.T) {
	st := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
	a := pluginTestApp(t, &pluginTestDevice{st: st})
	ctx := context.Background()
	file := "system/kll/kll.en.zh.klld"
	if err := os.MkdirAll(filepath.Dir(st.LocalPath(file)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.LocalPath(file), []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "out.klld")
	if err := os.WriteFile(src, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	preview := a.mcpDeviceHost(false)
	var data bytes.Buffer
	if err := preview.Read(ctx, st.ID(), deviceKLL, "", "kll.en.zh.klld", &data); err != nil || data.String() != "original" {
		t.Fatalf("preview read: %q %v", data.String(), err)
	}
	var pe *plugin.Error
	if err := preview.Write(ctx, st.ID(), deviceKLL, "", "kll.en.zh.klld", src); !errors.As(err, &pe) || pe.Kind != plugin.KindPermission {
		t.Fatalf("preview write: %v", err)
	}
	got, err := os.ReadFile(st.LocalPath(file))
	if err != nil || string(got) != "original" {
		t.Fatalf("preview changed device: %q %v", got, err)
	}
	if err := a.mcpDeviceHost(true).Write(ctx, st.ID(), deviceKLL, "", "kll.en.zh.klld", src); err != nil {
		t.Fatal(err)
	}
	got, err = os.ReadFile(st.LocalPath(file))
	if err != nil || string(got) != "replacement" {
		t.Fatalf("apply did not write device: %q %v", got, err)
	}
	assertNoDeviceParts(t, st)
}

// The first writer pauses with its file open until the second commits.
// It then writes some bytes but fails: the successful file must survive.
type pluginGatedStorage struct {
	device.Storage
	calls          atomic.Int32
	ready, release chan struct{}
	fail           error
}

func (s *pluginGatedStorage) Create(name string) (io.WriteCloser, error) {
	w, err := s.Storage.Create(name)
	if err != nil || s.calls.Add(1) != 1 {
		return w, err
	}
	return &pluginGatedWriter{WriteCloser: w, storage: s}, nil
}

type pluginGatedWriter struct {
	io.WriteCloser
	storage *pluginGatedStorage
}

func (w *pluginGatedWriter) Write(data []byte) (int, error) {
	close(w.storage.ready)
	<-w.storage.release
	n, err := w.WriteCloser.Write(data)
	if err == nil {
		err = w.storage.fail
	}
	return n, err
}

func TestDeviceConcurrentWrites(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "both succeed", true: "first fails"}[fail], func(t *testing.T) {
			volume := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
			st := &pluginGatedStorage{Storage: volume, ready: make(chan struct{}), release: make(chan struct{})}
			if fail {
				st.fail = errors.New("write failed")
			}
			a := pluginTestApp(t, &pluginTestDevice{st: st})
			dir := t.TempDir()
			first, second := filepath.Join(dir, "first.klld"), filepath.Join(dir, "second.klld")
			for file, content := range map[string]string{first: "FIRST!", second: "SECOND"} {
				if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			done := make(chan error, 1)
			go func() { done <- a.deviceWrite(context.Background(), st.ID(), deviceKLL, "", "kll.en.zh.klld", first) }()
			<-st.ready
			err2 := a.deviceWrite(context.Background(), st.ID(), deviceKLL, "", "kll.en.zh.klld", second)
			close(st.release)
			err1 := <-done
			if err2 != nil || !errors.Is(err1, st.fail) {
				t.Fatalf("writes: first=%v second=%v", err1, err2)
			}
			want := "FIRST!" // first commits last
			if fail {
				want = "SECOND"
			}
			data, err := os.ReadFile(volume.LocalPath("system/kll/kll.en.zh.klld"))
			if err != nil || string(data) != want {
				t.Fatalf("dictionary = %q, %v; want %q", data, err, want)
			}
			assertNoDeviceParts(t, volume)
		})
	}
}

type pluginCancelStorage struct {
	device.Storage
	cancel context.CancelFunc
}

func (s pluginCancelStorage) Create(name string) (io.WriteCloser, error) {
	w, err := s.Storage.Create(name)
	if err != nil {
		return nil, err
	}
	return pluginCancelWriter{WriteCloser: w, cancel: s.cancel}, nil
}

type pluginCancelWriter struct {
	io.WriteCloser
	cancel context.CancelFunc
}

func (w pluginCancelWriter) Close() error {
	err := w.WriteCloser.Close()
	w.cancel()
	return err
}

func TestDeviceWriteCancellation(t *testing.T) {
	volume := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := pluginCancelStorage{Storage: volume, cancel: cancel}
	a := pluginTestApp(t, &pluginTestDevice{st: st})
	original := volume.LocalPath("system/kll/kll.en.zh.klld")
	if err := os.MkdirAll(filepath.Dir(original), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(original, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join(t.TempDir(), "out.klld")
	if err := os.WriteFile(src, []byte("new dictionary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := a.deviceWrite(ctx, st.ID(), deviceKLL, "", "kll.en.zh.klld", src); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel before commit: %v", err)
	}
	data, err := os.ReadFile(original)
	if err != nil || string(data) != "original" {
		t.Fatalf("cancel replaced dictionary: %q %v", data, err)
	}
	assertNoDeviceParts(t, volume)
	if err := a.deviceWrite(ctx, st.ID(), deviceKLL, "", "kll.en.zh.klld", src); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel before write: %v", err)
	}
}

type pluginReadCancelWriter struct {
	buffer bytes.Buffer
	cancel context.CancelFunc
}

func (w *pluginReadCancelWriter) Write(data []byte) (int, error) {
	n, err := w.buffer.Write(data)
	w.cancel()
	return n, err
}

func TestDeviceReadCancellation(t *testing.T) {
	st := device.NewVolume(t.TempDir(), "", "Kindle", device.USBInfo{})
	a := pluginTestApp(t, &pluginTestDevice{st: st})
	if err := os.MkdirAll(st.LocalPath("system/kll"), 0o755); err != nil {
		t.Fatal(err)
	}
	data := bytes.Repeat([]byte("dictionary"), 16384)
	if err := os.WriteFile(st.LocalPath("system/kll/kll.en.zh.klld"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dst := &pluginReadCancelWriter{cancel: cancel}
	if err := a.deviceFile(ctx, st.ID(), deviceKLL, "", "kll.en.zh.klld", dst); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel during read: %v", err)
	}
	if dst.buffer.Len() == 0 || dst.buffer.Len() == len(data) {
		t.Fatalf("cancel did not stop between chunks: read %d of %d bytes", dst.buffer.Len(), len(data))
	}
}

func assertNoDeviceParts(t *testing.T, st *device.Volume) {
	t.Helper()
	entries, err := os.ReadDir(st.LocalPath("system/kll"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "kll.en.zh.klld" {
			t.Errorf("left temporary device file: %s", e.Name())
		}
	}
}
