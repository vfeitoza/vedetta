//go:build darwin

package watchdog

import "golang.org/x/sys/unix"

// SystemMemoryBytes returns total physical RAM in bytes, read from the
// hw.memsize sysctl.
func SystemMemoryBytes() (uint64, error) {
	return unix.SysctlUint64("hw.memsize")
}
