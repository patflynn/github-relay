package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/patflynn/github-relay/internal/config"
)

func sign(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func setupRelay(t *testing.T, consumers []config.Consumer) *relay {
	t.Helper()
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "secret")
	if err := os.WriteFile(secretPath, []byte("test-secret"), 0600); err != nil {
		t.Fatalf("writing secret: %v", err)
	}

	return &relay{cfg: &config.Config{
		Port:              8077,
		WebhookSecretFile: secretPath,
		Consumers:         consumers,
	}}
}

// waitForDispatches fails the test if the background dispatches do not finish.
func waitForDispatches(t *testing.T, rl *relay) {
	t.Helper()
	if !rl.waitForDispatches(30 * time.Second) {
		t.Fatal("timed out waiting for dispatches")
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

	rl := setupRelay(t, []config.Consumer{
		{
			Name:   "test-consumer",
			Repo:   "owner/repo",
			Events: []string{"push"},
			Action: "http",
			URL:    downstream.URL,
		},
	})

	payload := `{"ref":"refs/heads/main","repository":{"full_name":"owner/repo"}}`
	sig := sign([]byte(payload), "test-secret")

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-GitHub-Delivery", "abc-123")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr := httptest.NewRecorder()
	rl.handleWebhook(rr, req)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}

	waitForDispatches(t, rl)

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
	rl := setupRelay(t, nil)

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader("{}"))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", "sha256=invalid")

	rr := httptest.NewRecorder()
	rl.handleWebhook(rr, req)

	if rr.Code != 403 {
		t.Fatalf("status = %d, want 403", rr.Code)
	}
}

func TestWebhookHandler_MissingEventHeader(t *testing.T) {
	rl := setupRelay(t, nil)

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader("{}"))
	// No X-GitHub-Event header

	rr := httptest.NewRecorder()
	rl.handleWebhook(rr, req)

	if rr.Code != 400 {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestWebhookHandler_NoMatchingConsumers(t *testing.T) {
	rl := setupRelay(t, []config.Consumer{
		{
			Name:   "specific",
			Repo:   "other/repo",
			Events: []string{"push"},
			Action: "http",
			URL:    "http://localhost:1234",
		},
	})

	payload := `{"repository":{"full_name":"owner/repo"}}`
	sig := sign([]byte(payload), "test-secret")

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr := httptest.NewRecorder()
	rl.handleWebhook(rr, req)

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

	rl := setupRelay(t, []config.Consumer{
		{
			Name:     "main-only",
			Repo:     "owner/repo",
			Events:   []string{"push"},
			Branches: []string{"main"},
			Action:   "http",
			URL:      downstream.URL,
		},
	})

	// Push to develop — should NOT match
	payload := `{"ref":"refs/heads/develop","repository":{"full_name":"owner/repo"}}`
	sig := sign([]byte(payload), "test-secret")

	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	rr := httptest.NewRecorder()
	rl.handleWebhook(rr, req)
	waitForDispatches(t, rl)

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
	rl.handleWebhook(rr, req)
	waitForDispatches(t, rl)

	mu.Lock()
	if !dispatched {
		t.Error("should dispatch for main branch")
	}
	mu.Unlock()
}

// TestWebhookHandler_AcknowledgesBeforeDispatch exercises the fix for #7: the
// delivery is acknowledged as soon as it is validated, so a consumer that takes
// longer than GitHub's ~10s delivery timeout can no longer turn an accepted
// delivery into a failed one — and the dispatch still runs to completion.
func TestWebhookHandler_AcknowledgesBeforeDispatch(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "done")
	sleep := 3 * time.Second

	rl := setupRelay(t, []config.Consumer{
		{
			Name:    "slow-consumer",
			Repo:    "owner/repo",
			Events:  []string{"push"},
			Action:  "command",
			Command: fmt.Sprintf("sleep %d && touch %s", int(sleep.Seconds()), marker),
		},
	})

	payload := `{"ref":"refs/heads/main","repository":{"full_name":"owner/repo"}}`
	req := httptest.NewRequest("POST", "/hooks/github", strings.NewReader(payload))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sign([]byte(payload), "test-secret"))

	rr := httptest.NewRecorder()
	start := time.Now()
	rl.handleWebhook(rr, req)
	elapsed := time.Since(start)

	if rr.Code != 200 {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if elapsed > sleep/2 {
		t.Errorf("handler took %v, want well under the consumer's %v", elapsed, sleep)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("consumer finished before the handler returned; dispatch was not async")
	}

	waitForDispatches(t, rl)

	if _, err := os.Stat(marker); err != nil {
		t.Errorf("consumer did not run to completion after the response: %v", err)
	}
}
