package mqtt

import (
	"encoding/json"
	"testing"
)

func TestBuildDoorbellDiscovery(t *testing.T) {
	topic, payload := buildDoorbellDiscovery("vedetta", "front_door")
	if topic != "homeassistant/device_automation/vedetta_front_door_doorbell/config" {
		t.Errorf("topic = %q", topic)
	}
	var cfg map[string]any
	if err := json.Unmarshal(payload, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["automation_type"] != "trigger" {
		t.Errorf("automation_type = %v, want trigger", cfg["automation_type"])
	}
	if cfg["type"] != "button_short_press" {
		t.Errorf("type = %v, want button_short_press", cfg["type"])
	}
	if cfg["subtype"] != "press" {
		t.Errorf("subtype = %v, want press", cfg["subtype"])
	}
	if cfg["topic"] != "vedetta/front_door/doorbell" {
		t.Errorf("topic field = %v, want vedetta/front_door/doorbell", cfg["topic"])
	}

	// Device identifier must match publishCameraDiscovery so the doorbell trigger
	// groups under the existing camera device in Home Assistant, not a duplicate.
	deviceMap, ok := cfg["device"].(map[string]any)
	if !ok {
		t.Fatal("device field is not a map")
	}
	ids, ok := deviceMap["identifiers"].([]any)
	if !ok || len(ids) != 1 {
		t.Fatalf("identifiers = %v, want a single-element list", deviceMap["identifiers"])
	}
	if ids[0] != "vedetta_front_door" {
		t.Errorf("identifiers[0] = %v, want vedetta_front_door", ids[0])
	}
	if deviceMap["model"] != "NVR" {
		t.Errorf("model = %v, want NVR", deviceMap["model"])
	}
}

func TestBuildDoorbellDiscovery_SanitizesName(t *testing.T) {
	topic, payload := buildDoorbellDiscovery("vedetta", "Front Door")
	if topic != "homeassistant/device_automation/vedetta_front_door_doorbell/config" {
		t.Errorf("topic = %q", topic)
	}
	var cfg map[string]any
	if err := json.Unmarshal(payload, &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["topic"] != "vedetta/Front Door/doorbell" {
		t.Errorf("topic field = %v, want vedetta/Front Door/doorbell", cfg["topic"])
	}
}

func TestClientPublishDoorbellDiscovery_TopicQoSRetained(t *testing.T) {
	c, f := newTestClient()
	c.PublishDoorbellDiscovery([]string{"front_door"})

	calls := f.calls()
	if len(calls) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(calls))
	}
	got := calls[0]
	if got.topic != "homeassistant/device_automation/vedetta_front_door_doorbell/config" {
		t.Errorf("topic = %q", got.topic)
	}
	if got.qos != 1 {
		t.Errorf("qos = %d, want 1", got.qos)
	}
	if !got.retained {
		t.Error("doorbell discovery must be retained")
	}
	var cfg map[string]any
	if err := json.Unmarshal(got.payload, &cfg); err != nil {
		t.Fatalf("payload not JSON: %v", err)
	}
	if cfg["automation_type"] != "trigger" {
		t.Errorf("automation_type = %v, want trigger", cfg["automation_type"])
	}
}

func TestClientClearDoorbellDiscovery_EmptyRetained(t *testing.T) {
	c, f := newTestClient()
	c.ClearDoorbellDiscovery([]string{"front_door"})

	got := f.calls()
	if len(got) != 1 {
		t.Fatalf("expected 1 publish, got %d", len(got))
	}
	p := got[0]
	if p.topic != "homeassistant/device_automation/vedetta_front_door_doorbell/config" {
		t.Errorf("clear topic = %q", p.topic)
	}
	if p.qos != 1 {
		t.Errorf("qos = %d, want 1", p.qos)
	}
	if !p.retained {
		t.Error("clear must be retained so HA drops the entity")
	}
	if len(p.payload) != 0 {
		t.Errorf("clear payload must be empty, got %q", p.payload)
	}
}
