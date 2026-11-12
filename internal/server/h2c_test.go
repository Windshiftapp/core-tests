//go:build test

package server

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"sync"
	"testing"
)

func TestConfigureServerProtocols(t *testing.T) {
	tests := []struct {
		name        string
		enableHTTPS bool
		h2cEnabled  bool
		wantH2C     bool
	}{
		{name: "plain listener without h2c stays HTTP/1", wantH2C: false},
		{name: "plain listener with h2c accepts unencrypted HTTP/2", h2cEnabled: true, wantH2C: true},
		{name: "TLS listener negotiates h2 via ALPN", enableHTTPS: true, h2cEnabled: true, wantH2C: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := &http.Server{}
			configureServerProtocols(server, tt.enableHTTPS, tt.h2cEnabled)

			if !tt.wantH2C {
				if server.Protocols != nil {
					t.Fatalf("Protocols = %v, want nil (library defaults)", server.Protocols)
				}
				return
			}
			if server.Protocols == nil {
				t.Fatal("Protocols = nil, want HTTP/1 + unencrypted HTTP/2")
			}
			if !server.Protocols.HTTP1() || !server.Protocols.UnencryptedHTTP2() {
				t.Fatalf("Protocols = %v, want HTTP1 + UnencryptedHTTP2", server.Protocols)
			}
		})
	}
}

// openStream issues one request and reads the first SSE frame, leaving the body
// open so the stream stays live. It reports the negotiated protocol and the
// client-side connection address so the caller can assert multiplexing.
func openStream(t *testing.T, client *http.Client, url string, gotConn func(net.Addr)) (string, string, io.ReadCloser, error) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", "", nil, err
	}
	trace := &httptrace.ClientTrace{
		GotConn: func(info httptrace.GotConnInfo) { gotConn(info.Conn.LocalAddr()) },
	}
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
	resp, err := client.Do(req)
	if err != nil {
		return "", "", nil, err
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	return resp.Proto, line, resp.Body, err
}

// TestH2CMultiplexesConcurrentStreams proves the wiring accepts h2c and that two
// concurrent long-lived streams share one connection — the property that keeps
// the proxy -> app socket count at one per tab.
func TestH2CMultiplexesConcurrentStreams(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "event: connected\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})

	server := &http.Server{Handler: handler}
	configureServerProtocols(server, false, true)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })
	go func() { _ = server.Serve(ln) }()

	// A client with h2c only forces cleartext HTTP/2 (Go uses it for http:// URLs
	// only when UnencryptedHTTP2 is set and HTTP/1 is not).
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	client := &http.Client{Transport: &http.Transport{Protocols: protocols}}
	t.Cleanup(client.CloseIdleConnections)

	var mu sync.Mutex
	var connAddrs []string
	record := func(addr net.Addr) {
		mu.Lock()
		connAddrs = append(connAddrs, addr.String())
		mu.Unlock()
	}
	url := "http://" + ln.Addr().String() + "/events"

	// The first stream establishes the connection and stays open.
	proto, frame, firstBody, err := openStream(t, client, url, record)
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	defer firstBody.Close()
	if proto != "HTTP/2.0" {
		t.Fatalf("first stream proto = %s, want HTTP/2.0", proto)
	}
	if !strings.Contains(frame, "event: connected") {
		t.Fatalf("first stream frame = %q", frame)
	}

	// The second stream must reuse the same connection.
	proto, frame, secondBody, err := openStream(t, client, url, record)
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	defer secondBody.Close()
	if proto != "HTTP/2.0" {
		t.Fatalf("second stream proto = %s, want HTTP/2.0", proto)
	}
	if !strings.Contains(frame, "event: connected") {
		t.Fatalf("second stream frame = %q", frame)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(connAddrs) != 2 {
		t.Fatalf("GotConn called %d times, want 2", len(connAddrs))
	}
	if connAddrs[0] != connAddrs[1] {
		t.Fatalf("streams used different connections (%v), want one", connAddrs)
	}
}
