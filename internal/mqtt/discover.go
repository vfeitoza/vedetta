package mqtt

import (
	"context"
	"log/slog"
	"time"

	"github.com/grandcat/zeroconf"
)

// Broker represents an MQTT broker found via mDNS.
type Broker struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int    `json:"port"`
}

// DiscoverBrokers scans the local network for MQTT brokers via mDNS.
func DiscoverBrokers(timeout time.Duration) ([]Broker, error) {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}

	resolver, err := zeroconf.NewResolver(nil)
	if err != nil {
		return nil, err
	}

	entries := make(chan *zeroconf.ServiceEntry)
	var brokers []Broker

	done := make(chan struct{})
	go func() {
		defer close(done)
		for entry := range entries {
			host := entry.HostName
			if len(entry.AddrIPv4) > 0 {
				host = entry.AddrIPv4[0].String()
			} else if len(entry.AddrIPv6) > 0 {
				host = entry.AddrIPv6[0].String()
			}
			name := entry.Instance
			if name == "" {
				name = host
			}
			brokers = append(brokers, Broker{
				Name: name,
				Host: host,
				Port: entry.Port,
			})
			slog.Debug("mDNS: found MQTT broker", "name", name, "host", host, "port", entry.Port)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	if err := resolver.Browse(ctx, "_mqtt._tcp", "local.", entries); err != nil {
		return nil, err
	}

	<-ctx.Done()
	close(entries)
	<-done

	return brokers, nil
}
