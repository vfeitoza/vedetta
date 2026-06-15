package camera

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	mathrand "math/rand/v2"
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
	cameraName       string
	host             string
	username         string
	password         string
	events           chan<- OnvifEvent
	httpClient       *http.Client
	pullPointURL     string
	discoveredSvcURL string
}

// candidatePorts lists the ONVIF device-service ports to probe, in order.
// Reolink uses 8000; many others use 80 or 2020; 8080 is a fallback.
var candidatePorts = []string{"8000", "2020", "80", "8080"}

func NewOnvifEventSubscriber(cameraName, rtspURL string, events chan<- OnvifEvent) (*OnvifEventSubscriber, error) {
	u, err := url.Parse(rtspURL)
	if err != nil {
		return nil, fmt.Errorf("parse rtsp url: %w", err)
	}

	host := u.Hostname()

	var username, password string
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	return &OnvifEventSubscriber{
		cameraName: cameraName,
		host:       host,
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
		case <-time.After(backoff.Jitter(10*time.Second, mathrand.Float64())):
		}
	}
}

// discoverEventService probes candidate ONVIF device-service ports and returns
// the event-service XAddr reported by the camera. Falls back to port 80 if no
// candidate responds with a valid event XAddr.
func (s *OnvifEventSubscriber) discoverEventService(ctx context.Context) string {
	probe := http.Client{Timeout: 5 * time.Second}

	getServicesBody := `<tds:GetServices xmlns:tds="http://www.onvif.org/ver10/device/wsdl">` +
		`<tds:IncludeCapability>false</tds:IncludeCapability>` +
		`</tds:GetServices>`

	for _, port := range candidatePorts {
		deviceURL := fmt.Sprintf("http://%s:%s/onvif/device_service", s.host, port)

		envelope := s.buildEnvelope(getServicesBody, "")
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, deviceURL,
			bytes.NewBufferString(envelope))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type",
			`application/soap+xml; charset=utf-8; action="http://www.onvif.org/ver10/device/wsdl/GetServices"`)

		resp, err := probe.Do(req)
		if err != nil {
			slog.Debug("ONVIF GetServices probe failed", "camera", s.cameraName,
				"url", deviceURL, "error", err)
			continue
		}
		data, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK {
			slog.Debug("ONVIF GetServices bad response", "camera", s.cameraName,
				"url", deviceURL, "status", resp.StatusCode)
			continue
		}

		xaddr := parseEventXAddr(data)
		if xaddr != "" {
			slog.Info("ONVIF event service discovered", "camera", s.cameraName, "xaddr", xaddr)
			return xaddr
		}
	}

	// Fall back: derive from host on port 80 (preserves old behavior for cameras
	// that answer on a standard HTTP port but don't respond to our probes).
	fallback := fmt.Sprintf("http://%s:80/onvif/event_service", s.host)
	slog.Debug("ONVIF discovery exhausted candidates, using fallback", "camera", s.cameraName, "fallback", fallback)
	return fallback
}

// parseEventXAddr extracts the XAddr for the ONVIF Events namespace from a
// GetServices response.
func parseEventXAddr(data []byte) string {
	type service struct {
		Namespace string `xml:"Namespace"`
		XAddr     string `xml:"XAddr"`
	}
	type servicesResponse struct {
		XMLName  xml.Name  `xml:"Envelope"`
		Services []service `xml:"Body>GetServicesResponse>Service"`
	}

	var env servicesResponse
	if err := xml.Unmarshal(data, &env); err != nil {
		return ""
	}
	for _, svc := range env.Services {
		if strings.TrimSpace(svc.Namespace) == "http://www.onvif.org/ver10/events/wsdl" {
			return strings.TrimSpace(svc.XAddr)
		}
	}
	return ""
}

func (s *OnvifEventSubscriber) subscribe(ctx context.Context) error {
	// Discover (or re-discover) the event-service URL each time we reconnect.
	svcURL := s.discoverEventService(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	s.discoveredSvcURL = svcURL

	createBody := `<tev:CreatePullPointSubscription xmlns:tev="http://www.onvif.org/ver10/events/wsdl"` +
		` xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2">` +
		`<tev:InitialTerminationTime>PT600S</tev:InitialTerminationTime>` +
		`</tev:CreatePullPointSubscription>`

	resp, err := s.soapRequest(ctx, svcURL, createBody,
		"http://www.onvif.org/ver10/events/wsdl/EventPortType/CreatePullPointSubscriptionRequest",
		"")
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
	pullBody := `<tev:PullMessages xmlns:tev="http://www.onvif.org/ver10/events/wsdl">` +
		`<tev:Timeout>PT60S</tev:Timeout>` +
		`<tev:MessageLimit>10</tev:MessageLimit>` +
		`</tev:PullMessages>`

	wsaHeaders := wsAddressingHeaders(s.pullPointURL,
		"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessages")

	resp, err := s.soapRequest(ctx, s.pullPointURL, pullBody,
		"http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessagesRequest",
		wsaHeaders)
	if err != nil {
		return nil, err
	}

	return parseOnvifEvents(resp), nil
}

// soapRequest sends a SOAP 1.2 request. body is the operation element (no
// Envelope wrapper). extraHeader is optional raw XML injected after the
// WS-Security block (used for WS-Addressing on PullMessages).
func (s *OnvifEventSubscriber) soapRequest(ctx context.Context, endpoint, body, action, extraHeader string) ([]byte, error) {
	envelope := s.buildEnvelope(body, extraHeader)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewBufferString(envelope))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type",
		fmt.Sprintf(`application/soap+xml; charset=utf-8; action="%s"`, action))

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

// buildEnvelope wraps body in a SOAP 1.2 envelope, prepending a WS-Security
// header and any optional extra header XML.
func (s *OnvifEventSubscriber) buildEnvelope(body, extraHeader string) string {
	header := wssecHeader(s.username, s.password)
	if extraHeader != "" {
		header += extraHeader
	}
	return `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"` +
		` xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd"` +
		` xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"` +
		` xmlns:wsa="http://www.w3.org/2005/08/addressing">` +
		`<s:Header>` + header + `</s:Header>` +
		`<s:Body>` + body + `</s:Body>` +
		`</s:Envelope>`
}

// wsseDigest computes the ONVIF WS-Security PasswordDigest:
// Base64( SHA-1( nonceRaw || []byte(created) || []byte(password) ) )
func wsseDigest(nonceRaw []byte, created, password string) string {
	h := sha1.New()
	h.Write(nonceRaw)
	h.Write([]byte(created))
	h.Write([]byte(password))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}

// wssecHeader returns a <wsse:Security> header XML block with a fresh
// nonce/created/digest for the given credentials. Returns an empty string
// when username is empty (unauthenticated probes).
func wssecHeader(username, password string) string {
	if username == "" {
		return ""
	}

	nonceRaw := make([]byte, 16)
	_, _ = rand.Read(nonceRaw)
	created := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	nonce := base64.StdEncoding.EncodeToString(nonceRaw)
	digest := wsseDigest(nonceRaw, created, password)

	return `<wsse:Security s:mustUnderstand="1">` +
		`<wsse:UsernameToken>` +
		`<wsse:Username>` + xmlEscape(username) + `</wsse:Username>` +
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` +
		digest + `</wsse:Password>` +
		`<wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` +
		nonce + `</wsse:Nonce>` +
		`<wsu:Created>` + created + `</wsu:Created>` +
		`</wsse:UsernameToken>` +
		`</wsse:Security>`
}

// wsAddressingHeaders returns the WS-Addressing header block required by
// PullMessages. The block is fresh per call (unique MessageID).
func wsAddressingHeaders(to, action string) string {
	msgID := randomURN()
	return `<wsa:To s:mustUnderstand="1">` + xmlEscape(to) + `</wsa:To>` +
		`<wsa:Action s:mustUnderstand="1">` + action + `</wsa:Action>` +
		`<wsa:MessageID>` + msgID + `</wsa:MessageID>` +
		`<wsa:ReplyTo><wsa:Address>http://www.w3.org/2005/08/addressing/anonymous</wsa:Address></wsa:ReplyTo>`
}

// randomURN produces a unique urn:uuid string using crypto/rand.
func randomURN() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return fmt.Sprintf("urn:uuid:%08x-%04x-%04x-%04x-%012x",
		b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// xmlEscape escapes the five XML special characters in a string.
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
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
	// The ONVIF PullMessages response uses a two-level Message wrapping:
	// <wsnt:Message> contains <tt:Message> which contains <tt:Data>.
	// Using "Message>Data" as the path navigates through the inner element.
	type notificationMessage struct {
		Topic   string `xml:"Topic"`
		Message struct {
			Data struct {
				Items []struct {
					Name  string `xml:"Name,attr"`
					Value string `xml:"Value,attr"`
				} `xml:"SimpleItem"`
			} `xml:"Message>Data"`
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
	case strings.Contains(t, "digitalinput") || strings.Contains(t, "doorbell") ||
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
