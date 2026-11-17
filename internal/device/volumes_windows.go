package device

import (
	"errors"

	"golang.org/x/sys/windows"
)

// Volumes is the Source of the volumes mounted from drives: on Windows,
// the removable drives' letters. Which USB device a drive is on isn't read
// yet (SetupAPI); drivers match by what the drive holds.
type Volumes struct{}

func (Volumes) Storages() ([]Storage, error) {
	mask, err := windows.GetLogicalDrives()
	if err != nil {
		return nil, err
	}
	var out []Storage
	for i := 0; i < 26; i++ {
		if mask&(1<<i) == 0 {
			continue
		}
		root := string(rune('A'+i)) + `:\`
		p, _ := windows.UTF16PtrFromString(root)
		if windows.GetDriveType(p) != windows.DRIVE_REMOVABLE {
			continue
		}
		label := make([]uint16, windows.MAX_PATH+1)
		if windows.GetVolumeInformation(p, &label[0], uint32(len(label)), nil, nil, nil, nil, 0) != nil {
			continue // no medium
		}
		out = append(out, NewVolume(root, "", windows.UTF16ToString(label), USBInfo{}))
	}
	return out, nil
}

func diskUsage(dir string) (free, total int64, err error) {
	p, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return 0, 0, err
	}
	var avail, tot, all uint64
	if err := windows.GetDiskFreeSpaceEx(p, &avail, &tot, &all); err != nil {
		return 0, 0, err
	}
	return int64(avail), int64(tot), nil
}

var errNoEject = errors.New("eject it from the taskbar’s Safely Remove Hardware")

func eject(*Volume) error { return errNoEject }
