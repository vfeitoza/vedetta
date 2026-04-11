package camera

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"image/jpeg"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"

	"github.com/rvben/vedetta/internal/config"
	"github.com/rvben/vedetta/internal/media"
	"github.com/rvben/vedetta/internal/rtsp"
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
	XMLName xml.Name  `xml:"Envelope"`
	Body    probeBody `xml:"Body"`
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

// ProbeRTSPWithCredentials discovers RTSP streams using credentials.
// It first tries unauthenticated discovery, then injects credentials.
// If the camera requires auth even for RTSP Describe (common for Tapo,
// Reolink, etc.), it probes known URL patterns with credentials directly.
func ProbeRTSPWithCredentials(ip string, port int, manufacturer, username, password string) ([]StreamProfile, error) {
	brand := inferBrand(manufacturer)

	// Try unauthenticated first
	profiles, _ := ProbeRTSPForBrand(ip, port, brand)

	if len(profiles) > 0 {
		// Found streams without auth — inject credentials and verify
		var authed []StreamProfile
		for _, p := range profiles {
			u, err := url.Parse(p.URL)
			if err != nil {
				continue
			}
			u.User = url.UserPassword(username, password)
			if testRTSPURL(u.String()) {
				authed = append(authed, StreamProfile{URL: u.String(), Resolution: p.Resolution})
			}
		}
		if len(authed) == 0 {
			return nil, fmt.Errorf("authentication failed")
		}
		return authed, nil
	}

	// No streams found without auth — try patterns with credentials directly.
	// Many cameras require auth even for RTSP Describe.
	var patterns []struct {
		Path       string
		Resolution string
	}
	if p, ok := rtspPatterns[brand]; ok {
		patterns = p
	} else {
		patterns = rtspPatterns["generic"]
	}

	var authed []StreamProfile
	for _, p := range patterns {
		rtspURL := fmt.Sprintf("rtsp://%s:%s@%s:%d%s",
			url.PathEscape(username), url.PathEscape(password), ip, port, p.Path)
		if testRTSPURL(rtspURL) {
			authed = append(authed, StreamProfile{URL: rtspURL, Resolution: p.Resolution})
		}
	}

	if len(authed) == 0 {
		return nil, fmt.Errorf("authentication failed")
	}
	return authed, nil
}

// inferBrand guesses the camera brand from the manufacturer string or model name.
func inferBrand(manufacturer string) string {
	s := strings.ToLower(manufacturer)
	for brand := range rtspPatterns {
		if brand != "generic" && strings.Contains(s, brand) {
			return brand
		}
	}
	return s
}

// testRTSPURL uses gortsplib Describe to check if an RTSP URL is reachable.
func testRTSPURL(rtspURL string) bool {
	u, err := url.Parse(rtspURL)
	if err != nil {
		return false
	}

	proto := gortsplib.ProtocolTCP
	c := &gortsplib.Client{
		Scheme:       u.Scheme,
		Host:         u.Host,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,
		Protocol:     &proto,
	}

	err = c.Start()
	if err != nil {
		return false
	}
	defer c.Close()

	_, _, err = c.Describe(&base.URL{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Path:     u.Path,
		RawQuery: u.RawQuery,
		User:     u.User,
	})
	return err == nil
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

// GrabThumbnail connects to an RTSP URL, waits for one IDR frame,
// decodes it to JPEG, and returns the bytes. Times out after 10 seconds.
// Falls back to common HTTP snapshot URLs if RTSP decoding fails.
func GrabThumbnail(rtspURL string, quality int) ([]byte, error) {
	if quality <= 0 || quality > 100 {
		quality = 75
	}

	// Try RTSP IDR frame capture first
	jpegData, err := grabThumbnailRTSP(rtspURL, quality)
	if err == nil {
		return jpegData, nil
	}
	slog.Debug("RTSP thumbnail failed, trying HTTP snapshot", "url", rtsp.SanitizeURL(rtspURL), "error", err)

	// Extract credentials and host from the RTSP URL for HTTP fallback
	u, parseErr := url.Parse(rtspURL)
	if parseErr != nil {
		return nil, fmt.Errorf("RTSP thumbnail failed: %w; cannot parse URL for HTTP fallback", err)
	}
	host := u.Hostname()
	username := ""
	password := ""
	if u.User != nil {
		username = u.User.Username()
		password, _ = u.User.Password()
	}

	return grabThumbnailHTTP(host, username, password)
}

// grabThumbnailRTSP connects via RTSP, reads one IDR frame, decodes to JPEG.
func grabThumbnailRTSP(rtspURL string, quality int) ([]byte, error) {
	if !media.OpenH264Available() {
		return nil, fmt.Errorf("OpenH264 not available")
	}

	u, err := base.ParseURL(rtspURL)
	if err != nil {
		return nil, fmt.Errorf("parse RTSP URL: %w", err)
	}

	dialer := &net.Dialer{Timeout: 5 * time.Second}
	proto := gortsplib.ProtocolTCP
	client := &gortsplib.Client{
		Scheme:       u.Scheme,
		Host:         u.Host,
		DialContext:  dialer.DialContext,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 3 * time.Second,
		Protocol:     &proto,
	}

	if err := client.Start(); err != nil {
		return nil, fmt.Errorf("RTSP connect: %w", err)
	}
	defer client.Close()

	desc, _, err := client.Describe(u)
	if err != nil {
		return nil, fmt.Errorf("RTSP describe: %w", err)
	}

	// Find H264 format
	var h264Format *format.H264
	for _, m := range desc.Medias {
		for _, f := range m.Formats {
			if hf, ok := f.(*format.H264); ok {
				h264Format = hf
				break
			}
		}
		if h264Format != nil {
			break
		}
	}
	if h264Format == nil {
		return nil, fmt.Errorf("no H264 track found")
	}

	rtpDecoder, err := h264Format.CreateDecoder()
	if err != nil {
		return nil, fmt.Errorf("create RTP decoder: %w", err)
	}

	h264Dec := media.NewH264Decoder()
	if h264Dec == nil {
		return nil, fmt.Errorf("failed to create H264 decoder")
	}
	defer h264Dec.Close()

	sps := h264Format.SPS
	pps := h264Format.PPS

	result := make(chan []byte, 1)
	errCh := make(chan error, 1)

	// SetupAll must run before OnPacketRTPAny: gortsplib binds packet
	// handlers to the RTSP session created in SetupAll, and some peers
	// (notably Tapo C200) drop packets delivered to handlers attached
	// before the session exists.
	if err := client.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		return nil, fmt.Errorf("RTSP setup: %w", err)
	}

	client.OnPacketRTPAny(func(_ *description.Media, _ format.Format, pkt *rtp.Packet) {
		au, decErr := rtpDecoder.Decode(pkt)
		if decErr != nil {
			return
		}

		// Update SPS/PPS from in-band parameters
		for _, nalu := range au {
			if len(nalu) == 0 {
				continue
			}
			typ := h264.NALUType(nalu[0] & 0x1F)
			switch typ {
			case h264.NALUTypeSPS:
				sps = nalu
			case h264.NALUTypePPS:
				pps = nalu
			}
		}

		if !h264.IsRandomAccess(au) {
			return
		}
		if sps == nil {
			return
		}

		// Build NAL stream with start codes
		var nalStream []byte
		startCode := []byte{0, 0, 0, 1}

		hasSPS := false
		for _, nalu := range au {
			if len(nalu) > 0 && h264.NALUType(nalu[0]&0x1F) == h264.NALUTypeSPS {
				hasSPS = true
				break
			}
		}
		if !hasSPS {
			nalStream = append(nalStream, startCode...)
			nalStream = append(nalStream, sps...)
			if pps != nil {
				nalStream = append(nalStream, startCode...)
				nalStream = append(nalStream, pps...)
			}
		}

		for _, nalu := range au {
			if len(nalu) == 0 {
				continue
			}
			nalStream = append(nalStream, startCode...)
			nalStream = append(nalStream, nalu...)
		}

		ycbcr := h264Dec.Decode(nalStream)
		if ycbcr == nil {
			return
		}

		var buf bytes.Buffer
		if encErr := jpeg.Encode(&buf, ycbcr, &jpeg.Options{Quality: quality}); encErr != nil {
			select {
			case errCh <- fmt.Errorf("JPEG encode: %w", encErr):
			default:
			}
			return
		}

		select {
		case result <- buf.Bytes():
		default:
		}
	})

	if _, err := client.Play(nil); err != nil {
		return nil, fmt.Errorf("RTSP play: %w", err)
	}

	timeout := time.NewTimer(8 * time.Second)
	defer timeout.Stop()

	select {
	case data := <-result:
		return data, nil
	case err := <-errCh:
		return nil, err
	case <-timeout.C:
		return nil, fmt.Errorf("timeout waiting for IDR frame")
	}
}

// httpSnapshotPaths are common HTTP snapshot endpoints exposed by IP cameras.
var httpSnapshotPaths = []string{
	"/snap.jpg",
	"/cgi-bin/snapshot.cgi",
	"/ISAPI/Streaming/channels/101/picture",
}

// grabThumbnailHTTP tries common HTTP snapshot endpoints with digest/basic auth.
func grabThumbnailHTTP(host, username, password string) ([]byte, error) {
	httpClient := &http.Client{Timeout: 2 * time.Second}

	for _, path := range httpSnapshotPaths {
		snapshotURL := fmt.Sprintf("http://%s%s", host, path)
		req, err := http.NewRequest("GET", snapshotURL, nil)
		if err != nil {
			continue
		}
		if username != "" {
			req.SetBasicAuth(username, password)
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			continue
		}

		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 5*1024*1024))
		resp.Body.Close()

		if readErr != nil || resp.StatusCode != http.StatusOK {
			continue
		}

		// Verify the response looks like a JPEG
		if len(body) > 2 && body[0] == 0xFF && body[1] == 0xD8 {
			slog.Debug("HTTP snapshot succeeded", "url", snapshotURL)
			return body, nil
		}
	}

	return nil, fmt.Errorf("no HTTP snapshot endpoint responded with a JPEG")
}

// sanitizeName converts a camera name to a config-friendly identifier.
func sanitizeName(name string) string {
	return config.SanitizeCameraName(name)
}
