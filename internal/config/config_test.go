package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	if !strings.Contains(content, "\nauth:") && !strings.HasPrefix(strings.TrimSpace(content), "auth:") {
		content += `
auth:
  users:
    - username: admin
      password_hash: "$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu"
`
	}
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
	configDir := filepath.Dir(path)

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
	if !cam.IsEnabled() {
		t.Error("camera should be enabled by default")
	}

	// Global detect defaults
	if cfg.Detect.ScoreThreshold != 0.65 {
		t.Errorf("score_threshold = %f, want 0.65", cfg.Detect.ScoreThreshold)
	}
	if cfg.Detect.Motion.MinRegionScore != 0.02 {
		t.Errorf("motion.min_region_score = %f, want 0.02", cfg.Detect.Motion.MinRegionScore)
	}

	// Recording defaults
	if cfg.Recording.Path != filepath.Join(configDir, "recordings") {
		t.Errorf("recording path = %q, want %q", cfg.Recording.Path, filepath.Join(configDir, "recordings"))
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
	if cfg.Events.SnapshotPath != filepath.Join(configDir, "snapshots") {
		t.Errorf("snapshot_path = %q, want %q", cfg.Events.SnapshotPath, filepath.Join(configDir, "snapshots"))
	}
	if cfg.Events.SnapshotQuality != 85 {
		t.Errorf("snapshot_quality = %d, want 85", cfg.Events.SnapshotQuality)
	}

	// Storage defaults
	if cfg.Storage.DBPath != filepath.Join(configDir, "vedetta.db") {
		t.Errorf("db_path = %q, want %q", cfg.Storage.DBPath, filepath.Join(configDir, "vedetta.db"))
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
			name:   "no auth users",
			yaml:   "cameras:\n  - name: front\n    url: rtsp://localhost/stream\nauth:\n  users: []\n",
			errMsg: "at least one auth user",
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

func TestLoadZeroCamerasValid(t *testing.T) {
	path := writeConfig(t, `
detect:
  score_threshold: 0.5
`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() should accept zero cameras, got error: %v", err)
	}
	if len(cfg.Cameras) != 0 {
		t.Errorf("expected 0 cameras, got %d", len(cfg.Cameras))
	}
}

func TestLoadOrDefaultMissingFile(t *testing.T) {
	cfg, setupMode, err := LoadOrDefault("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("LoadOrDefault() error: %v", err)
	}
	if !setupMode {
		t.Error("expected setupMode=true for missing file")
	}
	if cfg.API.Port != 5050 {
		t.Errorf("default API port = %d, want 5050", cfg.API.Port)
	}
	if cfg.Detect.ScoreThreshold != 0.65 {
		t.Errorf("default score threshold = %f, want 0.65", cfg.Detect.ScoreThreshold)
	}
}

func TestLoadOrDefaultValidFile(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: front
    url: rtsp://192.168.1.10/stream
`)

	cfg, setupMode, err := LoadOrDefault(path)
	if err != nil {
		t.Fatalf("LoadOrDefault() error: %v", err)
	}
	if setupMode {
		t.Error("expected setupMode=false for existing valid file")
	}
	if len(cfg.Cameras) != 1 {
		t.Fatalf("expected 1 camera, got %d", len(cfg.Cameras))
	}
	if cfg.Cameras[0].Name != "front" {
		t.Errorf("camera name = %q, want %q", cfg.Cameras[0].Name, "front")
	}
}

func TestLoadOrDefaultInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("cameras:\n  - name: [invalid"), 0644); err != nil {
		t.Fatalf("failed to write config: %v", err)
	}

	_, setupMode, err := LoadOrDefault(path)
	if err == nil {
		t.Fatal("LoadOrDefault() should return error for invalid file")
	}
	if setupMode {
		t.Error("setupMode should be false on error")
	}
}

func TestTieredStorageDefaults(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: cam1
    url: rtsp://localhost/stream
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ts := cfg.Recording.TieredStorage
	if ts.Enabled {
		t.Error("expected tiered_storage.enabled = false by default")
	}
	if ts.AfterDays != 1 {
		t.Errorf("after_days = %d, want 1", ts.AfterDays)
	}
	if ts.TargetWidth != 1280 {
		t.Errorf("target_width = %d, want 1280", ts.TargetWidth)
	}
	if ts.TargetHeight != 720 {
		t.Errorf("target_height = %d, want 720", ts.TargetHeight)
	}
	if ts.Schedule != "02:00-05:00" {
		t.Errorf("schedule = %q, want \"02:00-05:00\"", ts.Schedule)
	}
}

func TestTieredStoragePerCameraInheritance(t *testing.T) {
	path := writeConfig(t, `
cameras:
  - name: cam_default
    url: rtsp://localhost/stream
  - name: cam_override_days
    url: rtsp://localhost/stream
    tiered_storage:
      after_days: 3
  - name: cam_disabled
    url: rtsp://localhost/stream
    tiered_storage:
      enabled: false
recording:
  tiered_storage:
    enabled: true
    after_days: 1
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	global := cfg.Recording.TieredStorage

	def := cfg.Cameras[0].EffectiveTieredStorage(global)
	if !def.Enabled || def.AfterDays != 1 {
		t.Errorf("cam_default: got enabled=%v after_days=%d, want true/1", def.Enabled, def.AfterDays)
	}

	over := cfg.Cameras[1].EffectiveTieredStorage(global)
	if !over.Enabled || over.AfterDays != 3 {
		t.Errorf("cam_override_days: got enabled=%v after_days=%d, want true/3", over.Enabled, over.AfterDays)
	}

	dis := cfg.Cameras[2].EffectiveTieredStorage(global)
	if dis.Enabled {
		t.Error("cam_disabled: expected enabled=false")
	}
}

func TestTieredStorageScheduleParse(t *testing.T) {
	tests := []struct {
		schedule string
		nowHour  int
		nowMin   int
		want     bool
		wantErr  bool
	}{
		{"02:00-05:00", 3, 0, true, false},
		{"02:00-05:00", 10, 0, false, false},
		{"02:00-05:00", 2, 0, true, false},
		{"02:00-05:00", 5, 0, false, false},
		{"23:00-01:00", 23, 30, true, false},
		{"23:00-01:00", 0, 30, true, false},
		{"23:00-01:00", 12, 0, false, false},
		{"bad", 0, 0, false, true},
		{"25:00-05:00", 0, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.schedule+"@"+fmt.Sprintf("%02d:%02d", tt.nowHour, tt.nowMin), func(t *testing.T) {
			now := time.Date(2026, 1, 1, tt.nowHour, tt.nowMin, 0, 0, time.Local)
			got, err := InScheduleWindow(tt.schedule, now)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Errorf("InScheduleWindow(%q, %02d:%02d) = %v, want %v",
					tt.schedule, tt.nowHour, tt.nowMin, got, tt.want)
			}
		})
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
