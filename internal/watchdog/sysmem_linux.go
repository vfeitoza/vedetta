//go:build linux

package watchdog

import "golang.org/x/sys/unix"

// SystemMemoryBytes returns total physical RAM in bytes, read from sysinfo(2).
func SystemMemoryBytes() (uint64, error) {
	var si unix.Sysinfo_t
	if err := unix.Sysinfo(&si); err != nil {
		return 0, err
	}
	// Totalram is expressed in units of si.Unit bytes.
	return uint64(si.Totalram) * uint64(si.Unit), nil
}
