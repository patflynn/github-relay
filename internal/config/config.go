package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type Config struct {
	Port              int        `json:"port"`
	WebhookSecretFile string     `json:"webhook_secret_file"`
	Consumers         []Consumer `json:"consumers"`
}

type Consumer struct {
	Name     string   `json:"name"`
	Repo     string   `json:"repo"`
	Events   []string `json:"events"`
	Branches []string `json:"branches,omitempty"`
	Action   string   `json:"action"`
	// Action-specific fields
	Unit    string `json:"unit,omitempty"`    // systemd
	URL     string `json:"url,omitempty"`     // http
	Command string `json:"command,omitempty"` // command
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Port == 0 {
		cfg.Port = 8077
	}

	return &cfg, nil
}

// Match returns all consumers that match the given event.
func Match(consumers []Consumer, repo, event, branch string) []Consumer {
	var matched []Consumer
	for _, c := range consumers {
		if !matchRepo(c.Repo, repo) {
			continue
		}
		if !matchEvent(c.Events, event) {
			continue
		}
		if !matchBranch(c.Branches, branch) {
			continue
		}
		matched = append(matched, c)
	}
	return matched
}

func matchRepo(pattern, repo string) bool {
	if pattern == "*" {
		return true
	}
	return strings.EqualFold(pattern, repo)
}

func matchEvent(events []string, event string) bool {
	for _, e := range events {
		if e == event {
			return true
		}
	}
	return false
}

func matchBranch(branches []string, branch string) bool {
	if len(branches) == 0 {
		return true
	}
	for _, b := range branches {
		if b == branch {
			return true
		}
	}
	return false
}

// ExtractRepo extracts the repository full name from a webhook payload.
func ExtractRepo(payload map[string]any) string {
	repo, ok := payload["repository"].(map[string]any)
	if !ok {
		return ""
	}
	name, _ := repo["full_name"].(string)
	return name
}

// ExtractBranch extracts the branch name from a webhook payload.
// For push events, it uses "ref" (refs/heads/main -> main).
// For pull_request events, it uses pull_request.head.ref.
func ExtractBranch(event string, payload map[string]any) string {
	switch event {
	case "push":
		ref, _ := payload["ref"].(string)
		return strings.TrimPrefix(ref, "refs/heads/")
	case "pull_request", "pull_request_review":
		pr, ok := payload["pull_request"].(map[string]any)
		if !ok {
			return ""
		}
		head, ok := pr["head"].(map[string]any)
		if !ok {
			return ""
		}
		ref, _ := head["ref"].(string)
		return ref
	default:
		return ""
	}
}
