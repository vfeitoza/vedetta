# Watchpost Roadmap

## Phase 1: Core Excellence (MVP that's actually usable)

### 1.1 Object Tracker
- [ ] IoU-based frame-to-frame object tracking
- [ ] Single event per tracked object (not per detection)
- [ ] Track lifecycle: entered → active → stationary → left
- [ ] Configurable tracking parameters (max_disappeared, min_hits)

### 1.2 Smart Motion Detection
- [ ] Contour-based motion (not just pixel diff average)
- [ ] Minimum contour area threshold (ignore small changes)
- [ ] Motion masks (configurable regions to ignore)
- [ ] Motion region extraction (only detect in the moving area)

### 1.3 Hardware-Accelerated Decoding
- [ ] macOS: `-hwaccel videotoolbox`
- [ ] Linux NVIDIA: `-hwaccel cuda`
- [ ] Linux Intel: `-hwaccel vaapi`
- [ ] Auto-detection of available hardware
- [ ] Graceful fallback to CPU

### 1.4 Event System
- [ ] Per-object cooldown timers (don't spam for parked cars)
- [ ] Zone-aware events (only trigger for configured zones)
- [ ] Event consolidation (merge close events for same object)
- [ ] Snapshot with bounding box overlay saved per event
- [ ] Event lifecycle: started → updated → ended

### 1.5 Segment Persistence
- [ ] Store segment index in SQLite (survive restarts)
- [ ] Scan existing segments on startup
- [ ] Atomic segment completion tracking

### 1.6 Snapshot System
- [ ] Save JPEG snapshot per event with bounding boxes drawn
- [ ] Configurable quality and resolution
- [ ] Latest snapshot per camera always available
- [ ] Clean snapshot API endpoint

## Phase 2: User Experience

### 2.1 Web UI
- [ ] Dashboard with camera grid (live snapshots)
- [ ] Event browser with thumbnail timeline
- [ ] Event detail view (snapshot + clip playback)
- [ ] Camera configuration editor
- [ ] System health/stats dashboard
- [ ] Mobile-responsive design

### 2.2 Live Streaming
- [ ] WebRTC for low-latency live view
- [ ] HLS/LLHLS fallback for broader compatibility
- [ ] MSE (Media Source Extensions) for in-browser playback
- [ ] Multi-camera composite view (birdseye)

### 2.3 Camera Auto-Discovery
- [ ] ONVIF WS-Discovery probe
- [ ] Get stream URLs from ONVIF profiles
- [ ] Suggest camera config from discovered devices
- [ ] `watchpost discover` CLI command
- [ ] Support Tapo, Reolink, Hikvision, Dahua ONVIF

### 2.4 Configuration
- [ ] YAML validation with helpful error messages
- [ ] Hot-reload on config file change (fsnotify)
- [ ] `watchpost validate` CLI command
- [ ] `watchpost init` interactive setup wizard

## Phase 3: Integration & Polish

### 3.1 Home Assistant
- [ ] MQTT auto-discovery for HA
- [ ] HA-compatible event topics
- [ ] Camera entity via MQTT
- [ ] Sensor entities (person count, last motion, etc.)

### 3.2 Notifications
- [ ] Webhook support (generic HTTP POST)
- [ ] Pushover integration
- [ ] Telegram bot integration
- [ ] Snapshot attachment in notifications

### 3.3 Monitoring
- [ ] Prometheus metrics endpoint `/metrics`
- [ ] Per-camera stats (FPS, CPU, detection rate)
- [ ] System resource usage tracking
- [ ] Health check endpoint with detailed status

### 3.4 Distribution
- [ ] Homebrew formula (`brew install watchpost`)
- [ ] APT/RPM packages
- [ ] Docker image (optional, for those who want it)
- [ ] GitHub Releases with cross-compiled binaries
- [ ] systemd service file
- [ ] launchd plist for macOS

## Phase 4: Advanced Features

### 4.1 Multi-Model Support
- [ ] Custom ONNX model loading
- [ ] Per-camera model assignment
- [ ] Model benchmarking command
- [ ] Pre-trained model downloader

### 4.2 Audio Detection
- [ ] Audio stream capture from RTSP
- [ ] Audio event detection (glass break, dog bark, etc.)
- [ ] Audio events linked to camera events

### 4.3 Advanced Recording
- [ ] Timeline scrubbing with thumbnails
- [ ] Export clips with date range
- [ ] Timelapse generation from segments
- [ ] Cloud backup (S3-compatible)

### 4.4 Face/License Plate Recognition
- [ ] Sub-labels for face recognition
- [ ] License plate detection + OCR
- [ ] Known faces/plates database
- [ ] Alert on unknown faces

## Non-Goals (Conscious Design Decisions)
- No re-encoding in the recording pipeline (always re-mux)
- No built-in PTZ control (use camera's native app)
- No cloud dependency (runs fully offline)
- No custom ML training UI (bring your own ONNX model)
