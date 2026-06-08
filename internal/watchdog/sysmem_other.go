//go:build !darwin && !linux

package watchdog

import "errors"

// SystemMemoryBytes is unsupported on this platform. The memory guard's auto
// limit degrades to disabled; an explicit limit (runtime.memory_limit_mb) still
// works everywhere.
func SystemMemoryBytes() (uint64, error) {
	return 0, errors.New("system memory probe not supported on this platform")
}
