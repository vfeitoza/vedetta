package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseByteSize(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    int64
		wantErr bool
	}{
		// Valid sizes
		{"10GB", "10GB", 10 * 1024 * 1024 * 1024, false},
		{"500MB", "500MB", 500 * 1024 * 1024, false},
		{"1TB", "1TB", 1024 * 1024 * 1024 * 1024, false},
		{"1024KB", "1024KB", 1024 * 1024, false},
		{"100B", "100B", 100, false},

		// Fractional sizes
		{"1.5GB", "1.5GB", int64(1.5 * 1024 * 1024 * 1024), false},
		{"0.5TB", "0.5TB", int64(0.5 * 1024 * 1024 * 1024 * 1024), false},

		// Plain number (bytes)
		{"plain bytes", "1048576", 1048576, false},

		// Zero and empty
		{"zero", "0", 0, false},
		{"empty", "", 0, false},

		// Case insensitivity
		{"lowercase gb", "10gb", 10 * 1024 * 1024 * 1024, false},
		{"lowercase mb", "500mb", 500 * 1024 * 1024, false},
		{"mixed case", "10Gb", 10 * 1024 * 1024 * 1024, false},

		// Whitespace
		{"leading space", "  10GB", 10 * 1024 * 1024 * 1024, false},
		{"trailing space", "10GB  ", 10 * 1024 * 1024 * 1024, false},

		// Invalid inputs
		{"letters only", "abc", 0, true},
		{"invalid suffix", "10XB", 0, true},
		{"negative plain", "-1", -1, false}, // ParseInt accepts negatives
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseByteSize(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseByteSize(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("parseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}
	return path
}

func TestLoadMinimalConfig(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://192.168.1.10/stream
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cfg.Cameras))
	}
	cam := cfg.Cameras[0]
	if cam.Name != "front" {
		t.Errorf("camera name = %q, want %q", cam.Name, "front")
	}
	if cam.URL != "rtsp://192.168.1.10/stream" {
		t.Errorf("camera url = %q, want rtsp://192.168.1.10/stream", cam.URL)
	}
}

func TestLoadDefaultValues(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://localhost/stream
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	cam := cfg.Cameras[0]

	// Detect stream defaults
	if cam.Detect.Width != 640 {
		t.Errorf("detect width = %d, want 640", cam.Detect.Width)
	}
	if cam.Detect.Height != 480 {
		t.Errorf("detect height = %d, want 480", cam.Detect.Height)
	}
	if cam.Detect.FPS != 5 {
		t.Errorf("detect fps = %d, want 5", cam.Detect.FPS)
	}

	// Record stream defaults
	if cam.Record.Width != 1920 {
		t.Errorf("record width = %d, want 1920", cam.Record.Width)
	}
	if cam.Record.Height != 1080 {
		t.Errorf("record height = %d, want 1080", cam.Record.Height)
	}
	if cam.Record.FPS != 15 {
		t.Errorf("record fps = %d, want 15", cam.Record.FPS)
	}

	// Camera enabled by default
	if !cam.Enabled {
		t.Error("camera should be enabled by default")
	}

	// Global detect defaults
	if cfg.Detect.ScoreThreshold != 0.5 {
		t.Errorf("score_threshold = %f, want 0.5", cfg.Detect.ScoreThreshold)
	}
	if cfg.Detect.MotionThreshold != 0.02 {
		t.Errorf("motion_threshold = %f, want 0.02", cfg.Detect.MotionThreshold)
	}

	// Recording defaults
	if cfg.Recording.Path != "./recordings" {
		t.Errorf("recording path = %q, want ./recordings", cfg.Recording.Path)
	}
	if cfg.Recording.PreCapture != 5*time.Second {
		t.Errorf("pre_capture = %v, want 5s", cfg.Recording.PreCapture)
	}
	if cfg.Recording.PostCapture != 10*time.Second {
		t.Errorf("post_capture = %v, want 10s", cfg.Recording.PostCapture)
	}
	if cfg.Recording.RetainDays != 7 {
		t.Errorf("retain_days = %d, want 7", cfg.Recording.RetainDays)
	}
	if cfg.Recording.EventRetain != 30 {
		t.Errorf("event_retain = %d, want 30", cfg.Recording.EventRetain)
	}
	if cfg.Recording.SegmentLength != 10*time.Minute {
		t.Errorf("segment_length = %v, want 10m", cfg.Recording.SegmentLength)
	}
	if !cfg.Recording.Continuous {
		t.Error("continuous should default to true")
	}

	// Event defaults
	if cfg.Events.CooldownSeconds != 30 {
		t.Errorf("cooldown_seconds = %d, want 30", cfg.Events.CooldownSeconds)
	}
	if cfg.Events.SnapshotPath != "./snapshots" {
		t.Errorf("snapshot_path = %q, want ./snapshots", cfg.Events.SnapshotPath)
	}
	if cfg.Events.SnapshotQuality != 85 {
		t.Errorf("snapshot_quality = %d, want 85", cfg.Events.SnapshotQuality)
	}

	// Storage defaults
	if cfg.Storage.DBPath != "./vedetta.db" {
		t.Errorf("db_path = %q, want ./vedetta.db", cfg.Storage.DBPath)
	}

	// API defaults
	if cfg.API.Host != "0.0.0.0" {
		t.Errorf("api host = %q, want 0.0.0.0", cfg.API.Host)
	}
	if cfg.API.Port != 5050 {
		t.Errorf("api port = %d, want 5050", cfg.API.Port)
	}
}

func TestLoadMultipleCameras(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://192.168.1.10/stream
  - name: back
    url: rtsp://192.168.1.11/stream
  - name: garage
    url: rtsp://192.168.1.12/stream
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if len(cfg.Cameras) != 3 {
		t.Fatalf("expected 3 cameras, got %d", len(cfg.Cameras))
	}

	names := []string{"front", "back", "garage"}
	for i, want := range names {
		if cfg.Cameras[i].Name != want {
			t.Errorf("camera %d name = %q, want %q", i, cfg.Cameras[i].Name, want)
		}
	}
}

func TestLoadMaxStorage(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://localhost/stream
recording:
  max_storage: "10GB"
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	want := int64(10 * 1024 * 1024 * 1024)
	if cfg.Recording.MaxStorageBytes() != want {
		t.Errorf("MaxStorageBytes() = %d, want %d", cfg.Recording.MaxStorageBytes(), want)
	}
}

func TestLoadMaxStorageNotSet(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://localhost/stream
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Recording.MaxStorageBytes() != 0 {
		t.Errorf("MaxStorageBytes() = %d, want 0 when not set", cfg.Recording.MaxStorageBytes())
	}
}

func TestLoadInvalidMaxStorage(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://localhost/stream
recording:
  max_storage: "notasize"
`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("Load() should return error for invalid max_storage")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name   string
		yaml   string
		errMsg string
	}{
		{
			name:   "missing camera name",
			yaml:   "cameras:\n  - url: rtsp://localhost/stream\n",
			errMsg: "name is required",
		},
		{
			name:   "missing camera url",
			yaml:   "cameras:\n  - name: front\n",
			errMsg: "url is required",
		},
		{
			name:   "no cameras",
			yaml:   "detect:\n  score_threshold: 0.5\n",
			errMsg: "at least one camera",
		},
		{
			name:   "invalid yaml",
			yaml:   "cameras:\n  - name: [invalid",
			errMsg: "parsing config",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.yaml)
			_, err := Load(path)
			if err == nil {
				t.Fatal("Load() should return error")
			}
			if got := err.Error(); !contains(got, tt.errMsg) {
				t.Errorf("error %q should contain %q", got, tt.errMsg)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml")
	if err == nil {
		t.Fatal("Load() should return error for missing file")
	}
}

func TestLoadCustomDetectStream(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://localhost/stream
    detect:
      width: 320
      height: 240
      fps: 10
    record:
      width: 3840
      height: 2160
      fps: 30
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	cam := cfg.Cameras[0]
	if cam.Detect.Width != 320 || cam.Detect.Height != 240 || cam.Detect.FPS != 10 {
		t.Errorf("detect stream = %+v, want {320 240 10}", cam.Detect)
	}
	if cam.Record.Width != 3840 || cam.Record.Height != 2160 || cam.Record.FPS != 30 {
		t.Errorf("record stream = %+v, want {3840 2160 30}", cam.Record)
	}
}

func TestLoadTLSConfigValidation(t *testing.T) {
	tests := []struct {
		name   string
		yaml   string
		errMsg string
	}{
		{
			name: "cert without key",
			yaml: `
cameras:
  - name: front
    url: rtsp://localhost/stream
api:
  tls_cert: /path/to/cert.pem
`,
			errMsg: "both tls_cert and tls_key must be set",
		},
		{
			name: "key without cert",
			yaml: `
cameras:
  - name: front
    url: rtsp://localhost/stream
api:
  tls_key: /path/to/key.pem
`,
			errMsg: "both tls_cert and tls_key must be set",
		},
		{
			name: "both set is valid",
			yaml: `
cameras:
  - name: front
    url: rtsp://localhost/stream
api:
  tls_cert: /path/to/cert.pem
  tls_key: /path/to/key.pem
`,
			errMsg: "",
		},
		{
			name: "neither set is valid",
			yaml: `
cameras:
  - name: front
    url: rtsp://localhost/stream
`,
			errMsg: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.yaml)
			cfg, err := Load(path)
			if tt.errMsg != "" {
				if err == nil {
					t.Fatal("Load() should return error")
				}
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("Load() unexpected error: %v", err)
				}
				_ = cfg
			}
		})
	}
}

func TestLoadRecordURL(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://localhost/low
    record_url: rtsp://localhost/high
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}

	if cfg.Cameras[0].RecordURL != "rtsp://localhost/high" {
		t.Errorf("record_url = %q, want rtsp://localhost/high", cfg.Cameras[0].RecordURL)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchString(s, substr)
}

func searchString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
