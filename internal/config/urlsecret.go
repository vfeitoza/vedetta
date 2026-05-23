package config

import "net/url"

// StripURLCredentials removes the userinfo (user:password) from a URL so it can
// be displayed in the management UI without leaking the secret, and reports
// whether any credentials were present. The rest of the URL (scheme, host,
// path, query) is preserved verbatim so the stripped value round-trips back
// through MergeURLCredentials. An unparseable URL is returned unchanged with
// hasCredentials=false.
func StripURLCredentials(raw string) (clean string, hasCredentials bool) {
	if raw == "" {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw, false
	}
	if u.User == nil {
		return raw, false
	}
	u.User = nil
	return u.String(), true
}

// MergeURLCredentials reconstitutes a URL's credentials for the write-only
// credential model. The UI edits the credential-stripped URL and sends it back
// without userinfo; to keep the stored secret, the old URL's userinfo is
// spliced onto the new URL. If the new URL already carries userinfo the
// operator is setting new credentials, so it is returned unchanged. An empty
// or unparseable new URL is returned as-is.
func MergeURLCredentials(newURL, oldURL string) string {
	if newURL == "" {
		return newURL
	}
	nu, err := url.Parse(newURL)
	if err != nil {
		return newURL
	}
	if nu.User != nil {
		// Operator supplied fresh credentials; keep them.
		return newURL
	}
	ou, err := url.Parse(oldURL)
	if err != nil || ou.User == nil {
		return newURL
	}
	nu.User = ou.User
	return nu.String()
}
