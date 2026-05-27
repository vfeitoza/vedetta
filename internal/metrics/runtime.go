package metrics

import (
	"fmt"
	"io"
	"runtime"
	"runtime/pprof"
)

// runtimeCollector renders Go runtime, GC, and process-RSS metrics under the
// standard client_golang names (go_*, process_resident_memory_bytes) so
// off-the-shelf dashboards bind without relabeling. It holds no state: every
// value is sampled live at scrape time from a single runtime.ReadMemStats plus
// the goroutine count, thread count, and (where supported) process RSS.
type runtimeCollector struct{}

// Registered as a package side effect, exactly like the detection instruments.
var _ = register(&runtimeCollector{})

// reset is a no-op: the collector accumulates no test-visible state, so there is
// nothing for ResetForTest to clear.
func (c *runtimeCollector) reset() {}

func writeGaugeUint(w io.Writer, name, help string, v uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %d\n", name, help, name, name, v)
}

func writeGaugeFloat(w io.Writer, name, help string, v float64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", name, help, name, name, formatFloat(v))
}

func writeCounterUint(w io.Writer, name, help string, v uint64) {
	fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", name, help, name, name, v)
}

// WriteProm samples the runtime once and renders the standard exposition.
func (c *runtimeCollector) WriteProm(w io.Writer) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	writeGaugeUint(w, "go_goroutines", "Number of goroutines that currently exist.",
		uint64(runtime.NumGoroutine()))
	writeGaugeUint(w, "go_threads", "Number of OS threads created.",
		uint64(pprof.Lookup("threadcreate").Count()))

	fmt.Fprintf(w, "# HELP go_info Information about the Go environment.\n")
	fmt.Fprintf(w, "# TYPE go_info gauge\n")
	fmt.Fprintf(w, "go_info{version=\"%s\"} 1\n", escapeLabel(runtime.Version()))

	writeGaugeUint(w, "go_memstats_alloc_bytes",
		"Number of bytes allocated and still in use.", m.Alloc)
	writeCounterUint(w, "go_memstats_alloc_bytes_total",
		"Total number of bytes allocated, even if freed.", m.TotalAlloc)
	writeGaugeUint(w, "go_memstats_sys_bytes",
		"Number of bytes obtained from system.", m.Sys)
	writeCounterUint(w, "go_memstats_lookups_total",
		"Total number of pointer lookups.", m.Lookups)
	writeCounterUint(w, "go_memstats_mallocs_total",
		"Total number of mallocs.", m.Mallocs)
	writeCounterUint(w, "go_memstats_frees_total",
		"Total number of frees.", m.Frees)

	writeGaugeUint(w, "go_memstats_heap_alloc_bytes",
		"Number of heap bytes allocated and still in use.", m.HeapAlloc)
	writeGaugeUint(w, "go_memstats_heap_sys_bytes",
		"Number of heap bytes obtained from system.", m.HeapSys)
	writeGaugeUint(w, "go_memstats_heap_idle_bytes",
		"Number of heap bytes waiting to be used.", m.HeapIdle)
	writeGaugeUint(w, "go_memstats_heap_inuse_bytes",
		"Number of heap bytes that are in use.", m.HeapInuse)
	writeGaugeUint(w, "go_memstats_heap_released_bytes",
		"Number of heap bytes released to OS.", m.HeapReleased)
	writeGaugeUint(w, "go_memstats_heap_objects",
		"Number of allocated objects.", m.HeapObjects)

	writeGaugeUint(w, "go_memstats_stack_inuse_bytes",
		"Number of bytes in use by the stack allocator.", m.StackInuse)
	writeGaugeUint(w, "go_memstats_stack_sys_bytes",
		"Number of bytes obtained from system for stack allocator.", m.StackSys)
	writeGaugeUint(w, "go_memstats_mspan_inuse_bytes",
		"Number of bytes in use by mspan structures.", m.MSpanInuse)
	writeGaugeUint(w, "go_memstats_mspan_sys_bytes",
		"Number of bytes used for mspan structures obtained from system.", m.MSpanSys)
	writeGaugeUint(w, "go_memstats_mcache_inuse_bytes",
		"Number of bytes in use by mcache structures.", m.MCacheInuse)
	writeGaugeUint(w, "go_memstats_mcache_sys_bytes",
		"Number of bytes used for mcache structures obtained from system.", m.MCacheSys)
	writeGaugeUint(w, "go_memstats_buck_hash_sys_bytes",
		"Number of bytes used by the profiling bucket hash table.", m.BuckHashSys)
	writeGaugeUint(w, "go_memstats_gc_sys_bytes",
		"Number of bytes used for garbage collection system metadata.", m.GCSys)
	writeGaugeUint(w, "go_memstats_other_sys_bytes",
		"Number of bytes used for other system allocations.", m.OtherSys)

	writeGaugeUint(w, "go_memstats_next_gc_bytes",
		"Number of heap bytes when next garbage collection will take place.", m.NextGC)
	writeGaugeFloat(w, "go_memstats_last_gc_time_seconds",
		"Number of seconds since 1970 of last garbage collection.",
		float64(m.LastGC)/1e9)
	writeGaugeFloat(w, "go_memstats_gc_cpu_fraction",
		"The fraction of this program's available CPU time used by the GC since the program started.",
		m.GCCPUFraction)

	// GC pause summary: sum + count only (no quantiles), so the standard
	// rate(go_gc_duration_seconds_sum) / rate(_count) panels work.
	fmt.Fprintf(w, "# HELP go_gc_duration_seconds A summary of the wall-time pause (stop-the-world) duration in garbage collection cycles.\n")
	fmt.Fprintf(w, "# TYPE go_gc_duration_seconds summary\n")
	fmt.Fprintf(w, "go_gc_duration_seconds_sum %s\n", formatFloat(float64(m.PauseTotalNs)/1e9))
	fmt.Fprintf(w, "go_gc_duration_seconds_count %d\n", m.NumGC)

	if rss, ok := readRSS(); ok {
		writeGaugeUint(w, "process_resident_memory_bytes",
			"Resident memory size in bytes.", rss)
	}
}
