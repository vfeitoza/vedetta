//go:build !linux && (!darwin || !cgo)

package metrics

// readRSS reports unsupported on platforms without a dedicated reader; the
// process_resident_memory_bytes series is omitted there.
func readRSS() (uint64, bool) { return 0, false }
