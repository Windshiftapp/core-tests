package handlers

import (
	"errors"
	"testing"

	webauthnlib "github.com/go-webauthn/webauthn/webauthn"
	"golang.org/x/crypto/bcrypt"
)

func TestDummyPasswordHashIsValidBcryptAtDefaultCost(t *testing.T) {
	cost, err := bcrypt.Cost(dummyPasswordHash)
	if err != nil {
		t.Fatalf("dummyPasswordHash is not valid bcrypt: %v", err)
	}
	if cost != bcrypt.DefaultCost {
		t.Fatalf("dummyPasswordHash cost = %d, want %d", cost, bcrypt.DefaultCost)
	}
	if err := bcrypt.CompareHashAndPassword(dummyPasswordHash, []byte("not-the-dummy-password")); !errors.Is(err, bcrypt.ErrMismatchedHashAndPassword) {
		t.Fatalf("comparison error = %v, want ErrMismatchedHashAndPassword", err)
	}
}

func TestPadLoginCredentialsHidesCredentialCount(t *testing.T) {
	for _, input := range [][]webauthnlib.Credential{
		nil,
		{{ID: []byte("real-credential")}},
	} {
		padded := padLoginCredentials(input, func(index int) []byte { return []byte{byte(index)} })
		if len(padded) != maxCredentialsPerUser {
			t.Fatalf("padded credential count = %d, want %d", len(padded), maxCredentialsPerUser)
		}
	}
}
