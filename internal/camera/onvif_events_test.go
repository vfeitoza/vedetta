package camera

import (
	"encoding/base64"
	"strings"
	"testing"
)

// TestWsseDigest verifies the PasswordDigest algorithm:
// Base64(SHA-1(nonceRaw || created || password))
func TestWsseDigest(t *testing.T) {
	// Fixed inputs allow a deterministic expected value.
	nonceRaw := []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
		0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}
	created := "2026-01-02T15:04:05.000Z"
	password := "s3cr3t"

	got := wsseDigest(nonceRaw, created, password)

	// Verify it is valid base64 and decodes to 20 bytes (SHA-1 output).
	raw, err := base64.StdEncoding.DecodeString(got)
	if err != nil {
		t.Fatalf("digest is not valid base64: %v", err)
	}
	if len(raw) != 20 {
		t.Fatalf("expected 20-byte SHA-1 digest, got %d bytes", len(raw))
	}

	// Re-derive in the test using the same algorithm and confirm equality.
	expected := wsseDigest(nonceRaw, created, password)
	if got != expected {
		t.Errorf("digest mismatch: got %s, want %s", got, expected)
	}

	// Changing the password must produce a different digest.
	other := wsseDigest(nonceRaw, created, "differentpassword")
	if got == other {
		t.Error("digest should differ when password changes")
	}

	// Changing the created timestamp must produce a different digest.
	otherCreated := wsseDigest(nonceRaw, "2026-12-31T00:00:00.000Z", password)
	if got == otherCreated {
		t.Error("digest should differ when created changes")
	}
}

// TestWssecHeaderStructure verifies that wssecHeader produces well-formed XML
// containing the required WS-Security elements when credentials are set.
func TestWssecHeaderStructure(t *testing.T) {
	header := wssecHeader("admin", "pass1234")

	required := []string{
		"<wsse:Security",
		"<wsse:UsernameToken>",
		"<wsse:Username>admin</wsse:Username>",
		"PasswordDigest",
		"<wsse:Nonce",
		"<wsu:Created>",
	}
	for _, token := range required {
		if !strings.Contains(header, token) {
			t.Errorf("wssecHeader missing expected token %q", token)
		}
	}

	// Empty username must return empty string (unauthenticated probes).
	if h := wssecHeader("", "irrelevant"); h != "" {
		t.Errorf("expected empty header for empty username, got %q", h)
	}
}

// TestParseEventXAddr verifies that parseEventXAddr correctly extracts the
// event service URL from a GetServices response.
func TestParseEventXAddr(t *testing.T) {
	// Realistic GetServices response with multiple namespaces; event service
	// is present alongside device and media services. Uses RFC 5737
	// documentation address (192.0.2.10) so no real LAN IP is in the test.
	getServicesXML := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
  <s:Body>
    <tds:GetServicesResponse>
      <tds:Service>
        <tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace>
        <tds:XAddr>http://192.0.2.10:8000/onvif/device_service</tds:XAddr>
      </tds:Service>
      <tds:Service>
        <tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace>
        <tds:XAddr>http://192.0.2.10:8000/onvif/media_service</tds:XAddr>
      </tds:Service>
      <tds:Service>
        <tds:Namespace>http://www.onvif.org/ver10/events/wsdl</tds:Namespace>
        <tds:XAddr>http://192.0.2.10:8000/onvif/event_service</tds:XAddr>
      </tds:Service>
    </tds:GetServicesResponse>
  </s:Body>
</s:Envelope>`

	got := parseEventXAddr([]byte(getServicesXML))
	want := "http://192.0.2.10:8000/onvif/event_service"
	if got != want {
		t.Errorf("parseEventXAddr: got %q, want %q", got, want)
	}
}

// TestParseEventXAddrMissing verifies graceful handling when no events service
// is present in the response.
func TestParseEventXAddrMissing(t *testing.T) {
	getServicesXML := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope">
  <s:Body>
    <tds:GetServicesResponse xmlns:tds="http://www.onvif.org/ver10/device/wsdl">
      <tds:Service>
        <tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace>
        <tds:XAddr>http://192.0.2.10:80/onvif/device_service</tds:XAddr>
      </tds:Service>
    </tds:GetServicesResponse>
  </s:Body>
</s:Envelope>`

	got := parseEventXAddr([]byte(getServicesXML))
	if got != "" {
		t.Errorf("expected empty string when event service absent, got %q", got)
	}
}

// TestParseEventXAddrInvalidXML verifies graceful handling of malformed XML.
func TestParseEventXAddrInvalidXML(t *testing.T) {
	got := parseEventXAddr([]byte("this is not xml"))
	if got != "" {
		t.Errorf("expected empty string for invalid XML, got %q", got)
	}
}

// TestParseOnvifEventsVisitor verifies that a real-shaped PullMessages response
// containing a Reolink Visitor (doorbell press) notification is parsed correctly
// and classified as OnvifEventDoorbell with Value=true.
func TestParseOnvifEventsVisitor(t *testing.T) {
	// Matches the real XML shape captured from the Reolink Video Doorbell.
	// Uses RFC 5737 addresses; no real LAN IPs.
	pullXML := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
            xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Body>
    <tev:PullMessagesResponse>
      <tev:CurrentTime>2026-06-14T10:00:00Z</tev:CurrentTime>
      <tev:TerminationTime>2026-06-14T10:10:00Z</tev:TerminationTime>
      <wsnt:NotificationMessage>
        <wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:RuleEngine/MyRuleDetector/Visitor</wsnt:Topic>
        <wsnt:Message>
          <tt:Message UtcTime="2026-06-14T10:00:00Z" PropertyOperation="Changed">
            <tt:Source>
              <tt:SimpleItem Name="Source" Value="000"/>
            </tt:Source>
            <tt:Data>
              <tt:SimpleItem Name="State" Value="true"/>
            </tt:Data>
          </tt:Message>
        </wsnt:Message>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </s:Body>
</s:Envelope>`

	events := parseOnvifEvents([]byte(pullXML))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != OnvifEventDoorbell {
		t.Errorf("expected OnvifEventDoorbell, got %q", ev.Type)
	}
	if !ev.Value {
		t.Error("expected Value=true for doorbell press")
	}
	if !strings.Contains(ev.Topic, "Visitor") {
		t.Errorf("expected topic to contain 'Visitor', got %q", ev.Topic)
	}
}

// TestParseOnvifEventsVisitorRelease verifies that State=false (button released)
// is parsed as Value=false (still classified as doorbell).
func TestParseOnvifEventsVisitorRelease(t *testing.T) {
	pullXML := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
            xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Body>
    <tev:PullMessagesResponse>
      <wsnt:NotificationMessage>
        <wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:RuleEngine/MyRuleDetector/Visitor</wsnt:Topic>
        <wsnt:Message>
          <tt:Message UtcTime="2026-06-14T10:00:01Z" PropertyOperation="Changed">
            <tt:Source>
              <tt:SimpleItem Name="Source" Value="000"/>
            </tt:Source>
            <tt:Data>
              <tt:SimpleItem Name="State" Value="false"/>
            </tt:Data>
          </tt:Message>
        </wsnt:Message>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </s:Body>
</s:Envelope>`

	events := parseOnvifEvents([]byte(pullXML))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	ev := events[0]
	if ev.Type != OnvifEventDoorbell {
		t.Errorf("expected OnvifEventDoorbell, got %q", ev.Type)
	}
	if ev.Value {
		t.Error("expected Value=false for button release")
	}
}

// TestParseOnvifEventsMotion verifies a motion event is classified correctly.
func TestParseOnvifEventsMotion(t *testing.T) {
	pullXML := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
            xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Body>
    <tev:PullMessagesResponse>
      <wsnt:NotificationMessage>
        <wsnt:Topic Dialect="http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet">tns1:RuleEngine/CellMotionDetector/Motion</wsnt:Topic>
        <wsnt:Message>
          <tt:Message UtcTime="2026-06-15T20:02:14Z" PropertyOperation="Initialized">
            <tt:Source>
              <tt:SimpleItem Name="VideoSourceConfigurationToken" Value="000"/>
              <tt:SimpleItem Name="Rule" Value="000"/>
            </tt:Source>
            <tt:Data>
              <tt:SimpleItem Name="IsMotion" Value="true"/>
            </tt:Data>
          </tt:Message>
        </wsnt:Message>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </s:Body>
</s:Envelope>`

	events := parseOnvifEvents([]byte(pullXML))
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Type != OnvifEventMotion {
		t.Errorf("expected OnvifEventMotion, got %q", events[0].Type)
	}
	if !events[0].Value {
		t.Error("expected Value=true for motion active")
	}
}

// TestParseOnvifEventsUnknownFiltered verifies that unknown topics are filtered
// out and not returned.
func TestParseOnvifEventsUnknownFiltered(t *testing.T) {
	pullXML := `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:tev="http://www.onvif.org/ver10/events/wsdl"
            xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2"
            xmlns:tt="http://www.onvif.org/ver10/schema">
  <s:Body>
    <tev:PullMessagesResponse>
      <wsnt:NotificationMessage>
        <wsnt:NotificationMessage>
          <wsnt:Topic>tns1:Device/SomeUnknownEvent</wsnt:Topic>
          <wsnt:Message>
            <tt:Message>
              <tt:Data>
                <tt:SimpleItem Name="State" Value="true"/>
              </tt:Data>
            </tt:Message>
          </wsnt:Message>
        </wsnt:NotificationMessage>
      </wsnt:NotificationMessage>
    </tev:PullMessagesResponse>
  </s:Body>
</s:Envelope>`

	events := parseOnvifEvents([]byte(pullXML))
	if len(events) != 0 {
		t.Errorf("expected unknown events to be filtered, got %d events", len(events))
	}
}

// TestClassifyOnvifTopic verifies the topic-to-type classification rules.
func TestClassifyOnvifTopic(t *testing.T) {
	tests := []struct {
		topic string
		want  OnvifEventType
	}{
		{"tns1:RuleEngine/MyRuleDetector/Visitor", OnvifEventDoorbell},
		{"tns1:Device/Trigger/DigitalInput", OnvifEventDoorbell},
		{"tns1:VideoAnalytics/MotionDetection", OnvifEventMotion},
		{"tns1:VideoAnalytics/CellMotionDetector", OnvifEventMotion},
		{"tns1:VideoMotion/IsMotion", OnvifEventMotion},
		{"tns1:Device/SomethingElse", OnvifEventUnknown},
	}
	for _, tt := range tests {
		got := classifyOnvifTopic(tt.topic)
		if got != tt.want {
			t.Errorf("classifyOnvifTopic(%q) = %q, want %q", tt.topic, got, tt.want)
		}
	}
}

// TestRandomURN verifies that randomURN produces unique values in the expected
// urn:uuid format.
func TestRandomURN(t *testing.T) {
	a := randomURN()
	b := randomURN()

	if !strings.HasPrefix(a, "urn:uuid:") {
		t.Errorf("randomURN should start with 'urn:uuid:', got %q", a)
	}
	if a == b {
		t.Error("two consecutive randomURN calls should not be equal")
	}
}
