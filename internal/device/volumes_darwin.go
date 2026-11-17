package device

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// Volumes is the Source of the volumes mounted from drives: on macOS, the
// folders in /Volumes other than the startup disk. Which USB device a
// volume is on isn't read yet (IOKit); drivers match by what the volume
// holds.
type Volumes struct{}

func (Volumes) Storages() ([]Storage, error) {
	ents, err := os.ReadDir("/Volumes")
	if err != nil {
		return nil, err
	}
	var out []Storage
	for _, e := range ents {
		p := filepath.Join("/Volumes", e.Name())
		if real, err := filepath.EvalSymlinks(p); err != nil || real == "/" {
			continue // the startup disk links to /
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, NewVolume(p, "", e.Name(), USBInfo{}))
	}
	return out, nil
}

func eject(v *Volume) error {
	if out, err := exec.Command("diskutil", "eject", v.Mount).CombinedOutput(); err != nil {
		return fmt.Errorf("couldn’t eject %s: %s", v.label, firstLine(out, err))
	}
	return nil
}
