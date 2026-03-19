# Camera Reference

## Discovered Cameras on Network

### Tapo Cameras (TP-Link)
| IP | MAC | RTSP Port | Model (TBD) |
|----|-----|-----------|-------------|
| 192.168.1.17 | 70:03:9f:17:c4:8a | — | Tapo (HTTP on 80) |
| 192.168.1.100 | c0:4b:24:9f:45:74 | — | Tapo |

### Reolink Cameras
| IP | MAC | RTSP Port | Model (TBD) |
|----|-----|-----------|-------------|
| 192.168.1.190 | 60:22:32:58:b8:0 | — | Reolink |
| 192.168.1.198 | d8:b3:70:dc:f0:14 | — | Reolink |
| 192.168.1.241 | 3c:39:e7:2d:5a:30 | — | Reolink |
| 192.168.1.242 | 48:0b:b2:58:52:15 | — | Reolink |

### RTSP-capable devices (d8:07:b6 prefix — possibly Tapo or TP-Link IoT)
| IP | MAC | RTSP Port | Notes |
|----|-----|-----------|-------|
| 192.168.1.97 | d8:07:b6:25:e3:c8 | 554 | RTSP active |
| 192.168.1.139 | d8:07:b6:25:dd:a9 | 554 | RTSP active |
| 192.168.1.236 | d8:07:b6:16:e4:e9 | 554 | RTSP active |

### Other RTSP devices
| IP | RTSP Port | Notes |
|----|-----------|-------|
| 192.168.1.143 | 554 | Supports OPTIONS, DESCRIBE, SETUP, TEARDOWN, PLAY |
| 192.168.1.215 | 554 | RTSP active |

## Common RTSP URLs

### Tapo Cameras
```
# Stream 1 (high quality)
rtsp://<username>:<password>@<ip>:554/stream1

# Stream 2 (low quality, good for detection)
rtsp://<username>:<password>@<ip>:554/stream2
```

### Reolink Cameras
```
# Main stream (high quality)
rtsp://<username>:<password>@<ip>:554/h264Preview_01_main

# Sub stream (low quality, good for detection)
rtsp://<username>:<password>@<ip>:554/h264Preview_01_sub

# Alternative URL format (newer firmware)
rtsp://<username>:<password>@<ip>:554//Preview_01_main
```

## Testing Configuration

To test with real cameras, create `config.yml`:

```yaml
cameras:
  - name: tapo_camera
    url: rtsp://user:pass@192.168.1.97:554/stream2
    record_url: rtsp://user:pass@192.168.1.97:554/stream1
    detect:
      width: 640
      height: 480
      fps: 5

  - name: reolink_camera
    url: rtsp://admin:pass@192.168.1.190:554/h264Preview_01_sub
    record_url: rtsp://admin:pass@192.168.1.190:554/h264Preview_01_main
    detect:
      width: 640
      height: 480
      fps: 5
```

## Camera Credentials

Credentials are NOT stored in this file. Set them via environment variables:

```bash
export TAPO_USER=...
export TAPO_PASS=...
export REOLINK_USER=...
export REOLINK_PASS=...
```

Or use the config file with actual credentials (do not commit).
