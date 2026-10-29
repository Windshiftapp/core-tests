//go:build test

package smtp

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	"windshift/internal/models"
)

type multiMessageSMTPFixture struct {
	listener    net.Listener
	connections atomic.Int64
	messages    atomic.Int64
}

func newMultiMessageSMTPFixture(t *testing.T) *multiMessageSMTPFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fixture := &multiMessageSMTPFixture{listener: listener}
	go fixture.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return fixture
}

func (f *multiMessageSMTPFixture) addr() (string, int) {
	tcp := f.listener.Addr().(*net.TCPAddr)
	return "127.0.0.1", tcp.Port
}

func (f *multiMessageSMTPFixture) serve() {
	for {
		conn, err := f.listener.Accept()
		if err != nil {
			return
		}
		f.connections.Add(1)
		go f.converse(conn)
	}
}

func (f *multiMessageSMTPFixture) converse(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	write := func(line string) bool {
		if _, err := writer.WriteString(line); err != nil {
			return false
		}
		return writer.Flush() == nil
	}
	if !write("220 fixture ESMTP ready\r\n") {
		return
	}
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			if !write("250 fixture\r\n") {
				return
			}
		case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"), line == "RSET":
			if !write("250 ok\r\n") {
				return
			}
		case line == "NOOP":
			if !write("250 ok\r\n") {
				return
			}
		case line == "DATA":
			if !write("354 send content\r\n") {
				return
			}
			for {
				bodyLine, readErr := reader.ReadString('\n')
				if readErr != nil {
					return
				}
				if bodyLine == ".\r\n" {
					break
				}
			}
			f.messages.Add(1)
			if !write("250 queued\r\n") {
				return
			}
		case line == "QUIT":
			_ = write("221 bye\r\n")
			return
		default:
			if !write("250 ok\r\n") {
				return
			}
		}
	}
}

func TestSMTPSenderReusesOneConnectionForConsecutiveMessages(t *testing.T) {
	allowLocalSMTPFixture(t)
	fixture := newMultiMessageSMTPFixture(t)
	host, port := fixture.addr()
	config := &models.ChannelConfig{
		SMTPHost:       host,
		SMTPPort:       port,
		SMTPFromEmail:  "sender@example.com",
		SMTPEncryption: "none",
		SMTPAuthMethod: "none",
	}
	sender := &NotificationSMTPSender{}
	message := []byte("Subject: reuse\r\n\r\nbody\r\n")
	for i := 0; i < 3; i++ {
		if err := sender.dispatchContext(context.Background(), config, "recipient@example.com", message); err != nil {
			t.Fatalf("send %d: %v", i+1, err)
		}
	}
	if got := fixture.connections.Load(); got != 1 {
		t.Fatalf("connections = %d, want 1 reused session", got)
	}
	if got := fixture.messages.Load(); got != 3 {
		t.Fatalf("delivered messages = %d, want 3", got)
	}
}
