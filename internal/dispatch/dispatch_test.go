package dispatch

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/patflynn/github-relay/internal/config"
)

func TestDispatchHTTP(t *testing.T) {
	var (
		gotBody    []byte
		gotEvent   string
		gotDeliver string
	)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		gotEvent = r.Header.Get("X-GitHub-Event")
		gotDeliver = r.Header.Get("X-GitHub-Delivery")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	consumer := config.Consumer{
		Name:   "test-http",
		Action: "http",
		URL:    server.URL,
	}

	payload := []byte(`{"action":"completed"}`)
	err := Dispatch(context.Background(), consumer, payload, "check_run", "delivery-123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if string(gotBody) != string(payload) {
		t.Errorf("body = %q, want %q", gotBody, payload)
	}
	if gotEvent != "check_run" {
		t.Errorf("event = %q, want %q", gotEvent, "check_run")
	}
	if gotDeliver != "delivery-123" {
		t.Errorf("delivery = %q, want %q", gotDeliver, "delivery-123")
	}
}

func TestDispatchHTTP_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	consumer := config.Consumer{
		Name:   "test-http-err",
		Action: "http",
		URL:    server.URL,
	}

	err := Dispatch(context.Background(), consumer, []byte("{}"), "push", "")
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
}

func TestDispatchCommand(t *testing.T) {
	payload := map[string]any{"ref": "refs/heads/main"}
	payloadBytes, _ := json.Marshal(payload)

	consumer := config.Consumer{
		Name:    "test-cmd",
		Action:  "command",
		Command: "cat", // cat reads stdin and prints it — just verifies stdin works
	}

	err := Dispatch(context.Background(), consumer, payloadBytes, "push", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDispatchCommand_Failure(t *testing.T) {
	consumer := config.Consumer{
		Name:    "test-cmd-fail",
		Action:  "command",
		Command: "false", // always exits 1
	}

	err := Dispatch(context.Background(), consumer, []byte("{}"), "push", "")
	if err == nil {
		t.Fatal("expected error for failing command")
	}
}

func TestDispatchUnknownAction(t *testing.T) {
	consumer := config.Consumer{
		Name:   "test-unknown",
		Action: "bogus",
	}

	err := Dispatch(context.Background(), consumer, []byte("{}"), "push", "")
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
