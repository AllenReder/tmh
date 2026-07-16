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

	"github.com/AllenReder/tmh/internal/model"
	"github.com/AllenReder/tmh/internal/tool"
)

func writeTestConfig(t *testing.T, baseURL string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("TMH_API_KEY", "")
	t.Setenv("TMH_SHELL", "")
	t.Setenv("SHELL", "/bin/bash")
	path := filepath.Join(dir, "tmh", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "base_url = \"" + baseURL + "\"\nmodel = \"test-model\"\nshell = \"bash\"\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func completion(command, explanation string) map[string]any {
	content, _ := json.Marshal(map[string]string{"command": command, "explanation": explanation})
	return map[string]any{"choices": []any{map[string]any{"message": map[string]any{
		"role": "assistant", "content": string(content),
	}}}}
}

func TestRunShorthandSeparatesCommandAndExplanation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(request.Messages[0].Content, "Bash 3.2") || len(request.Tools) != 0 {
			t.Fatalf("unexpected direct request: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(completion("find . -type f | head -n 10", "Finds ten files."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "find", "files"}, strings.NewReader(""), &stdout, &stderr)
	if exit != 0 || stdout.String() != "find . -type f | head -n 10\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if !strings.Contains(stderr.String(), "Explanation: Finds ten files.") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestExplicitGenerateDisambiguatesReservedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.Request
		_ = json.NewDecoder(r.Body).Decode(&request)
		if !strings.Contains(request.Messages[1].Content, "User request:\nagent restart policy") {
			t.Fatalf("unexpected user message: %q", request.Messages[1].Content)
		}
		_ = json.NewEncoder(w).Encode(completion("printf '%s\\n' agent", "Prints a word."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmh", "generate", "agent", "restart", "policy"}, strings.NewReader(""), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
}

func TestRunWarnsButEmitsRiskyCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(completion("rm -rf ./old-logs", "Deletes old logs."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "delete", "old", "logs"}, strings.NewReader(""), &stdout, &stderr)
	if exit != 0 || !strings.Contains(stdout.String(), "rm -rf") || !strings.Contains(stderr.String(), "Warning [destructive]") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestAgentUsesFileToolsAndDoesNotExposeExecutionByDefault(t *testing.T) {
	t.Setenv("TMH_EXEC", "inspection")
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(`{"scripts":{"start":"vite"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	withWorkingDirectory(t, root)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Tools) != 2 {
			t.Fatalf("expected two file tools, got %d: %+v", len(request.Tools), request.Tools)
		}
		for _, definition := range request.Tools {
			if definition.Function.Name == "run_command" {
				t.Fatal("run_command was exposed without --exec=inspection")
			}
		}
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "call-1", "type": "function", "function": map[string]any{"name": "read_text_file", "arguments": `{"path":"package.json"}`},
				}},
			}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(completion("npm run start", "Uses the start script."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "agent", "start", "this", "project"}, strings.NewReader(""), &stdout, &stderr)
	if exit != 0 || stdout.String() != "npm run start\n" || !strings.Contains(stderr.String(), "tool=read_text_file") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestInspectionFlagIsRejectedOutsideAgent(t *testing.T) {
	for _, args := range [][]string{
		{"tmh", "--exec=inspection", "inspect", "repo"},
		{"tmh", "generate", "--exec=inspection", "inspect", "repo"},
	} {
		var stdout, stderr bytes.Buffer
		exit := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "flag provided but not defined") {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, exit, stdout.String(), stderr.String())
		}
	}
}

type fakeInspectionHandler struct{}

func (fakeInspectionHandler) Definition() model.ToolDefinition {
	return model.ToolDefinition{Type: "function", Function: model.FunctionDefinition{
		Name: "run_command", Parameters: map[string]any{"type": "object"},
	}}
}

func (fakeInspectionHandler) Prepare(context.Context, model.ToolCall) (tool.Invocation, tool.Result) {
	return tool.InvocationFunc(func(context.Context) tool.Result {
		return tool.Result{Status: tool.StatusCompleted}
	}), tool.Result{}
}

func TestInspectionToolIsExposedOnlyWhenExplicitlyEnabled(t *testing.T) {
	previous := newInspectionHandler
	newInspectionHandler = func(context.Context, *tool.Scope) (tool.Handler, error) { return fakeInspectionHandler{}, nil }
	t.Cleanup(func() { newInspectionHandler = previous })

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request model.Request
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if len(request.Tools) != 3 {
			t.Fatalf("expected file tools plus run_command, got %+v", request.Tools)
		}
		found := false
		for _, definition := range request.Tools {
			found = found || definition.Function.Name == "run_command"
		}
		if !found || !strings.Contains(request.Messages[0].Content, "read-only, no-network") {
			t.Fatalf("inspection capability was not described: %+v", request)
		}
		_ = json.NewEncoder(w).Encode(completion("git status --short", "Shows repository status."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmh", "agent", "--exec=inspection", "inspect", "repo"}, strings.NewReader(""), &stdout, &stderr); exit != 0 {
		t.Fatalf("exit=%d stderr=%s", exit, stderr.String())
	}
}

func TestAgentSensitiveFileFailureDoesNotEmitCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	withWorkingDirectory(t, root)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "call-sensitive", "type": "function", "function": map[string]any{"name": "read_text_file", "arguments": `{"path":".env"}`},
				}},
			}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(completion("", "The required file is blocked by policy."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	exit := Run(context.Background(), []string{"tmh", "agent", "inspect", "the", "environment"}, strings.NewReader(""), &stdout, &stderr)
	if exit == 0 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "code=sensitive_path") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestConfigShowAndTest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	if strings.Contains(stdout.String(), "super-secret") || !strings.Contains(stdout.String(), "resolved_shell = bash") || !strings.Contains(stdout.String(), "agent_timeout = 1m30s") {
		t.Fatalf("unexpected config output: %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"tmh", "config", "test"}, strings.NewReader(""), &stdout, &stderr); exit != 0 || !strings.Contains(stdout.String(), "Connection successful") {
		t.Fatalf("test exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestShellResolutionErrorReportsEffectiveSource(t *testing.T) {
	writeTestConfig(t, "https://example.test/v1")
	t.Setenv("SHELL", "/bin/sh")

	for _, test := range []struct {
		name string
		args []string
		env  string
		want string
	}{
		{name: "environment", args: []string{"tmh", "config", "show"}, env: "auto", want: "selected by TMH_SHELL"},
		{name: "cli", args: []string{"tmh", "config", "show", "--shell", "auto"}, env: "fish", want: "selected by --shell"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("TMH_SHELL", test.env)
			var stdout, stderr bytes.Buffer
			exit := Run(context.Background(), test.args, strings.NewReader(""), &stdout, &stderr)
			if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), test.want) {
				t.Fatalf("exit=%d stdout=%q stderr=%q, want %q", exit, stdout.String(), stderr.String(), test.want)
			}
		})
	}
}

func TestExplicitZeroTimeoutIsRejected(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)

	for _, args := range [][]string{
		{"tmh", "--timeout=0", "show", "files"},
		{"tmh", "agent", "--timeout=0", "show", "files"},
		{"tmh", "config", "test", "--timeout=0"},
	} {
		var stdout, stderr bytes.Buffer
		exit := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "between 1s and 30m") {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, exit, stdout.String(), stderr.String())
		}
	}
	if calls.Load() != 0 {
		t.Fatalf("invalid timeout made %d model requests", calls.Load())
	}
}

func TestExplicitEmptyShellIsRejectedInsteadOfFallingThrough(t *testing.T) {
	writeTestConfig(t, "https://example.test/v1")
	for _, args := range [][]string{
		{"tmh", "--shell=", "show", "files"},
		{"tmh", "agent", "--shell=", "show", "files"},
		{"tmh", "config", "show", "--shell="},
	} {
		var stdout, stderr bytes.Buffer
		exit := Run(context.Background(), args, strings.NewReader(""), &stdout, &stderr)
		if exit != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "--shell requires") {
			t.Fatalf("args=%v exit=%d stdout=%q stderr=%q", args, exit, stdout.String(), stderr.String())
		}
	}
}

func TestShellInitHelpVersionAndLegacyAlias(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmh", "shell", "init", "zsh", "--no-bind"}, strings.NewReader(""), &stdout, &stderr); exit != 0 {
		t.Fatalf("shell init exit=%d stderr=%s", exit, stderr.String())
	}
	if !strings.Contains(stdout.String(), "_tmh_bind_mode='none'") || !strings.Contains(stdout.String(), "print -z") {
		t.Fatalf("unexpected shell init: %q", stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"tmh", "help"}, strings.NewReader(""), &stdout, &stderr); exit != 0 || !strings.Contains(stdout.String(), "tmh agent") {
		t.Fatalf("help exit=%d stdout=%q", exit, stdout.String())
	}
	stdout.Reset()
	if exit := Run(context.Background(), []string{"tmh", "version"}, strings.NewReader(""), &stdout, &stderr); exit != 0 || strings.TrimSpace(stdout.String()) != Version {
		t.Fatalf("version exit=%d stdout=%q", exit, stdout.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"tmha", "help"}, strings.NewReader(""), &stdout, &stderr); exit != 2 || !strings.Contains(stderr.String(), "tmha was removed") {
		t.Fatalf("legacy exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestRunReadsPipedStdinAndRejectsLegacyAgentFlag(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(completion("pwd", "Prints the current directory."))
	}))
	defer server.Close()
	writeTestConfig(t, server.URL)
	var stdout, stderr bytes.Buffer
	if exit := Run(context.Background(), []string{"tmh"}, strings.NewReader("show current directory"), &stdout, &stderr); exit != 0 || stdout.String() != "pwd\n" {
		t.Fatalf("exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	if exit := Run(context.Background(), []string{"tmh", "--agent", "inspect"}, strings.NewReader(""), &stdout, &stderr); exit != 2 {
		t.Fatalf("legacy --agent exit=%d stderr=%q", exit, stderr.String())
	}
}

func TestRunFailureNeverWritesCommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func withWorkingDirectory(t *testing.T, directory string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(directory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}
