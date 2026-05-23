package camera

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rvben/vedetta/internal/backoff"
)

// OnvifEventType classifies ONVIF events.
type OnvifEventType string

const (
	OnvifEventDoorbell OnvifEventType = "doorbell"
	OnvifEventMotion   OnvifEventType = "motion"
	OnvifEventUnknown  OnvifEventType = "unknown"
)

// OnvifEvent represents a parsed ONVIF notification.
type OnvifEvent struct {
	Type      OnvifEventType
	Camera    string
	Timestamp time.Time
	Topic     string
	Value     bool
}

// OnvifEventSubscriber subscribes to a camera's ONVIF events via PullPoint.
type OnvifEventSubscriber struct {
	cameraName   string
	serviceURL   string
	username     string
	password     string
	events       chan<- OnvifEvent
	httpClient   *http.Client
	pullPointURL string
}

func NewOnvifEventSubscriber(cameraName, rtspURL string, events chan<- OnvifEvent) (*OnvifEventSubscriber, error) {
	u, err := url.Parse(rtspURL)
	if err != nil {
		return nil, fmt.Errorf("parse rtsp url: %w", err)
	}

	host := u.Hostname()
	port := u.Port()
	if port == "" || port == "554" {
		port = "80"
	}

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	serviceURL := fmt.Sprintf("http://%s:%s/onvif/event_service", host, port)

	return &OnvifEventSubscriber{
		cameraName: cameraName,
		serviceURL: serviceURL,
		username:   username,
		password:   password,
		events:     events,
		httpClient: &http.Client{Timeout: 65 * time.Second},
	}, nil
}

// Run subscribes and polls for events until ctx is cancelled.
func (s *OnvifEventSubscriber) Run(ctx context.Context) {
	for {
		if err := s.subscribe(ctx); err != nil {
			slog.Warn("ONVIF subscribe failed, retrying", "camera", s.cameraName, "error", err)
		}
		// Jitter the retry so cameras that fail their subscription together (e.g.
		// a switch reboot) do not all re-subscribe in lockstep.
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff.Jitter(10*time.Second, rand.Float64())):
		}
	}
}

func (s *OnvifEventSubscriber) subscribe(ctx context.Context) error {
	// Create PullPoint subscription
	createReq := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
            xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2">
  <s:Body>
    <tev:CreatePullPointSubscription>
      <tev:InitialTerminationTime>PT600S</tev:InitialTerminationTime>
    </tev:CreatePullPointSubscription>
  </s:Body>
</s:Envelope>`

	resp, err := s.soapRequest(ctx, s.serviceURL, createReq,
		"http://www.onvif.org/ver10/events/wsdl/EventPortType/CreatePullPointSubscriptionRequest")
	if err != nil {
		return fmt.Errorf("create subscription: %w", err)
	}

	pullPointURL := extractPullPointURL(resp)
	if pullPointURL == "" {
		return fmt.Errorf("no PullPoint URL in subscription response")
	}
	s.pullPointURL = pullPointURL
	slog.Info("ONVIF event subscription created", "camera", s.cameraName, "pullpoint", pullPointURL)

	// Poll loop
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		events, err := s.pullMessages(ctx)
		if err != nil {
			return fmt.Errorf("pull messages: %w", err)
		}

		for _, ev := range events {
			ev.Camera = s.cameraName
			select {
			case s.events <- ev:
			default:
				slog.Warn("ONVIF event channel full", "camera", s.cameraName)
			}
		}
	}
}

func (s *OnvifEventSubscriber) pullMessages(ctx context.Context) ([]OnvifEvent, error) {
	pullReq := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tev="http://www.onvif.org/ver10/events/wsdl">
  <s:Body>
    <tev:PullMessages>
      <tev:Timeout>PT60S</tev:Timeout>
      <tev:MessageLimit>10</tev:MessageLimit>
    </tev:PullMessages>
  </s:Body>
</s:Envelope>`

	resp, err := s.soapRequest(ctx, s.pullPointURL, pullReq,
		"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessagesRequest")
	if err != nil {
		return nil, err
	}

	return parseOnvifEvents(resp), nil
}

func (s *OnvifEventSubscriber) soapRequest(ctx context.Context, endpoint, body, action string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/soap+xml; charset=utf-8")
	req.Header.Set("SOAPAction", action)
	if s.username != "" {
		req.SetBasicAuth(s.username, s.password)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("SOAP %d: %s", resp.StatusCode, truncate(string(data), 200))
	}

	return data, nil
}

func extractPullPointURL(data []byte) string {
	type addressEnvelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Reference struct {
					Address string `xml:"Address"`
				} `xml:"SubscriptionReference"`
			} `xml:"CreatePullPointSubscriptionResponse"`
		} `xml:"Body"`
	}
	var env addressEnvelope
	if err := xml.Unmarshal(data, &env); err != nil {
		return ""
	}
	return strings.TrimSpace(env.Body.Response.Reference.Address)
}

func parseOnvifEvents(data []byte) []OnvifEvent {
	type notificationMessage struct {
		Topic   string `xml:"Topic"`
		Message struct {
			Data struct {
				Items []struct {
					Name  string `xml:"Name,attr"`
					Value string `xml:"Value,attr"`
				} `xml:"SimpleItem"`
			} `xml:"Data"`
		} `xml:"Message"`
	}
	type pullEnvelope struct {
		XMLName xml.Name `xml:"Envelope"`
		Body    struct {
			Response struct {
				Messages []struct {
					Notification notificationMessage `xml:"NotificationMessage"`
				} `xml:"NotificationMessage"`
			} `xml:"PullMessagesResponse"`
		} `xml:"Body"`
	}

	var env pullEnvelope
	if err := xml.Unmarshal(data, &env); err != nil {
		return nil
	}

	var events []OnvifEvent
	for _, msg := range env.Body.Response.Messages {
		topic := msg.Notification.Topic
		ev := OnvifEvent{
			Type:      classifyOnvifTopic(topic),
			Timestamp: time.Now(),
			Topic:     topic,
		}

		for _, item := range msg.Notification.Message.Data.Items {
			if strings.EqualFold(item.Name, "State") || strings.EqualFold(item.Name, "IsMotion") {
				ev.Value = strings.EqualFold(item.Value, "true") || item.Value == "1"
			}
		}

		if ev.Type != OnvifEventUnknown {
			events = append(events, ev)
		}
	}
	return events
}

func classifyOnvifTopic(topic string) OnvifEventType {
	t := strings.ToLower(topic)
	switch {
	case strings.Contains(t, "digitalnput") || strings.Contains(t, "doorbell") ||
		strings.Contains(t, "visitor") || strings.Contains(t, "button"):
		return OnvifEventDoorbell
	case strings.Contains(t, "motiondetector") || strings.Contains(t, "motion") ||
		strings.Contains(t, "cellmotion") || strings.Contains(t, "videomotion"):
		return OnvifEventMotion
	default:
		return OnvifEventUnknown
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
