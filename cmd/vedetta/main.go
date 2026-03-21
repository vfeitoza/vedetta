package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rvben/vedetta/internal/api"
	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/mqtt"
	"github.com/rvben/vedetta/internal/recording"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/storage"
)

func main() {
	// Handle subcommands before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "discover" {
		runDiscover()
		return
	}

	configPath := flag.String("config", "config.yml", "path to configuration file")
	flag.Parse()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	db, err := storage.New(cfg.Storage.DBPath)
	if err != nil {
		slog.Error("failed to open database", "error", err)
		os.Exit(1)
	}
	defer func() { _ = db.Close() }()

	var mqttClient *mqtt.Client
	if cfg.MQTT.Enabled {
		mqttClient, err = mqtt.New(cfg.MQTT)
		if err != nil {
			slog.Error("failed to connect to MQTT", "error", err)
			os.Exit(1)
		}
		defer mqttClient.Close()
	}

	detector := detect.New(cfg.Detect)
	defer detector.Close()

	// Create RTSP Hub — central connection manager
	hub := rtsp.NewHub(ctx)
	defer hub.Close()

	slog.Info("native Go media pipeline active (no ffmpeg required)")

	recorder := recording.New(cfg.Recording, db, hub, cfg.Events.SnapshotPath)

	// Register cameras for recording
	for _, cam := range cfg.Cameras {
		if !cam.Enabled {
			continue
		}
		recordURL := cam.RecordURL
		if recordURL == "" {
			recordURL = cam.URL
		}
		recorder.RegisterCamera(cam.Name, recordURL)
	}

	// Start continuous segment recording
	recorder.StartContinuousRecording(ctx)
	recorder.StartRetentionCleanup(ctx)

	// Publish HA MQTT discovery for all enabled cameras
	if mqttClient != nil {
		var cameraNames []string
		for _, cam := range cfg.Cameras {
			if cam.Enabled {
				cameraNames = append(cameraNames, cam.Name)
			}
		}
		mqttClient.PublishDiscovery(cameraNames)
	}

	events := make(chan camera.Event, 100)

	manager := camera.NewManager(cfg.Cameras, detector, events, hub, cfg.Events.SnapshotPath, cfg.Events.SnapshotQuality)
	manager.Start(ctx)

	// Periodically publish camera online/offline status to MQTT.
	// Uses a short initial interval so cameras that connect quickly get
	// reported promptly, then switches to the normal 30s interval.
	if mqttClient != nil {
		go func() {
			publishStatuses := func() {
				for _, st := range manager.CameraStatuses() {
					mqttClient.PublishCameraStatus(st.Name, st.Online)
				}
			}

			// Publish a few times quickly at startup to catch cameras as they connect
			for range 3 {
				select {
				case <-ctx.Done():
					return
				case <-time.After(5 * time.Second):
					publishStatuses()
				}
			}

			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					publishStatuses()
				}
			}
		}()
	}

	// Process events: record clips and publish to MQTT
	go func() {
		for event := range events {
			slog.Info("event detected",
				"camera", event.CameraName,
				"label", event.Label,
				"score", fmt.Sprintf("%.2f", event.Score),
			)

			if err := db.SaveEvent(event); err != nil {
				slog.Error("failed to save event", "error", err)
			}

			if err := recorder.SaveClip(ctx, event); err != nil {
				slog.Error("failed to save clip", "error", err)
			}

			if mqttClient != nil {
				if err := mqttClient.PublishEvent(event); err != nil {
					slog.Error("failed to publish event", "error", err)
				}
			}
		}
	}()

	server := api.New(cfg.API, db, manager, recorder, hub)
	go func() {
		if err := server.Start(); err != nil {
			slog.Error("API server failed", "error", err)
			cancel()
		}
	}()

	slog.Info("vedetta started", "cameras", len(cfg.Cameras))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	cancel()

	// Wait for recording goroutines to finalize segments before closing DB
	recorder.Close()
}
