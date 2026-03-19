package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/rvben/watchpost/internal/api"
	"github.com/rvben/watchpost/internal/camera"
	"github.com/rvben/watchpost/internal/config"
	"github.com/rvben/watchpost/internal/detect"
	"github.com/rvben/watchpost/internal/mqtt"
	"github.com/rvben/watchpost/internal/recording"
	"github.com/rvben/watchpost/internal/storage"
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
	defer db.Close()

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

	hwaccel := camera.DetectHWAccel()
	if hwaccel != nil {
		slog.Info("hardware acceleration detected", "backend", hwaccel.Name)
	} else {
		slog.Info("no hardware acceleration available, using CPU decoding")
	}

	recorder := recording.New(cfg.Recording, db)

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

	events := make(chan camera.Event, 100)

	manager := camera.NewManager(cfg.Cameras, detector, events, hwaccel)
	manager.Start(ctx)

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

	server := api.New(cfg.API, db, manager)
	go func() {
		if err := server.Start(); err != nil {
			slog.Error("API server failed", "error", err)
			cancel()
		}
	}()

	slog.Info("watchpost started", "cameras", len(cfg.Cameras))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")
	cancel()
}
