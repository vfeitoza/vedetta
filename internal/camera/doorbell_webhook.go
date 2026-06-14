package camera

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

var doorbellWebhookClient = &http.Client{Timeout: 5 * time.Second}

// fireDoorbellWebhook POSTs a small JSON body to an external webhook. Best-effort:
// failures are logged, never propagated. A blank URL is a no-op. Call it in its own
// goroutine - it blocks for up to the client timeout.
func fireDoorbellWebhook(url, cameraName, eventID, person string) {
	if url == "" {
		return
	}
	body, _ := json.Marshal(map[string]string{
		"camera":    cameraName,
		"event_id":  eventID,
		"person":    person,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Warn("doorbell webhook build failed", "camera", cameraName, "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := doorbellWebhookClient.Do(req)
	if err != nil {
		slog.Warn("doorbell webhook failed", "camera", cameraName, "error", err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		slog.Warn("doorbell webhook non-2xx", "camera", cameraName, "status", resp.StatusCode)
	}
}
