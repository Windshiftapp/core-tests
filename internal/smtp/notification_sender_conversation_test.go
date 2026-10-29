//go:build test

package smtp

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/utils"
)

type smtpFixtureBehavior struct {
	rejectStage string
	stallStage  string
}

type smtpConversationFixture struct {
	listener  net.Listener
	conn      net.Conn
	behavior  smtpFixtureBehavior
	stalled   chan struct{}
	release   chan struct{}
	done      chan error
	delivered atomic.Bool
	mu        sync.Mutex
	commands  []string
	body      string
	stallOnce sync.Once
	closeOnce sync.Once
}

func newSMTPConversationFixture(t *testing.T, behavior smtpFixtureBehavior) *smtpConversationFixture {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	fixture := &smtpConversationFixture{
		listener: listener,
		behavior: behavior,
		stalled:  make(chan struct{}),
		release:  make(chan struct{}),
		done:     make(chan error, 1),
	}
	go fixture.serve()
	t.Cleanup(fixture.close)
	return fixture
}

func (f *smtpConversationFixture) addr() string { return f.listener.Addr().String() }

func (f *smtpConversationFixture) close() {
	f.closeOnce.Do(func() {
		close(f.release)
		_ = f.listener.Close()
		f.mu.Lock()
		conn := f.conn
		f.mu.Unlock()
		if conn != nil {
			_ = conn.Close()
		}
	})
}

func (f *smtpConversationFixture) serve() {
	conn, err := f.listener.Accept()
	if err != nil {
		f.done <- err
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	defer func() { _ = conn.Close() }()
	f.done <- f.converse(conn)
}

func (f *smtpConversationFixture) converse(conn net.Conn) error {
	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)
	if f.stall("greeting") {
		return nil
	}
	if err := smtpFixtureWrite(writer, "220 smtp.fixture ESMTP ready\r\n"); err != nil {
		return err
	}
	if err := f.command(reader, writer, "EHLO ", "250 smtp.fixture\r\n"); err != nil {
		return err
	}
	if f.stall("mail") {
		return nil
	}
	if err := f.command(reader, writer, "MAIL FROM:<sender@example.com>", f.response("mail", "250 sender accepted\r\n")); err != nil || f.behavior.rejectStage == "mail" {
		return err
	}
	if f.stall("rcpt") {
		return nil
	}
	if err := f.command(reader, writer, "RCPT TO:<recipient@example.com>", f.response("rcpt", "250 recipient accepted\r\n")); err != nil || f.behavior.rejectStage == "rcpt" {
		return err
	}
	if f.stall("data") {
		return nil
	}
	if err := f.command(reader, writer, "DATA", f.response("data", "354 send content\r\n")); err != nil || f.behavior.rejectStage == "data" {
		return err
	}

	var body strings.Builder
	for {
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return readErr
		}
		if line == ".\r\n" {
			break
		}
		body.WriteString(line)
	}
	f.mu.Lock()
	f.body = body.String()
	f.mu.Unlock()
	if f.stall("final") {
		return nil
	}
	if f.behavior.rejectStage == "final" {
		return smtpFixtureWrite(writer, "550 message rejected after DATA\r\n")
	}
	if err := smtpFixtureWrite(writer, "250 queued\r\n"); err != nil {
		return err
	}
	f.delivered.Store(true)
	return nil
}

func (f *smtpConversationFixture) command(reader *bufio.Reader, writer *bufio.Writer, wantPrefix, response string) error {
	line, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	f.mu.Lock()
	f.commands = append(f.commands, line)
	f.mu.Unlock()
	if !strings.HasPrefix(line, wantPrefix) {
		return fmt.Errorf("command %q does not start with %q", line, wantPrefix)
	}
	return smtpFixtureWrite(writer, response)
}

func (f *smtpConversationFixture) response(stage, success string) string {
	if f.behavior.rejectStage == stage {
		return "550 " + stage + " rejected\r\n"
	}
	return success
}

func (f *smtpConversationFixture) stall(stage string) bool {
	if f.behavior.stallStage != stage {
		return false
	}
	f.stallOnce.Do(func() { close(f.stalled) })
	<-f.release
	return true
}

func smtpFixtureWrite(writer *bufio.Writer, response string) error {
	if _, err := writer.WriteString(response); err != nil {
		return err
	}
	return writer.Flush()
}

func (f *smtpConversationFixture) snapshot() ([]string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.commands...), f.body
}

func sendFixtureMessage(ctx context.Context, addr string) error {
	return sendPlaintextContext(ctx, addr, "sender@example.com", "recipient@example.com", []byte("Subject: fixture\r\n\r\nhello\r\n"))
}

// The bytes handed to the transport are the multipart message itself: the
// boundary announced in Content-Type, both text parts and their transfer
// encoding must survive the SMTP DATA exchange unchanged.
func TestSMTPConversationDeliversMultipartMessage(t *testing.T) {
	allowLocalSMTPFixture(t)
	fixture := newSMTPConversationFixture(t, smtpFixtureBehavior{})

	message, err := buildMime(mimeOptions{
		FromEmail: "team@example.com",
		FromName:  "Windshift Support",
		ToEmail:   "customer@example.com",
		ToName:    "Lastname, Firstname",
		Subject:   "We received your request",
		TextBody:  "Hello, we received your request.\nGrüße\n",
		HTMLBody:  "<p>Hello, we received your request.</p>",
	})
	if err != nil {
		t.Fatalf("buildMime: %v", err)
	}
	if err := sendPlaintextContext(context.Background(), fixture.addr(), "sender@example.com", "recipient@example.com", message); err != nil {
		t.Fatalf("sendPlaintextContext: %v", err)
	}

	_, body := fixture.snapshot()
	// The server strips the DATA terminator; dot-stuffing is not needed because
	// the generated message never starts a line with a dot.
	if body != string(message) {
		t.Fatalf("DATA body differs from the generated message:\n got %q\nwant %q", body, string(message))
	}

	parsed := parseGeneratedMime(t, []byte(body))
	if got, want := parsed.parts["text/plain"], "Hello, we received your request.\nGrüße\n"; got != want {
		t.Fatalf("delivered text part = %q, want %q", got, want)
	}
	to := singleMailbox(t, parsed.header, "To")
	if to.Name != "Lastname, Firstname" || to.Address != "customer@example.com" {
		t.Fatalf("delivered To = %q <%q>", to.Name, to.Address)
	}
}

func allowLocalSMTPFixture(t *testing.T) {
	t.Helper()
	previous := utils.AllowLocalConnections()
	utils.SetAllowLocalConnections(true)
	t.Cleanup(func() { utils.SetAllowLocalConnections(previous) })
}

func TestSMTPConversationFinalAcceptanceControlsDelivery(t *testing.T) {
	allowLocalSMTPFixture(t)

	t.Run("accepted", func(t *testing.T) {
		fixture := newSMTPConversationFixture(t, smtpFixtureBehavior{})
		if err := sendFixtureMessage(context.Background(), fixture.addr()); err != nil {
			t.Fatalf("sendPlaintextContext: %v", err)
		}
		if !fixture.delivered.Load() {
			t.Fatal("server accepted DATA but delivery was not recorded")
		}
		commands, body := fixture.snapshot()
		if len(commands) != 4 || !strings.HasPrefix(commands[0], "EHLO ") || commands[1] != "MAIL FROM:<sender@example.com>" || commands[2] != "RCPT TO:<recipient@example.com>" || commands[3] != "DATA" {
			t.Fatalf("SMTP commands = %#v", commands)
		}
		if got, want := body, "Subject: fixture\r\n\r\nhello\r\n"; got != want {
			t.Fatalf("DATA body = %q, want %q", got, want)
		}
	})

	t.Run("rejected after DATA", func(t *testing.T) {
		fixture := newSMTPConversationFixture(t, smtpFixtureBehavior{rejectStage: "final"})
		err := sendFixtureMessage(context.Background(), fixture.addr())
		if err == nil || !strings.Contains(err.Error(), `550 "message rejected after DATA"`) {
			t.Fatalf("send error = %v, want final DATA rejection", err)
		}
		if fixture.delivered.Load() {
			t.Fatal("final DATA rejection was recorded as delivered")
		}
	})
}

func TestSMTPConversationReportsEarlierStageRejections(t *testing.T) {
	allowLocalSMTPFixture(t)

	for _, stage := range []string{"mail", "rcpt", "data"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newSMTPConversationFixture(t, smtpFixtureBehavior{rejectStage: stage})
			err := sendFixtureMessage(context.Background(), fixture.addr())
			if err == nil || !strings.Contains(err.Error(), `550 "`+stage+` rejected"`) {
				t.Fatalf("send error = %v, want %s rejection", err, stage)
			}
			if fixture.delivered.Load() {
				t.Fatalf("%s rejection was recorded as delivered", stage)
			}
		})
	}
}

func TestSMTPConversationCancellationUnblocksStalledStages(t *testing.T) {
	allowLocalSMTPFixture(t)

	for _, stage := range []string{"greeting", "mail", "final"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newSMTPConversationFixture(t, smtpFixtureBehavior{stallStage: stage})
			ctx, cancel := context.WithCancel(context.Background())
			result := make(chan error, 1)
			go func() { result <- sendFixtureMessage(ctx, fixture.addr()) }()

			select {
			case <-fixture.stalled:
			case <-time.After(2 * time.Second):
				t.Fatalf("client did not reach stalled %s stage", stage)
			}
			cancel()

			select {
			case err := <-result:
				if err == nil {
					t.Fatalf("stalled %s send returned nil after cancellation", stage)
				}
			case <-time.After(2 * time.Second):
				t.Fatalf("stalled %s send did not stop after cancellation", stage)
			}
			if fixture.delivered.Load() {
				t.Fatalf("stalled %s send was recorded as delivered", stage)
			}
		})
	}
}

func TestSMTPConversationHonorsContextDeadline(t *testing.T) {
	allowLocalSMTPFixture(t)
	fixture := newSMTPConversationFixture(t, smtpFixtureBehavior{stallStage: "greeting"})
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()

	err := sendFixtureMessage(ctx, fixture.addr())
	if err == nil {
		t.Fatal("stalled greeting returned nil after context deadline")
	}
	<-ctx.Done()
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("context error = %v, want deadline exceeded", ctx.Err())
	}
	if fixture.delivered.Load() {
		t.Fatal("deadline-exceeded send was recorded as delivered")
	}
}
