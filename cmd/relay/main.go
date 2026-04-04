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

	mux := http.NewServeMux()
	mux.HandleFunc("POST /hooks/github", webhookHandler(cfg))

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
}

func webhookHandler(cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
		if err := webhook.ValidateSignature(body, signature, cfg.WebhookSecretFile); err != nil {
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
		matched := config.Match(cfg.Consumers, repo, event, branch)
		if len(matched) == 0 {
			slog.Debug("no matching consumers", "event", event, "repo", repo)
			w.WriteHeader(http.StatusOK)
			return
		}

		slog.Info("dispatching to consumers", "count", len(matched))

		// Dispatch to all matching consumers
		for _, consumer := range matched {
			if err := dispatch.Dispatch(r.Context(), consumer, body, event, delivery); err != nil {
				// Log error but return 200 to GitHub to avoid retry storms
				slog.Error("dispatch failed",
					"consumer", consumer.Name,
					"action", consumer.Action,
					"error", err,
				)
			}
		}

		w.WriteHeader(http.StatusOK)
	}
}
