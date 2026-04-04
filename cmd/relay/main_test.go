package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/patflynn/github-relay/internal/config"
)

func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func setupConfig(t *testing.T, consumers []config.Consumer) *config.Config {
	t.Helper()
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret")
	os.WriteFile(secretPath, []byte("test-secret"), 0600)

	return &config.Config{
		Port:              8077,
		WebhookSecretFile: secretPath,
		Consumers:         consumers,
	}
}

func TestWebhookHandler_ValidPush(t *testing.T) {
	// Set up an HTTP consumer to verify dispatch
	var mu sync.Mutex
	var receivedBody string
	var receivedEvent string

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		receivedEvent = r.Header.Get("X-GitHub-Event")
		w.WriteHeader(200)
	}))
	defer downstream.Close()

	cfg := setupConfig(t, []config.Consumer{
		{
			Name:   "test-consumer",
			Repo:   "owner/repo",
			Events: []string{"push"},
			Action: "http",
			URL:    downstream.URL,
		},
	})

	handler := webhookHandler(cfg)
	payload := `{"ref":"refs/heads/main","repository":{"full_name":"owner/repo"}}`
	sig := sign([]byte(payload), "test-secret")

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "abc-123")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	mu.Lock()
	defer mu.Unlock()
	if receivedBody != payload {
		t.Errorf("downstream body = %q, want %q", receivedBody, payload)
	}
	if receivedEvent != "push" {
		t.Errorf("downstream event = %q, want %q", receivedEvent, "push")
	}
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	cfg := setupConfig(t, nil)
	handler := webhookHandler(cfg)

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader("{}"))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != 403 {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestWebhookHandler_MissingEventHeader(t *testing.T) {
	cfg := setupConfig(t, nil)
	handler := webhookHandler(cfg)

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader("{}"))
	// No X-GitHub-Event header

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != 400 {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestWebhookHandler_NoMatchingConsumers(t *testing.T) {
	cfg := setupConfig(t, []config.Consumer{
		{
			Name:   "specific",
			Repo:   "other/repo",
			Events: []string{"push"},
			Action: "http",
			URL:    "http://localhost:1234",
		},
	})

	handler := webhookHandler(cfg)
	payload := `{"repository":{"full_name":"owner/repo"}}`
	sig := sign([]byte(payload), "test-secret")

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr := httptest.NewRecorder()
	handler(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200 for unmatched events", rr.Code)
	}
}

func TestWebhookHandler_BranchFilter(t *testing.T) {
	var mu sync.Mutex
	var dispatched bool

	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		dispatched = true
		mu.Unlock()
		w.WriteHeader(200)
	}))
	defer downstream.Close()

	cfg := setupConfig(t, []config.Consumer{
		{
			Name:     "main-only",
			Repo:     "owner/repo",
			Events:   []string{"push"},
			Branches: []string{"main"},
			Action:   "http",
			URL:      downstream.URL,
		},
	})

	handler := webhookHandler(cfg)

	// Push to develop — should NOT match
	payload := `{"ref":"refs/heads/develop","repository":{"full_name":"owner/repo"}}`
	sig := sign([]byte(payload), "test-secret")

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr := httptest.NewRecorder()
	handler(rr, req)

	mu.Lock()
	if dispatched {
		t.Error("should not dispatch for develop branch")
	}
	mu.Unlock()

	// Push to main — should match
	payload = `{"ref":"refs/heads/main","repository":{"full_name":"owner/repo"}}`
	sig = sign([]byte(payload), "test-secret")

	req = httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr = httptest.NewRecorder()
	handler(rr, req)

	mu.Lock()
	if !dispatched {
		t.Error("should dispatch for main branch")
	}
	mu.Unlock()
}
