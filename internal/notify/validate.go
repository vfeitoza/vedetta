package notify

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Subscription body size caps.
const (
	MaxEndpointLength  = 2048
	MaxUserAgentLength = 512
)

// ValidateSubscriptionEndpoint enforces HTTPS-only and rejects private,
// loopback, and link-local destinations — without this, registering a
// subscription would turn the dispatcher into an SSRF gadget.
//
// Resolution is done at registration time against the default resolver.
// If the host later changes DNS, the dispatcher still sends to the bare
// hostname; that's acceptable because legitimate push services don't
// rotate their A records to private space, and an attacker who can do so
// already has a stronger position.
func ValidateSubscriptionEndpoint(endpoint string) error {
	if endpoint == "" {
		return errors.New("endpoint is empty")
	}
	if len(endpoint) > MaxEndpointLength {
		return fmt.Errorf("endpoint length %d exceeds max %d", len(endpoint), MaxEndpointLength)
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return fmt.Errorf("parse endpoint: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("endpoint must use https, got %q", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return errors.New("endpoint host is empty")
	}
	if strings.EqualFold(host, "localhost") {
		return errors.New("loopback hostname rejected")
	}
	// Resolve to concrete IPs. Reject if ANY address is disallowed.
	// IP-literal hosts resolve trivially through net.LookupIP as well.
	var addrs []net.IP
	if ip := net.ParseIP(host); ip != nil {
		addrs = []net.IP{ip}
	} else {
		resolved, err := net.LookupIP(host)
		if err != nil {
			return fmt.Errorf("resolve endpoint host: %w", err)
		}
		addrs = resolved
	}
	for _, ip := range addrs {
		if err := checkIP(ip); err != nil {
			return err
		}
	}
	return nil
}

func checkIP(ip net.IP) error {
	if ip.IsLoopback() {
		return errors.New("loopback address rejected")
	}
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return errors.New("link-local address rejected")
	}
	if ip.IsPrivate() {
		return errors.New("private (RFC1918 / ULA) address rejected")
	}
	return nil
}

// ValidateSubscriptionKeys checks that p256dh decodes to 65 bytes
// (uncompressed P-256 point) and auth to 16 bytes. Accepts both
// base64url (no padding) and standard base64 (with padding) forms,
// since browsers have historically differed on which they send.
func ValidateSubscriptionKeys(p256dh, auth string) error {
	pub, err := base64.RawURLEncoding.DecodeString(p256dh)
	if err != nil {
		pub, err = base64.StdEncoding.DecodeString(p256dh)
		if err != nil {
			return fmt.Errorf("p256dh not base64url: %w", err)
		}
	}
	if len(pub) != 65 {
		return fmt.Errorf("p256dh decodes to %d bytes, want 65", len(pub))
	}
	a, err := base64.RawURLEncoding.DecodeString(auth)
	if err != nil {
		a, err = base64.StdEncoding.DecodeString(auth)
		if err != nil {
			return fmt.Errorf("auth not base64url: %w", err)
		}
	}
	if len(a) != 16 {
		return fmt.Errorf("auth decodes to %d bytes, want 16", len(a))
	}
	return nil
}
