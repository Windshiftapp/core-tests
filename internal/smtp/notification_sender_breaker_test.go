//go:build test

package smtp

import (
	"context"
	"errors"
	"net"
	"net/textproto"
	"sync/atomic"
	"testing"

	"windshift/internal/models"
)

func TestSMTPBreakerStopsDialingAfterConsecutiveTransportFailures(t *testing.T) {
	allowLocalSMTPFixture(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()

	var accepts atomic.Int64
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			accepts.Add(1)
			_ = conn.Close()
		}
	}()

	addr := listener.Addr().(*net.TCPAddr)
	config := &models.ChannelConfig{
		SMTPHost:       "127.0.0.1",
		SMTPPort:       addr.Port,
		SMTPFromEmail:  "sender@example.com",
		SMTPEncryption: "none",
		SMTPAuthMethod: "none",
	}
	sender := &NotificationSMTPSender{}
	message := []byte("Subject: breaker\r\n\r\nbody\r\n")

	for i := 0; i < smtpBreakerFailureThreshold; i++ {
		if err := sender.dispatchContext(context.Background(), config, "recipient@example.com", message); err == nil {
			t.Fatalf("attempt %d returned nil, want transport failure", i+1)
		}
	}
	if got := accepts.Load(); got != int64(smtpBreakerFailureThreshold) {
		t.Fatalf("dial attempts = %d, want %d", got, smtpBreakerFailureThreshold)
	}

	err = sender.dispatchContext(context.Background(), config, "recipient@example.com", message)
	if !errors.Is(err, ErrSMTPCircuitOpen) {
		t.Fatalf("open breaker error = %v, want ErrSMTPCircuitOpen", err)
	}
	if got := accepts.Load(); got != int64(smtpBreakerFailureThreshold) {
		t.Fatalf("dial attempts after breaker opened = %d, want no new dial", got)
	}
}

func TestSMTPConnectionFailureClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "dial", err: &net.OpError{Op: "dial", Err: errors.New("refused")}, want: true},
		{name: "timeout", err: context.DeadlineExceeded, want: true},
		{name: "canceled", err: context.Canceled, want: false},
		{name: "auth", err: &textproto.Error{Code: 535, Msg: "auth failed"}, want: true},
		{name: "service unavailable", err: &textproto.Error{Code: 421, Msg: "try later"}, want: true},
		{name: "recipient rejected", err: &textproto.Error{Code: 550, Msg: "no such user"}, want: false},
		{name: "message rejected", err: &textproto.Error{Code: 554, Msg: "spam"}, want: false},
		{name: "nil", err: nil, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := smtpConnectionFailure(tc.err); got != tc.want {
				t.Fatalf("smtpConnectionFailure(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
