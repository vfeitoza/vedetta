package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Cameras   []CameraConfig  `yaml:"cameras"`
	Detect    DetectConfig    `yaml:"detect"`
	Recording RecordingConfig `yaml:"recording"`
	Events    EventConfig     `yaml:"events"`
	Storage   StorageConfig   `yaml:"storage"`
	MQTT      MQTTConfig      `yaml:"mqtt"`
	API       APIConfig       `yaml:"api"`
}

type CameraConfig struct {
	Name      string       `yaml:"name"`
	URL       string       `yaml:"url"`
	RecordURL string       `yaml:"record_url"` // Separate high-res stream for recording (optional, defaults to URL)
	Detect    StreamConfig `yaml:"detect"`
	Record    StreamConfig `yaml:"record"`
	Zones     []Zone       `yaml:"zones"`
	Enabled   bool         `yaml:"enabled"`
}

type StreamConfig struct {
	Width  int `yaml:"width"`
	Height int `yaml:"height"`
	FPS    int `yaml:"fps"`
}

type Zone struct {
	Name        string    `yaml:"name"`
	Coordinates []float64 `yaml:"coordinates"`
	Objects     []string  `yaml:"objects"`
}

type DetectConfig struct {
	ModelPath       string  `yaml:"model_path"`
	Backend         string  `yaml:"backend"`          // "auto" (default), "go", or "onnxruntime_c"
	ScoreThreshold  float32 `yaml:"score_threshold"`
	MotionThreshold float64 `yaml:"motion_threshold"`
}

type RecordingConfig struct {
	Path          string        `yaml:"path"`
	PreCapture    time.Duration `yaml:"pre_capture"`
	PostCapture   time.Duration `yaml:"post_capture"`
	RetainDays    int           `yaml:"retain_days"`
	EventRetain   int           `yaml:"event_retain_days"` // Keep event clips longer than continuous
	SegmentLength time.Duration `yaml:"segment_length"`
	Continuous    bool          `yaml:"continuous"`        // Record continuously, not just events
	MaxStorage    string        `yaml:"max_storage"`       // Human-readable max storage (e.g. "10GB", "500MB"); 0 or empty = unlimited
	maxStorageBytes int64
}

// MaxStorageBytes returns the parsed max storage limit in bytes.
func (r *RecordingConfig) MaxStorageBytes() int64 {
	return r.maxStorageBytes
}

type EventConfig struct {
	CooldownSeconds int    `yaml:"cooldown_seconds"`
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
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := &Config{
		Detect: DetectConfig{
			ScoreThreshold:  0.5,
			MotionThreshold: 0.02,
		},
		Recording: RecordingConfig{
			Path:          "./recordings",
			PreCapture:    5 * time.Second,
			PostCapture:   10 * time.Second,
			RetainDays:    7,
			EventRetain:   30,
			SegmentLength: 10 * time.Minute,
			Continuous:    true,
		},
		Events: EventConfig{
			CooldownSeconds: 30,
			SnapshotPath:    "./snapshots",
			SnapshotQuality: 85,
		},
		Storage: StorageConfig{
			DBPath: "./watchpost.db",
		},
		API: APIConfig{
			Host: "0.0.0.0",
			Port: 5050,
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

	if len(cfg.Cameras) == 0 {
		return nil, fmt.Errorf("at least one camera must be configured")
	}

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
		if !cam.Enabled {
			cam.Enabled = true
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
