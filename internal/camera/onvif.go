package camera

import (
	"encoding/xml"
	"fmt"
	"log/slog"
	"net"
	"os/exec"
	"strings"
	"time"
)

// DiscoveredCamera represents a camera found via ONVIF WS-Discovery.
type DiscoveredCamera struct {
	IP           string   `json:"ip"`
	Port         int      `json:"port"`
	Name         string   `json:"name"`
	Manufacturer string   `json:"manufacturer"`
	Model        string   `json:"model"`
	XAddrs       []string `json:"xaddrs"`
	Scopes       []string `json:"scopes"`
}

// StreamProfile represents an RTSP stream endpoint.
type StreamProfile struct {
	URL        string `json:"url"`
	Resolution string `json:"resolution"` // "main" or "sub"
}

const (
	wsDiscoveryMulticast = "239.255.255.250:3702"
	wsDiscoveryProbe     = `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"
            xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"
            xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery"
            xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
  <s:Header>
    <a:Action s:mustUnderstand="1">http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</a:Action>
    <a:MessageID>uuid:probe-message-001</a:MessageID>
    <a:ReplyTo>
      <a:Address>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:Address>
    </a:ReplyTo>
    <a:To s:mustUnderstand="1">urn:schemas-xmlsoap-org:ws:2005:04:discovery</a:To>
  </s:Header>
  <s:Body>
    <d:Probe>
      <d:Types>dn:NetworkVideoTransmitter</d:Types>
    </d:Probe>
  </s:Body>
</s:Envelope>`
)

// WS-Discovery XML response structures
type probeMatchEnvelope struct {
	XMLName xml.Name   `xml:"Envelope"`
	Body    probeBody  `xml:"Body"`
}

type probeBody struct {
	ProbeMatches probeMatches `xml:"ProbeMatches"`
}

type probeMatches struct {
	Matches []probeMatch `xml:"ProbeMatch"`
}

type probeMatch struct {
	XAddrs string `xml:"XAddrs"`
	Scopes string `xml:"Scopes"`
}

// DiscoverCameras sends a WS-Discovery probe and collects ONVIF camera responses.
func DiscoverCameras(timeout time.Duration) ([]DiscoveredCamera, error) {
	addr, err := net.ResolveUDPAddr("udp4", wsDiscoveryMulticast)
	if err != nil {
		return nil, fmt.Errorf("resolve multicast addr: %w", err)
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("listen udp: %w", err)
	}
	defer func() { _ = conn.Close() }()

	_, err = conn.WriteToUDP([]byte(wsDiscoveryProbe), addr)
	if err != nil {
		return nil, fmt.Errorf("send probe: %w", err)
	}

	slog.Info("sent WS-Discovery probe", "multicast", wsDiscoveryMulticast)

	if err := conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	seen := make(map[string]bool)
	var cameras []DiscoveredCamera
	buf := make([]byte, 65535)

	for {
		n, remoteAddr, err := conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				break
			}
			return nil, fmt.Errorf("read response: %w", err)
		}

		cam, err := parseProbeResponse(buf[:n], remoteAddr)
		if err != nil {
			slog.Debug("failed to parse probe response", "from", remoteAddr, "error", err)
			continue
		}

		if seen[cam.IP] {
			continue
		}
		seen[cam.IP] = true
		cameras = append(cameras, cam)

		slog.Info("discovered camera",
			"ip", cam.IP,
			"name", cam.Name,
			"manufacturer", cam.Manufacturer,
			"model", cam.Model,
		)
	}

	return cameras, nil
}

// parseProbeResponse extracts camera info from a WS-Discovery XML response.
func parseProbeResponse(data []byte, remoteAddr *net.UDPAddr) (DiscoveredCamera, error) {
	var envelope probeMatchEnvelope
	if err := xml.Unmarshal(data, &envelope); err != nil {
		return DiscoveredCamera{}, fmt.Errorf("unmarshal xml: %w", err)
	}

	if len(envelope.Body.ProbeMatches.Matches) == 0 {
		return DiscoveredCamera{}, fmt.Errorf("no probe matches in response")
	}

	match := envelope.Body.ProbeMatches.Matches[0]

	cam := DiscoveredCamera{
		IP:     remoteAddr.IP.String(),
		Port:   554,
		XAddrs: strings.Fields(match.XAddrs),
		Scopes: strings.Fields(match.Scopes),
	}

	// Extract device info from scopes
	for _, scope := range cam.Scopes {
		switch {
		case strings.Contains(scope, "onvif://www.onvif.org/name/"):
			cam.Name = extractScopeValue(scope, "name/")
		case strings.Contains(scope, "onvif://www.onvif.org/manufacturer/"):
			cam.Manufacturer = extractScopeValue(scope, "manufacturer/")
		case strings.Contains(scope, "onvif://www.onvif.org/model/"):
			cam.Model = extractScopeValue(scope, "model/")
		case strings.Contains(scope, "onvif://www.onvif.org/hardware/"):
			if cam.Model == "" {
				cam.Model = extractScopeValue(scope, "hardware/")
			}
		}
	}

	if cam.Name == "" {
		cam.Name = fmt.Sprintf("camera-%s", cam.IP)
	}

	return cam, nil
}

// extractScopeValue extracts the value portion from an ONVIF scope URI.
func extractScopeValue(scope, key string) string {
	idx := strings.Index(scope, key)
	if idx < 0 {
		return ""
	}
	value := scope[idx+len(key):]
	// URL-decode common patterns
	value = strings.ReplaceAll(value, "%20", " ")
	value = strings.ReplaceAll(value, "%2F", "/")
	return strings.TrimSpace(value)
}

// Known RTSP URL patterns per manufacturer.
var rtspPatterns = map[string][]struct {
	Path       string
	Resolution string
}{
	"tapo": {
		{"/stream1", "main"},
		{"/stream2", "sub"},
	},
	"tp-link": {
		{"/stream1", "main"},
		{"/stream2", "sub"},
	},
	"reolink": {
		{"/h264Preview_01_main", "main"},
		{"/h264Preview_01_sub", "sub"},
	},
	"hikvision": {
		{"/Streaming/Channels/101", "main"},
		{"/Streaming/Channels/102", "sub"},
	},
	"dahua": {
		{"/cam/realmonitor?channel=1&subtype=0", "main"},
		{"/cam/realmonitor?channel=1&subtype=1", "sub"},
	},
	"amcrest": {
		{"/cam/realmonitor?channel=1&subtype=0", "main"},
		{"/cam/realmonitor?channel=1&subtype=1", "sub"},
	},
	"generic": {
		{"/Streaming/Channels/101", "main"},
		{"/Streaming/Channels/102", "sub"},
		{"/stream1", "main"},
		{"/stream2", "sub"},
		{"/h264Preview_01_main", "main"},
		{"/h264Preview_01_sub", "sub"},
		{"/live/ch00_1", "main"},
		{"/live/ch00_0", "sub"},
	},
}

// ProbeRTSP tests common RTSP URL patterns for a camera and returns valid streams.
func ProbeRTSP(ip string, port int) ([]StreamProfile, error) {
	var profiles []StreamProfile

	// Try all generic patterns
	patterns := rtspPatterns["generic"]

	for _, p := range patterns {
		url := fmt.Sprintf("rtsp://%s:%d%s", ip, port, p.Path)
		if testRTSPURL(url) {
			profiles = append(profiles, StreamProfile{
				URL:        url,
				Resolution: p.Resolution,
			})
		}
	}

	return profiles, nil
}

// ProbeRTSPForBrand tests RTSP URL patterns specific to a camera brand.
func ProbeRTSPForBrand(ip string, port int, manufacturer string) ([]StreamProfile, error) {
	brand := strings.ToLower(manufacturer)

	patterns, ok := rtspPatterns[brand]
	if !ok {
		return ProbeRTSP(ip, port)
	}

	var profiles []StreamProfile
	for _, p := range patterns {
		url := fmt.Sprintf("rtsp://%s:%d%s", ip, port, p.Path)
		if testRTSPURL(url) {
			profiles = append(profiles, StreamProfile{
				URL:        url,
				Resolution: p.Resolution,
			})
		}
	}

	// Fall back to generic if brand-specific didn't find anything
	if len(profiles) == 0 {
		return ProbeRTSP(ip, port)
	}

	return profiles, nil
}

// testRTSPURL uses ffprobe to check if an RTSP URL is reachable.
func testRTSPURL(url string) bool {
	cmd := exec.Command("ffprobe",
		"-v", "quiet",
		"-rtsp_transport", "tcp",
		"-i", url,
		"-show_entries", "stream=codec_type",
		"-of", "csv=p=0",
	)

	// 3 second timeout for probe
	output, err := runWithTimeout(cmd, 3*time.Second)
	if err != nil {
		return false
	}

	return strings.Contains(string(output), "video")
}

// runWithTimeout runs a command with a timeout and returns its output.
func runWithTimeout(cmd *exec.Cmd, timeout time.Duration) ([]byte, error) {
	done := make(chan struct{})
	var output []byte
	var cmdErr error

	go func() {
		output, cmdErr = cmd.CombinedOutput()
		close(done)
	}()

	select {
	case <-done:
		return output, cmdErr
	case <-time.After(timeout):
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		return nil, fmt.Errorf("command timed out after %v", timeout)
	}
}

// GenerateConfig produces a YAML config snippet for discovered cameras.
func GenerateConfig(cameras []DiscoveredCamera) string {
	if len(cameras) == 0 {
		return "# No cameras discovered\n"
	}

	var b strings.Builder
	b.WriteString("cameras:\n")

	for _, cam := range cameras {
		name := sanitizeName(cam.Name)
		fmt.Fprintf(&b, "  - name: %s\n", name)
		fmt.Fprintf(&b, "    url: rtsp://user:pass@%s:%d/stream1  # adjust credentials and path\n", cam.IP, cam.Port)
		fmt.Fprintf(&b, "    record_url: rtsp://user:pass@%s:%d/stream1  # high-res stream\n", cam.IP, cam.Port)
		b.WriteString("    enabled: true\n")
		b.WriteString("    detect:\n")
		b.WriteString("      width: 640\n")
		b.WriteString("      height: 480\n")
		b.WriteString("      fps: 5\n")
		b.WriteString("    record:\n")
		b.WriteString("      width: 1920\n")
		b.WriteString("      height: 1080\n")
		b.WriteString("      fps: 15\n")

		if cam.Manufacturer != "" || cam.Model != "" {
			fmt.Fprintf(&b, "    # Discovered: %s %s (%s)\n", cam.Manufacturer, cam.Model, cam.IP)
		}
		b.WriteString("\n")
	}

	return b.String()
}

// sanitizeName converts a camera name to a config-friendly identifier.
func sanitizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, " ", "_")
	name = strings.ReplaceAll(name, "-", "_")

	var clean strings.Builder
	for _, c := range name {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' {
			clean.WriteRune(c)
		}
	}

	result := clean.String()
	if result == "" {
		return "camera"
	}
	return result
}
