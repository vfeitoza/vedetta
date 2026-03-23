package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/rvben/vedetta/internal/api"
	"github.com/rvben/vedetta/internal/auth"
	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/detect"
	"github.com/rvben/vedetta/internal/mqtt"
	"github.com/rvben/vedetta/internal/recording"
	"github.com/rvben/vedetta/internal/rtsp"
	"github.com/rvben/vedetta/internal/snapshot"
	"github.com/rvben/vedetta/internal/storage"
	"github.com/rvben/vedetta/internal/stream"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	// Handle subcommands before flag parsing
	if len(os.Args) > 1 && os.Args[1] == "discover" {
		runDiscover()
		return
	}

	if len(os.Args) > 2 && os.Args[1] == "auth" && os.Args[2] == "hash-password" {
		runHashPassword(os.Args[3:])
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

	if err := auth.ValidateConfig(cfg.Auth); err != nil {
		slog.Error("invalid auth config", "error", err)
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

	// Reconcile event media availability with the filesystem without deleting metadata.
	go reconcileEventMediaAvailability(db)

	authChecker := auth.New(cfg.Auth, cfg.API, db)
	defer authChecker.Close()

	// Start API server early so the UI is available during initialization
	server := api.New(cfg.API, authChecker, db)
	go func() {
		if err := server.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("API server failed", "error", err)
			cancel()
		}
	}()

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

	var faceRecognizer *detect.FaceRecognizer
	fr, frErr := detect.NewFaceRecognizer(detect.FaceRecognizerConfig{
		CropDir: filepath.Join(cfg.Events.SnapshotPath, "faces"),
	})
	if frErr != nil {
		slog.Warn("face recognition disabled", "error", frErr)
	} else {
		faceRecognizer = fr
		defer fr.Close()
		slog.Info("face recognition enabled")
	}

	objectEmbedder, oeErr := detect.NewObjectEmbedder(detect.ObjectEmbedderConfig{})
	if oeErr != nil {
		slog.Warn("object re-identification disabled", "error", oeErr)
	} else {
		slog.Info("object re-identification enabled")
	}

	// Create RTSP Hub — central connection manager
	hub := rtsp.NewHub(ctx)
	defer hub.Close()

	slog.Info("native Go media pipeline active (no ffmpeg required)")

	recorder := recording.New(cfg.Recording, cfg.Events, db, hub, cfg.Events.SnapshotPath)

	// Register cameras for recording
	for _, cam := range cfg.Cameras {
		if !cam.IsEnabled() {
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
	recorder.StartStatsRefresh(ctx)

	// Publish HA MQTT discovery for all enabled cameras
	if mqttClient != nil {
		var cameraNames []string
		for _, cam := range cfg.Cameras {
			if cam.IsEnabled() {
				cameraNames = append(cameraNames, cam.Name)
			}
		}
		mqttClient.PublishDiscovery(cameraNames)

		// Publish discovery for tracked objects
		if knownObjects, err := db.ListKnownObjects(); err == nil {
			var objInfos []mqtt.ObjectInfo
			for _, obj := range knownObjects {
				objInfos = append(objInfos, mqtt.ObjectInfo{Name: obj.Name, Label: obj.Label})
			}
			mqttClient.PublishObjectDiscovery(objInfos)
		}
	}

	events := make(chan camera.Event, 100)
	eventEnds := make(chan camera.EventEnd, 100)
	presenceEvents := make(chan camera.PresenceEvent, 100)
	faceEvents := make(chan camera.FaceEvent, 100)

	manager := camera.NewManager(cfg.Cameras, detector, cfg.Detect.Motion, events, eventEnds, presenceEvents, hub, cfg.Events.SnapshotPath, cfg.Events.SnapshotQuality, cfg.Recording.Path, faceRecognizer, faceEvents, filepath.Join(cfg.Events.SnapshotPath, "faces"))

	// Sync zones from config to DB and load them into cameras
	syncConfigZones(db, cfg.Cameras, manager)

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

	// Event lifecycle manager: tracks active events and schedules clip extraction
	// when the tracked object leaves the frame or max duration is reached.
	go func() {
		type activeEvent struct {
			event      camera.Event
			timer      *time.Timer
			tempCancel context.CancelFunc // for non-continuous temporary recording
		}
		active := make(map[string]*activeEvent) // eventID → state
		cooldowns := make(map[string]time.Time)
		maxDur := cfg.Recording.MaxEventDuration
		timeouts := make(chan string, 100) // eventIDs that hit max duration

		finalizeEvent := func(ae *activeEvent, endTime time.Time) {
			ae.timer.Stop()
			ev := ae.event
			ev.EndTime = endTime
			duration := endTime.Sub(ev.Timestamp)

			if err := db.UpdateEventEndTime(ev.ID, endTime); err != nil {
				slog.Error("failed to update event end time", "event", ev.ID, "error", err)
			}
			slog.Info("event ended",
				"event", ev.ID,
				"camera", ev.CameraName,
				"label", ev.Label,
				"duration", duration.Round(time.Second),
			)
			cooldowns[cooldownKey(ev)] = endTime

			if ae.tempCancel != nil {
				tc := ae.tempCancel
				go func() {
					select {
					case <-time.After(cfg.Recording.PostCapture + 5*time.Second):
					case <-ctx.Done():
					}
					tc()
				}()
			}

			// Schedule clip extraction after post-capture + segment finalization buffer
			go func() {
				delay := cfg.Recording.PostCapture + 15*time.Second
				select {
				case <-time.After(delay):
				case <-ctx.Done():
					return
				}
				for attempt := range 5 {
					err := recorder.SaveClip(ctx, ev)
					if err == nil {
						return
					}
					if attempt < 4 {
						slog.Debug("clip not ready, retrying", "event", ev.ID, "attempt", attempt+1)
						select {
						case <-time.After(time.Duration(attempt+1) * 30 * time.Second):
						case <-ctx.Done():
							return
						}
					} else {
						slog.Error("failed to save clip after retries", "event", ev.ID, "error", err)
					}
				}
			}()
		}

		for {
			select {
			case <-ctx.Done():
				for id, ae := range active {
					ae.timer.Stop()
					if ae.tempCancel != nil {
						ae.tempCancel()
					}
					delete(active, id)
				}
				return

			case event := <-events:
				if until, ok := cooldowns[cooldownKey(event)]; ok && time.Since(until) < time.Duration(cfg.Events.CooldownSeconds)*time.Second {
					slog.Info("event suppressed by cooldown",
						"camera", event.CameraName,
						"label", event.Label,
						"zone", event.ZoneName,
					)
					continue
				}
				slog.Info("event detected",
					"camera", event.CameraName,
					"label", event.Label,
					"score", fmt.Sprintf("%.2f", event.Score),
				)

				if err := db.SaveEvent(event); err != nil {
					slog.Error("failed to save event", "error", err)
				} else if event.SnapshotImage != nil && event.SnapshotPath != "" {
					if err := snapshot.SaveSnapshot(event.SnapshotImage, event.SnapshotPath, cfg.Events.SnapshotQuality); err != nil {
						slog.Error("failed to save event snapshot", "event", event.ID, "error", err)
					} else if err := db.UpdateEventSnapshotPath(event.ID, event.SnapshotPath); err != nil {
						slog.Error("failed to persist event snapshot path", "event", event.ID, "error", err)
					}
				}

				var matchedObjects []string
				if objectEmbedder != nil && event.SnapshotImage != nil {
					matchedObjects = matchEventToKnownObjects(db, objectEmbedder, event)
				}

				if mqttClient != nil {
					if err := mqttClient.PublishEvent(event, matchedObjects); err != nil {
						slog.Error("failed to publish event", "error", err)
					}
					for _, name := range matchedObjects {
						mqttClient.PublishObjectSighting(name, event)
					}
				}

				// Start temporary recording if continuous is off
				var tempCancel context.CancelFunc
				if !cfg.Recording.Continuous {
					if url := recorder.CameraURL(event.CameraName); url != "" {
						tempCtx, cancel := context.WithCancel(ctx)
						tempCancel = cancel
						recorder.StartTemporaryRecording(tempCtx, event.CameraName, url)
					}
				}

				// Max duration timer sends to timeouts channel (avoids data race)
				evID := event.ID
				timer := time.AfterFunc(maxDur, func() {
					select {
					case timeouts <- evID:
					default:
					}
				})

				active[evID] = &activeEvent{
					event:      event,
					timer:      timer,
					tempCancel: tempCancel,
				}

			case end := <-eventEnds:
				if ae, ok := active[end.EventID]; ok {
					finalizeEvent(ae, end.EndTime)
					delete(active, end.EventID)
				}

			case evID := <-timeouts:
				if ae, ok := active[evID]; ok {
					endTime := ae.event.Timestamp.Add(maxDur)
					finalizeEvent(ae, endTime)
					delete(active, evID)
				}

			case pe := <-presenceEvents:
				if err := db.UpdateZonePresence(pe.ZoneID, pe.Label, pe.Type == "zone_enter"); err != nil {
					slog.Error("failed to persist presence event", "zone", pe.ZoneName, "label", pe.Label, "error", err)
				}
				if mqttClient != nil {
					mqttClient.PublishPresence(pe)
				}

			case fe := <-faceEvents:
				for _, result := range fe.Results {
					personID, similarity := matchFaceToPerson(db, result.Embedding, faceRecognizer)

					face := storage.Face{
						EventID:    fe.EventID,
						Camera:     fe.Camera,
						Embedding:  detect.Float32ToBytes(result.Embedding),
						CropPath:   result.CropPath,
						Confidence: float64(result.Confidence),
						Timestamp:  time.Now(),
					}
					if personID > 0 {
						face.PersonID = &personID
						face.Similarity = &similarity
					}

					faceID, saveErr := db.SaveFace(face)
					if saveErr != nil {
						slog.Error("failed to save face", "error", saveErr)
						continue
					}

					if personID > 0 {
						updatePersonCentroid(db, personID, result.Embedding)
						slog.Info("face matched to person", "person_id", personID, "similarity", fmt.Sprintf("%.3f", similarity), "camera", fe.Camera)
					} else {
						clusterUnmatchedFace(db, faceID, result.Embedding, fe.Camera)
					}
				}
			}
		}
	}()

	// Start RTSP re-publishing server if enabled
	if cfg.RTSPServer.Enabled {
		rtspServer := stream.NewRTSPServer(hub, cfg.RTSPServer, authChecker, cfg.Cameras)
		if err := rtspServer.Start(); err != nil {
			slog.Error("RTSP re-publish server failed to start", "error", err)
		} else {
			defer rtspServer.Close()
			slog.Info("RTSP re-publish server started", "port", cfg.RTSPServer.Port)
		}
	}

	// Wire subsystems into the API server now that everything is initialized
	server.SetSubsystems(manager, recorder, hub, faceRecognizer, objectEmbedder, cfg.Events.SnapshotPath, filepath.Join(cfg.Events.SnapshotPath, "faces"), cfg.Cameras)

	slog.Info("vedetta started", "cameras", len(cfg.Cameras))

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	slog.Info("shutting down")

	// Gracefully shut down the HTTP server (5s timeout for in-flight requests)
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("HTTP server shutdown error", "error", err)
	}

	cancel()

	// Wait for recording goroutines to finalize segments before closing DB
	recorder.Close()
}

// syncConfigZones inserts zones from config into the database (if not already present)
// and loads all zones from DB into the corresponding cameras.
func syncConfigZones(db *storage.DB, cameras []config.CameraConfig, manager *camera.Manager) {
	for _, camCfg := range cameras {
		if !camCfg.IsEnabled() {
			continue
		}

		// Insert config zones into DB if they don't already exist
		for _, cfgZone := range camCfg.Zones {
			existing, err := db.GetZone(camCfg.Name, cfgZone.Name)
			if err != nil {
				slog.Error("failed to check zone existence", "camera", camCfg.Name, "zone", cfgZone.Name, "error", err)
				continue
			}
			if existing != nil {
				continue // Don't overwrite zones created/modified via API
			}

			z := camera.Zone{
				Camera:          camCfg.Name,
				Name:            cfgZone.Name,
				Points:          cfgZone.Points,
				Labels:          cfgZone.Labels,
				TrackPresence:   cfgZone.TrackPresence,
				FaceRecognition: cfgZone.FaceRecognition,
				Enabled:         true,
			}
			if err := db.SaveZone(z); err != nil {
				slog.Error("failed to save config zone", "camera", camCfg.Name, "zone", cfgZone.Name, "error", err)
			} else {
				slog.Info("synced zone from config", "camera", camCfg.Name, "zone", cfgZone.Name)
			}
		}

		// Load all zones from DB into the camera
		cam := manager.GetCamera(camCfg.Name)
		if cam == nil {
			continue
		}
		zones, err := db.ListZones(camCfg.Name)
		if err != nil {
			slog.Error("failed to load zones", "camera", camCfg.Name, "error", err)
			continue
		}
		cam.SetZones(zones)
		if len(zones) > 0 {
			slog.Info("loaded zones", "camera", camCfg.Name, "count", len(zones))
		}
	}
}

// matchFaceToPerson finds the best matching person for a face embedding.
// Returns (personID, similarity) or (0, 0) if no match above threshold.
func matchFaceToPerson(db *storage.DB, embedding []float32, fr *detect.FaceRecognizer) (int64, float64) {
	if fr == nil {
		return 0, 0
	}
	people, err := db.ListPeople()
	if err != nil {
		slog.Error("failed to list people for face matching", "error", err)
		return 0, 0
	}

	var bestID int64
	var bestSim float64
	threshold := fr.MatchThreshold()

	for _, p := range people {
		if p.Ignore || len(p.Centroid) == 0 {
			continue
		}
		centroid := detect.BytesToFloat32(p.Centroid)
		sim := detect.CosineSimilarity(embedding, centroid)
		if sim > bestSim {
			bestSim = sim
			bestID = p.ID
		}
	}

	if bestSim >= threshold {
		return bestID, bestSim
	}
	return 0, 0
}

// updatePersonCentroid updates a person's centroid with a running average.
func updatePersonCentroid(db *storage.DB, personID int64, newEmbedding []float32) {
	p, err := db.GetPerson(personID)
	if err != nil || p == nil {
		return
	}

	if len(p.Centroid) == 0 {
		_ = db.UpdatePersonCentroid(personID, detect.Float32ToBytes(newEmbedding))
		return
	}

	old := detect.BytesToFloat32(p.Centroid)
	if len(old) != len(newEmbedding) {
		_ = db.UpdatePersonCentroid(personID, detect.Float32ToBytes(newEmbedding))
		return
	}

	alpha := float32(0.3)
	merged := make([]float32, len(old))
	var norm float64
	for i := range merged {
		merged[i] = (1-alpha)*old[i] + alpha*newEmbedding[i]
		norm += float64(merged[i]) * float64(merged[i])
	}
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range merged {
			merged[i] *= invNorm
		}
	}

	_ = db.UpdatePersonCentroid(personID, detect.Float32ToBytes(merged))
}

const clusterThreshold = 0.62

func clusterUnmatchedFace(db *storage.DB, newFaceID int64, embedding []float32, camera string) {
	unmatched, err := db.ListUnmatchedFaces(200)
	if err != nil || len(unmatched) == 0 {
		return
	}

	var bestFace *storage.Face
	var bestSim float64
	for i := range unmatched {
		if unmatched[i].ID == newFaceID {
			continue
		}
		other := detect.BytesToFloat32(unmatched[i].Embedding)
		if len(other) == 0 {
			continue
		}
		sim := detect.CosineSimilarity(embedding, other)
		if sim > bestSim {
			bestSim = sim
			bestFace = &unmatched[i]
		}
	}

	if bestFace == nil || bestSim < clusterThreshold {
		return
	}

	centroid := averageEmbeddings(embedding, detect.BytesToFloat32(bestFace.Embedding))
	personID, err := db.SavePerson("", false, detect.Float32ToBytes(centroid))
	if err != nil {
		slog.Error("failed to create person from cluster", "error", err)
		return
	}
	_ = db.UpdateFacePerson(bestFace.ID, personID, bestSim)
	_ = db.UpdateFacePerson(newFaceID, personID, 1.0)
	slog.Info("auto-clustered faces into new person", "person_id", personID, "similarity", fmt.Sprintf("%.3f", bestSim), "camera", camera)
}

func averageEmbeddings(a, b []float32) []float32 {
	if len(a) != len(b) {
		return a
	}
	out := make([]float32, len(a))
	var norm float64
	for i := range out {
		out[i] = (a[i] + b[i]) / 2
		norm += float64(out[i]) * float64(out[i])
	}
	if norm > 1e-10 {
		invNorm := float32(1.0 / math.Sqrt(norm))
		for i := range out {
			out[i] *= invNorm
		}
	}
	return out
}

const objectMatchThreshold = 0.65

func matchEventToKnownObjects(db *storage.DB, oe *detect.ObjectEmbedder, event camera.Event) []string {
	knownObjects, err := db.ListKnownObjectsByLabel(event.Label)
	if err != nil || len(knownObjects) == 0 {
		return nil
	}

	embedding, err := oe.Embed(event.SnapshotImage, event.Box)
	if err != nil {
		slog.Error("object re-ID embed failed", "event", event.ID, "error", err)
		return nil
	}

	var matched []string
	for _, obj := range knownObjects {
		centroid := detect.BytesToFloat32(obj.Centroid)
		if len(centroid) == 0 {
			continue
		}
		sim := detect.CosineSimilarity(embedding, centroid)
		if sim >= objectMatchThreshold {
			if _, err := db.SaveObjectSighting(storage.ObjectSighting{
				EventID:    event.ID,
				Camera:     event.CameraName,
				ObjectID:   obj.ID,
				Similarity: sim,
				Timestamp:  event.Timestamp,
			}); err != nil {
				slog.Error("failed to save object sighting", "error", err)
			} else {
				matched = append(matched, obj.Name)
				_ = db.UpdateEventObjectName(event.ID, obj.Name)
				slog.Info("object recognized", "object", obj.Name, "event", event.ID,
					"similarity", fmt.Sprintf("%.3f", sim))
			}
		}
	}
	return matched
}

func reconcileEventMediaAvailability(db *storage.DB) {
	events, err := db.EventsWithSnapshots()
	if err != nil {
		slog.Error("failed to query events for media reconciliation", "error", err)
		return
	}

	for _, ev := range events {
		snapshotAvailable := ev.SnapshotPath != ""
		if snapshotAvailable {
			if _, err := os.Stat(ev.SnapshotPath); err != nil {
				snapshotAvailable = false
			}
		}
		if err := db.UpdateEventSnapshotAvailability(ev.ID, snapshotAvailable); err != nil {
			slog.Error("failed to update snapshot availability", "id", ev.ID, "error", err)
		}

		clipAvailable := ev.ClipPath != ""
		if clipAvailable {
			if _, err := os.Stat(ev.ClipPath); err != nil {
				clipAvailable = false
			}
		}
		if err := db.UpdateEventClipAvailability(ev.ID, clipAvailable); err != nil {
			slog.Error("failed to update clip availability", "id", ev.ID, "error", err)
		}
	}
}

func cooldownKey(event camera.Event) string {
	return event.CameraName + "|" + event.Label + "|" + event.ZoneName
}

func runHashPassword(args []string) {
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: vedetta auth hash-password <password>")
		os.Exit(2)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(args[0]), bcrypt.DefaultCost)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(string(hash))
}
