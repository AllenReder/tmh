package config

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultBaseURL       = "https://api.openai.com/v1"
	DefaultTMHTimeout    = 30 * time.Second
	DefaultTMHATimeout   = 90 * time.Second
	defaultConfigDirName = "tmh"
	defaultConfigName    = "config.toml"
)

type fileConfig struct {
	BaseURL            string `toml:"base_url"`
	Model              string `toml:"model"`
	TMHTimeoutSeconds  int    `toml:"tmh_timeout_seconds"`
	TMHATimeoutSeconds int    `toml:"tmha_timeout_seconds"`
}

// Overrides are explicit command-line values. Empty strings mean unset.
type Overrides struct {
	BaseURL string
	Model   string
}

// Config is the effective runtime configuration.
type Config struct {
	Path        string
	BaseURL     string
	Model       string
	APIKey      string
	TMHTimeout  time.Duration
	TMHATimeout time.Duration
	Sources     map[string]string
}

func Path() (string, error) {
	if xdg := strings.TrimSpace(os.Getenv("XDG_CONFIG_HOME")); xdg != "" {
		return filepath.Join(xdg, defaultConfigDirName, defaultConfigName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}
	return filepath.Join(home, ".config", defaultConfigDirName, defaultConfigName), nil
}

func Load(overrides Overrides, requireModel bool) (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}

	cfg := Config{
		Path:        path,
		BaseURL:     DefaultBaseURL,
		TMHTimeout:  DefaultTMHTimeout,
		TMHATimeout: DefaultTMHATimeout,
		Sources: map[string]string{
			"base_url":     "default",
			"model":        "unset",
			"tmh_timeout":  "default",
			"tmha_timeout": "default",
			"api_key":      "unset",
		},
	}

	if err := applyFile(&cfg, path); err != nil {
		return Config{}, err
	}
	applyEnvironment(&cfg)
	applyOverrides(&cfg, overrides)

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)
	if err := validate(cfg, requireModel); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func applyFile(cfg *Config, path string) error {
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open config %s: %w", path, err)
	}
	defer f.Close()

	var fc fileConfig
	decoder := toml.NewDecoder(io.LimitReader(f, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fc); err != nil {
		return fmt.Errorf("parse config %s: %w", path, err)
	}
	if strings.TrimSpace(fc.BaseURL) != "" {
		cfg.BaseURL = fc.BaseURL
		cfg.Sources["base_url"] = path
	}
	if strings.TrimSpace(fc.Model) != "" {
		cfg.Model = fc.Model
		cfg.Sources["model"] = path
	}
	if fc.TMHTimeoutSeconds != 0 {
		cfg.TMHTimeout = time.Duration(fc.TMHTimeoutSeconds) * time.Second
		cfg.Sources["tmh_timeout"] = path
	}
	if fc.TMHATimeoutSeconds != 0 {
		cfg.TMHATimeout = time.Duration(fc.TMHATimeoutSeconds) * time.Second
		cfg.Sources["tmha_timeout"] = path
	}
	return nil
}

func applyEnvironment(cfg *Config) {
	if value := strings.TrimSpace(os.Getenv("TMH_BASE_URL")); value != "" {
		cfg.BaseURL = value
		cfg.Sources["base_url"] = "TMH_BASE_URL"
	}
	if value := strings.TrimSpace(os.Getenv("TMH_MODEL")); value != "" {
		cfg.Model = value
		cfg.Sources["model"] = "TMH_MODEL"
	}
	if value, ok := os.LookupEnv("TMH_API_KEY"); ok {
		cfg.APIKey = value
		cfg.Sources["api_key"] = "TMH_API_KEY"
	} else if value, ok := os.LookupEnv("OPENAI_API_KEY"); ok {
		cfg.APIKey = value
		cfg.Sources["api_key"] = "OPENAI_API_KEY"
	}
}

func applyOverrides(cfg *Config, overrides Overrides) {
	if value := strings.TrimSpace(overrides.BaseURL); value != "" {
		cfg.BaseURL = value
		cfg.Sources["base_url"] = "--base-url"
	}
	if value := strings.TrimSpace(overrides.Model); value != "" {
		cfg.Model = value
		cfg.Sources["model"] = "--model"
	}
}

func validate(cfg Config, requireModel bool) error {
	parsed, err := url.Parse(cfg.BaseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("base_url must be an absolute http(s) URL, got %q", cfg.BaseURL)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("base_url must not contain credentials, a query, or a fragment")
	}
	if requireModel && cfg.Model == "" {
		return fmt.Errorf("model is required; set it in %s, TMH_MODEL, or --model", cfg.Path)
	}
	if cfg.TMHTimeout <= 0 || cfg.TMHTimeout > 30*time.Minute {
		return fmt.Errorf("tmh_timeout_seconds must be between 1 and 1800")
	}
	if cfg.TMHATimeout <= 0 || cfg.TMHATimeout > 30*time.Minute {
		return fmt.Errorf("tmha_timeout_seconds must be between 1 and 1800")
	}
	return nil
}
