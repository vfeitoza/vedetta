package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rvben/vedetta/internal/camera"
	"github.com/rvben/vedetta/internal/config"
)

// Publisher defines the interface for MQTT publishing operations.
type Publisher interface {
	PublishEvent(event camera.Event, matchedObjects []string) error
	PublishPresence(pe camera.PresenceEvent)
	PublishCameraStatus(cameraName string, online bool)
	PublishDiscovery(cameraNames []string)
	Close()
}

// Client wraps an MQTT connection for publishing detection events
// and Home Assistant MQTT discovery messages.
type Client struct {
	client pahomqtt.Client
	topic  string
}

func New(cfg config.MQTTConfig) (*Client, error) {
	topic := cfg.Topic
	if topic == "" {
		topic = "vedetta"
	}

	availabilityTopic := topic + "/availability"

	opts := pahomqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.Host, cfg.Port)).
		SetClientID("vedetta").
		SetAutoReconnect(true).
		SetWill(availabilityTopic, "offline", 1, true)

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	c := &Client{topic: topic}

	opts.SetOnConnectHandler(func(_ pahomqtt.Client) {
		slog.Info("MQTT connected, publishing availability")
		c.publishAvailability("online")
	})

	opts.SetConnectionLostHandler(func(_ pahomqtt.Client, err error) {
		slog.Warn("MQTT connection lost", "error", err)
	})

	client := pahomqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", token.Error())
	}

	c.client = client

	slog.Info("connected to MQTT", "host", cfg.Host, "port", cfg.Port)

	return c, nil
}

func (c *Client) publishAvailability(status string) {
	topic := c.topic + "/availability"
	token := c.client.Publish(topic, 1, true, status)
	token.Wait()
	if token.Error() != nil {
		slog.Error("failed to publish availability", "error", token.Error())
	}
}

func (c *Client) PublishEvent(event camera.Event, matchedObjects []string) error {
	type eventPayload struct {
		camera.Event
		Objects []string `json:"objects,omitempty"`
	}
	payload, err := json.Marshal(eventPayload{Event: event, Objects: matchedObjects})
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	topic := fmt.Sprintf("%s/events/%s", c.topic, event.CameraName)
	token := c.client.Publish(topic, 1, false, payload)
	token.Wait()
	return token.Error()
}

func (c *Client) PublishPresence(pe camera.PresenceEvent) {
	state := "entered"
	if pe.Type == "zone_leave" {
		state = "left"
	}
	payload, err := json.Marshal(map[string]string{
		"zone":  pe.ZoneName,
		"label": pe.Label,
		"state": state,
	})
	if err != nil {
		return
	}

	topic := fmt.Sprintf("%s/presence/%s/%s", c.topic, sanitizeName(pe.ZoneName), pe.Label)
	token := c.client.Publish(topic, 1, true, payload)
	token.Wait()
	if token.Error() != nil {
		slog.Error("failed to publish presence", "zone", pe.ZoneName, "error", token.Error())
	}
}

func (c *Client) PublishObjectSighting(objectName string, event camera.Event) {
	payload, err := json.Marshal(map[string]any{
		"object":   objectName,
		"camera":   event.CameraName,
		"label":    event.Label,
		"event_id": event.ID,
		"zone":     event.ZoneName,
	})
	if err != nil {
		return
	}

	topic := fmt.Sprintf("%s/objects/%s/sighted", c.topic, sanitizeName(objectName))
	token := c.client.Publish(topic, 1, false, payload)
	token.Wait()
	if token.Error() != nil {
		slog.Error("failed to publish object sighting", "object", objectName, "error", token.Error())
	}
}

func (c *Client) PublishCameraStatus(cameraName string, online bool) {
	status := "OFF"
	if online {
		status = "ON"
	}
	topic := fmt.Sprintf("%s/camera/%s/status", c.topic, cameraName)
	token := c.client.Publish(topic, 1, true, status)
	token.Wait()
	if token.Error() != nil {
		slog.Error("failed to publish camera status",
			"camera", cameraName, "error", token.Error())
	}
}

// PublishDiscovery publishes Home Assistant MQTT discovery messages for each camera.
func (c *Client) PublishDiscovery(cameraNames []string) {
	for _, name := range cameraNames {
		c.publishCameraDiscovery(name)
	}
}

// PublishObjectDiscovery publishes HA discovery for tracked objects as device triggers.
func (c *Client) PublishObjectDiscovery(objects []ObjectInfo) {
	for _, obj := range objects {
		c.publishObjectTriggerDiscovery(obj)
	}
}

// ObjectInfo carries the minimal info needed for MQTT discovery.
type ObjectInfo struct {
	Name  string
	Label string
}

func (c *Client) publishObjectTriggerDiscovery(obj ObjectInfo) {
	objectID := fmt.Sprintf("vedetta_object_%s", sanitizeName(obj.Name))

	device := haDevice{
		Identifiers:  []string{objectID},
		Name:         "Vedetta " + obj.Name,
		Manufacturer: "Vedetta",
		Model:        "Tracked Object",
	}

	triggerConfig := haDeviceTriggerConfig{
		AutomationType: "trigger",
		Type:           "object_sighted",
		Subtype:        sanitizeName(obj.Label),
		Topic:          fmt.Sprintf("%s/objects/%s/sighted", c.topic, sanitizeName(obj.Name)),
		Device:         device,
	}

	payload, err := json.Marshal(triggerConfig)
	if err != nil {
		return
	}

	topic := fmt.Sprintf("homeassistant/device_automation/%s_sighted/config", objectID)
	token := c.client.Publish(topic, 1, true, payload)
	token.Wait()
}

func (c *Client) publishCameraDiscovery(cameraName string) {
	objectID := fmt.Sprintf("vedetta_%s", sanitizeName(cameraName))

	device := haDevice{
		Identifiers:  []string{"vedetta_" + sanitizeName(cameraName)},
		Name:         "Vedetta " + cameraName,
		Manufacturer: "Vedetta",
		Model:        "NVR",
	}

	// Binary sensor for camera online/offline status
	sensorConfig := haBinarySensorConfig{
		Name:              cameraName,
		UniqueID:          objectID + "_status",
		StateTopic:        fmt.Sprintf("%s/camera/%s/status", c.topic, cameraName),
		AvailabilityTopic: c.topic + "/availability",
		DeviceClass:       "connectivity",
		PayloadOn:         "ON",
		PayloadOff:        "OFF",
		Device:            device,
	}

	sensorPayload, err := json.Marshal(sensorConfig)
	if err != nil {
		slog.Error("failed to marshal discovery config", "camera", cameraName, "error", err)
		return
	}

	sensorTopic := fmt.Sprintf("homeassistant/binary_sensor/%s/config", objectID)
	token := c.client.Publish(sensorTopic, 1, true, sensorPayload)
	token.Wait()
	if token.Error() != nil {
		slog.Error("failed to publish discovery", "camera", cameraName, "error", token.Error())
	}

	// Device trigger for detection events
	triggerConfig := haDeviceTriggerConfig{
		AutomationType: "trigger",
		Type:           "detection",
		Subtype:        "object_detected",
		Topic:          fmt.Sprintf("%s/events/%s", c.topic, cameraName),
		Device:         device,
	}

	triggerPayload, err := json.Marshal(triggerConfig)
	if err != nil {
		slog.Error("failed to marshal trigger config", "camera", cameraName, "error", err)
		return
	}

	triggerTopic := fmt.Sprintf("homeassistant/device_automation/%s_detection/config", objectID)
	token = c.client.Publish(triggerTopic, 1, true, triggerPayload)
	token.Wait()
	if token.Error() != nil {
		slog.Error("failed to publish trigger discovery", "camera", cameraName, "error", token.Error())
	}

	slog.Info("published HA discovery", "camera", cameraName)
}

func (c *Client) Close() {
	c.publishAvailability("offline")
	c.client.Disconnect(1000)
}

// sanitizeName converts a camera name to a safe identifier for MQTT topics.
func sanitizeName(name string) string {
	r := strings.NewReplacer(" ", "_", "-", "_", ".", "_")
	return strings.ToLower(r.Replace(name))
}

// Home Assistant discovery payload types

type haDevice struct {
	Identifiers  []string `json:"identifiers"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
}

type haBinarySensorConfig struct {
	Name              string   `json:"name"`
	UniqueID          string   `json:"unique_id"`
	StateTopic        string   `json:"state_topic"`
	AvailabilityTopic string   `json:"availability_topic"`
	DeviceClass       string   `json:"device_class"`
	PayloadOn         string   `json:"payload_on"`
	PayloadOff        string   `json:"payload_off"`
	Device            haDevice `json:"device"`
}

type haDeviceTriggerConfig struct {
	AutomationType string   `json:"automation_type"`
	Type           string   `json:"type"`
	Subtype        string   `json:"subtype"`
	Topic          string   `json:"topic"`
	Device         haDevice `json:"device"`
}
