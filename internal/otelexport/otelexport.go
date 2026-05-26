// Package otelexport holds the OTLP endpoint classification shared by the
// tracing and logging subsystems. It is intentionally pure: it reads no
// environment and encodes no config-vs-env precedence. Each signal layers its
// own resolution policy on top.
package otelexport

import "strings"

// Endpoint describes how an OTLP exporter is addressed. When AsURL is true,
// Value is a full URL whose scheme decides plaintext vs TLS (Insecure is then
// irrelevant). Otherwise Value is a scheme-less host:port and Insecure selects
// plaintext.
type Endpoint struct {
	Value    string
	AsURL    bool
	Insecure bool
}

// Classify interprets a non-empty endpoint string. A value containing "://" is
// treated as a full URL (Insecure forced false, since the scheme decides);
// otherwise it is a scheme-less host:port carrying the Insecure flag.
func Classify(endpoint string, insecure bool) Endpoint {
	if strings.Contains(endpoint, "://") {
		return Endpoint{Value: endpoint, AsURL: true}
	}
	return Endpoint{Value: endpoint, AsURL: false, Insecure: insecure}
}

// ParseProtocol normalizes an OTLP protocol string (trim + lower-case). It
// leaves the empty string empty so callers can apply their own default.
func ParseProtocol(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
