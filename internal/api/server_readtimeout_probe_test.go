package api

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// This probe validates the design assumption behind buildHTTPServer: that a
// global ReadTimeout does NOT sever a long-lived streaming response (SSE/MSE/HLS
// stream for minutes). net/http's background read can cancel the request
// context on a read-deadline error, which would drop the stream. We reproduce
// the exact server timeout shape with a short ReadTimeout and assert the stream
// keeps flowing well past it.
func TestReadTimeoutDoesNotSeverStreamingResponse(t *testing.T) {
	const readTimeout = 200 * time.Millisecond

	streamFor := 1500 * time.Millisecond // > 7x readTimeout
	tick := 50 * time.Millisecond

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Error("ResponseWriter is not a Flusher")
			return
		}
		deadline := time.Now().Add(streamFor)
		ticker := time.NewTicker(tick)
		defer ticker.Stop()
		for time.Now().Before(deadline) {
			select {
			case <-r.Context().Done():
				// Stream was severed early - the failure we are probing for.
				return
			case <-ticker.C:
				if _, err := w.Write([]byte("data: tick\n\n")); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	}))
	// Mirror the production timeout shape: ReadTimeout set, WriteTimeout zero.
	srv.Config.ReadHeaderTimeout = readTimeout
	srv.Config.ReadTimeout = readTimeout
	srv.Start()
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("GET / HTTP/1.1\r\nHost: x\r\n\r\n")); err != nil {
		t.Fatalf("write request: %v", err)
	}

	br := bufio.NewReader(conn)
	var ticks int
	start := time.Now()
	// Read for longer than readTimeout; count how many ticks arrive after it.
	for time.Since(start) < streamFor {
		_ = conn.SetReadDeadline(time.Now().Add(streamFor))
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.Contains(line, "data: tick") && time.Since(start) > readTimeout {
			ticks++
		}
	}

	if ticks == 0 {
		t.Fatalf("no stream data received after ReadTimeout (%v) elapsed - a global ReadTimeout severs streaming responses", readTimeout)
	}
}
