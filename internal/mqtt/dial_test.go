package mqtt

import (
	"net"
	"net/url"
	"strings"
	"testing"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
)

// The custom broker opener must apply the SSRF policy to the real broker
// address. Routing the dial through this opener (instead of paho's default
// path) also keeps the guard effective when ALL_PROXY would otherwise wrap the
// dialer with a SOCKS proxy that the Control hook never sees.
func TestGuardedDialBroker_BlocksLinkLocal(t *testing.T) {
	uri := &url.URL{Scheme: "tcp", Host: "169.254.169.254:1883"}
	conn, err := guardedDialBroker(uri, pahomqtt.ClientOptions{})
	if conn != nil {
		conn.Close()
		t.Fatal("expected no connection to a link-local broker")
	}
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("expected 'not allowed' error, got %v", err)
	}
}

func TestGuardedDialBroker_AllowsLoopback(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	uri := &url.URL{Scheme: "tcp", Host: ln.Addr().String()}
	conn, err := guardedDialBroker(uri, pahomqtt.ClientOptions{})
	if err != nil {
		t.Fatalf("loopback broker must be allowed, got %v", err)
	}
	conn.Close()
}
