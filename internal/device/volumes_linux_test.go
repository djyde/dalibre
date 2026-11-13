package device

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestParseMounts(t *testing.T) {
	data := []byte("/dev/nvme0n1p2 / ext4 rw,relatime 0 0\n" +
		"/dev/sde1 /run/media/laurent/My\\040Kindle vfat rw,nosuid 0 0\n" +
		"tmpfs /tmp tmpfs rw 0 0\n")
	ms := parseMounts(data)
	if len(ms) != 3 || ms[1].dev != "/dev/sde1" || ms[1].mount != "/run/media/laurent/My Kindle" || ms[1].fstype != "vfat" {
		t.Fatalf("parseMounts = %+v", ms)
	}
}

func TestUnescapeHex(t *testing.T) {
	if got := unescapeHex(`My\x20Kindle`); got != "My Kindle" {
		t.Errorf("unescapeHex = %q", got)
	}
}

// fakeSys lays out a sysfs, /dev and mounts file: a Kindle's partition
// sde1 on USB, a USB stick sdf with no partitions, a card reader sdg
// without a card, and an internal disk nvme0n1p1.
func fakeSys(t *testing.T, mounts string) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	write := func(p, data string) {
		p = filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	link := func(name, target string) {
		dir := filepath.Join(root, "sys/class/block")
		os.MkdirAll(dir, 0o755)
		if err := os.Symlink(filepath.Join(root, target), filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
	kindle := "sys/devices/pci0000:00/usb1/1-2"
	write(kindle+"/idVendor", "1949\n")
	write(kindle+"/idProduct", "0004\n")
	write(kindle+"/product", "Amazon Kindle\n")
	write(kindle+"/manufacturer", "Amazon\n")
	disk := kindle + "/1-2:1.0/host6/block/sde"
	write(disk+"/size", "6400000\n")
	write(disk+"/sde1/size", "6385648\n")
	write(disk+"/sde1/partition", "1\n")
	link("sde", disk)
	link("sde1", disk+"/sde1")

	stick := "sys/devices/pci0000:00/usb1/1-3"
	write(stick+"/idVendor", "0781\n")
	write(stick+"/idProduct", "5567\n")
	write(stick+"/1-3:1.0/host7/block/sdf/size", "100\n")
	link("sdf", stick+"/1-3:1.0/host7/block/sdf")

	reader := "sys/devices/pci0000:00/usb1/1-4"
	write(reader+"/idVendor", "05e3\n")
	write(reader+"/1-4:1.0/host8/block/sdg/size", "0\n")
	link("sdg", reader+"/1-4:1.0/host8/block/sdg")

	write("sys/devices/pci0000:00/nvme/nvme0n1/nvme0n1p1/size", "999\n")
	write("sys/devices/pci0000:00/nvme/nvme0n1/nvme0n1p1/partition", "1\n")
	link("nvme0n1p1", "sys/devices/pci0000:00/nvme/nvme0n1/nvme0n1p1")

	os.MkdirAll(filepath.Join(root, "dev/disk/by-label"), 0o755)
	os.WriteFile(filepath.Join(root, "dev/sde1"), nil, 0o644)
	os.Symlink(filepath.Join(root, "dev/sde1"), filepath.Join(root, "dev/disk/by-label/Kindle"))
	write("proc/mounts", mounts)

	oldSys, oldMounts, oldLabels, oldDev := sysRoot, mountsFile, labelsDir, devDir
	sysRoot, mountsFile, labelsDir, devDir = filepath.Join(root, "sys"), filepath.Join(root, "proc/mounts"), filepath.Join(root, "dev/disk/by-label"), filepath.Join(root, "dev")
	t.Cleanup(func() { sysRoot, mountsFile, labelsDir, devDir = oldSys, oldMounts, oldLabels, oldDev })
	return root
}

func TestPendingFindsUnmountedUSBVolumes(t *testing.T) {
	root := fakeSys(t, "/dev/nvme0n1p1 / ext4 rw 0 0\n")
	got, err := Volumes{}.Pending()
	if err != nil {
		t.Fatal(err)
	}
	byNode := map[string]Pending{}
	for _, p := range got {
		byNode[filepath.Base(p.Node)] = p
	}
	if len(byNode) != 2 {
		t.Fatalf("pending = %+v", got)
	}
	k, ok := byNode["sde1"]
	if !ok || k.Label != "Kindle" || k.USB.Vendor != 0x1949 || k.USB.Name != "Amazon Kindle" || k.USB.Protocol != "usbms" {
		t.Errorf("Kindle's partition = %+v", k)
	}
	if k.Node != filepath.Join(root, "dev", "sde1") {
		t.Errorf("node = %q", k.Node)
	}
	// The stick has no partitions: its disk is the volume.
	if _, ok := byNode["sdf"]; !ok {
		t.Error("a disk without partitions wasn't offered")
	}

	var mounted string
	old := mount
	mount = func(_ context.Context, node string) error { mounted = node; return nil }
	t.Cleanup(func() { mount = old })
	if err := k.Connect(context.Background()); err != nil || mounted != k.Node {
		t.Errorf("Connect = %v, mounted %q", err, mounted)
	}
}

func TestPendingSkipsMounted(t *testing.T) {
	root := fakeSys(t, "")
	os.WriteFile(mountsFile, []byte(filepath.Join(root, "dev/sde1")+" /run/media/u/Kindle vfat rw 0 0\n"), 0o644)
	got, _ := Volumes{}.Pending()
	for _, p := range got {
		if filepath.Base(p.Node) == "sde1" {
			t.Errorf("a mounted volume is pending: %+v", p)
		}
	}
}
