package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func writeTestConfig(t *testing.T, baseURL string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMH_API_KEY", "")
	path := filepath.Join(dir, "tmh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "base_url = \"" + baseURL + "\"\nmodel = \"test-model\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunDirectSeparatesCommandAndExplanation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "content": `{"command":"find . -type f | head -n 10","explanation":"Finds ten files."}`,
			}}},
		})
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "find", "files"}, strings.NewReader(""), &stdout, &stderr)
	if exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if stdout.String() != "find . -type f | head -n 10\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Explanation: Finds ten files.") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunWarnsButEmitsRiskyCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "content": `{"command":"rm -rf ./old-logs","explanation":"Deletes old logs."}`,
			}}},
		})
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "delete", "old", "logs"}, strings.NewReader(""), &stdout, &stderr)
	if exit != 0 || !strings.Contains(stdout.String(), "rm -rf") || !strings.Contains(stderr.String(), "Warning [destructive]") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunAgentAliasUsesToolCalling(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"start":"vite"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []any `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Tools) != 2 {
			t.Fatalf("expected two tools, got %d", len(request.Tools))
		}
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "call-1", "type": "function", "function": map[string]any{"name": "read_text_file", "arguments": `{"path":"package.json"}`},
				}},
			}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": `{"command":"npm run start","explanation":"Uses the start script."}`,
		}}}})
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmha", "start", "this", "project"}, strings.NewReader(""), &stdout, &stderr)
	if exit != 0 || stdout.String() != "npm run start\n" || !strings.Contains(stderr.String(), "Inspect: read_text_file") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunFailureNeverWritesCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "hello"}, strings.NewReader(""), &stdout, &stderr)
	if exit == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "401") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestRunReadsPipedStdin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": `{"command":"pwd","explanation":"Prints the current directory."}`,
		}}}})
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmh"}, strings.NewReader("show current directory"), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
	if stdout.String() != "pwd\n" {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestConfigShowRedactsKeyAndConfigTestConnects(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": "OK",
		}}}})
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	t.Setenv("TMH_API_KEY", "super-secret")

	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmh", "config", "show"}, strings.NewReader(""), &stdout, &stderr); exit != 0 {
		t.Fatalf("show exit=%d stderr=%s", exit, stderr.String())
	}
	if strings.Contains(stdout.String(), "super-secret") || !strings.Contains(stdout.String(), "configured via TMH_API_KEY") {
		t.Fatalf("config show leaked or omitted key state: %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"tmh", "config", "test"}, strings.NewReader(""), &stdout, &stderr); exit != 0 {
		t.Fatalf("test exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Connection successful") {
		t.Fatalf("unexpected config test output: %q", stdout.String())
	}
}

func TestHelpVersionAndAgentConfigDoNotCallModel(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("TMH_API_KEY", "")
	t.Setenv("OPENAI_API_KEY", "")

	for _, args := range [][]string{
		{"tmh", "help"},
		{"tmha", "help"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr); exit != 0 {
			t.Fatalf("args=%v exit=%d stderr=%s", args, exit, stderr.String())
		}
		if !strings.Contains(stdout.String(), "Usage:") {
			t.Fatalf("args=%v missing usage: %q", args, stdout.String())
		}
	}

	for _, args := range [][]string{
		{"tmh", "version"},
		{"tmha", "version"},
	} {
		var stdout, stderr bytes.Buffer
		if exit := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr); exit != 0 {
			t.Fatalf("args=%v exit=%d stderr=%s", args, exit, stderr.String())
		}
		if strings.TrimSpace(stdout.String()) != Version {
			t.Fatalf("args=%v unexpected version: %q", args, stdout.String())
		}
	}

	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmha", "config", "show"}, strings.NewReader(""), &stdout, &stderr); exit != 0 {
		t.Fatalf("config exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "config_file =") {
		t.Fatalf("unexpected config output: %q", stdout.String())
	}
}

func TestAgentSensitiveFileFailureDoesNotEmitCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWD) })
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "call-sensitive", "type": "function", "function": map[string]any{"name": "read_text_file", "arguments": `{"path":".env"}`},
				}},
			}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": `{"command":"","explanation":"The required file is blocked by policy."}`,
		}}}})
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmha", "inspect", "the", "environment"}, strings.NewReader(""), &stdout, &stderr)
	if exit == 0 || stdout.Len() != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Inspect: read_text_file") || !strings.Contains(stderr.String(), "blocked by policy") {
		t.Fatalf("missing audit/error output: %q", stderr.String())
	}
}
