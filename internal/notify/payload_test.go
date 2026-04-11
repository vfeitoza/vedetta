package notify

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/rvben/vedetta/internal/camera"
)

func TestBuildPayload_WithSnapshot(t *testing.T) {
	ev := camera.Event{
		ID:                "front-t91-1712847123456",
		CameraName:        "front",
		Label:             "person",
		Score:             0.87,
		Timestamp:         time.Date(2026, 4, 11, 18, 42, 0, 0, time.UTC),
		SnapshotAvailable: true,
	}
	data := BuildPayload(ev)

	var got map[string]interface{}
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["title"] != "front" {
		t.Errorf("title = %v", got["title"])
	}
	if !strings.Contains(got["body"].(string), "Person") || !strings.Contains(got["body"].(string), "18:42 UTC") {
		t.Errorf("body = %v", got["body"])
	}
	if got["url"] != "/event.html?id=front-t91-1712847123456" {
		t.Errorf("url = %v", got["url"])
	}
	if got["image"] != "/api/events/front-t91-1712847123456/snapshot" {
		t.Errorf("image = %v", got["image"])
	}
	if got["tag"] != "front:person" {
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
	data := BuildPayload(ev)
	var got map[string]interface{}
	_ = json.Unmarshal(data, &got)
	if _, has := got["image"]; has {
		t.Fatalf("image should be omitted when SnapshotAvailable is false, payload: %s", string(data))
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
	data := BuildPayload(ev)
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
