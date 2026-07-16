// Package sandbox executes already-authorized inspection commands in a
// fail-closed, read-only, no-network platform sandbox.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
	StatusTimeout   Status = "timeout"
	StatusCanceled  Status = "canceled"
)

type Command struct {
	Program     string   `json:"program"`
	Args        []string `json:"args"`
	Dir         string   `json:"dir"`
	Env         []string `json:"env"`
	Roots       []string `json:"roots"`
	Secrets     []string `json:"-"`
	StdoutLimit int      `json:"-"`
	StderrLimit int      `json:"-"`
}

type Result struct {
	Status          Status
	ExitCode        *int
	Stdout          string
	Stderr          string
	DurationMS      int64
	StdoutTruncated bool
	StderrTruncated bool
	Err             error
}

type Runner interface {
	Canary(context.Context, []string) error
	Run(context.Context, Command) Result
}

const trustedPath = "/usr/bin:/bin:/usr/local/bin:/opt/homebrew/bin:/home/linuxbrew/.linuxbrew/bin"

// CleanEnvironment returns a minimal deterministic child environment and the
// parent values that must be redacted if a child happens to print them.
func CleanEnvironment(additions map[string]string) ([]string, []string) {
	values := map[string]string{
		"PATH": trustedPath,
		"TERM": "dumb",
	}
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE"} {
		if value := safeLocale(os.Getenv(key)); value != "" {
			values[key] = value
		}
	}
	if _, ok := values["LANG"]; !ok {
		values["LANG"] = "C"
	}
	for key, value := range additions {
		if unsafeEnvironmentAddition(key, value) {
			continue
		}
		values[key] = value
	}

	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}

	seen := make(map[string]struct{})
	secrets := make([]string, 0)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if !ok || len(value) < 4 || !secretEnvironmentKey(key) {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		secrets = append(secrets, value)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return environment, secrets
}

func safeLocale(value string) string {
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("_.@-", r) {
			continue
		}
		return ""
	}
	return value
}

func secretEnvironmentKey(key string) bool {
	upper := strings.ToUpper(key)
	for _, marker := range []string{
		"API_KEY", "ACCESS_KEY", "ACCOUNT_KEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD",
		"CREDENTIAL", "AUTH", "PRIVATE_KEY", "COOKIE", "SESSION", "CONNECTION_STRING",
		"DATABASE_URL", "REDIS_URL", "WEBHOOK", "SIGNATURE", "PROXY",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func unsafeEnvironmentAddition(key, value string) bool {
	if !validEnvironmentKey(key) || strings.IndexByte(value, 0) >= 0 {
		return true
	}
	upper := strings.ToUpper(key)
	if secretEnvironmentKey(upper) {
		return true
	}
	switch upper {
	case "PATH", "TERM", "LANG", "LC_ALL", "LC_CTYPE", "HOME", "USER", "LOGNAME", "TMPDIR",
		"SHELL", "PWD", "OLDPWD", "ENV", "BASH_ENV", "ZDOTDIR", "NODE_OPTIONS", "PYTHONPATH",
		"PYTHONHOME", "RUBYOPT", "PERL5LIB", "GPG_AGENT_INFO":
		return true
	}
	for _, prefix := range []string{"LC_", "LD_", "DYLD_", "NPM_CONFIG_", "AWS_", "AZURE_", "GOOGLE_", "CLOUDSDK_"} {
		if strings.HasPrefix(upper, prefix) {
			return true
		}
	}
	return false
}

func validEnvironmentKey(key string) bool {
	for index, r := range key {
		if r == '_' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || index > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return key != ""
}

func resultFailureDetail(result Result) string {
	parts := make([]string, 0, 2)
	if stderr := strings.TrimSpace(result.Stderr); stderr != "" {
		parts = append(parts, stderr)
	}
	if result.Err != nil {
		parts = append(parts, result.Err.Error())
	}
	if len(parts) == 0 {
		return "no diagnostic output"
	}
	return strings.Join(parts, "; ")
}

func rootsKey(roots []string) string {
	copyOfRoots := append([]string(nil), roots...)
	sort.Strings(copyOfRoots)
	return strings.Join(copyOfRoots, "\x00")
}

func validateCommand(command Command) error {
	if !filepath.IsAbs(command.Program) || !filepath.IsAbs(command.Dir) {
		return fmt.Errorf("sandbox command paths must be absolute")
	}
	if len(command.Roots) == 0 {
		return fmt.Errorf("sandbox command requires at least one read root")
	}
	if command.StdoutLimit < 0 || command.StderrLimit < 0 {
		return fmt.Errorf("sandbox output limits cannot be negative")
	}
	return nil
}
