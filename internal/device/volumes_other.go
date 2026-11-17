//go:build !linux && !darwin && !windows

package device

import "errors"

// Volumes finds no volumes on this system yet.
type Volumes struct{}

func (Volumes) Storages() ([]Storage, error) { return nil, nil }

func eject(*Volume) error { return errors.New("ejecting isn’t supported on this system") }
