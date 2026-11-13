//go:build !linux && !windows

package storage

import "errors"

func availableBytes(string) (uint64, error) {
	return 0, errors.New("disk capacity check is supported on Linux and Windows")
}
