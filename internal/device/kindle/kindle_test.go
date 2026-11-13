package kindle

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"dalibre/internal/device"
)

func mkdirs(t *testing.T, root string, dirs ...string) {
	for _, d := range dirs {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMatch(t *testing.T) {
	kindle := t.TempDir()
	mkdirs(t, kindle, "documents", "system")
	other := t.TempDir()
	mkdirs(t, other, "documents")
	cases := []struct {
		name  string
		root  string
		label string
		usb   device.USBInfo
		want  bool
	}{
		{"Lab126 device", kindle, "WHATEVER", device.USBInfo{Vendor: VendorID, Product: 4}, true},
		{"other vendor", kindle, "Kindle", device.USBInfo{Vendor: 0x2237}, false},
		{"unknown USB, Kindle label", kindle, "Kindle", device.USBInfo{}, true},
		{"unknown USB, other label", kindle, "USB STICK", device.USBInfo{}, false},
		{"no system folder", other, "Kindle", device.USBInfo{Vendor: VendorID}, false},
	}
	for _, c := range cases {
		if got := (Driver{}).Match(device.NewVolume(c.root, "", c.label, c.usb)); got != c.want {
			t.Errorf("%s: Match = %v, want %v", c.name, got, c.want)
		}
	}
	// Without the USB device or the label, version.txt tells.
	if err := os.WriteFile(filepath.Join(kindle, "system", "version.txt"), []byte("Kindle 5.16.2.1.1 (409747 002)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !(Driver{}).Match(device.NewVolume(kindle, "", "NO NAME", device.USBInfo{})) {
		t.Error("a volume whose version.txt says Kindle wasn't matched")
	}
}

func TestOpen(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "documents", "system/thumbnails", "koreader")
	os.WriteFile(filepath.Join(root, "system", "version.txt"), []byte("Kindle 5.16.2.1.1 (409747 002)\n"), 0o644)
	os.WriteFile(filepath.Join(root, "documents", "Nous - Evgueni Zamiatine.epub"), []byte("not really"), 0o644)
	d, err := Driver{}.Open(context.Background(), device.NewVolume(root, "", "Kindle", device.USBInfo{Vendor: VendorID, Name: "Amazon Kindle"}))
	if err != nil {
		t.Fatal(err)
	}
	info := d.Info()
	if info.Kind != "kindle" || info.Firmware != "5.16.2.1.1" || info.Model != "Amazon Kindle" || info.ID == "" {
		t.Errorf("Info = %+v", info)
	}
	// KOReader is there, so EPUB is read, after the Kindle's own formats.
	if f := d.Formats(); f[0] != "AZW3" || !slices.Contains(f, "EPUB") {
		t.Errorf("Formats = %v", f)
	}
	if len(d.Books()) != 1 {
		t.Errorf("Books = %v", d.Books())
	}
}

func TestSidecars(t *testing.T) {
	got := Sidecars("documents/A/B - C.mobi")
	if got[0] != "documents/A/B - C.sdr" || !slices.Contains(got, "documents/A/B - C.apnx") {
		t.Errorf("Sidecars = %v", got)
	}
}
