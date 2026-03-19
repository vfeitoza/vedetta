# Watchpost Architecture

## Overview

Watchpost is a native, cross-platform NVR (Network Video Recorder) with real-time AI
object detection. Written in Go for single-binary distribution.

## Design Principles

1. **Zero-config sensible defaults** — works out of the box with minimal YAML
2. **Single binary** — no Docker, no Python, no containers
3. **Native acceleration** — CoreML on Mac, CUDA on Linux, CPU fallback
4. **Efficient by design** — motion gates detection, re-mux instead of re-encode
5. **Camera-friendly** — separate detect/record streams, ONVIF discovery

## Pipeline Architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  RTSP Stream │────▶│  Frame       │────▶│  Motion      │
│  (ffmpeg)    │     │  Decoder     │     │  Detector    │
│  Low-res     │     │  RGB24→RGBA  │     │  Contour     │
└─────────────┘     └──────────────┘     └──────┬───────┘
                                                │ motion region
                                                ▼
┌─────────────┐     ┌──────────────┐     ┌──────────────┐
│  RTSP Stream │     │  Object      │◀────│  Region      │
│  (ffmpeg)    │     │  Detector    │     │  Cropper     │
│  High-res    │     │  ONNX/YOLO   │     └──────────────┘
└──────┬──────┘     └──────┬───────┘
       │                   │ detections
       ▼                   ▼
┌──────────────┐    ┌──────────────┐     ┌──────────────┐
│  Segment     │    │  Object      │────▶│  Event       │
│  Recorder    │    │  Tracker     │     │  Manager     │
│  10min .mp4  │    │  IoU-based   │     │  Dedup/Cool  │
└──────┬──────┘    └──────────────┘     └──────┬───────┘
       │                                       │
       ▼                                       ▼
┌──────────────┐    ┌──────────────┐    ┌──────────────┐
│  Clip        │    │  Snapshot    │    │  Notify      │
│  Extractor   │    │  + BBox      │    │  MQTT/Hook   │
│  Pre+Post    │    │  Overlay     │    │              │
└──────────────┘    └──────────────┘    └──────────────┘
       │                   │                   │
       ▼                   ▼                   ▼
┌─────────────────────────────────────────────────────┐
│                    SQLite (WAL)                      │
│  events │ segments │ cameras │ recordings            │
└─────────────────────────────────────────────────────┘
       │
       ▼
┌─────────────────────────────────────────────────────┐
│                    HTTP API + WebUI                   │
│  REST │ WebRTC live │ HLS │ Event browser │ Config   │
└─────────────────────────────────────────────────────┘
```

## Key Differences from Frigate

| Aspect | Frigate | Watchpost |
|--------|---------|-----------|
| Distribution | Docker-only | Single binary |
| Language | Python glue + C++ ML | Go + ONNX Runtime |
| macOS support | None | Native with CoreML |
| Camera discovery | Manual config | ONVIF auto-discovery |
| Live streaming | go2rtc sidecar | Embedded WebRTC |
| Config | Static YAML, restart needed | Hot-reload, validation |
| Hardware accel | Manual per-platform | Auto-detected |
| First-run | Complex YAML required | CLI wizard + discovery |

## Directory Structure

```
cmd/watchpost/          Entry point
internal/
├── api/                HTTP API + WebSocket
├── camera/             RTSP stream management
│   ├── camera.go       Single camera lifecycle
│   ├── manager.go      Multi-camera orchestration
│   └── onvif.go        ONVIF discovery
├── config/             YAML config + validation + hot-reload
├── detect/
│   ├── detector.go     ONNX Runtime session management
│   ├── motion.go       Contour-based motion detection
│   ├── tracker.go      IoU object tracker
│   ├── yolo.go         YOLOv8 pre/post processing
│   └── labels.go       COCO-80 class labels
├── event/              Event dedup, cooldown, lifecycle
├── mqtt/               MQTT publishing
├── recording/
│   ├── segment.go      Continuous segment recorder
│   ├── clip.go         Event clip extraction
│   └── retention.go    Storage cleanup
├── snapshot/           JPEG snapshots with bbox overlay
├── stream/             WebRTC / HLS live streaming
└── storage/            SQLite persistence
```

## Hardware Acceleration Matrix

| Platform | Video Decode | ML Inference |
|----------|-------------|--------------|
| macOS ARM | VideoToolbox | CoreML |
| macOS x86 | VideoToolbox | CPU |
| Linux NVIDIA | NVDEC | CUDA/TensorRT |
| Linux Intel | VAAPI | OpenVINO (future) |
| Linux ARM | V4L2 | CPU |
| Generic | CPU | CPU |
