package logging

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	"google.golang.org/protobuf/proto"
)

func baseHandler(buf *bytes.Buffer) slog.Handler {
	return slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelInfo})
}

func TestInitDisabledReturnsBaseHandler(t *testing.T) {
	var buf bytes.Buffer
	base := baseHandler(&buf)
	p, err := Init(context.Background(), Config{Enabled: false}, "test", base)
	if err != nil {
		t.Fatalf("Init returned %v", err)
	}
	if p.Handler() != base {
		t.Error("disabled Init must return the exact base handler")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("disabled Shutdown must be a no-op, got %v", err)
	}
}

func TestInitDegradesToBaseWhenExporterConstructionFails(t *testing.T) {
	// Exporter construction rarely fails synchronously in the real OTLP
	// exporters, so substitute a deterministic failure to prove the
	// degrade-to-base path: Init must return the EXACT base handler, no error.
	orig := exporterFactory
	t.Cleanup(func() { exporterFactory = orig })
	exporterFactory = func(context.Context, Config, func(string) string) (log.Exporter, error) {
		return nil, errors.New("boom")
	}

	var buf bytes.Buffer
	base := baseHandler(&buf)
	p, err := Init(context.Background(), Config{Enabled: true}, "test", base)
	if err != nil {
		t.Fatalf("Init must never return a fatal error, got %v", err)
	}
	if p.Handler() != base {
		t.Error("construction failure must degrade to the exact base handler")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Errorf("degraded Shutdown must be a no-op, got %v", err)
	}
}

func TestInitExportsToOTLPReceiverWithResourceAndCorrelation(t *testing.T) {
	type captured struct {
		req  *collogspb.ExportLogsServiceRequest
		path string
	}
	got := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if r.Header.Get("Content-Encoding") == "gzip" {
			zr, _ := gzip.NewReader(bytes.NewReader(body))
			body, _ = io.ReadAll(zr)
		}
		var req collogspb.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Errorf("unmarshal export request: %v", err)
		}
		select {
		case got <- captured{req: &req, path: r.URL.Path}:
		default:
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	var buf bytes.Buffer
	base := baseHandler(&buf)
	// httptest serves plain HTTP at the server root; use the URL form so the http
	// exporter targets it. ensureLogsPath must append /v1/logs.
	p, err := Init(context.Background(), Config{
		Enabled:     true,
		Endpoint:    srv.URL,
		Protocol:    "http/protobuf",
		ServiceName: "vedetta-test",
	}, "v9.9.9", base)
	if err != nil {
		t.Fatalf("Init returned %v", err)
	}

	// Emit under a known span context so we can assert trace-log correlation.
	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled})
	lctx := trace.ContextWithSpanContext(context.Background(), sc)
	slog.New(p.Handler()).InfoContext(lctx, "hello-loki", "camera", "front")

	// Flush.
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := p.Shutdown(sctx); err != nil {
		t.Fatalf("Shutdown returned %v", err)
	}

	select {
	case c := <-got:
		if c.path != "/v1/logs" {
			t.Errorf("OTLP/HTTP logs must POST to /v1/logs, got %q", c.path)
		}
		assertHasLog(t, c.req, "hello-loki", "vedetta-test", "v9.9.9", tid[:], sid[:])
	case <-time.After(5 * time.Second):
		t.Fatal("no OTLP export received")
	}
}

func assertHasLog(t *testing.T, req *collogspb.ExportLogsServiceRequest, body, svc, ver string, traceID, spanID []byte) {
	t.Helper()
	var sawBody, sawService, sawVersion, sawTrace, sawSpan bool
	for _, rl := range req.GetResourceLogs() {
		for _, kv := range rl.GetResource().GetAttributes() {
			if kv.GetKey() == "service.name" && kv.GetValue().GetStringValue() == svc {
				sawService = true
			}
			if kv.GetKey() == "service.version" && kv.GetValue().GetStringValue() == ver {
				sawVersion = true
			}
		}
		for _, sl := range rl.GetScopeLogs() {
			for _, lr := range sl.GetLogRecords() {
				if strings.Contains(lr.GetBody().GetStringValue(), body) {
					sawBody = true
				}
				if bytes.Equal(lr.GetTraceId(), traceID) {
					sawTrace = true
				}
				if bytes.Equal(lr.GetSpanId(), spanID) {
					sawSpan = true
				}
			}
		}
	}
	if !sawBody {
		t.Error("exported logs missing the record body")
	}
	if !sawService {
		t.Errorf("exported logs missing service.name=%q", svc)
	}
	if !sawVersion {
		t.Errorf("exported logs missing service.version=%q", ver)
	}
	if !sawTrace {
		t.Error("exported logs missing the correlated trace_id")
	}
	if !sawSpan {
		t.Error("exported logs missing the correlated span_id")
	}
}
