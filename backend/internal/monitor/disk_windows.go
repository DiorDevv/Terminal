//go:build windows

package monitor

import "errors"

// DiskUsage is not implemented on Windows: the panel manages a Linux squid.
func DiskUsage(path string) (Usage, error) {
	return Usage{}, errors.New("disk usage is not available on this platform")
}
