package notify

import (
	"encoding/json"
	"fmt"
	"strings"

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
//
// When signer is non-nil and ev.SnapshotAvailable is true, the payload's
// image field is set to a short-lived HMAC-signed URL that iOS can fetch
// anonymously (no session cookies) to render the notification thumbnail.
// The authenticated /api/events/<id>/snapshot endpoint returns 401 to
// unauthenticated fetches, which iOS silently treats as "no image".
func BuildPayload(ev camera.Event, signer *SnapshotSigner) []byte {
	p := pushPayload{
		Title: friendlyCameraName(ev.CameraName),
		Body:  fmt.Sprintf("%s detected · %s UTC", titleCase(ev.Label), ev.Timestamp.UTC().Format("15:04")),
		URL:   fmt.Sprintf("/event.html?id=%s", ev.ID),
		Tag:   fmt.Sprintf("%s:%s", ev.CameraName, ev.Label),
		TS:    ev.Timestamp.UTC().Unix(),
	}
	if ev.SnapshotAvailable && signer != nil {
		p.Image = signer.Sign(ev.ID)
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

// friendlyCameraName turns a config-level camera identifier like
// "kids_bedroom_3" or "front_door" into a display string suitable for a
// notification title: "Kids Bedroom 3", "Front Door". Numeric suffixes
// stay numeric. This is a cosmetic fallback — a future DisplayName
// field in CameraConfig should take priority once it exists.
func friendlyCameraName(name string) string {
	if name == "" {
		return name
	}
	parts := strings.FieldsFunc(name, func(r rune) bool {
		return r == '_' || r == '-'
	})
	for i, part := range parts {
		parts[i] = titleCase(part)
	}
	return strings.Join(parts, " ")
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
