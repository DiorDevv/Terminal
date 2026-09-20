//go:build !windows

package monitor

import (
	"fmt"
	"syscall"
)

// DiskUsage reports the filesystem holding path, the way `df` does: free space
// is what an unprivileged process may use, and the percentage is used/(used+free).
func DiskUsage(path string) (Usage, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Usage{}, fmt.Errorf("statfs %s: %w", path, err)
	}
	bsize := uint64(st.Bsize)
	total := st.Blocks * bsize
	free := st.Bavail * bsize
	used := (st.Blocks - st.Bfree) * bsize

	u := Usage{Path: path, TotalBytes: total, UsedBytes: used, FreeBytes: free}
	if used+free > 0 {
		u.UsedPercent = float64(used) / float64(used+free) * 100
	}
	return u, nil
}
