package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AllenReder/tmh/internal/command"
	toml "github.com/pelletier/go-toml/v2"
)

const (
	DefaultBaseURL         = "https://api.openai.com/v1"
	DefaultShell           = command.Auto
	DefaultGenerateTimeout = 30 * time.Second
	DefaultAgentTimeout    = 90 * time.Second
	defaultConfigDirName   = "tmh"
	defaultConfigName      = "config.toml"
	maxConfigBytes         = 1 << 20
)

type fileConfig struct {
	BaseURL                string  `toml:"base_url"`
	Model                  string  `toml:"model"`
	Shell                  *string `toml:"shell"`
	GenerateTimeoutSeconds *int    `toml:"generate_timeout_seconds"`
	AgentTimeoutSeconds    *int    `toml:"agent_timeout_seconds"`
}

// Overrides are explicit command-line values. Empty strings mean unset.
type Overrides struct {
	BaseURL string
	Model   string
	Shell   string
}

// Config is the effective runtime configuration.
type Config struct {
	Path            string
	BaseURL         string
	Model           string
	Shell           command.Shell
	APIKey          string
	GenerateTimeout time.Duration
	AgentTimeout    time.Duration
	Sources         map[string]string
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
		Path:            path,
		BaseURL:         DefaultBaseURL,
		Shell:           DefaultShell,
		GenerateTimeout: DefaultGenerateTimeout,
		AgentTimeout:    DefaultAgentTimeout,
		Sources: map[string]string{
			"base_url":         "default",
			"model":            "unset",
			"shell":            "default",
			"generate_timeout": "default",
			"agent_timeout":    "default",
			"api_key":          "unset",
		},
	}

	if err := applyFile(&cfg, path); err != nil {
		return Config{}, err
	}
	applyEnvironment(&cfg)
	applyOverrides(&cfg, overrides)

	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	cfg.Model = strings.TrimSpace(cfg.Model)
	parsedShell, err := command.ParseShell(string(cfg.Shell))
	if err != nil {
		return Config{}, fmt.Errorf("shell from %s: %w", cfg.Sources["shell"], err)
	}
	cfg.Shell = parsedShell
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

	data, err := io.ReadAll(io.LimitReader(f, maxConfigBytes+1))
	if err != nil {
		return fmt.Errorf("read config %s: %w", path, err)
	}
	if len(data) > maxConfigBytes {
		return fmt.Errorf("config %s exceeds the %d byte safety limit", path, maxConfigBytes)
	}

	var fc fileConfig
	decoder := toml.NewDecoder(bytes.NewReader(data))
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
	if fc.Shell != nil {
		cfg.Shell = command.Shell(strings.TrimSpace(*fc.Shell))
		cfg.Sources["shell"] = path
	}
	if fc.GenerateTimeoutSeconds != nil {
		if *fc.GenerateTimeoutSeconds < 1 || *fc.GenerateTimeoutSeconds > 1800 {
			return fmt.Errorf("generate_timeout_seconds must be between 1 and 1800")
		}
		cfg.GenerateTimeout = time.Duration(*fc.GenerateTimeoutSeconds) * time.Second
		cfg.Sources["generate_timeout"] = path
	}
	if fc.AgentTimeoutSeconds != nil {
		if *fc.AgentTimeoutSeconds < 1 || *fc.AgentTimeoutSeconds > 1800 {
			return fmt.Errorf("agent_timeout_seconds must be between 1 and 1800")
		}
		cfg.AgentTimeout = time.Duration(*fc.AgentTimeoutSeconds) * time.Second
		cfg.Sources["agent_timeout"] = path
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
	if value := strings.TrimSpace(os.Getenv("TMH_SHELL")); value != "" {
		cfg.Shell = command.Shell(value)
		cfg.Sources["shell"] = "TMH_SHELL"
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
	if value := strings.TrimSpace(overrides.Shell); value != "" {
		cfg.Shell = command.Shell(value)
		cfg.Sources["shell"] = "--shell"
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
	if cfg.GenerateTimeout <= 0 || cfg.GenerateTimeout > 30*time.Minute {
		return fmt.Errorf("generate_timeout_seconds must be between 1 and 1800")
	}
	if cfg.AgentTimeout <= 0 || cfg.AgentTimeout > 30*time.Minute {
		return fmt.Errorf("agent_timeout_seconds must be between 1 and 1800")
	}
	return nil
}
