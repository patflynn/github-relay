package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/patflynn/github-relay/internal/config"
	"github.com/patflynn/github-relay/internal/dispatch"
	"github.com/patflynn/github-relay/internal/webhook"
)

func main() {
	configPath := flag.String("config", "/etc/github-relay/config.json", "path to config file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load(*configPath)
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	slog.Info("loaded config", "consumers", len(cfg.Consumers), "port", cfg.Port)

	rl := &relay{cfg: cfg}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/github", rl.handleWebhook)

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.Port),
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	// Graceful shutdown
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	go func() {
		slog.Info("starting server", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	<-ctx.Done()
	slog.Info("shutting down")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("shutdown error", "error", err)
	}

	// Deliveries are acknowledged before they are dispatched, so wait for the
	// dispatches already in flight rather than dropping them on the floor.
	if !rl.waitForDispatches(dispatchTimeout) {
		slog.Warn("gave up waiting for in-flight dispatches", "timeout", dispatchTimeout)
	}
}

// dispatchTimeout bounds a single consumer dispatch. It is deliberately longer
// than GitHub's ~10s delivery timeout: dispatch runs after the delivery has been
// acknowledged, so it is no longer racing the HTTP response.
const dispatchTimeout = 60 * time.Second

type relay struct {
	cfg *config.Config
	wg  sync.WaitGroup
}

// waitForDispatches blocks until every in-flight dispatch has finished, or the
// timeout elapses. It reports whether they all finished.
func (rl *relay) waitForDispatches(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		rl.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// handleWebhook validates a delivery, acknowledges it, and then dispatches to
// the matching consumers in the background. GitHub gives us ~10s to respond and
// never retries a failed delivery, so a slow consumer must not be able to turn a
// delivery we accepted into a delivery GitHub records as failed.
func (rl *relay) handleWebhook(w http.ResponseWriter, r *http.Request) {
	event := r.Header.Get("X-GitHub-Event")
	if event == "" {
		http.Error(w, "missing X-GitHub-Event header", http.StatusBadRequest)
		return
	}

	delivery := r.Header.Get("X-GitHub-Delivery")

	body, err := io.ReadAll(r.Body)
	if err != nil {
		slog.Error("failed to read body", "error", err)
		http.Error(w, "failed to read body", http.StatusInternalServerError)
		return
	}

	// Validate signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if err := webhook.ValidateSignature(body, signature, rl.cfg.WebhookSecretFile); err != nil {
		slog.Warn("signature validation failed", "error", err, "delivery", delivery)
		http.Error(w, "invalid signature", http.StatusForbidden)
		return
	}

	// Parse payload for matching
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		slog.Error("failed to parse payload", "error", err, "delivery", delivery)
		http.Error(w, "invalid JSON payload", http.StatusBadRequest)
		return
	}

	repo := config.ExtractRepo(payload)
	branch := config.ExtractBranch(event, payload)

	slog.Info("received webhook",
		"event", event,
		"delivery", delivery,
		"repo", repo,
		"branch", branch,
	)

	// Match consumers
	matched := config.Match(rl.cfg.Consumers, repo, event, branch)
	if len(matched) == 0 {
		slog.Debug("no matching consumers", "event", event, "repo", repo)
		w.WriteHeader(http.StatusOK)
		return
	}

	slog.Info("dispatching to consumers", "count", len(matched), "delivery", delivery)

	// Acknowledge before dispatching: the delivery is valid and accepted, and
	// whether a consumer succeeds is our problem, not GitHub's.
	w.WriteHeader(http.StatusOK)

	for _, consumer := range matched {
		rl.wg.Add(1)
		go func() {
			defer rl.wg.Done()

			// Detached from the request context, which is cancelled as soon as
			// the response above is written.
			ctx, cancel := context.WithTimeout(context.Background(), dispatchTimeout)
			defer cancel()

			if err := dispatch.Dispatch(ctx, consumer, body, event, delivery); err != nil {
				slog.Error("dispatch failed",
					"consumer", consumer.Name,
					"action", consumer.Action,
					"delivery", delivery,
					"error", err,
				)
			}
		}()
	}
}
