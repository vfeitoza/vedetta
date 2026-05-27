package metrics

import (
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
