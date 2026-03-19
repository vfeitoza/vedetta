package mqtt

import (
	"encoding/json"
	"fmt"
	"log/slog"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/rvben/watchpost/internal/camera"
	"github.com/rvben/watchpost/internal/config"
)

// Client wraps an MQTT connection for publishing detection events.
type Client struct {
	client pahomqtt.Client
	topic  string
}

func New(cfg config.MQTTConfig) (*Client, error) {
	opts := pahomqtt.NewClientOptions().
		AddBroker(fmt.Sprintf("tcp://%s:%d", cfg.Host, cfg.Port)).
		SetClientID("watchpost")

	if cfg.Username != "" {
		opts.SetUsername(cfg.Username)
		opts.SetPassword(cfg.Password)
	}

	client := pahomqtt.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, fmt.Errorf("mqtt connect: %w", token.Error())
	}

	topic := cfg.Topic
	if topic == "" {
		topic = "watchpost"
	}

	slog.Info("connected to MQTT", "host", cfg.Host, "port", cfg.Port)

	return &Client{
		client: client,
		topic:  topic,
	}, nil
}

func (c *Client) PublishEvent(event camera.Event) error {
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	topic := fmt.Sprintf("%s/events/%s", c.topic, event.CameraName)
	token := c.client.Publish(topic, 1, false, payload)
	token.Wait()
	return token.Error()
}

func (c *Client) Close() {
	c.client.Disconnect(1000)
}
