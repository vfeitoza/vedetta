// Package netguard restricts user-supplied connection targets (the RTSP and
// MQTT "test connection" endpoints) to addresses that are safe to dial.
//
// It is deliberately NOT a blanket private-IP filter: an NVR's whole purpose is
// to reach cameras on RFC1918 ranges and brokers on loopback, so those stay
// allowed. It blocks only the ranges that are never a legitimate camera or
// broker yet are dangerous as an SSRF pivot: link-local (which covers the cloud
// metadata endpoint at 169.254.169.254), link-local multicast, and the
// unspecified address.
package netguard

import (
	"context"
	"fmt"
	"net"
	"strings"
)

// CheckHost resolves host (a hostname or IP literal) and returns an error if it,
// or any address it resolves to, falls in a blocked range. A blank host is an
// error. IP literals short-circuit the resolver, so no network call is made for
// them.
func CheckHost(ctx context.Context, host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("host is required")
	}

	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("resolve %q: %w", host, err)
	}
	if len(ips) == 0 {
		return fmt.Errorf("resolve %q: no addresses", host)
	}

	for _, ipa := range ips {
		if reason := blockedReason(ipa.IP); reason != "" {
			return fmt.Errorf("connection to %s (%s) is not allowed: %s", host, ipa.IP, reason)
		}
	}
	return nil
}

// blockedReason classifies a single IP. An empty string means the address may
// be dialed.
func blockedReason(ip net.IP) string {
	switch {
	case ip.IsUnspecified():
		return "unspecified address"
	case ip.IsLinkLocalUnicast():
		return "link-local address (cloud metadata range)"
	case ip.IsLinkLocalMulticast(), ip.IsInterfaceLocalMulticast():
		return "link-local multicast address"
	}
	return ""
}
