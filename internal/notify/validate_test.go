package notify

import (
	"strings"
	"testing"
)

func TestValidateSubscriptionEndpoint(t *testing.T) {
	cases := []struct {
		name    string
		ep      string
		wantErr string // substring; empty means success
	}{
		{"fcm", "https://fcm.googleapis.com/fcm/send/abc", ""},
		{"apple", "https://web.push.apple.com/QACABC", ""},
		{"mozilla", "https://updates.push.services.mozilla.com/wpush/v2/abc", ""},

		{"http", "http://fcm.googleapis.com/x", "https"},
		{"ftp", "ftp://example.com/", "https"},
		{"unparseable", "http://[::1]:namedport/x", "parse"},
		{"empty", "", "empty"},
		{"too long", "https://example.com/" + strings.Repeat("a", 2100), "length"},

		{"loopback v4", "https://127.0.0.1/x", "loopback"},
		{"loopback v6", "https://[::1]/x", "loopback"},
		{"localhost", "https://localhost/x", "loopback"},
		{"10/8", "https://10.0.0.5/x", "private"},
		{"172.16/12", "https://172.20.1.1/x", "private"},
		{"192.168/16", "https://192.168.1.1/x", "private"},
		{"link-local v4", "https://169.254.1.1/x", "link-local"},
		{"link-local v6", "https://[fe80::1]/x", "link-local"},
		{"unique-local v6", "https://[fc00::1]/x", "private"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSubscriptionEndpoint(tc.ep)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected ok, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("expected error containing %q, got %v", tc.wantErr, err)
			}
		})
	}
}

func TestValidateSubscriptionKeys(t *testing.T) {
	// Valid: p256dh is 65 bytes (uncompressed P-256), auth is 16 bytes.
	validP256dh := "BNcRdreALRFXTkOOUHK1EtK2wtaz5Ry4YfYCA_0QTpQtUbVlUls0VJXg7A8u-Ts1XbjhazAkj7I99e8QcYP7DkM"
	validAuth := "tBHItJI5svbpez7KI4CCXg"

	if err := ValidateSubscriptionKeys(validP256dh, validAuth); err != nil {
		t.Fatalf("valid keys rejected: %v", err)
	}
	if err := ValidateSubscriptionKeys("short", validAuth); err == nil {
		t.Fatalf("expected error for short p256dh")
	}
	if err := ValidateSubscriptionKeys(validP256dh, "short"); err == nil {
		t.Fatalf("expected error for short auth")
	}
	if err := ValidateSubscriptionKeys("not!base64url", validAuth); err == nil {
		t.Fatalf("expected error for non-base64url p256dh")
	}
}
