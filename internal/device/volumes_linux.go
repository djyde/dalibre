package device

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Volumes is the Source of the volumes mounted from USB drives: on Linux,
// the mounts in /proc/self/mounts whose block device sits on a USB device
// in /sys.
type Volumes struct{}

// Where Linux says what is mounted and what the volumes are called; tests
// point them elsewhere, as sysRoot.
var (
	mountsFile = "/proc/self/mounts"
	labelsDir  = "/dev/disk/by-label"
	devDir     = "/dev"
)

func (Volumes) Storages() ([]Storage, error) {
	data, err := os.ReadFile(mountsFile)
	if err != nil {
		return nil, err
	}
	labels := diskLabels()
	var out []Storage
	seen := map[string]bool{}
	for _, m := range parseMounts(data) {
		if !strings.HasPrefix(m.dev, "/dev/") || seen[m.mount] {
			continue
		}
		dev := m.dev
		if real, err := filepath.EvalSymlinks(dev); err == nil {
			dev = real
		}
		usb, ok := usbOfBlock(filepath.Base(dev))
		if !ok {
			continue // an internal disk
		}
		seen[m.mount] = true
		out = append(out, NewVolume(m.mount, dev, labels[dev], usb))
	}
	return out, nil
}

type mountEntry struct{ dev, mount, fstype string }

// parseMounts reads /proc/self/mounts, whose fields escape spaces and
// other bytes as \ooo.
func parseMounts(data []byte) []mountEntry {
	var out []mountEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		out = append(out, mountEntry{dev: unescapeOctal(f[0]), mount: unescapeOctal(f[1]), fstype: f[2]})
	}
	return out
}

func unescapeOctal(s string) string {
	if !strings.Contains(s, `\`) {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) {
			if n, err := strconv.ParseUint(s[i+1:i+4], 8, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// diskLabels are the volume labels of the block devices, from
// /dev/disk/by-label, whose names escape bytes as \xHH.
func diskLabels() map[string]string {
	out := map[string]string{}
	ents, err := os.ReadDir(labelsDir)
	if err != nil {
		return out
	}
	for _, e := range ents {
		dev, err := filepath.EvalSymlinks(filepath.Join(labelsDir, e.Name()))
		if err == nil {
			out[dev] = unescapeHex(e.Name())
		}
	}
	return out
}

func unescapeHex(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] == 'x' {
			if n, err := strconv.ParseUint(s[i+2:i+4], 16, 8); err == nil {
				b.WriteByte(byte(n))
				i += 3
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

// sysRoot is where sysfs is; tests point it elsewhere.
var sysRoot = "/sys"

// usbOfBlock finds the USB device a block device (as "sde1") is on, from
// its path in sysfs: the first folder up from it with an idVendor.
func usbOfBlock(name string) (USBInfo, bool) {
	p, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "class", "block", name))
	if err != nil || !strings.Contains(p, "/usb") {
		return USBInfo{}, false
	}
	for dir := p; dir != "/" && dir != "." && strings.HasPrefix(dir, sysRoot); dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "idVendor")); err == nil {
			read := func(f string) string {
				b, _ := os.ReadFile(filepath.Join(dir, f))
				return strings.TrimSpace(string(b))
			}
			hex := func(f string) uint16 {
				n, _ := strconv.ParseUint(read(f), 16, 16)
				return uint16(n)
			}
			return USBInfo{
				Vendor: hex("idVendor"), Product: hex("idProduct"),
				Manufacturer: read("manufacturer"), Name: read("product"), Serial: read("serial"),
				Protocol: "usbms",
			}, true
		}
	}
	return USBInfo{}, false
}

// Pending are the volumes of USB drives that aren't mounted: the
// partitions in /sys/class/block on a USB device, with a medium, not in
// /proc/self/mounts; a whole disk counts when it has no partitions, as
// some readers' drives have none.
func (Volumes) Pending() ([]Pending, error) {
	data, err := os.ReadFile(mountsFile)
	if err != nil {
		return nil, err
	}
	mounted := map[string]bool{}
	for _, m := range parseMounts(data) {
		dev := m.dev
		if real, err := filepath.EvalSymlinks(dev); err == nil {
			dev = real
		}
		mounted[filepath.Base(dev)] = true
	}
	ents, err := os.ReadDir(filepath.Join(sysRoot, "class", "block"))
	if err != nil {
		return nil, err
	}
	labels := diskLabels()
	var out []Pending
	for _, e := range ents {
		name := e.Name()
		if mounted[name] || !hasMedium(name) {
			continue
		}
		dir, err := filepath.EvalSymlinks(filepath.Join(sysRoot, "class", "block", name))
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "partition")); err != nil && hasPartitions(dir, name) {
			continue // a disk whose partitions are the volumes
		}
		usb, ok := usbOfBlock(name)
		if !ok {
			continue
		}
		node := filepath.Join(devDir, name)
		out = append(out, Pending{Node: node, Label: labels[node], USB: usb, Connect: func(ctx context.Context) error {
			return mount(ctx, node)
		}})
	}
	return out, nil
}

// hasMedium reports whether the block device holds anything: a card
// reader without a card is 0 sectors.
func hasMedium(name string) bool {
	b, err := os.ReadFile(filepath.Join(sysRoot, "class", "block", name, "size"))
	if err != nil {
		return false
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(string(b)), 10, 64)
	return n > 0
}

// hasPartitions reports whether the disk in sysfs folder dir has
// partitions: folders named after it with a partition file.
func hasPartitions(dir, name string) bool {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range ents {
		if strings.HasPrefix(e.Name(), name) && e.Name() != name {
			if _, err := os.Stat(filepath.Join(dir, e.Name(), "partition")); err == nil {
				return true
			}
		}
	}
	return false
}

// mount mounts the volume at node as the desktop does, with udisks, for
// the user and without asking for a password; tests replace it.
var mount = func(ctx context.Context, node string) error {
	exe, err := exec.LookPath("udisksctl")
	if err != nil {
		return errNoUdisks
	}
	if out, err := exec.CommandContext(ctx, exe, "mount", "--no-user-interaction", "-b", node).CombinedOutput(); err != nil {
		return fmt.Errorf("%s", firstLine(out, err))
	}
	return nil
}

var errNoUdisks = errors.New("udisks isn’t installed: mount the reader from your file manager")

// eject unmounts the volume and powers its drive off, with udisks as the
// desktop does, so the reader leaves USB mode; with plain umount when
// udisks isn't there.
func eject(v *Volume) error {
	if exe, err := exec.LookPath("udisksctl"); err == nil && v.Dev != "" {
		if out, err := exec.Command(exe, "unmount", "--no-user-interaction", "-b", v.Dev).CombinedOutput(); err != nil {
			return fmt.Errorf("couldn’t unmount %s: %s", v.label, firstLine(out, err))
		}
		// The reader is unmounted already; powering off only tells it to
		// leave USB mode, which some don't support.
		exec.Command(exe, "power-off", "--no-user-interaction", "-b", v.Dev).Run()
		return nil
	}
	if out, err := exec.Command("umount", v.Mount).CombinedOutput(); err != nil {
		return fmt.Errorf("couldn’t unmount %s: %s", v.label, firstLine(out, err))
	}
	return nil
}
