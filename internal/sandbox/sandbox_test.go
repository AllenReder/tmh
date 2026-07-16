package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCleanEnvironmentDropsCredentialsAndProxies(t *testing.T) {
	t.Setenv("TMH_TEST_API_KEY", "super-secret-value")
	t.Setenv("HTTPS_PROXY", "http://proxy.invalid")
	t.Setenv("ALL_PROXY", "socks5://proxy.invalid")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")
	t.Setenv("TMH_CLIENT_COOKIE", "client-cookie-value")
	t.Setenv("DATABASE_URL", "postgres://user:password@db.invalid/name")
	environment, secrets := CleanEnvironment(map[string]string{
		"EXTRA":                  "value",
		"PATH":                   "/tmp/untrusted",
		"LD_PRELOAD":             "/tmp/injected.dylib",
		"TMH_ADDITION_API_KEY":   "addition-secret",
		"RIPGREP_CONFIG_PATH":    "/dev/null",
		"TMH_INTERNAL_TEST_MODE": "1",
	})
	joined := strings.Join(environment, "\n")
	for _, forbidden := range []string{"TMH_TEST_API_KEY", "HTTPS_PROXY", "ALL_PROXY", "AWS_ACCESS_KEY_ID", "TMH_CLIENT_COOKIE", "DATABASE_URL", "/tmp/untrusted", "LD_PRELOAD", "addition-secret"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("sensitive environment leaked %q: %s", forbidden, joined)
		}
	}
	if !strings.Contains(joined, "PATH="+trustedPath) || !strings.Contains(joined, "RIPGREP_CONFIG_PATH=/dev/null") || !strings.Contains(joined, "TMH_INTERNAL_TEST_MODE=1") {
		t.Fatalf("sensitive environment leaked: %s", joined)
	}
	secretText := strings.Join(secrets, "\n")
	for _, secret := range []string{
		"super-secret-value", "http://proxy.invalid", "socks5://proxy.invalid",
		"AKIAIOSFODNN7EXAMPLE", "client-cookie-value", "postgres://user:password@db.invalid/name",
	} {
		if !strings.Contains(secretText, secret) {
			t.Fatalf("known test secret %q missing from redaction set", secret)
		}
	}
	if !strings.Contains(joined, "EXTRA=value") {
		t.Fatalf("expected safe addition missing from sanitized environment: %s", joined)
	}
}

func TestSanitizeOutput(t *testing.T) {
	output := sanitizeOutput([]byte("\x1b[31mred\x1b[0m\x00 token-value\xff\u0085\u202e"), []string{"token", "token-value"})
	if strings.Contains(output, "\x1b") || strings.Contains(output, "token-value") || strings.ContainsRune(output, 0) || strings.ContainsRune(output, '\u0085') || strings.ContainsRune(output, '\u202e') {
		t.Fatalf("unsafe output: %q", output)
	}
	if !strings.Contains(output, "red") || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("unexpected output: %q", output)
	}
}

func TestOutputRedactionCrossesCaptureLimit(t *testing.T) {
	const limit = 16
	secrets := []string{"secret-value"}
	writer := newCappedWriter(limit, secrets)
	input := []byte("123456789012secret-value-tail")
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	data, truncated := writer.result()
	output, sanitizedTruncated := finalizeOutput(data, secrets, limit, truncated)
	if !truncated || !sanitizedTruncated {
		t.Fatalf("cross-limit output was not marked truncated: raw=%v sanitized=%v", truncated, sanitizedTruncated)
	}
	if strings.Contains(output, "secr") || len(output) > limit {
		t.Fatalf("secret prefix leaked across output boundary: %q", output)
	}
}

func TestOutputRedactsObfuscatedSecretPrefixAtCaptureLimit(t *testing.T) {
	const limit = 16
	secrets := []string{"secret-value"}
	writer := newCappedWriter(limit, secrets)
	input := append([]byte("123456789012secr"), make([]byte, 20)...)
	input = append(input, []byte("et-value")...)
	if _, err := writer.Write(input); err != nil {
		t.Fatal(err)
	}
	data, truncated := writer.result()
	output, _ := finalizeOutput(data, secrets, limit, truncated)
	if strings.Contains(output, "secr") {
		t.Fatalf("obfuscated secret prefix leaked at output boundary: %q", output)
	}
}

func TestSanitizeOutputNormalizesSecretsBeforeRedaction(t *testing.T) {
	output := sanitizeOutput([]byte("before tok\x00en-value after"), []string{"tok\x00en-value"})
	if strings.Contains(output, "token-value") || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("normalized secret was not redacted: %q", output)
	}
}

func TestPlatformSandboxCanary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/readable", []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := New()
	if err := runner.Canary(t.Context(), []string{root}); err != nil {
		t.Fatalf("platform sandbox canary: %v", err)
	}
}

func TestFailedCanaryRevokesPreviousReadiness(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runner := New()
	if err := runner.Canary(t.Context(), []string{root}); err != nil {
		t.Fatalf("initial canary: %v", err)
	}
	if err := runner.Canary(t.Context(), nil); err == nil {
		t.Fatal("empty-root canary unexpectedly succeeded")
	}

	environment, secrets := CleanEnvironment(nil)
	result := runner.Run(t.Context(), Command{
		Program: "/bin/true",
		Dir:     root,
		Env:     environment,
		Roots:   []string{root},
		Secrets: secrets,
	})
	if result.Status != StatusFailed || result.Err == nil || !strings.Contains(result.Err.Error(), "has not passed") {
		t.Fatalf("runner retained readiness after failed canary: %+v", result)
	}
}

func TestPlatformSandboxRunsReadsAndBlocksWrites(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	readable := filepath.Join(root, "readable")
	blocked := filepath.Join(root, "blocked")
	truncateTarget := filepath.Join(root, "truncate-target")
	renameSource := filepath.Join(root, "rename-source")
	renameTarget := filepath.Join(root, "rename-target")
	hardLink := filepath.Join(root, "hard-link")
	symlink := filepath.Join(root, "symbolic-link")
	metadataTarget := filepath.Join(root, "metadata-target")
	if err := os.WriteFile(readable, []byte("read-ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(truncateTarget, []byte("keep-truncate-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(renameSource, []byte("keep-rename-source"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(metadataTarget, []byte("keep-metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	initialMetadataTime := time.Unix(123456, 0)
	if err := os.Chtimes(metadataTarget, initialMetadataTime, initialMetadataTime); err != nil {
		t.Fatal(err)
	}
	metadataBefore, err := os.Stat(metadataTarget)
	if err != nil {
		t.Fatal(err)
	}
	runner := New()
	if err := runner.Canary(t.Context(), []string{root}); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	environment, secrets := CleanEnvironment(map[string]string{
		"TMH_INTERNAL_SANDBOX_PROBE":          "1",
		"TMH_INTERNAL_SANDBOX_PROBE_READ":     readable,
		"TMH_INTERNAL_SANDBOX_PROBE_CREATE":   blocked,
		"TMH_INTERNAL_SANDBOX_PROBE_TRUNCATE": truncateTarget,
		"TMH_INTERNAL_SANDBOX_PROBE_RENAME":   renameSource,
		"TMH_INTERNAL_SANDBOX_PROBE_RENAMED":  renameTarget,
		"TMH_INTERNAL_SANDBOX_PROBE_HARDLINK": hardLink,
		"TMH_INTERNAL_SANDBOX_PROBE_SYMLINK":  symlink,
		"TMH_INTERNAL_SANDBOX_PROBE_METADATA": metadataTarget,
	})
	result := runner.Run(t.Context(), Command{
		Program:     executable,
		Args:        []string{"-test.run=^TestSandboxChildProbe$"},
		Dir:         root,
		Env:         environment,
		Roots:       []string{root},
		Secrets:     secrets,
		StdoutLimit: 1024,
		StderrLimit: 1024,
	})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "read-ok") || !strings.Contains(result.Stdout, "mutations-blocked") {
		t.Fatalf("sandboxed read failed: %+v", result)
	}
	if _, err := os.Stat(blocked); !os.IsNotExist(err) {
		t.Fatalf("sandbox created blocked file: %v", err)
	}
	if content, err := os.ReadFile(truncateTarget); err != nil || string(content) != "keep-truncate-content" {
		t.Fatalf("sandbox truncated existing file: content=%q err=%v", content, err)
	}
	if content, err := os.ReadFile(renameSource); err != nil || string(content) != "keep-rename-source" {
		t.Fatalf("sandbox moved rename source: content=%q err=%v", content, err)
	}
	for _, path := range []string{renameTarget, hardLink, symlink} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("sandbox created mutation target %q: %v", path, err)
		}
	}
	metadataAfter, err := os.Stat(metadataTarget)
	if err != nil {
		t.Fatal(err)
	}
	if metadataAfter.Mode() != metadataBefore.Mode() || !metadataAfter.ModTime().Equal(metadataBefore.ModTime()) {
		t.Fatalf("sandbox changed file metadata: before=%+v after=%+v", metadataBefore, metadataAfter)
	}
}

func TestSandboxChildProbe(t *testing.T) {
	if os.Getenv("TMH_INTERNAL_SANDBOX_PROBE") != "1" {
		return
	}
	readable := os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_READ")
	data, err := os.ReadFile(readable)
	if err != nil {
		t.Fatalf("read allowed file: %v", err)
	}
	if _, err := os.OpenFile("/dev/null", os.O_WRONLY, 0); err != nil {
		t.Fatalf("write /dev/null: %v", err)
	}
	if err := os.WriteFile(os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_CREATE"), []byte("bad"), 0o600); err == nil {
		t.Fatal("created a file")
	}
	if err := os.Truncate(os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_TRUNCATE"), 0); err == nil {
		t.Fatal("truncated a file")
	}
	if err := os.Rename(os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_RENAME"), os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_RENAMED")); err == nil {
		t.Fatal("renamed a file")
	}
	if err := os.Link(os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_RENAME"), os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_HARDLINK")); err == nil {
		t.Fatal("created a hard link")
	}
	if err := os.Symlink(os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_RENAME"), os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_SYMLINK")); err == nil {
		t.Fatal("created a symbolic link")
	}
	metadataTarget := os.Getenv("TMH_INTERNAL_SANDBOX_PROBE_METADATA")
	if err := os.Chmod(metadataTarget, 0o777); err == nil {
		t.Fatal("changed file mode")
	}
	if err := os.Chtimes(metadataTarget, time.Unix(1, 0), time.Unix(1, 0)); err == nil {
		t.Fatal("changed file timestamps")
	}
	if err := exec.Command("/bin/true").Run(); err == nil {
		t.Fatal("executed an unapproved child program")
	}
	_, _ = fmt.Printf("%smutations-blocked", data)
}

func TestPlatformSandboxBlocksReadsOutsideRoots(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outsideConfig := filepath.Join(outside, ".gitconfig")
	if err := os.WriteFile(outsideConfig, []byte("outside-config-marker"), 0o600); err != nil {
		t.Fatal(err)
	}
	runner := New()
	if err := runner.Canary(t.Context(), []string{root}); err != nil {
		t.Fatal(err)
	}
	environment, secrets := CleanEnvironment(nil)
	result := runner.Run(t.Context(), Command{
		Program:     "/bin/cat",
		Args:        []string{outsideConfig},
		Dir:         root,
		Env:         environment,
		Roots:       []string{root},
		Secrets:     secrets,
		StdoutLimit: 1024,
		StderrLimit: 1024,
	})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode == 0 {
		t.Fatalf("outside-root read was not denied normally: %+v", result)
	}
	if strings.Contains(result.Stdout, "outside-config-marker") {
		t.Fatalf("outside-root config leaked through sandbox: %+v", result)
	}
}
