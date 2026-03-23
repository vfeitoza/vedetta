package config

import (
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cameras    []CameraConfig   `yaml:"cameras"`
	Detect     DetectConfig     `yaml:"detect"`
	Recording  RecordingConfig  `yaml:"recording"`
	Events     EventConfig      `yaml:"events"`
	Storage    StorageConfig    `yaml:"storage"`
	MQTT       MQTTConfig       `yaml:"mqtt"`
	API        APIConfig        `yaml:"api"`
	RTSPServer RTSPServerConfig `yaml:"rtsp_server"`
	Auth       AuthConfig       `yaml:"auth"`
}

type CameraConfig struct {
	Name      string             `yaml:"name"`
	URL       string             `yaml:"url"`
	RecordURL string             `yaml:"record_url"` // Separate high-res stream for recording (optional, defaults to URL)
	Detect    DetectStreamConfig `yaml:"detect"`
	Record    StreamConfig       `yaml:"record"`
	Zones     []Zone             `yaml:"zones"`
	Enabled   *bool              `yaml:"enabled"`
	Doorbell  DoorbellConfig     `yaml:"doorbell"`
}

type DoorbellConfig struct {
	Enabled    bool   `yaml:"enabled"`
	WebhookURL string `yaml:"webhook_url"` // external webhook to call on press (optional)
}

func (c CameraConfig) IsEnabled() bool {
	return c.Enabled == nil || *c.Enabled
}

func (c CameraConfig) DetectEnabled() bool {
	return c.Detect.Enabled == nil || *c.Detect.Enabled
}

type StreamConfig struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
	FPS    int `yaml:"fps"`
}

type DetectStreamConfig struct {
	Width   int   `yaml:"width"`
	Height  int   `yaml:"height"`
	FPS     int   `yaml:"fps"`
	Enabled *bool `yaml:"enabled"`
}

type Zone struct {
	Name            string      `yaml:"name"`
	Points          [][]float64 `yaml:"points"`
	Labels          []string    `yaml:"labels"`
	TrackPresence   bool        `yaml:"track_presence"`
	FaceRecognition bool        `yaml:"face_recognition"`
}

type DetectConfig struct {
	ModelPath            string       `yaml:"model_path"`
	Backend              string       `yaml:"backend"` // "auto" (default), "go", or "onnxruntime_c"
	ScoreThreshold       float32      `yaml:"score_threshold"`
	Motion               MotionConfig `yaml:"motion"`
	Labels               []string     `yaml:"labels"`                 // Only emit events for these labels; empty = all
	ObjectMatchThreshold float64      `yaml:"object_match_threshold"` // Cosine similarity threshold for object re-ID (0.0-1.0)
}

type MotionConfig struct {
	PixelThreshold  uint8   `yaml:"pixel_threshold"`
	MinArea         int     `yaml:"min_area"`
	BackgroundAlpha float64 `yaml:"background_alpha"`
	MinRegionScore  float64 `yaml:"min_region_score"`
}

type RecordingConfig struct {
	Path             string        `yaml:"path"`
	PreCapture       time.Duration `yaml:"pre_capture"`
	PostCapture      time.Duration `yaml:"post_capture"`
	MaxEventDuration time.Duration `yaml:"max_event_duration"` // Cap on dynamic event clip length (default 2m)
	RetainDays       int           `yaml:"retain_days"`
	EventRetain      int           `yaml:"event_retain_days"` // Keep event clips longer than continuous
	SegmentLength    time.Duration `yaml:"segment_length"`
	Continuous       bool          `yaml:"continuous"`  // Record continuously, not just events
	MaxStorage       string        `yaml:"max_storage"` // Human-readable max storage (e.g. "10GB", "500MB"); 0 or empty = unlimited
	maxStorageBytes  int64
}

// MaxStorageBytes returns the parsed max storage limit in bytes.
func (r *RecordingConfig) MaxStorageBytes() int64 {
	return r.maxStorageBytes
}

type EventConfig struct {
	CooldownSeconds int    `yaml:"cooldown_seconds"`
	RetainDays      int    `yaml:"retain_days"`
	SnapshotPath    string `yaml:"snapshot_path"`
	SnapshotQuality int    `yaml:"snapshot_quality"`
}

type StorageConfig struct {
	DBPath string `yaml:"db_path"`
}

type MQTTConfig struct {
	Enabled  bool   `yaml:"enabled"`
	Host     string `yaml:"host"`
	Port     int    `yaml:"port"`
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	Topic    string `yaml:"topic"`
}

type APIConfig struct {
	Host           string   `yaml:"host"`
	Port           int      `yaml:"port"`
	Exposure       string   `yaml:"exposure"`
	TrustedProxies []string `yaml:"trusted_proxies"`
	TLSCert        string   `yaml:"tls_cert"` // path to TLS certificate file (enables HTTPS)
	TLSKey         string   `yaml:"tls_key"`  // path to TLS private key file
}

type RTSPServerConfig struct {
	Enabled bool `yaml:"enabled"`
	Port    int  `yaml:"port"`
}

type AuthConfig struct {
	Users []AuthUser `yaml:"users"`
}

type AuthUser struct {
	Username     string `yaml:"username"`
	PasswordHash string `yaml:"password_hash"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{
		Detect: DetectConfig{
			ScoreThreshold:       0.65,
			ObjectMatchThreshold: 0.65,
			Motion: MotionConfig{
				PixelThreshold:  25,
				MinArea:         200,
				BackgroundAlpha: 0.05,
				MinRegionScore:  0.02,
			},
			Labels: []string{"person", "car", "truck", "bus", "motorcycle", "bicycle", "dog", "cat", "bird"},
		},
		Recording: RecordingConfig{
			Path:             "./recordings",
			PreCapture:       5 * time.Second,
			PostCapture:      10 * time.Second,
			MaxEventDuration: 2 * time.Minute,
			RetainDays:       7,
			EventRetain:      30,
			SegmentLength:    10 * time.Minute,
			Continuous:       true,
		},
		Events: EventConfig{
			CooldownSeconds: 30,
			RetainDays:      90,
			SnapshotPath:    "./snapshots",
			SnapshotQuality: 85,
		},
		Storage: StorageConfig{
			DBPath: "./vedetta.db",
		},
		API: APIConfig{
			Host:     "0.0.0.0",
			Port:     5050,
			Exposure: "lan",
		},
		RTSPServer: RTSPServerConfig{
			Port: 8554,
		},
	}

	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Recording.MaxStorage != "" {
		bytes, err := parseByteSize(cfg.Recording.MaxStorage)
		if err != nil {
			return nil, fmt.Errorf("recording.max_storage: %w", err)
		}
		cfg.Recording.maxStorageBytes = bytes
	}

	// Validate TLS config: both cert and key must be set together
	if (cfg.API.TLSCert != "") != (cfg.API.TLSKey != "") {
		return nil, fmt.Errorf("api: both tls_cert and tls_key must be set")
	}
	if cfg.API.Exposure != "lan" && cfg.API.Exposure != "internet" {
		return nil, fmt.Errorf("api.exposure: must be \"lan\" or \"internet\"")
	}
	if cfg.API.Exposure == "internet" && cfg.API.TLSCert == "" && len(cfg.API.TrustedProxies) == 0 {
		return nil, fmt.Errorf("api.exposure=internet requires tls_cert/tls_key or at least one trusted proxy")
	}
	for i, proxy := range cfg.API.TrustedProxies {
		if _, err := parseProxyPrefix(proxy); err != nil {
			return nil, fmt.Errorf("api.trusted_proxies[%d]: %w", i, err)
		}
	}

	if len(cfg.Cameras) == 0 {
		return nil, fmt.Errorf("at least one camera must be configured")
	}
	if len(cfg.Auth.Users) == 0 {
		return nil, fmt.Errorf("at least one auth user must be configured")
	}

	configDir := filepath.Dir(path)
	cfg.Storage.DBPath = normalizePath(configDir, cfg.Storage.DBPath)
	cfg.Recording.Path = normalizePath(configDir, cfg.Recording.Path)
	cfg.Events.SnapshotPath = normalizePath(configDir, cfg.Events.SnapshotPath)
	cfg.Detect.ModelPath = normalizePath(configDir, cfg.Detect.ModelPath)
	cfg.API.TLSCert = normalizePath(configDir, cfg.API.TLSCert)
	cfg.API.TLSKey = normalizePath(configDir, cfg.API.TLSKey)

	for i := range cfg.Cameras {
		cam := &cfg.Cameras[i]
		if cam.Name == "" {
			return nil, fmt.Errorf("camera %d: name is required", i)
		}
		if cam.URL == "" {
			return nil, fmt.Errorf("camera %q: url is required", cam.Name)
		}
		if cam.Detect.Width == 0 {
			cam.Detect.Width = 640
			cam.Detect.Height = 480
			cam.Detect.FPS = 5
		}
		if cam.Record.Width == 0 {
			cam.Record.Width = 1920
			cam.Record.Height = 1080
			cam.Record.FPS = 15
		}
		for j, z := range cam.Zones {
			if z.Name == "" {
				return nil, fmt.Errorf("camera %q: zone %d: name is required", cam.Name, j)
			}
			if len(z.Points) < 3 {
				return nil, fmt.Errorf("camera %q: zone %q: points must contain at least 3 polygon points", cam.Name, z.Name)
			}
			for _, point := range z.Points {
				if len(point) != 2 {
					return nil, fmt.Errorf("camera %q: zone %q: each point must be [x, y]", cam.Name, z.Name)
				}
				if point[0] < 0 || point[0] > 1 || point[1] < 0 || point[1] > 1 {
					return nil, fmt.Errorf("camera %q: zone %q: points must be between 0.0 and 1.0", cam.Name, z.Name)
				}
			}
		}
	}

	for i, user := range cfg.Auth.Users {
		if user.Username == "" {
			return nil, fmt.Errorf("auth.users[%d]: username is required", i)
		}
		if user.PasswordHash == "" {
			return nil, fmt.Errorf("auth.users[%d]: password_hash is required", i)
		}
	}

	return cfg, nil
}

// parseByteSize parses human-readable byte sizes like "10GB", "500MB", "1TB".
func parseByteSize(s string) (int64, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == "0" {
		return 0, nil
	}

	s = strings.ToUpper(s)

	// Check longer suffixes first to avoid "B" matching "GB", "MB", etc.
	suffixes := []struct {
		suffix string
		mult   int64
	}{
		{"TB", 1024 * 1024 * 1024 * 1024},
		{"GB", 1024 * 1024 * 1024},
		{"MB", 1024 * 1024},
		{"KB", 1024},
		{"B", 1},
	}

	for _, entry := range suffixes {
		if strings.HasSuffix(s, entry.suffix) {
			numStr := strings.TrimSpace(strings.TrimSuffix(s, entry.suffix))
			val, err := strconv.ParseFloat(numStr, 64)
			if err != nil {
				return 0, fmt.Errorf("invalid size %q: %w", s, err)
			}
			return int64(val * float64(entry.mult)), nil
		}
	}

	// Try as plain number (bytes)
	val, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid size %q: expected format like '10GB', '500MB'", s)
	}
	return val, nil
}

func normalizePath(baseDir, p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}
	return filepath.Clean(filepath.Join(baseDir, p))
}

func parseProxyPrefix(value string) (netip.Prefix, error) {
	if prefix, err := netip.ParsePrefix(value); err == nil {
		return prefix, nil
	}
	addr, err := netip.ParseAddr(value)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("invalid proxy CIDR or IP %q", value)
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}
