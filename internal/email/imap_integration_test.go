package email

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"windshift/internal/models"
	"windshift/internal/services"
	"windshift/internal/utils"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
	"github.com/emersion/go-sasl"
)

const (
	testIMAPUsername = "ingest@example.com"
	testIMAPPassword = "test-password"
)

type artificialIMAPServer struct {
	host      string
	port      int
	user      *imapmemserver.User
	clientTLS *tls.Config
}

func startArtificialIMAPServer(
	t *testing.T,
	implicitTLS bool,
	wrapSession func(imapserver.Session) imapserver.Session,
) *artificialIMAPServer {
	t.Helper()

	serverTLS, clientTLS := testIMAPTLSConfigs(t)
	backend := imapmemserver.New()
	user := imapmemserver.NewUser(testIMAPUsername, testIMAPPassword)
	if err := user.Create("INBOX", nil); err != nil {
		t.Fatalf("create artificial INBOX: %v", err)
	}
	backend.AddUser(user)

	server := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			session := backend.NewSession()
			if wrapSession != nil {
				session = wrapSession(session)
			}
			return session, nil, nil
		},
		Caps: imap.CapSet{
			imap.CapIMAP4rev1: {},
			imap.CapUIDPlus:   {},
		},
		TLSConfig: serverTLS,
	})

	baseListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for artificial IMAP server: %v", err)
	}
	listener := net.Listener(baseListener)
	if implicitTLS {
		listener = tls.NewListener(baseListener, serverTLS)
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- server.Serve(listener) }()
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("close artificial IMAP server: %v", err)
		}
		if err := <-serveDone; err != nil {
			t.Errorf("serve artificial IMAP server: %v", err)
		}
	})

	host, portText, err := net.SplitHostPort(baseListener.Addr().String())
	if err != nil {
		t.Fatalf("split artificial IMAP address: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse artificial IMAP port: %v", err)
	}
	return &artificialIMAPServer{
		host: host, port: port, user: user, clientTLS: clientTLS,
	}
}

func testIMAPTLSConfigs(t *testing.T) (*tls.Config, *tls.Config) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate IMAP test key: %v", err)
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "artificial-imap"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, publicKey, privateKey)
	if err != nil {
		t.Fatalf("create IMAP test certificate: %v", err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse IMAP test certificate: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	return &tls.Config{ //nolint:gosec // TLS 1.2 is the production minimum under test.
			Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: privateKey}},
			MinVersion:   tls.VersionTLS12,
		}, &tls.Config{ //nolint:gosec // TLS 1.2 is the production minimum under test.
			RootCAs: roots, ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12,
		}
}

type memoryLiteral struct {
	*bytes.Reader
	size int64
}

func (r *memoryLiteral) Size() int64 { return r.size }

func appendArtificialMessage(t *testing.T, user *imapmemserver.User, raw string) uint32 {
	t.Helper()
	literal := &memoryLiteral{Reader: bytes.NewReader([]byte(raw)), size: int64(len(raw))}
	data, err := user.Append("INBOX", literal, &imap.AppendOptions{})
	if err != nil {
		t.Fatalf("append artificial message: %v", err)
	}
	return uint32(data.UID)
}

func connectArtificialClient(t *testing.T, server *artificialIMAPServer, encryption string) *Client {
	t.Helper()
	client, err := connectWithDialer(ConnectOptions{
		Context: context.Background(), Host: server.host, Port: server.port,
		Encryption: encryption, Timeout: 3 * time.Second,
	}, &net.Dialer{Timeout: 3 * time.Second}, server.clientTLS)
	if err != nil {
		t.Fatalf("connect to artificial IMAP server: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestConnectAcceptsSelfSignedCertificateOnlyWhenEnabled(t *testing.T) {
	t.Cleanup(func() { utils.SetSkipTLSVerify(false) })
	for _, encryption := range []string{"ssl", "starttls"} {
		t.Run(encryption, func(t *testing.T) {
			server := startArtificialIMAPServer(t, encryption == "ssl", nil)
			opts := ConnectOptions{
				Context: context.Background(), Host: server.host, Port: server.port,
				Encryption: encryption, Timeout: 3 * time.Second,
			}

			utils.SetSkipTLSVerify(false)
			if client, err := Connect(opts); err == nil {
				_ = client.Close()
				t.Fatal("self-signed IMAP certificate was accepted while verification was enabled")
			}

			utils.SetSkipTLSVerify(true)
			client, err := Connect(opts)
			if err != nil {
				t.Fatalf("self-signed IMAP certificate was rejected with verification bypass enabled: %v", err)
			}
			if err := client.Close(); err != nil {
				t.Fatalf("close IMAP client: %v", err)
			}
		})
	}
}

func TestArtificialIMAPIngestion(t *testing.T) {
	for _, encryption := range []string{"ssl", "starttls"} {
		t.Run(encryption, func(t *testing.T) {
			server := startArtificialIMAPServer(t, encryption == "ssl", nil)
			firstUID := appendArtificialMessage(t, server.user, firstArtificialMessage)
			replyUID := appendArtificialMessage(t, server.user, replyArtificialMessage)

			client := connectArtificialClient(t, server, encryption)
			if err := client.AuthenticateBasic(testIMAPUsername, testIMAPPassword); err != nil {
				t.Fatalf("authenticate to artificial IMAP server: %v", err)
			}
			selected, err := client.SelectMailbox("INBOX")
			if err != nil {
				t.Fatalf("select artificial INBOX: %v", err)
			}
			if selected.NumMessages != 2 || selected.UIDValidity == 0 {
				t.Fatalf("selected mailbox = %d messages, validity %d", selected.NumMessages, selected.UIDValidity)
			}

			messages, err := client.FetchMessages(0, 50)
			if err != nil {
				t.Fatalf("fetch artificial messages: %v", err)
			}
			if len(messages) != 2 || messages[0].UID != firstUID || messages[1].UID != replyUID {
				t.Fatalf("fetched UIDs = %v, want [%d %d]", fetchedUIDs(messages), firstUID, replyUID)
			}

			parser := NewParser()
			first := parser.Parse(messages[0])
			if first.MessageID != "<customer-1@example.com>" {
				t.Fatalf("first Message-ID = %q, want canonical bracketed form", first.MessageID)
			}
			if first.Subject != "Prüfung request" || first.From.Address != "customer@example.com" {
				t.Fatalf("parsed envelope = subject %q, sender %q", first.Subject, first.From.Address)
			}
			if len(first.Attachments) != 1 || first.Attachments[0].Filename != "evidence.txt" || string(first.Attachments[0].Data) != "attachment evidence\n" {
				t.Fatalf("parsed attachments = %#v", first.Attachments)
			}

			db := newProcessorTestDB(t)
			var workspaceID, itemTypeID, channelID int
			if err := db.QueryRow(`INSERT INTO workspaces (name, key) VALUES ('Email integration', 'EML') RETURNING id`).Scan(&workspaceID); err != nil {
				t.Fatalf("insert workspace: %v", err)
			}
			if err := db.QueryRow(`INSERT INTO item_types (name) VALUES ('Inbound email') RETURNING id`).Scan(&itemTypeID); err != nil {
				t.Fatalf("insert item type: %v", err)
			}
			if err := db.QueryRow(`INSERT INTO channels (name, type, direction) VALUES ('Artificial IMAP', 'email', 'inbound') RETURNING id`).Scan(&channelID); err != nil {
				t.Fatalf("insert channel: %v", err)
			}

			attachmentRoot := t.TempDir()
			processor := NewProcessor(db, attachmentRoot)
			processor.SetCommentService(services.NewCommentService(db))
			config := &models.ChannelConfig{EmailWorkspaceID: workspaceID, EmailItemTypeID: &itemTypeID}
			created, err := processor.ProcessEmail(context.Background(), first, channelID, selected.UIDValidity, config)
			if err != nil {
				t.Fatalf("process first artificial email: %v", err)
			}
			if created.Action != ActionItemCreated || created.ItemID == nil {
				t.Fatalf("first processing result = %#v", created)
			}

			var title, description, relativeAttachmentPath, dedupKey string
			if err := db.QueryRow(`SELECT title, description FROM items WHERE id = ?`, *created.ItemID).Scan(&title, &description); err != nil {
				t.Fatalf("load email-created item: %v", err)
			}
			if title != "Prüfung request" || description != "First line\nSecond line" {
				t.Fatalf("created item = title %q, description %q", title, description)
			}
			if err := db.QueryRow(`SELECT file_path FROM attachments WHERE item_id = ?`, *created.ItemID).Scan(&relativeAttachmentPath); err != nil {
				t.Fatalf("load ingested attachment: %v", err)
			}
			attachmentData, err := os.ReadFile(filepath.Join(attachmentRoot, relativeAttachmentPath))
			if err != nil || string(attachmentData) != "attachment evidence\n" {
				t.Fatalf("stored attachment = %q, %v", attachmentData, err)
			}
			if err := db.QueryRow(`SELECT dedup_key FROM email_message_tracking WHERE item_id = ? AND direction = 'inbound'`, *created.ItemID).Scan(&dedupKey); err != nil {
				t.Fatalf("load inbound tracking: %v", err)
			}
			if dedupKey != "customer-1@example.com" {
				t.Fatalf("dedup key = %q, want legacy-compatible bare form", dedupKey)
			}

			// Outbound rows historically use the bracketed RFC form. The reply has
			// only In-Reply-To (no References), which reproduces the thread split
			// caused by go-imap exposing ENVELOPE IDs without brackets.
			if _, err := db.ExecWrite(`
				INSERT INTO email_message_tracking
					(channel_id, message_id, dedup_key, from_email, item_id, direction)
				VALUES (?, '<ws-comment-7@example.com>', '<ws-comment-7@example.com>', 'team@example.com', ?, 'outbound')
			`, channelID, *created.ItemID); err != nil {
				t.Fatalf("insert outbound tracking: %v", err)
			}

			reply := parser.Parse(messages[1])
			if reply.InReplyTo != "<ws-comment-7@example.com>" {
				t.Fatalf("reply In-Reply-To = %q, want canonical bracketed form", reply.InReplyTo)
			}
			commented, err := processor.ProcessEmail(context.Background(), reply, channelID, selected.UIDValidity, config)
			if err != nil {
				t.Fatalf("process artificial reply: %v", err)
			}
			if commented.Action != ActionCommentAdded || commented.ItemID == nil || *commented.ItemID != *created.ItemID {
				t.Fatalf("reply processing result = %#v", commented)
			}
			var commentContent string
			if err := db.QueryRow(`SELECT content FROM comments WHERE id = ?`, *commented.CommentID).Scan(&commentContent); err != nil {
				t.Fatalf("load email-created comment: %v", err)
			}
			if commentContent != "Customer follow-up" {
				t.Fatalf("comment content = %q", commentContent)
			}

			duplicate, err := processor.ProcessEmail(context.Background(), first, channelID, selected.UIDValidity, config)
			if err != nil || duplicate.Action != ActionAlreadyExists {
				t.Fatalf("duplicate processing = (%#v, %v)", duplicate, err)
			}

			newer, err := client.FetchMessages(firstUID, 50)
			if err != nil || len(newer) != 1 || newer[0].UID != replyUID {
				t.Fatalf("incremental fetch = (%v, %v)", fetchedUIDs(newer), err)
			}
			if exhausted, err := client.FetchMessages(math.MaxUint32, 50); err != nil || len(exhausted) != 0 {
				t.Fatalf("max-UID fetch = (%v, %v), want empty", fetchedUIDs(exhausted), err)
			}

			if err := client.MarkAsRead(firstUID); err != nil {
				t.Fatalf("mark artificial message read: %v", err)
			}
			flagged, err := client.FetchMessages(0, 50)
			if err != nil || !slices.Contains(flagged[0].Flags, imap.FlagSeen) {
				t.Fatalf("flags after mark-as-read = (%v, %v)", flagged[0].Flags, err)
			}
			if err := client.DeleteMessage(firstUID); err != nil {
				t.Fatalf("mark artificial message deleted: %v", err)
			}
			if err := client.Expunge(); err != nil {
				t.Fatalf("expunge artificial message: %v", err)
			}
			remaining, err := client.FetchMessages(0, 50)
			if err != nil || len(remaining) != 1 || remaining[0].UID != replyUID {
				t.Fatalf("messages after expunge = (%v, %v)", fetchedUIDs(remaining), err)
			}
		})
	}
}

func fetchedUIDs(messages []*FetchedMessage) []uint32 {
	uids := make([]uint32, len(messages))
	for i, message := range messages {
		uids[i] = message.UID
	}
	return uids
}

type xoauthSession struct {
	imapserver.Session
	expected []byte
}

func (*xoauthSession) AuthenticateMechanisms() []string { return []string{"XOAUTH2"} }

func (s *xoauthSession) Authenticate(mech string) (sasl.Server, error) {
	if mech != "XOAUTH2" {
		return nil, fmt.Errorf("unsupported SASL mechanism %q", mech)
	}
	return &exactSASLServer{expected: s.expected}, nil
}

type exactSASLServer struct {
	expected []byte
}

func (s *exactSASLServer) Next(response []byte) ([]byte, bool, error) {
	if !bytes.Equal(response, s.expected) {
		return nil, false, fmt.Errorf("XOAUTH2 payload = %q, want %q", response, s.expected)
	}
	return nil, true, nil
}

func TestArtificialIMAPXOAUTH2WireFormat(t *testing.T) {
	expected := []byte("user=oauth@example.com\x01auth=Bearer access-token\x01\x01")
	server := startArtificialIMAPServer(t, true, func(session imapserver.Session) imapserver.Session {
		return &xoauthSession{Session: session, expected: expected}
	})
	client := connectArtificialClient(t, server, "ssl")
	if err := client.AuthenticateXOAuth2("oauth@example.com", "access-token"); err != nil {
		t.Fatalf("XOAUTH2 against artificial IMAP server: %v", err)
	}
}

func TestStartTLSHonorsConnectionTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for stalled IMAP server: %v", err)
	}
	release := make(chan struct{})
	accepted := make(chan struct{})
	go func() {
		conn, acceptErr := listener.Accept()
		close(accepted)
		if acceptErr == nil {
			<-release // Accept the TCP connection but never send an IMAP greeting.
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		close(release)
		_ = listener.Close()
	})

	host, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split stalled IMAP address: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse stalled IMAP port: %v", err)
	}
	started := time.Now()
	_, err = connectWithDialer(ConnectOptions{
		Host: host, Port: port, Encryption: "starttls", Timeout: 100 * time.Millisecond,
	}, &net.Dialer{Timeout: time.Second}, &tls.Config{ //nolint:gosec // No handshake occurs in this timeout test.
		ServerName: host, MinVersion: tls.VersionTLS12,
	})
	<-accepted
	if err == nil {
		t.Fatal("STARTTLS against a silent server unexpectedly succeeded")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("STARTTLS timeout took %v, want under one second", elapsed)
	}
}

const firstArtificialMessage = "Date: Fri, 17 Jul 2026 10:00:00 +0200\r\n" +
	"From: =?UTF-8?Q?Bj=C3=B6rn_Customer?= <customer@example.com>\r\n" +
	"To: Intake <ingest@example.com>\r\n" +
	"Subject: =?UTF-8?Q?Pr=C3=BCfung_request?=\r\n" +
	"Message-ID: <customer-1@example.com>\r\n" +
	"MIME-Version: 1.0\r\n" +
	"Content-Type: multipart/mixed; boundary=artificial-boundary\r\n" +
	"\r\n" +
	"--artificial-boundary\r\n" +
	"Content-Type: text/plain; charset=UTF-8\r\n" +
	"\r\n" +
	"First line\r\nSecond line\r\n" +
	"--artificial-boundary\r\n" +
	"Content-Type: text/plain; name=evidence.txt\r\n" +
	"Content-Disposition: attachment; filename=evidence.txt\r\n" +
	"Content-Transfer-Encoding: base64\r\n" +
	"\r\n" +
	"YXR0YWNobWVudCBldmlkZW5jZQo=\r\n" +
	"--artificial-boundary--\r\n"

const replyArtificialMessage = "Date: Fri, 17 Jul 2026 10:05:00 +0200\r\n" +
	"From: Björn Customer <customer@example.com>\r\n" +
	"To: Intake <ingest@example.com>\r\n" +
	"Subject: Re: Prüfung request\r\n" +
	"Message-ID: <customer-2@example.com>\r\n" +
	"In-Reply-To: <ws-comment-7@example.com>\r\n" +
	"Content-Type: text/plain; charset=UTF-8\r\n" +
	"\r\n" +
	"Customer follow-up\r\n"
