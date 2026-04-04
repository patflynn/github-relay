package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoad(t *testing.T) {
	cfgJSON := `{
		"port": 9090,
		"webhook_secret_file": "/tmp/secret",
		"consumers": [
			{
				"name": "test",
				"repo": "owner/repo",
				"events": ["push"],
				"action": "command",
				"command": "echo hello"
			}
		]
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(cfgJSON), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Port != 9090 {
		t.Errorf("port = %d, want 9090", cfg.Port)
	}
	if len(cfg.Consumers) != 1 {
		t.Fatalf("consumers = %d, want 1", len(cfg.Consumers))
	}
	if cfg.Consumers[0].Name != "test" {
		t.Errorf("name = %q, want %q", cfg.Consumers[0].Name, "test")
	}
}

func TestLoad_DefaultPort(t *testing.T) {
	cfgJSON := `{"webhook_secret_file": "/tmp/s", "consumers": []}`
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	os.WriteFile(path, []byte(cfgJSON), 0644)

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8077 {
		t.Errorf("default port = %d, want 8077", cfg.Port)
	}
}

func TestMatch_RepoAndEvent(t *testing.T) {
	consumers := []Consumer{
		{Name: "a", Repo: "owner/repo", Events: []string{"push"}},
		{Name: "b", Repo: "owner/other", Events: []string{"push"}},
		{Name: "c", Repo: "owner/repo", Events: []string{"pull_request"}},
	}

	matched := Match(consumers, "owner/repo", "push", "")
	if len(matched) != 1 || matched[0].Name != "a" {
		t.Errorf("expected [a], got %v", names(matched))
	}
}

func TestMatch_Wildcard(t *testing.T) {
	consumers := []Consumer{
		{Name: "all", Repo: "*", Events: []string{"push"}},
		{Name: "specific", Repo: "owner/repo", Events: []string{"push"}},
	}

	matched := Match(consumers, "anyone/anything", "push", "")
	if len(matched) != 1 || matched[0].Name != "all" {
		t.Errorf("expected [all], got %v", names(matched))
	}

	matched = Match(consumers, "owner/repo", "push", "")
	if len(matched) != 2 {
		t.Errorf("expected 2 matches, got %v", names(matched))
	}
}

func TestMatch_BranchFilter(t *testing.T) {
	consumers := []Consumer{
		{Name: "main-only", Repo: "*", Events: []string{"push"}, Branches: []string{"main"}},
		{Name: "all-branches", Repo: "*", Events: []string{"push"}},
	}

	matched := Match(consumers, "o/r", "push", "main")
	if len(matched) != 2 {
		t.Errorf("expected 2 matches for main, got %v", names(matched))
	}

	matched = Match(consumers, "o/r", "push", "develop")
	if len(matched) != 1 || matched[0].Name != "all-branches" {
		t.Errorf("expected [all-branches] for develop, got %v", names(matched))
	}
}

func TestMatch_NoMatch(t *testing.T) {
	consumers := []Consumer{
		{Name: "a", Repo: "owner/repo", Events: []string{"push"}},
	}

	matched := Match(consumers, "other/repo", "push", "")
	if len(matched) != 0 {
		t.Errorf("expected no matches, got %v", names(matched))
	}
}

func TestExtractRepo(t *testing.T) {
	payload := map[string]any{
		"repository": map[string]any{
			"full_name": "owner/repo",
		},
	}
	if got := ExtractRepo(payload); got != "owner/repo" {
		t.Errorf("repo = %q, want %q", got, "owner/repo")
	}
}

func TestExtractRepo_Missing(t *testing.T) {
	if got := ExtractRepo(map[string]any{}); got != "" {
		t.Errorf("repo = %q, want empty", got)
	}
}

func TestExtractBranch_Push(t *testing.T) {
	payload := map[string]any{"ref": "refs/heads/main"}
	if got := ExtractBranch("push", payload); got != "main" {
		t.Errorf("branch = %q, want %q", got, "main")
	}
}

func TestExtractBranch_PR(t *testing.T) {
	payload := map[string]any{
		"pull_request": map[string]any{
			"head": map[string]any{
				"ref": "feature-branch",
			},
		},
	}
	if got := ExtractBranch("pull_request", payload); got != "feature-branch" {
		t.Errorf("branch = %q, want %q", got, "feature-branch")
	}
}

func TestExtractBranch_OtherEvent(t *testing.T) {
	payload := map[string]any{"ref": "refs/heads/main"}
	if got := ExtractBranch("check_run", payload); got != "" {
		t.Errorf("branch = %q, want empty for check_run", got)
	}
}

func names(consumers []Consumer) []string {
	var out []string
	for _, c := range consumers {
		out = append(out, c.Name)
	}
	return out
}
