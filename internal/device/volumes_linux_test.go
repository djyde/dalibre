package device

import "testing"

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
