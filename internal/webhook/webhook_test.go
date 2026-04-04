package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func writeSecret(t *testing.T, secret string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "secret")
	if err := os.WriteFile(path, []byte(secret), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestValidateSignature_Valid(t *testing.T) {
	secret := "test-secret-123"
	payload := []byte(`{"action":"opened"}`)
	sig := sign(payload, secret)
	secretFile := writeSecret(t, secret)

	if err := ValidateSignature(payload, sig, secretFile); err != nil {
		t.Fatalf("expected valid signature, got: %v", err)
	}
}

func TestValidateSignature_Invalid(t *testing.T) {
	secret := "test-secret-123"
	payload := []byte(`{"action":"opened"}`)
	secretFile := writeSecret(t, secret)

	err := ValidateSignature(payload, "sha256=deadbeef", secretFile)
	if err != ErrInvalidSignature {
		t.Fatalf("expected ErrInvalidSignature, got: %v", err)
	}
}

func TestValidateSignature_Missing(t *testing.T) {
	secretFile := writeSecret(t, "secret")

	err := ValidateSignature([]byte("{}"), "", secretFile)
	if err != ErrMissingSignature {
		t.Fatalf("expected ErrMissingSignature, got: %v", err)
	}
}

func TestValidateSignature_SecretWithNewline(t *testing.T) {
	secret := "my-secret"
	payload := []byte(`{"ref":"refs/heads/main"}`)
	sig := sign(payload, secret)
	// Secret file has trailing newline (common with echo > file)
	secretFile := writeSecret(t, secret+"\n")

	if err := ValidateSignature(payload, sig, secretFile); err != nil {
		t.Fatalf("expected valid signature with newline-trimmed secret, got: %v", err)
	}
}

func TestValidateSignature_BadSecretFile(t *testing.T) {
	err := ValidateSignature([]byte("{}"), "sha256=abc", "/nonexistent/secret")
	if err == nil {
		t.Fatal("expected error for missing secret file")
	}
}
