package inspection

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/AllenReder/tmh/internal/model"
	"github.com/AllenReder/tmh/internal/sandbox"
	"github.com/AllenReder/tmh/internal/tool"
)

type fakeRunner struct {
	canaryErr error
	command   sandbox.Command
	runs      int
}

func (r *fakeRunner) Canary(context.Context, []string) error { return r.canaryErr }

func (r *fakeRunner) Run(_ context.Context, command sandbox.Command) sandbox.Result {
	r.command = command
	r.runs++
	exit := 0
	return sandbox.Result{Status: sandbox.StatusCompleted, ExitCode: &exit, Stdout: "ok"}
}

func TestRunCommandRegistersOnlyAfterCanary(t *testing.T) {
	scope, err := tool.NewScope(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{canaryErr: os.ErrPermission}
	if _, err := NewRunCommand(context.Background(), scope, runner); err == nil {
		t.Fatal("expected canary failure")
	}
}

func TestRunCommandAllowsGitStatusWithoutShell(t *testing.T) {
	root := t.TempDir()
	t.Setenv("PATH", root)
	if err := os.WriteFile(filepath.Join(root, "git"), []byte("#!/bin/sh\nexit 99\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	handler, err := NewRunCommand(context.Background(), scope, runner)
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, handler)
	result := registry.Execute(context.Background(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "run_command",
			Arguments: `{"program":"git","args":["status","--short"],"cwd":"."}`,
		},
	})
	if result.Status != tool.StatusCompleted || runner.runs != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if !filepath.IsAbs(runner.command.Program) || strings.HasPrefix(runner.command.Program, root) || !slices.Contains(runner.command.Args, "--no-optional-locks") {
		t.Fatalf("unsafe execution plan: %+v", runner.command)
	}
}

func TestGitAndRGPoliciesFailClosed(t *testing.T) {
	root := t.TempDir()
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"reset", "--hard"},
		{"diff", "--ext-diff"},
		{"diff", "-User-controlled"},
		{"diff", "--unified=not-a-number"},
		{"show", "HEAD:.env"},
		{"show", "HEAD:.envrc"},
		{"show", "--format=%G?"},
		{"log", "--pretty=local-config-alias"},
		{"log", "-no-walk"},
		{"log", "--max-count=invalid"},
		{"status", "--pathspec-from-file=-"},
	} {
		if _, _, _, err := planGit(args, root, scope); err == nil {
			t.Fatalf("expected git args to be denied: %#v", args)
		}
	}

	for _, args := range [][]string{
		{"--pre", "cat", "pattern", "."},
		{"-L", "pattern", "."},
		{"--hidden", "pattern", "."},
		{"pattern", "../"},
		{"pattern", ".envrc"},
		{"secret", "."},
	} {
		if _, _, err := planRG(t.Context(), args, root, scope); err == nil {
			t.Fatalf("expected rg args to be denied: %#v", args)
		}
	}
}

func TestGitAllowsValidatedDisplayFormats(t *testing.T) {
	root := t.TempDir()
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"log", "--format=%h %s", "--max-count=10"},
		{"log", "--pretty=oneline", "-n5"},
		{"show", "--pretty=format:%h %s"},
		{"diff", "-U3", "--diff-filter=AM"},
		{"status", "--porcelain=v2", "--untracked-files=all"},
	} {
		if _, _, _, err := planGit(args, root, scope); err != nil {
			t.Fatalf("expected safe git args to pass: %#v: %v", args, err)
		}
	}
}

func TestRGInjectionTokensRemainOrdinaryArguments(t *testing.T) {
	root := t.TempDir()
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	pattern := `$(touch /tmp/tmh-pwn); > output`
	args, _, err := planRG(t.Context(), []string{pattern, "."}, root, scope)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(args, pattern) {
		t.Fatalf("pattern was reinterpreted: %#v", args)
	}
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "--iglob=!**/.env") || !strings.Contains(joined, "--no-config") {
		t.Fatalf("missing enforced rg policy: %#v", args)
	}
}

func TestRGRejectsSymlinkPathEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := planRG(t.Context(), []string{"pattern", "escape"}, root, scope); err == nil {
		t.Fatal("expected symlink escape denial")
	}
}

func TestPlatformSandboxRunsApprovedGitInspection(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("/usr/bin/git", "init", "--quiet", root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Skipf("git init unavailable: %v: %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRunCommand(t.Context(), scope, sandbox.New())
	if err != nil {
		t.Fatalf("inspection sandbox unavailable: %v", err)
	}
	result := tool.NewRegistry(nil, handler).Execute(t.Context(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "run_command",
			Arguments: `{"program":"git","args":["status","--short"],"cwd":"."}`,
		},
	})
	if result.Status != tool.StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "untracked.txt") {
		t.Fatalf("unexpected inspected git result: %+v", result)
	}
}

func TestPlatformSandboxRunsApprovedRGInspection(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("needle\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "API-TOKEN.txt"), []byte("needle secret-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte("needle direnv-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRunCommand(t.Context(), scope, sandbox.New())
	if err != nil {
		t.Fatalf("inspection sandbox unavailable: %v", err)
	}
	registry := tool.NewRegistry(nil, handler)
	for name, rawArguments := range map[string]string{
		"default":        `{"program":"rg","args":["needle","."],"cwd":"."}`,
		"wide glob":      `{"program":"rg","args":["--glob=*","needle","."],"cwd":"."}`,
		"wide glob pair": `{"program":"rg","args":["-g","**","needle","."],"cwd":"."}`,
	} {
		t.Run(name, func(t *testing.T) {
			result := registry.Execute(t.Context(), model.ToolCall{
				Type: "function",
				Function: model.FunctionCall{
					Name:      "run_command",
					Arguments: rawArguments,
				},
			})
			if result.Status != tool.StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "visible.txt") {
				t.Fatalf("unexpected inspected rg result: %+v", result)
			}
			if strings.Contains(strings.ToLower(result.Stdout), "api-token") || strings.Contains(result.Stdout, "secret-value") || strings.Contains(result.Stdout, "direnv-value") {
				t.Fatalf("rg exposed a sensitive file: %q", result.Stdout)
			}
		})
	}
}

func TestGitDiffFiltersSensitiveTrackedPaths(t *testing.T) {
	root := t.TempDir()
	for name, content := range map[string]string{".ENV": "TOKEN=old\n", ".envrc": "export API_KEY=old\n", ".docker/config.json": "old-auth\n", "visible.txt": "old\n"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "--quiet", root}, {"-C", root, "add", "-f", "--", ".ENV", ".envrc", ".docker/config.json", "visible.txt"}} {
		command := exec.Command("/usr/bin/git", args...)
		if output, err := command.CombinedOutput(); err != nil {
			t.Skipf("git setup unavailable: %v: %s", err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, ".ENV"), []byte("TOKEN=TOP_SECRET_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".envrc"), []byte("export API_KEY=DIRENV_SECRET_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".docker", "config.json"), []byte("DOCKER_AUTH_PRIVATE_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("visible-change\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRunCommand(t.Context(), scope, sandbox.New())
	if err != nil {
		t.Fatal(err)
	}
	result := tool.NewRegistry(nil, handler).Execute(t.Context(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "run_command",
			Arguments: `{"program":"git","args":["diff"],"cwd":"."}`,
		},
	})
	if result.Status != tool.StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "visible-change") {
		t.Fatalf("unexpected git diff result: %+v", result)
	}
	if strings.Contains(result.Stdout, "TOP_SECRET_VALUE") || strings.Contains(result.Stdout, "DIRENV_SECRET_VALUE") || strings.Contains(result.Stdout, "DOCKER_AUTH_PRIVATE_VALUE") || strings.Contains(strings.ToLower(result.Stdout), ".env") {
		t.Fatalf("git diff exposed sensitive path: %q", result.Stdout)
	}
}

func TestLocalGitConfigHelpersAreDeniedBeforeExecution(t *testing.T) {
	root := t.TempDir()
	helper := filepath.Join(root, "malicious-helper")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\nprintf 'EXTERNAL_HELPER_INVOKED\\n' >&2\nexit 9\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("visible.txt diff=malicious filter=malicious\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	commands := [][]string{
		{"init", "--quiet", root},
		{"-C", root, "add", "visible.txt", ".gitattributes"},
		{"-C", root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "initial"},
		{"-C", root, "config", "core.fsmonitor", helper},
		{"-C", root, "config", "diff.external", helper},
		{"-C", root, "config", "diff.malicious.command", helper},
		{"-C", root, "config", "diff.malicious.textconv", helper},
		{"-C", root, "config", "filter.malicious.clean", helper},
		{"-C", root, "config", "core.pager", helper},
		{"-C", root, "config", "gpg.program", helper},
	}
	for _, args := range commands {
		if output, err := exec.Command("/usr/bin/git", args...).CombinedOutput(); err != nil {
			t.Skipf("git setup unavailable: %v: %s", err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{}
	handler, err := NewRunCommand(t.Context(), scope, runner)
	if err != nil {
		t.Fatal(err)
	}
	registry := tool.NewRegistry(nil, handler)
	for _, args := range [][]string{{"status", "--short"}, {"diff"}, {"log", "--format=%h", "-n1"}} {
		encoded, err := json.Marshal(arguments{Program: "git", Args: args, CWD: "."})
		if err != nil {
			t.Fatal(err)
		}
		result := registry.Execute(t.Context(), model.ToolCall{
			Type: "function",
			Function: model.FunctionCall{
				Name:      "run_command",
				Arguments: string(encoded),
			},
		})
		if result.Status != tool.StatusDenied || result.Code != tool.CodePolicyDenied {
			t.Fatalf("dangerous local config was not denied for %#v: %+v", args, result)
		}
	}
	if runner.runs != 0 {
		t.Fatalf("dangerous repository configuration reached the sandbox: runs=%d", runner.runs)
	}
}

func TestRunCommandRejectsArgumentResourceAbuse(t *testing.T) {
	scope, err := tool.NewScope(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRunCommand(context.Background(), scope, &fakeRunner{})
	if err != nil {
		t.Fatal(err)
	}
	tooMany := make([]string, maxArgumentCount+1)
	for index := range tooMany {
		tooMany[index] = "x"
	}
	encoded, err := json.Marshal(arguments{Program: "git", Args: tooMany, CWD: "."})
	if err != nil {
		t.Fatal(err)
	}
	result := tool.NewRegistry(nil, handler).Execute(context.Background(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "run_command",
			Arguments: string(encoded),
		},
	})
	if result.Status != tool.StatusDenied || result.Code != tool.CodeInvalidArguments {
		t.Fatalf("unexpected oversized argument result: %+v", result)
	}
}
