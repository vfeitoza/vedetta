# Vedetta

Vedetta is an open-source Network Video Recorder (NVR) written in Go. Inspired by [Frigate](https://frigate.video), it ships as a single binary with no Python dependency.

## Features

- **Object detection** -- YOLOv8 via ONNX Runtime (pure-Go or C backend)
- **Continuous recording** -- segment-based with configurable retention
- **Event clips** -- pre/post capture around detected objects
- **Motion detection** -- contour-based; YOLO runs only when motion is detected
- **Object tracking** -- Hungarian algorithm across frames
- **Live streaming** -- WebRTC with MJPEG fallback
- **Web dashboard** -- dark theme, htmx + vanilla JS, no build step
- **Session auth + API tokens** -- browser sessions with CSRF protection and scoped bearer tokens
- **Home Assistant** -- MQTT integration with auto-discovery
- **ONVIF discovery** -- find cameras on the network (`vedetta discover`)
- **Per-camera zones** -- filter which objects matter in each zone
- **Storage management** -- max storage cap, automatic cleanup
- **SQLite** -- WAL-mode database, embedded in the binary
- **Single binary** -- static files embedded with `go:embed`

## Quick Start

### Binary

Download the latest release from [Releases](https://github.com/rvben/vedetta/releases), or build from source:

```sh
make build
./build/vedetta
```

### Docker

```sh
docker run -d \
  --name vedetta \
  --network host \
  -v vedetta-config:/config \
  -v vedetta-data:/data \
  ghcr.io/rvben/vedetta:latest
```

A `docker-compose.yml` is included in the repository.

Host networking is required for RTSP camera access and ONVIF multicast discovery. On first run without a config file, Vedetta starts a setup wizard at `http://<host>:5050`.

## Configuration

Vedetta is configured with a single YAML file. See [`config.example.yml`](config.example.yml) for a complete example.

### Cameras

```yaml
cameras:
  - name: front_door
    url: rtsp://user:pass@192.168.1.100:554/stream1
    record_url: rtsp://user:pass@192.168.1.100:554/stream0  # optional high-res stream
    detect:
      enabled: true
      width: 640
      height: 480
      fps: 5
    record:
      width: 1920
      height: 1080
      fps: 15
    zones:
      - name: driveway
        points:
          - [0.1, 0.5]
          - [0.9, 0.5]
          - [0.9, 1.0]
          - [0.1, 1.0]
        labels: [person, car]
```

Each camera has two optional streams: `url` for the detection stream (lower resolution, less CPU) and `record_url` for recording (full resolution). If `record_url` is omitted, `url` is used for both.

Zones are polygons defined as normalized points (0.0--1.0). Event matching uses the detection anchor point `(center_x, bottom_y)`.

### Detection

```yaml
detect:
  model_path: ""            # path to YOLOv8 ONNX model
  score_threshold: 0.5      # minimum confidence score
  motion:
    pixel_threshold: 25
    min_area: 200
    background_alpha: 0.05
    min_region_score: 0.02
```

### Recording

```yaml
recording:
  path: ./recordings
  continuous: true           # record continuously, not just events
  segment_length: 10m        # length of each continuous segment
  pre_capture: 5s            # seconds before event to include in clip
  post_capture: 10s          # seconds after event to include in clip
  retain_days: 7             # delete continuous segments after N days
  event_retain_days: 30      # keep event clips longer
```

### Storage

```yaml
storage:
  db_path: ./vedetta.db
```

### MQTT

```yaml
mqtt:
  enabled: false
  host: 127.0.0.1
  port: 1883
  topic: vedetta
```

### API

```yaml
api:
  host: 0.0.0.0
  port: 5050
  exposure: lan
  # trusted_proxies:
  #   - 127.0.0.1/32
  # tls_cert: /etc/vedetta/tls.crt
  # tls_key: /etc/vedetta/tls.key
```

### Auth

```yaml
auth:
  users:
    - username: admin
      password_hash: "$2a$10$7EqJtq98hPqEX7fNZaFWoOHi8V6I5WJFlQ7Y7S6d6n9zQ0jD4S3yu"
```

Generate hashes with `vedetta auth hash-password <password>`.

## Camera Setup

Common RTSP URL formats for popular camera brands:

| Brand | Main Stream | Sub Stream |
|-------|------------|------------|
| **Tapo** | `rtsp://user:pass@IP:554/stream1` | `rtsp://user:pass@IP:554/stream2` |
| **Reolink** | `rtsp://user:pass@IP:554/h264Preview_01_main` | `rtsp://user:pass@IP:554/h264Preview_01_sub` |
| **Hikvision** | `rtsp://user:pass@IP:554/Streaming/Channels/101` | `rtsp://user:pass@IP:554/Streaming/Channels/102` |
| **Dahua** | `rtsp://user:pass@IP:554/cam/realmonitor?channel=1&subtype=0` | `rtsp://user:pass@IP:554/cam/realmonitor?channel=1&subtype=1` |

Use `vedetta discover` to scan the local network for ONVIF-compatible cameras and print their RTSP URLs.

## MQTT / Home Assistant

When MQTT is enabled, Vedetta publishes Home Assistant auto-discovery messages so cameras and sensors appear automatically.

The default topic prefix is `vedetta`. Messages are published under:

- `vedetta/<camera>/detection` -- object detection events
- `homeassistant/binary_sensor/vedetta_<camera>/config` -- auto-discovery

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/api/health` | Health check |
| `GET` | `/api/health/live` | Liveness probe |
| `GET` | `/api/health/ready` | Readiness probe |
| `POST` | `/api/auth/login` | Create browser session |
| `POST` | `/api/auth/logout` | End browser session |
| `GET` | `/api/auth/me` | Current principal |
| `POST` | `/api/tokens` | Create scoped API token |
| `DELETE` | `/api/tokens/{id}` | Revoke API token |
| `GET` | `/metrics` | Prometheus metrics |
| `GET` | `/api/system` | System status (CPU, memory, storage) |
| `GET` | `/api/cameras` | List all cameras and their status |
| `GET` | `/api/cameras/{name}/snapshot` | Current JPEG snapshot from camera |
| `GET` | `/api/cameras/{name}/mjpeg` | MJPEG live stream |
| `POST` | `/api/cameras/{name}/webrtc/offer` | WebRTC signaling (SDP offer/answer) |
| `GET` | `/api/events` | List recorded events |
| `GET` | `/api/events/{id}` | Get single event details |
| `GET` | `/api/events/{id}/snapshot` | Event thumbnail |
| `GET` | `/api/events/{id}/clip` | Download event video clip |

The web dashboard is served at `/` and uses htmx partials for dynamic updates.

## Monitoring

`/metrics` serves Prometheus metrics: HTTP request rate and latency by status
class, per-camera detection and decode latency, frames processed/dropped,
camera reconnect counts, and storage/disk usage. Like the rest of the API the
endpoint requires authentication, so its labels (camera names, online state,
activity counts) are never readable anonymously.

Scrape it with a least-privilege bearer token. Create one scoped to
`metrics:read` via `POST /api/tokens`. A token principal can only grant scopes
it already holds, so mint the scrape token from an admin browser session or from
a token that carries the `*` scope. The `metrics:read` scope can read `/metrics`
and nothing else, so a leaked scrape credential cannot pull snapshots, events,
or the people/faces database:

```sh
curl -X POST https://vedetta-host/api/tokens \
  -H "Authorization: Bearer <token with the * scope>" \
  -H "Content-Type: application/json" \
  -d '{"name": "prometheus", "scopes": ["metrics:read"]}'
```

The response returns the raw token once; use it as the scrape credential:

```yaml
# prometheus.yml
scrape_configs:
  - job_name: vedetta
    scheme: https
    authorization:
      type: Bearer
      credentials: "<metrics:read token>"
    static_configs:
      - targets: ["vedetta-host:5050"]
```

## Development

Prerequisites: Go 1.22+. Vedetta no longer downloads the ONNX model or OpenH264 at runtime; install them ahead of time or bundle them with your deployment.

```sh
make build          # build the binary
make build-capi     # build with C ONNX Runtime backend
make test           # run tests
make bench          # run detection benchmarks
make lint           # run golangci-lint
make fmt            # format code
make check          # lint + test
make clean          # remove build artifacts
```

## Architecture

```
RTSP Camera
    |
    v
 Native Go RTP/H264 decode ──> Motion Detector
                                   |
                              (motion detected)
                                   |
                                   v
                             YOLOv8 Detector ──> Object Tracker
                                   |                  |
                                   v                  v
                             Event Manager       MQTT Publisher
                                   |
                             +-----+-----+
                             |           |
                       Event Clips   Continuous Segments
```

Frames flow from camera through motion detection. YOLO only runs when motion is detected, keeping CPU usage low. Detected objects are tracked across frames with the Hungarian algorithm to maintain identity. Events trigger clip extraction from the continuous recording buffer.
