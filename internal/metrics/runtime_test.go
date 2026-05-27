package metrics

import (
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// readRSS returns a positive byte count on platforms that support it. On
// unsupported builds it reports ok=false and the metric is simply omitted, so
// the test skips rather than fails there.
func TestReadRSS(t *testing.T) {
	rss, ok := readRSS()
	if !ok {
		t.Skip("readRSS unsupported on this build")
	}
	if rss == 0 {
		t.Fatalf("readRSS reported ok but returned 0 bytes")
	}
}

// scrapeRuntime renders just the runtime collector.
func scrapeRuntime() string {
	var b strings.Builder
	(&runtimeCollector{}).WriteProm(&b)
	return b.String()
}

// metricUint returns the value of an unlabeled metric line "name <value>".
func metricUint(t *testing.T, out, name string) uint64 {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == name {
			v, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				t.Fatalf("metric %q value %q not a uint: %v", name, fields[1], err)
			}
			return v
		}
	}
	t.Fatalf("metric %q not found in:\n%s", name, out)
	return 0
}

// The collector emits the standard Go runtime series with HELP/TYPE headers and
// the go_info build-version label.
func TestRuntimeCollectorNamesAndTypes(t *testing.T) {
	out := scrapeRuntime()
	for _, want := range []string{
		"# TYPE go_goroutines gauge",
		"# TYPE go_threads gauge",
		"# TYPE go_memstats_alloc_bytes gauge",
		"# TYPE go_memstats_sys_bytes gauge",
		"# TYPE go_memstats_heap_inuse_bytes gauge",
		"# TYPE go_memstats_alloc_bytes_total counter",
		"# TYPE go_memstats_mallocs_total counter",
		"# TYPE go_memstats_frees_total counter",
		"# TYPE go_memstats_lookups_total counter",
		"# TYPE go_gc_duration_seconds summary",
		"# TYPE go_info gauge",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
	if !strings.Contains(out, `go_info{version="`+runtime.Version()+`"} 1`) {
		t.Errorf("go_info missing version label:\n%s", out)
	}
}

// go_memstats_sys_bytes must equal the sum of its component *_sys gauges
// exactly, matching the client_golang invariant.
func TestRuntimeCollectorSysIdentity(t *testing.T) {
	out := scrapeRuntime()
	sys := metricUint(t, out, "go_memstats_sys_bytes")
	sum := metricUint(t, out, "go_memstats_heap_sys_bytes") +
		metricUint(t, out, "go_memstats_stack_sys_bytes") +
		metricUint(t, out, "go_memstats_mspan_sys_bytes") +
		metricUint(t, out, "go_memstats_mcache_sys_bytes") +
		metricUint(t, out, "go_memstats_buck_hash_sys_bytes") +
		metricUint(t, out, "go_memstats_gc_sys_bytes") +
		metricUint(t, out, "go_memstats_other_sys_bytes")
	if sys != sum {
		t.Errorf("go_memstats_sys_bytes=%d != component sum=%d", sys, sum)
	}
}

// Forcing a GC increases the go_gc_duration_seconds_count between scrapes.
func TestRuntimeCollectorGCCountIncreases(t *testing.T) {
	before := metricUint(t, scrapeRuntime(), "go_gc_duration_seconds_count")
	runtime.GC()
	after := metricUint(t, scrapeRuntime(), "go_gc_duration_seconds_count")
	if after <= before {
		t.Errorf("gc count did not increase: before=%d after=%d", before, after)
	}
}

// At least the test goroutine is running, so go_goroutines is >= 1.
func TestRuntimeCollectorGoroutines(t *testing.T) {
	if got := metricUint(t, scrapeRuntime(), "go_goroutines"); got < 1 {
		t.Errorf("go_goroutines = %d, want >= 1", got)
	}
}

// Every non-comment, non-blank line must be a series followed by a single
// numeric value.
func TestRuntimeCollectorFormatWellFormed(t *testing.T) {
	for _, line := range strings.Split(scrapeRuntime(), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("malformed exposition line %q", line)
		}
		if _, err := strconv.ParseFloat(fields[1], 64); err != nil {
			t.Fatalf("non-numeric value in line %q", line)
		}
	}
}

// When RSS is supported, the collector emits process_resident_memory_bytes; when
// not, it omits the series entirely.
func TestRuntimeCollectorRSS(t *testing.T) {
	out := scrapeRuntime()
	_, ok := readRSS()
	has := strings.Contains(out, "process_resident_memory_bytes ")
	if ok && !has {
		t.Errorf("RSS supported but series missing:\n%s", out)
	}
	if !ok && has {
		t.Errorf("RSS unsupported but series present:\n%s", out)
	}
}
