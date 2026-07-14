package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadPrecedenceAndKeyFallback(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMH_BASE_URL", "https://env.example/v1")
	t.Setenv("TMH_MODEL", "env-model")
	t.Setenv("OPENAI_API_KEY", "fallback-key")
	t.Setenv("TMH_API_KEY", "")
	path := filepath.Join(dir, "tmh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `base_url = "https://file.example/v1"
model = "file-model"
tmh_timeout_seconds = 17
tmha_timeout_seconds = 41
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(Overrides{BaseURL: "https://flag.example/v1", Model: "flag-model"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.BaseURL != "https://flag.example/v1" || cfg.Model != "flag-model" {
		t.Fatalf("unexpected overrides: %+v", cfg)
	}
	if cfg.APIKey != "" || cfg.Sources["api_key"] != "TMH_API_KEY" {
		t.Fatalf("explicit empty TMH_API_KEY must disable fallback: %+v", cfg)
	}
	if cfg.TMHTimeout != 17*time.Second || cfg.TMHATimeout != 41*time.Second {
		t.Fatalf("unexpected timeouts: %+v", cfg)
	}
}

func TestLoadFallsBackToOpenAIKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMH_API_KEY", "")
	if err := os.Unsetenv("TMH_API_KEY"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "openai-key")
	cfg, err := Load(Overrides{Model: "test"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "openai-key" || cfg.Sources["api_key"] != "OPENAI_API_KEY" {
		t.Fatalf("unexpected key selection: %+v", cfg)
	}
}

func TestLoadRejectsUnknownFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "tmh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("model = \"test\"\nunknown = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(Overrides{}, true)
	if err == nil || !strings.Contains(err.Error(), "strict mode") {
		t.Fatalf("expected unknown field error, got %v", err)
	}
}

func TestLoadRequiresModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMH_MODEL", "")
	_, err := Load(Overrides{}, true)
	if err == nil || !strings.Contains(err.Error(), "model is required") {
		t.Fatalf("expected model error, got %v", err)
	}
}
