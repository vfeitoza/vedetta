package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func newTestSigner(t *testing.T) *SnapshotSigner {
	t.Helper()
	signer, err := LoadOrGenerateSnapshotSigner(newFakeKVStore())
	if err != nil {
		t.Fatalf("LoadOrGenerateSnapshotSigner: %v", err)
	}
	return signer
}

func TestBuildPayload_WithSnapshot(t *testing.T) {
	ev := camera.Event{
		ID:                "front-t91-1712847123456",
		CameraName:        "front_door",
		Label:             "person",
		Score:             0.87,
		Timestamp:         time.Date(2026, 4, 11, 18, 42, 0, 0, time.UTC),
		SnapshotAvailable: true,
	}
	signer := newTestSigner(t)
	data := BuildPayload(ev, signer)

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// Title should be the friendly form of the camera name — underscores
	// become spaces, each word title-cased. "front_door" → "Front Door".
	if got["title"] != "Front Door" {
		t.Errorf("title = %v, want %q", got["title"], "Front Door")
	}
	if !strings.Contains(got["body"].(string), "Person") || !strings.Contains(got["body"].(string), "18:42 UTC") {
		t.Errorf("body = %v", got["body"])
	}
	if got["url"] != "/event.html?id=front-t91-1712847123456" {
		t.Errorf("url = %v", got["url"])
	}
	image, _ := got["image"].(string)
	if !strings.HasPrefix(image, "/api/push/snapshot/front-t91-1712847123456?") {
		t.Errorf("image = %v, expected signed /api/push/snapshot/ URL", got["image"])
	}
	// The signed URL must carry both an expiry and a signature so the
	// handler can validate it without any session state.
	if !strings.Contains(image, "e=") || !strings.Contains(image, "s=") {
		t.Errorf("image URL missing e= or s= params: %v", image)
	}
	// Tag is a deduplication key used by showNotification() — keep the
	// raw camera name so distinct cameras never collide in the OS
	// notification stack, regardless of the friendly title.
	if got["tag"] != "front_door:person" {
		t.Errorf("tag = %v", got["tag"])
	}
}

func TestBuildPayload_OmitsImageWhenUnavailable(t *testing.T) {
	ev := camera.Event{
		ID:                "front-t91",
		CameraName:        "front",
		Label:             "person",
		Timestamp:         time.Now().UTC(),
		SnapshotAvailable: false,
	}
	signer := newTestSigner(t)
	data := BuildPayload(ev, signer)
	var got map[string]interface{}
	_ = json.Unmarshal(data, &got)
	if _, has := got["image"]; has {
		t.Fatalf("image should be omitted when SnapshotAvailable is false, payload: %s", string(data))
	}
}

func TestBuildPayload_OmitsImageWhenSignerNil(t *testing.T) {
	ev := camera.Event{
		ID:                "front-t91",
		CameraName:        "front",
		Label:             "person",
		Timestamp:         time.Now().UTC(),
		SnapshotAvailable: true,
	}
	data := BuildPayload(ev, nil)
	var got map[string]interface{}
	_ = json.Unmarshal(data, &got)
	if _, has := got["image"]; has {
		t.Fatalf("image should be omitted when signer is nil, payload: %s", string(data))
	}
}

func TestBuildPayload_FitsUnder4KB(t *testing.T) {
	ev := camera.Event{
		ID:                strings.Repeat("x", 200),
		CameraName:        strings.Repeat("c", 100),
		Label:             strings.Repeat("l", 100),
		Timestamp:         time.Now().UTC(),
		SnapshotAvailable: true,
	}
	data := BuildPayload(ev, newTestSigner(t))
	if len(data) > 4000 {
		t.Fatalf("payload too large: %d bytes", len(data))
	}
}

func TestTitleCase(t *testing.T) {
	if titleCase("person") != "Person" {
		t.Fatalf("expected 'Person', got %q", titleCase("person"))
	}
	if titleCase("") != "" {
		t.Fatalf("expected empty, got %q", titleCase(""))
	}
}

func TestFriendlyCameraName(t *testing.T) {
	cases := map[string]string{
		"front_door":     "Front Door",
		"kids_bedroom_3": "Kids Bedroom 3",
		"garage":         "Garage",
		"driveway-east":  "Driveway East",
		"":               "",
		"A":              "A",
		"back_YARD":      "Back YARD", // uppercase preserved, only first byte touched
	}
	for in, want := range cases {
		if got := friendlyCameraName(in); got != want {
			t.Errorf("friendlyCameraName(%q) = %q, want %q", in, got, want)
		}
	}
}
