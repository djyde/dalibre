//go:build !unix && !windows

package device

import "errors"

func diskUsage(string) (free, total int64, err error) {
	return 0, 0, errors.New("disk space isn’t known on this system")
}
