package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/stream"
)

// runStreams prints the consumable stream inventory derived from the config
// file, without starting a server. It mirrors GET /api/streaming/capabilities
// so agents can discover how to consume cameras offline.
func runStreams() {
	fs := flag.NewFlagSet("streams", flag.ExitOnError)
	configPath := fs.String("config", "config.yml", "path to configuration file")
	host := fs.String("host", "", "host clients dial for RTSP (default: api.host, or <vedetta-host>)")
	jsonOut := fs.Bool("json", false, "output JSON instead of a table")

	if err := fs.Parse(os.Args[2:]); err != nil {
		slog.Error("failed to parse flags", "error", err)
		os.Exit(1)
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "path", *configPath, "error", err)
		os.Exit(1)
	}

	rtspHost := *host
	if rtspHost == "" {
		rtspHost = cfg.API.Host
		if rtspHost == "" || rtspHost == "0.0.0.0" {
			rtspHost = "<vedetta-host>"
		}
	}

	sets := stream.CameraStreamCapabilities(cfg.Cameras, cfg.RTSPServer, rtspHost)

	if *jsonOut {
		out := map[string]any{
			"auth_required": len(cfg.Auth.Users) > 0,
			"rtsp_server": map[string]any{
				"enabled": cfg.RTSPServer.Enabled,
				"port":    cfg.RTSPServer.Port,
			},
			"cameras": sets,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			slog.Error("failed to encode JSON", "error", err)
			os.Exit(1)
		}
		return
	}

	fmt.Printf("RTSP republish server: ")
	if cfg.RTSPServer.Enabled {
		fmt.Printf("enabled (port %d)\n", cfg.RTSPServer.Port)
	} else {
		fmt.Printf("disabled\n")
	}
	fmt.Printf("Auth required: %t\n\n", len(cfg.Auth.Users) > 0)

	if len(sets) == 0 {
		fmt.Println("No enabled cameras configured.")
		return
	}

	for _, s := range sets {
		fmt.Printf("%s\n", s.Name)
		printStream("rtsp (main)", s.Streams.RTSPMain)
		printStream("rtsp (sub)", s.Streams.RTSPSub)
		printStream("webrtc", s.Streams.WebRTC)
		printStream("hls", s.Streams.HLS)
		printStream("mjpeg", s.Streams.MJPEG)
		printStream("mse", s.Streams.MSE)
		printStream("snapshot", s.Streams.Snapshot)
		fmt.Println()
	}
}

func printStream(label, url string) {
	if url == "" {
		return
	}
	fmt.Printf("  %-12s %s\n", label, url)
}
