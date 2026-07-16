package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AllenReder/tmh/internal/command"
)

func TestLoadPrecedenceAndSources(t *testing.T) {
	isolateConfigEnvironment(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMH_BASE_URL", "https://env.example/v1")
	t.Setenv("TMH_MODEL", "env-model")
	t.Setenv("TMH_SHELL", "fish")
	t.Setenv("OPENAI_API_KEY", "fallback-key")
	t.Setenv("TMH_API_KEY", "")
	path := writeConfig(t, dir, `base_url = "https://file.example/v1"
model = "file-model"
shell = "zsh"
generate_timeout_seconds = 17
agent_timeout_seconds = 41
`)

	cfg, err := Load(Overrides{
		BaseURL: "https://flag.example/v1/",
		Model:   "flag-model",
		Shell:   "bash",
	}, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://flag.example/v1" || cfg.Model != "flag-model" {
		t.Fatalf("unexpected overrides: %+v", cfg)
	}
	if cfg.Shell != command.Bash || cfg.Sources["shell"] != "--shell" {
		t.Fatalf("unexpected shell selection: %+v", cfg)
	}
	if cfg.APIKey != "" || cfg.Sources["api_key"] != "TMH_API_KEY" {
		t.Fatalf("explicit empty TMH_API_KEY must disable fallback: %+v", cfg)
	}
	if cfg.GenerateTimeout != 17*time.Second || cfg.AgentTimeout != 41*time.Second {
		t.Fatalf("unexpected timeouts: %+v", cfg)
	}
	if cfg.Sources["generate_timeout"] != path || cfg.Sources["agent_timeout"] != path {
		t.Fatalf("timeout source was not retained: %+v", cfg.Sources)
	}
}

func TestLoadShellPrecedenceWithoutCLIOverride(t *testing.T) {
	isolateConfigEnvironment(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMH_SHELL", "fish")
	writeConfig(t, dir, "shell = \"zsh\"\n")

	cfg, err := Load(Overrides{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shell != command.Fish || cfg.Sources["shell"] != "TMH_SHELL" {
		t.Fatalf("TMH_SHELL did not override config: %+v", cfg)
	}
}

func TestLoadDefaults(t *testing.T) {
	isolateConfigEnvironment(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	cfg, err := Load(Overrides{}, false)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Shell != command.Auto || cfg.Sources["shell"] != "default" {
		t.Fatalf("unexpected default shell: %+v", cfg)
	}
	if cfg.GenerateTimeout != DefaultGenerateTimeout || cfg.AgentTimeout != DefaultAgentTimeout {
		t.Fatalf("unexpected default timeouts: %+v", cfg)
	}
}

func TestLoadFallsBackToOpenAIKey(t *testing.T) {
	isolateConfigEnvironment(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "openai-key")
	cfg, err := Load(Overrides{Model: "test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "openai-key" || cfg.Sources["api_key"] != "OPENAI_API_KEY" {
		t.Fatalf("unexpected key selection: %+v", cfg)
	}
}

func TestLoadRejectsUnknownAndLegacyFields(t *testing.T) {
	for _, field := range []string{"unknown = true\n", "exec = \"inspection\"\n", "tmh_timeout_seconds = 30\n", "tmha_timeout_seconds = 90\n"} {
		t.Run(strings.Fields(field)[0], func(t *testing.T) {
			isolateConfigEnvironment(t)
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeConfig(t, dir, field)
			_, err := Load(Overrides{}, false)
			if err == nil || !strings.Contains(err.Error(), "strict mode") {
				t.Fatalf("expected unknown field error, got %v", err)
			}
		})
	}
}

func TestLoadRejectsOversizedConfigBeforeUnknownFieldsCanBeTruncated(t *testing.T) {
	isolateConfigEnvironment(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	content := strings.Repeat("# padding\n", maxConfigBytes/10+1) + "unknown_after_limit = true\n"
	writeConfig(t, dir, content)
	_, err := Load(Overrides{}, false)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized config error, got %v", err)
	}
}

func TestLoadRejectsInvalidShellFromItsSource(t *testing.T) {
	isolateConfigEnvironment(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMH_SHELL", "pwsh")
	_, err := Load(Overrides{}, false)
	if err == nil || !strings.Contains(err.Error(), "shell from TMH_SHELL") {
		t.Fatalf("expected sourced shell error, got %v", err)
	}
}

func TestLoadRejectsExplicitInvalidTimeouts(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{"zero generate", "generate_timeout_seconds = 0\n", "generate_timeout_seconds"},
		{"large generate", "generate_timeout_seconds = 1801\n", "generate_timeout_seconds"},
		{"zero agent", "agent_timeout_seconds = 0\n", "agent_timeout_seconds"},
		{"large agent", "agent_timeout_seconds = 1801\n", "agent_timeout_seconds"},
		{"overflow generate", "generate_timeout_seconds = 36028797018963969\n", "generate_timeout_seconds"},
		{"overflow agent", "agent_timeout_seconds = 36028797018963969\n", "agent_timeout_seconds"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			isolateConfigEnvironment(t)
			dir := t.TempDir()
			t.Setenv("XDG_CONFIG_HOME", dir)
			writeConfig(t, dir, test.content)
			_, err := Load(Overrides{}, false)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s error, got %v", test.want, err)
			}
		})
	}
}

func TestLoadRequiresModel(t *testing.T) {
	isolateConfigEnvironment(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	_, err := Load(Overrides{}, true)
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected model error, got %v", err)
	}
}

func writeConfig(t *testing.T, configHome, content string) string {
	t.Helper()
	path := filepath.Join(configHome, "tmh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func isolateConfigEnvironment(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"TMH_BASE_URL",
		"TMH_MODEL",
		"TMH_SHELL",
		"TMH_API_KEY",
		"OPENAI_API_KEY",
	} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
