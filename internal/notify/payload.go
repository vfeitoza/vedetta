package notify

import (
	"encoding/json"
	"fmt"

	"github.com/rvben/vedetta/internal/camera"
)

// pushPayload is the JSON shape delivered to the service worker.
type pushPayload struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Image string `json:"image,omitempty"` // omitted when SnapshotAvailable is false
	Tag   string `json:"tag"`
	TS    int64  `json:"ts"`
}

// BuildPayload produces the JSON push body for a detection event.
// See design spec → "Service worker" and "payload.go" sections.
func BuildPayload(ev camera.Event) []byte {
	p := pushPayload{
		Title: ev.CameraName,
		Body:  fmt.Sprintf("%s detected · %s UTC", titleCase(ev.Label), ev.Timestamp.UTC().Format("15:04")),
		URL:   fmt.Sprintf("/event.html?id=%s", ev.ID),
		Tag:   fmt.Sprintf("%s:%s", ev.CameraName, ev.Label),
		TS:    ev.Timestamp.UTC().Unix(),
	}
	if ev.SnapshotAvailable {
		p.Image = fmt.Sprintf("/api/events/%s/snapshot", ev.ID)
	}
	data, _ := json.Marshal(p)
	if len(data) > 4000 {
		// Defensive truncation: drop image first (already conditional above),
		// then clip body. Extreme case only.
		p.Image = ""
		if len(p.Body) > 120 {
			p.Body = p.Body[:120]
		}
		data, _ = json.Marshal(p)
	}
	return data
}

// titleCase uppercases the first byte of an ASCII word. Good enough for
// English detection labels like "person", "car", "bicycle".
func titleCase(s string) string {
	if s == "" {
		return s
	}
	b := []byte(s)
	if b[0] >= 'a' && b[0] <= 'z' {
		b[0] -= 'a' - 'A'
	}
	return string(b)
}
