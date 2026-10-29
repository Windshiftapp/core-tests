package smtp

import (
	"errors"
	"net/smtp"
	"strings"
	"testing"

	"windshift/internal/models"
)

func serverInfo(tls bool, auth ...string) *smtp.ServerInfo {
	return &smtp.ServerInfo{Name: "smtp.example.com", TLS: tls, Auth: auth}
}

func TestNormalizeAuthMethod(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: AuthMethodAuto},
		{in: "   ", want: AuthMethodAuto},
		{in: "AUTO", want: AuthMethodAuto},
		{in: " Plain ", want: AuthMethodPlain},
		{in: "LOGIN", want: AuthMethodLogin},
		{in: "CRAM-MD5", want: AuthMethodCRAMMD5},
		{in: "None", want: AuthMethodNone},
	}
	for _, tt := range tests {
		if got := normalizeAuthMethod(tt.in); got != tt.want {
			t.Errorf("normalizeAuthMethod(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestAuthMethodAllowed(t *testing.T) {
	allowed := []string{"", "auto", "AUTO", "plain", " login ", "cram-md5", "none"}
	for _, method := range allowed {
		if !AuthMethodAllowed(method) {
			t.Errorf("AuthMethodAllowed(%q) = false, want true", method)
		}
	}
	rejected := []string{"bogus", "plaintext", "xoauth2", "cram"}
	for _, method := range rejected {
		if AuthMethodAllowed(method) {
			t.Errorf("AuthMethodAllowed(%q) = true, want false", method)
		}
	}
}

func TestPickAuthMethod(t *testing.T) {
	tests := []struct {
		name       string
		advertised []string
		want       string
	}{
		{name: "nothing advertised falls back to plain", advertised: nil, want: AuthMethodPlain},
		{name: "plain preferred", advertised: []string{"PLAIN", "LOGIN", "CRAM-MD5"}, want: AuthMethodPlain},
		{name: "login when plain absent", advertised: []string{"LOGIN", "CRAM-MD5"}, want: AuthMethodLogin},
		{name: "cram-md5 last", advertised: []string{"CRAM-MD5"}, want: AuthMethodCRAMMD5},
		{name: "case insensitive", advertised: []string{"login"}, want: AuthMethodLogin},
		{name: "unknown mechanisms fall back to plain", advertised: []string{"XOAUTH2"}, want: AuthMethodPlain},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pickAuthMethod(tt.advertised); got != tt.want {
				t.Fatalf("pickAuthMethod(%v) = %q, want %q", tt.advertised, got, tt.want)
			}
		})
	}
}

func TestNewSMTPAuth(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		username string
		password string
		wantNil  bool
		wantErr  string
	}{
		{name: "unknown method rejected", method: "bogus", username: "u", password: "p", wantErr: "not allowed"},
		{name: "none never authenticates", method: "none", username: "u", password: "p", wantNil: true},
		{name: "none without credentials", method: "none", wantNil: true},
		{name: "auto without credentials", method: "auto", wantNil: true},
		{name: "auto with credentials", method: "auto", username: "u", password: "p"},
		{name: "explicit plain requires credentials", method: "plain", username: "u", wantErr: "requires both a username and a password"},
		{name: "explicit login requires password", method: "login", username: "u", wantErr: "requires both a username and a password"},
		{name: "explicit cram requires username", method: "cram-md5", password: "p", wantErr: "requires both a username and a password"},
		{name: "explicit login with credentials", method: "login", username: "u", password: "p"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, err := newSMTPAuth(tt.method, tt.username, tt.password, "smtp.example.com")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("newSMTPAuth error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("newSMTPAuth: %v", err)
			}
			if tt.wantNil {
				if auth != nil {
					t.Fatalf("expected nil auth, got %T", auth)
				}
				return
			}
			if auth == nil {
				t.Fatal("expected auth, got nil")
			}
		})
	}
}

func TestSelectingAuthStart(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		advertised []string
		wantProto  string
		wantErr    string
	}{
		{name: "auto negotiates plain", method: "auto", advertised: []string{"PLAIN", "LOGIN"}, wantProto: "PLAIN"},
		{name: "auto negotiates login", method: "auto", advertised: []string{"LOGIN"}, wantProto: "LOGIN"},
		{name: "auto negotiates cram-md5", method: "auto", advertised: []string{"CRAM-MD5"}, wantProto: "CRAM-MD5"},
		{name: "empty method behaves as auto", method: "", advertised: []string{"LOGIN"}, wantProto: "LOGIN"},
		{name: "explicit login", method: "login", advertised: []string{"PLAIN", "LOGIN"}, wantProto: "LOGIN"},
		{name: "explicit cram-md5", method: "cram-md5", advertised: []string{"CRAM-MD5"}, wantProto: "CRAM-MD5"},
		{name: "explicit method not advertised", method: "login", advertised: []string{"PLAIN"}, wantErr: "does not support AUTH LOGIN"},
		{name: "explicit method with no advertisement proceeds", method: "login", advertised: nil, wantProto: "LOGIN"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth, err := newSMTPAuth(tt.method, "u", "p", "smtp.example.com")
			if err != nil {
				t.Fatalf("newSMTPAuth: %v", err)
			}
			proto, _, err := auth.Start(serverInfo(true, tt.advertised...))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Start error = %v, want containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Start: %v", err)
			}
			if proto != tt.wantProto {
				t.Fatalf("proto = %q, want %q", proto, tt.wantProto)
			}
		})
	}
}

func TestSelectingAuthRejectsPlaintext(t *testing.T) {
	auth, err := newSMTPAuth("auto", "u", "p", "smtp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = auth.Start(serverInfo(false, "PLAIN"))
	if !errors.Is(err, errAuthRequiresTLS) {
		t.Fatalf("error = %v, want errAuthRequiresTLS", err)
	}
}

func TestSelectingAuthDelegatesToLogin(t *testing.T) {
	auth, err := newSMTPAuth("login", "alice", "s3cret", "smtp.example.com")
	if err != nil {
		t.Fatal(err)
	}
	proto, _, err := auth.Start(serverInfo(true, "LOGIN"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if proto != "LOGIN" {
		t.Fatalf("proto = %q, want LOGIN", proto)
	}
	got, err := auth.Next([]byte("Username:"), true)
	if err != nil {
		t.Fatalf("Next username: %v", err)
	}
	if string(got) != "alice" {
		t.Fatalf("username = %q, want alice", got)
	}
	got, err = auth.Next([]byte("Password:"), true)
	if err != nil {
		t.Fatalf("Next password: %v", err)
	}
	if string(got) != "s3cret" {
		t.Fatalf("password = %q, want s3cret", got)
	}
}

func TestSelectingAuthNextBeforeStart(t *testing.T) {
	auth := &selectingAuth{method: AuthMethodLogin, username: "u", password: "p"}
	if _, err := auth.Next(nil, true); err == nil {
		t.Fatal("expected error when Next is called before Start")
	}
}

func TestLoginAuthChallengeFlow(t *testing.T) {
	a := &loginAuth{username: "alice", password: "s3cret"}
	proto, initial, err := a.Start(serverInfo(true, "LOGIN"))
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if proto != "LOGIN" {
		t.Fatalf("proto = %q, want LOGIN", proto)
	}
	if len(initial) != 0 {
		t.Fatalf("initial response = %q, want empty", initial)
	}
	got, err := a.Next([]byte("Username:"), true)
	if err != nil {
		t.Fatalf("username: %v", err)
	}
	if string(got) != "alice" {
		t.Fatalf("username = %q, want alice", got)
	}
	got, err = a.Next([]byte("Password:"), true)
	if err != nil {
		t.Fatalf("password: %v", err)
	}
	if string(got) != "s3cret" {
		t.Fatalf("password = %q, want s3cret", got)
	}
	got, err = a.Next([]byte("235 ok"), false)
	if err != nil {
		t.Fatalf("final: %v", err)
	}
	if got != nil {
		t.Fatalf("final = %q, want nil", got)
	}
}

func TestLoginAuthFallsBackToPromptOrder(t *testing.T) {
	a := &loginAuth{username: "alice", password: "s3cret"}
	if _, _, err := a.Start(serverInfo(true, "LOGIN")); err != nil {
		t.Fatal(err)
	}
	// Servers that send empty or localized prompts rely on the order instead.
	got, err := a.Next([]byte(""), true)
	if err != nil {
		t.Fatalf("first challenge: %v", err)
	}
	if string(got) != "alice" {
		t.Fatalf("first response = %q, want username", got)
	}
	got, err = a.Next([]byte("Код:"), true)
	if err != nil {
		t.Fatalf("second challenge: %v", err)
	}
	if string(got) != "s3cret" {
		t.Fatalf("second response = %q, want password", got)
	}
}

func TestLoginAuthRejectsUnexpectedChallenge(t *testing.T) {
	a := &loginAuth{username: "alice", password: "s3cret"}
	if _, _, err := a.Start(serverInfo(true, "LOGIN")); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Next([]byte("Username:"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Next([]byte("Password:"), true); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Next([]byte("More:"), true); err == nil {
		t.Fatal("expected error for a third challenge")
	}
}

func TestLoginAuthRejectsPlaintext(t *testing.T) {
	a := &loginAuth{username: "alice", password: "s3cret"}
	if _, _, err := a.Start(serverInfo(false, "LOGIN")); !errors.Is(err, errAuthRequiresTLS) {
		t.Fatalf("error = %v, want errAuthRequiresTLS", err)
	}
}

func TestValidateTransportAuthMethod(t *testing.T) {
	base := func(method string) *models.ChannelConfig {
		return &models.ChannelConfig{
			SMTPHost:       "smtp.example.com",
			SMTPPort:       587,
			SMTPFromEmail:  "from@example.com",
			SMTPEncryption: "tls",
			SMTPAuthMethod: method,
		}
	}

	for _, method := range []string{"", "auto", "plain", "login", "cram-md5", "none", "LOGIN"} {
		if err := ValidateTransport(base(method)); err != nil {
			t.Errorf("ValidateTransport(auth=%q) = %v, want nil", method, err)
		}
	}
	for _, method := range []string{"bogus", "plaintext", "xoauth2"} {
		err := ValidateTransport(base(method))
		if err == nil || !strings.Contains(err.Error(), "not allowed") {
			t.Errorf("ValidateTransport(auth=%q) = %v, want not-allowed error", method, err)
		}
	}
}
