package email

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"windshift/internal/database"
	"windshift/internal/models"
)

type testEncryptor struct{}

func (testEncryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "fail" {
		return "", errors.New("encrypt failed")
	}
	return "cipher:" + plaintext, nil
}

func (testEncryptor) Decrypt(ciphertext string) (string, error) {
	const prefix = "cipher:"
	if len(ciphertext) < len(prefix) || ciphertext[:len(prefix)] != prefix {
		return "", errors.New("authentication failed")
	}
	return ciphertext[len(prefix):], nil
}

func TestEncryptSecretUsesVersionedEnvelope(t *testing.T) {
	got, err := EncryptSecret(testEncryptor{}, "secret")
	if err != nil {
		t.Fatalf("EncryptSecret: %v", err)
	}
	if got != "enc:v1:cipher:secret" {
		t.Fatalf("ciphertext = %q, want versioned envelope", got)
	}
	plain, err := DecryptOrLegacy(testEncryptor{}, got)
	if err != nil || plain != "secret" {
		t.Fatalf("DecryptOrLegacy = (%q, %v), want (secret, nil)", plain, err)
	}
}

func TestDecryptOrLegacyAcceptsLongBase64Plaintext(t *testing.T) {
	password := base64.StdEncoding.EncodeToString(make([]byte, 32))
	got, err := DecryptOrLegacy(testEncryptor{}, password)
	if err != nil {
		t.Fatalf("DecryptOrLegacy: %v", err)
	}
	if got != password {
		t.Fatalf("long base64 plaintext changed: got %q, want %q", got, password)
	}
}

func TestDecryptOrLegacyRejectsCorruptVersionedCiphertext(t *testing.T) {
	if _, err := DecryptOrLegacy(testEncryptor{}, "enc:v1:not-a-ciphertext"); err == nil {
		t.Fatal("corrupt versioned ciphertext was accepted")
	}
}

type blockingOAuthProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
	calls   atomic.Int32
}

func (*blockingOAuthProvider) GetType() string { return models.EmailProviderTypeGoogle }

func (*blockingOAuthProvider) GetIMAPServer(*models.ChannelConfig) (string, int) {
	return "imap.example.com", 993
}

func (*blockingOAuthProvider) Connect(context.Context, *models.ChannelConfig) (IMAPClient, error) {
	return nil, nil
}

func (*blockingOAuthProvider) TestConnection(context.Context, *models.ChannelConfig) error {
	return nil
}

func (*blockingOAuthProvider) GetOAuthURL(string, string) string { return "" }

func (*blockingOAuthProvider) ExchangeCode(context.Context, string, string) (*OAuthTokens, error) {
	return nil, errors.New("not implemented")
}

func (p *blockingOAuthProvider) RefreshToken(context.Context, string) (*OAuthTokens, error) {
	p.calls.Add(1)
	p.once.Do(func() { close(p.started) })
	<-p.release
	expiresAt := time.Now().Add(time.Hour)
	return &OAuthTokens{AccessToken: "new-access", RefreshToken: "new-refresh", ExpiresAt: &expiresAt}, nil
}

func (*blockingOAuthProvider) GetUserEmail(context.Context, string) (string, error) {
	return "user@example.com", nil
}

func TestCredentialLeaseSerializesRefreshAcrossManagers(t *testing.T) {
	db, err := database.NewSQLiteDB(filepath.Join(t.TempDir(), "credential-lease.db"))
	if err != nil {
		t.Fatalf("NewSQLiteDB: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.Initialize(); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	expiredAt := time.Now().Add(-time.Hour)
	accessToken, err := EncryptSecret(testEncryptor{}, "old-access")
	if err != nil {
		t.Fatalf("encrypt access token: %v", err)
	}
	refreshToken, err := EncryptSecret(testEncryptor{}, "old-refresh")
	if err != nil {
		t.Fatalf("encrypt refresh token: %v", err)
	}
	stored := models.ChannelConfig{
		EmailAuthMethod:        "oauth",
		EmailOAuthProviderType: models.EmailProviderTypeGoogle,
		EmailOAuthClientID:     "client-id",
		EmailOAuthAccessToken:  accessToken,
		EmailOAuthRefreshToken: refreshToken,
		EmailOAuthExpiresAt:    &expiredAt,
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatalf("marshal channel config: %v", err)
	}
	var channelID int
	if err := db.QueryRow(`
		INSERT INTO channels(name, type, direction, config)
		VALUES ('Email', 'email', 'inbound', ?) RETURNING id
	`, string(raw)).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	callerConfig := stored
	callerConfig.EmailOAuthAccessToken = "old-access"
	callerConfig.EmailOAuthRefreshToken = "old-refresh"
	provider := &blockingOAuthProvider{started: make(chan struct{}), release: make(chan struct{})}
	first := NewCredentialManager(db, testEncryptor{})
	second := NewCredentialManager(db, testEncryptor{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	results := make(chan error, 2)
	refresh := func(manager *CredentialManager) {
		_, refreshErr := manager.RefreshOAuthTokenIfNeeded(ctx, channelID, &callerConfig, provider)
		results <- refreshErr
	}
	go refresh(first)
	select {
	case <-provider.started:
	case <-time.After(2 * time.Second):
		close(provider.release)
		t.Fatal("first manager did not begin refresh")
	}
	secondStarted := make(chan struct{})
	go func() {
		close(secondStarted)
		refresh(second)
	}()
	<-secondStarted
	close(provider.release)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("refresh: %v", err)
		}
	}
	if got := provider.calls.Load(); got != 1 {
		t.Fatalf("provider refresh calls = %d, want 1", got)
	}
}
