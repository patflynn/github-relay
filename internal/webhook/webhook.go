package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
)

var (
	ErrMissingSignature = errors.New("missing X-Hub-Signature-256 header")
	ErrInvalidSignature = errors.New("invalid webhook signature")
)

// ValidateSignature checks the HMAC-SHA256 signature of a GitHub webhook payload.
func ValidateSignature(payload []byte, signature string, secretFile string) error {
	if signature == "" {
		return ErrMissingSignature
	}

	secret, err := os.ReadFile(secretFile)
	if err != nil {
		return fmt.Errorf("reading secret file: %w", err)
	}

	// Trim whitespace/newlines from secret
	secret = []byte(strings.TrimSpace(string(secret)))

	mac := hmac.New(sha256.New, secret)
	mac.Write(payload)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ErrInvalidSignature
	}

	return nil
}
