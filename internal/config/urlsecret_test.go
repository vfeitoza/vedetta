package config

import "testing"

// RTSP URLs embed credentials as userinfo (rtsp://user:pass@host/path).
// StripURLCredentials must remove the userinfo for safe display in the camera
// management UI while reporting whether any credentials were present, so the
// frontend can show a "credentials hidden" indicator without ever receiving
// the secret.
func TestStripURLCredentials(t *testing.T) {
	tests := []struct {
		name     string
		raw      string
		want     string
		wantCred bool
	}{
		{"user and password", "rtsp://admin:s3cret@cam.lan:554/stream1", "rtsp://cam.lan:554/stream1", true},
		{"username only", "rtsp://admin@cam.lan/stream1", "rtsp://cam.lan/stream1", true},
		{"no credentials", "rtsp://cam.lan:554/stream1", "rtsp://cam.lan:554/stream1", false},
		{"empty stays empty", "", "", false},
		{"preserves query and path", "rtsp://u:p@cam.lan/live?channel=1", "rtsp://cam.lan/live?channel=1", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, gotCred := StripURLCredentials(tt.raw)
			if got != tt.want || gotCred != tt.wantCred {
				t.Fatalf("StripURLCredentials(%q) = (%q, %v), want (%q, %v)",
					tt.raw, got, gotCred, tt.want, tt.wantCred)
			}
		})
	}
}

// An unparseable URL must not crash and must report no credentials: the caller
// will surface it to the UI unchanged, never as a leaked secret.
func TestStripURLCredentials_Unparseable(t *testing.T) {
	got, gotCred := StripURLCredentials("://not a url")
	if gotCred {
		t.Fatalf("unparseable URL must report no credentials, got cred=true (%q)", got)
	}
}

// MergeURLCredentials backs the write-only credential model: the UI edits the
// stripped URL and sends it back without userinfo. To preserve the stored
// secret ("placeholder = keep existing"), the server splices the old URL's
// userinfo back onto the new one. If the new URL carries its own userinfo the
// operator is changing the credentials, so it is used unchanged.
func TestMergeURLCredentials(t *testing.T) {
	tests := []struct {
		name   string
		newURL string
		oldURL string
		want   string
	}{
		{
			name:   "new url without creds inherits old creds",
			newURL: "rtsp://cam.lan:554/stream1",
			oldURL: "rtsp://admin:s3cret@cam.lan:554/stream1",
			want:   "rtsp://admin:s3cret@cam.lan:554/stream1",
		},
		{
			name:   "new url with creds is kept (operator changed them)",
			newURL: "rtsp://newuser:newpass@cam.lan/stream1",
			oldURL: "rtsp://admin:s3cret@cam.lan/stream1",
			want:   "rtsp://newuser:newpass@cam.lan/stream1",
		},
		{
			name:   "old url has no creds: new url unchanged",
			newURL: "rtsp://cam.lan/stream1",
			oldURL: "rtsp://cam.lan/stream1",
			want:   "rtsp://cam.lan/stream1",
		},
		{
			name:   "new path with inherited creds",
			newURL: "rtsp://cam.lan/stream2",
			oldURL: "rtsp://admin:s3cret@cam.lan/stream1",
			want:   "rtsp://admin:s3cret@cam.lan/stream2",
		},
		{
			name:   "empty new url stays empty",
			newURL: "",
			oldURL: "rtsp://admin:s3cret@cam.lan/stream1",
			want:   "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := MergeURLCredentials(tt.newURL, tt.oldURL); got != tt.want {
				t.Fatalf("MergeURLCredentials(%q, %q) = %q, want %q",
					tt.newURL, tt.oldURL, got, tt.want)
			}
		})
	}
}
