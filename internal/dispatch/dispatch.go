package dispatch

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/patflynn/github-relay/internal/config"
)

const httpTimeout = 10 * time.Second

// Dispatch sends a webhook payload to the given consumer.
func Dispatch(ctx context.Context, consumer config.Consumer, payload []byte, event, delivery string) error {
	switch consumer.Action {
	case "systemd":
		return dispatchSystemd(ctx, consumer, payload)
	case "http":
		return dispatchHTTP(ctx, consumer, payload, event, delivery)
	case "command":
		return dispatchCommand(ctx, consumer, payload)
	default:
		return fmt.Errorf("unknown action %q", consumer.Action)
	}
}

func dispatchSystemd(ctx context.Context, consumer config.Consumer, payload []byte) error {
	if consumer.Unit == "" {
		return fmt.Errorf("systemd consumer %q has no unit", consumer.Name)
	}

	slog.Info("starting systemd unit", "consumer", consumer.Name, "unit", consumer.Unit)

	cmd := exec.CommandContext(ctx, "systemctl", "start", consumer.Unit)
	cmd.Env = append(cmd.Environ(), "GITHUB_EVENT="+string(payload))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("systemctl start %s: %w: %s", consumer.Unit, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func dispatchHTTP(ctx context.Context, consumer config.Consumer, payload []byte, event, delivery string) error {
	if consumer.URL == "" {
		return fmt.Errorf("http consumer %q has no url", consumer.Name)
	}

	slog.Info("posting to HTTP endpoint", "consumer", consumer.Name, "url", consumer.URL)

	ctx, cancel := context.WithTimeout(ctx, httpTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, consumer.URL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	if delivery != "" {
		req.Header.Set("X-GitHub-Delivery", delivery)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("http post to %s: %w", consumer.URL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return fmt.Errorf("http post to %s returned %d", consumer.URL, resp.StatusCode)
	}

	return nil
}

func dispatchCommand(ctx context.Context, consumer config.Consumer, payload []byte) error {
	if consumer.Command == "" {
		return fmt.Errorf("command consumer %q has no command", consumer.Name)
	}

	slog.Info("running command", "consumer", consumer.Name, "command", consumer.Command)

	cmd := exec.CommandContext(ctx, "sh", "-c", consumer.Command)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Env = append(cmd.Environ(), "GITHUB_EVENT="+string(payload))

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("command %q: %w: %s", consumer.Command, err, strings.TrimSpace(string(output)))
	}
	return nil
}
