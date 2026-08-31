package services

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/url"
	"testing"
)

func stubEndpointLookup(t *testing.T, ips map[string][]net.IP) {
	t.Helper()
	original := lookupEndpointIPs
	lookupEndpointIPs = func(_ context.Context, host string) ([]net.IP, error) {
		if resolved, ok := ips[host]; ok {
			return resolved, nil
		}
		return nil, &net.DNSError{IsNotFound: true, Name: host}
	}
	t.Cleanup(func() { lookupEndpointIPs = original })
}

func TestValidatePushEndpoint(t *testing.T) {
	stubEndpointLookup(t, map[string][]net.IP{
		"push.example.com":     {net.ParseIP("93.184.216.34")},
		"mixed.example.com":    {net.ParseIP("93.184.216.34"), net.ParseIP("10.0.0.8")},
		"internal.example.com": {net.ParseIP("192.168.1.10")},
		"v6.example.com":       {net.ParseIP("2606:2800:220:1:248:1893:25c8:1946")},
	})

	tests := []struct {
		name     string
		endpoint string
		wantErr  bool
	}{
		{name: "public https hostname resolves public", endpoint: "https://push.example.com/send/abc", wantErr: false},
		{name: "public https IPv6 hostname", endpoint: "https://v6.example.com/send/abc", wantErr: false},
		{name: "public IP literal", endpoint: "https://93.184.216.34/send/abc", wantErr: false},
		{name: "explicit port 443", endpoint: "https://push.example.com:443/send/abc", wantErr: false},

		{name: "plain http rejected", endpoint: "http://push.example.com/send/abc", wantErr: true},
		{name: "missing scheme rejected", endpoint: "push.example.com/send/abc", wantErr: true},
		{name: "empty endpoint rejected", endpoint: "", wantErr: true},
		{name: "userinfo rejected", endpoint: "https://user:pass@push.example.com/send", wantErr: true},
		{name: "invalid port rejected", endpoint: "https://push.example.com:notaport/send", wantErr: true},
		{name: "unresolvable host rejected", endpoint: "https://does-not-exist.example/send", wantErr: true},
		{name: "hostname resolving private rejected", endpoint: "https://internal.example.com/send", wantErr: true},
		{name: "hostname resolving mixed rejected", endpoint: "https://mixed.example.com/send", wantErr: true},

		{name: "loopback v4 rejected", endpoint: "https://127.0.0.1/send", wantErr: true},
		{name: "private rfc1918 rejected", endpoint: "https://10.1.2.3/send", wantErr: true},
		{name: "link-local metadata rejected", endpoint: "https://169.254.169.254/latest/meta-data", wantErr: true},
		{name: "cgnat rejected", endpoint: "https://100.64.0.1/send", wantErr: true},
		{name: "benchmark range rejected", endpoint: "https://198.18.0.1/send", wantErr: true},
		{name: "reserved class e rejected", endpoint: "https://240.0.0.1/send", wantErr: true},
		{name: "unspecified rejected", endpoint: "https://0.0.0.0/send", wantErr: true},
		{name: "loopback v6 rejected", endpoint: "https://[::1]/send", wantErr: true},
		{name: "unique-local v6 rejected", endpoint: "https://[fd00::1]/send", wantErr: true},
		{name: "link-local v6 rejected", endpoint: "https://[fe80::1]/send", wantErr: true},
		{name: "ipv4-mapped v6 loopback rejected", endpoint: "https://[::ffff:127.0.0.1]/send", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validatePushEndpoint(tt.endpoint)
			if tt.wantErr && !errors.Is(err, ErrInvalidEndpoint) {
				t.Fatalf("validatePushEndpoint(%q) = %v, want ErrInvalidEndpoint", tt.endpoint, err)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validatePushEndpoint(%q) = %v, want nil", tt.endpoint, err)
			}
		})
	}
}

func TestPushServiceSubscribeRejectsInvalidEndpoint(t *testing.T) {
	db := newPushTestDB(t)
	service := newPushService(db, enabledPushConfig(), DefaultPushServiceConfig(), nil)

	err := service.Subscribe(1, "https://169.254.169.254/latest/meta-data", "auth", "p256dh", "agent")
	if !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("Subscribe() = %v, want ErrInvalidEndpoint", err)
	}
	subs, err := service.List(1)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(subs) != 0 {
		t.Fatalf("rejected endpoint was persisted: %+v", subs)
	}
}

func TestPushServiceSubscribeAcceptsPublicEndpoint(t *testing.T) {
	stubEndpointLookup(t, map[string][]net.IP{
		"push.example.com": {net.ParseIP("93.184.216.34")},
	})
	db := newPushTestDB(t)
	service := newPushService(db, enabledPushConfig(), DefaultPushServiceConfig(), nil)

	err := service.Subscribe(1, "https://push.example.com/send/abc", "auth", "p256dh", "agent")
	if err != nil {
		t.Fatalf("Subscribe() = %v, want nil", err)
	}
	subs, err := service.List(1)
	if err != nil {
		t.Fatalf("List() = %v", err)
	}
	if len(subs) != 1 || subs[0].Endpoint != "https://push.example.com/send/abc" {
		t.Fatalf("subscriptions = %+v, want the accepted endpoint", subs)
	}
}

func TestPushServiceRedirectPolicyRevalidatesTargets(t *testing.T) {
	stubEndpointLookup(t, map[string][]net.IP{
		"push.example.com": {net.ParseIP("93.184.216.34")},
	})
	service := newPushService(newPushTestDB(t), enabledPushConfig(), DefaultPushServiceConfig(), nil)
	check := service.httpClient.CheckRedirect
	if check == nil {
		t.Fatal("push http client has no redirect policy")
	}

	publicReq := &http.Request{URL: &url.URL{Scheme: "https", Host: "push.example.com", Path: "/send"}}
	if err := check(publicReq, nil); err != nil {
		t.Fatalf("redirect to public push host rejected: %v", err)
	}

	internalReq := &http.Request{URL: &url.URL{Scheme: "https", Host: "169.254.169.254", Path: "/latest/meta-data"}}
	if err := check(internalReq, []*http.Request{publicReq}); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("redirect to metadata address = %v, want ErrInvalidEndpoint", err)
	}

	httpReq := &http.Request{URL: &url.URL{Scheme: "http", Host: "push.example.com", Path: "/send"}}
	if err := check(httpReq, []*http.Request{publicReq}); !errors.Is(err, ErrInvalidEndpoint) {
		t.Fatalf("redirect downgrade to http = %v, want ErrInvalidEndpoint", err)
	}

	via := make([]*http.Request, 5)
	for i := range via {
		via[i] = publicReq
	}
	if err := check(publicReq, via); err == nil {
		t.Fatal("redirect chain beyond limit accepted")
	}
}
