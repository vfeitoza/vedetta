//go:build linux

package metrics

import (
	"os"
	"strconv"
	"strings"
)

// readRSS returns current resident set size in bytes from /proc/self/statm.
// The second field is resident pages; multiply by the page size.
func readRSS() (uint64, bool) {
	data, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}
