package inspection

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/AllenReder/tmh/internal/model"
	"github.com/AllenReder/tmh/internal/sandbox"
	"github.com/AllenReder/tmh/internal/tool"
)

// nativeRunner deliberately crosses the Runner seam without an OS sandbox so
// these tests isolate policy planning and Git's object semantics. It is only
// used with repositories created under t.TempDir and approved read commands.
type nativeRunner struct {
	commands []sandbox.Command
}

func (*nativeRunner) Canary(context.Context, []string) error { return nil }

func (r *nativeRunner) Run(ctx context.Context, command sandbox.Command) sandbox.Result {
	r.commands = append(r.commands, command)
	started := time.Now()
	cmd := exec.CommandContext(ctx, command.Program, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = append([]string(nil), command.Env...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := sandbox.Result{
		Status:     sandbox.StatusCompleted,
		Stdout:     stdout.String(),
		Stderr:     stderr.String(),
		DurationMS: time.Since(started).Milliseconds(),
	}
	if cmd.ProcessState != nil {
		exitCode := cmd.ProcessState.ExitCode()
		result.ExitCode = &exitCode
	}
	if ctx.Err() != nil {
		result.Err = ctx.Err()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			result.Status = sandbox.StatusTimeout
		} else {
			result.Status = sandbox.StatusCanceled
		}
		return result
	}
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			result.Status = sandbox.StatusFailed
			result.Err = err
		}
	}
	return result
}

func TestGitObjectInputsMustResolveToCommits(t *testing.T) {
	root := newGitHistory(t)
	secretBlob := gitOutput(t, root, "rev-parse", "HEAD~1:.env")
	oldBlob := gitOutput(t, root, "rev-parse", "HEAD~1:visible.txt")
	newBlob := gitOutput(t, root, "rev-parse", "HEAD:visible.txt")

	for name, args := range map[string][]string{
		"show blob":  {"show", secretBlob},
		"diff blobs": {"diff", oldBlob, newBlob},
	} {
		t.Run(name, func(t *testing.T) {
			runner := &nativeRunner{}
			result := runInspection(t, root, runner, "git", args)
			if result.Status != tool.StatusDenied || result.Code != tool.CodePolicyDenied {
				t.Fatalf("non-commit object was not denied: %+v", result)
			}
			if len(runner.commands) != 1 {
				t.Fatalf("main Git command ran after failed preflight: %#v", runner.commands)
			}
			if strings.Contains(result.Stdout+result.Stderr, "TOP_SECRET_VALUE") {
				t.Fatalf("sensitive blob content escaped through Git output: %+v", result)
			}
			if got := strings.Join(runner.commands[0].Args, " "); !strings.Contains(got, "^{commit}") {
				t.Fatalf("preflight did not require a commit object: %s", got)
			}
		})
	}
}

func TestGitCommitInspectionKeepsSensitivePathsFiltered(t *testing.T) {
	root := newGitHistory(t)

	t.Run("show", func(t *testing.T) {
		runner := &nativeRunner{}
		result := runInspection(t, root, runner, "git", []string{"show", "HEAD~1"})
		if result.Status != tool.StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("safe commit show failed: %+v", result)
		}
		if len(runner.commands) != 2 {
			t.Fatalf("expected one preflight and one main command, got %d", len(runner.commands))
		}
		if !strings.Contains(result.Stdout, "visible-old") {
			t.Fatalf("visible commit content was missing: %q", result.Stdout)
		}
		if strings.Contains(result.Stdout, "TOP_SECRET_VALUE") || strings.Contains(strings.ToLower(result.Stdout), ".env") {
			t.Fatalf("git show exposed a sensitive path: %q", result.Stdout)
		}
	})

	t.Run("diff", func(t *testing.T) {
		runner := &nativeRunner{}
		result := runInspection(t, root, runner, "git", []string{"diff", "HEAD~1", "HEAD"})
		if result.Status != tool.StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 {
			t.Fatalf("safe commit diff failed: %+v", result)
		}
		if len(runner.commands) != 3 {
			t.Fatalf("expected two preflights and one main command, got %d", len(runner.commands))
		}
		if !strings.Contains(result.Stdout, "visible-new") {
			t.Fatalf("visible diff content was missing: %q", result.Stdout)
		}
		if strings.Contains(result.Stdout, "TOP_SECRET_VALUE") || strings.Contains(strings.ToLower(result.Stdout), ".env") {
			t.Fatalf("git diff exposed a sensitive path: %q", result.Stdout)
		}
	})
}

func TestGitDiffRequiresFilePathsAfterSeparator(t *testing.T) {
	root := newGitHistory(t)
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("working-tree\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	withoutSeparator := &nativeRunner{}
	result := runInspection(t, root, withoutSeparator, "git", []string{"diff", "visible.txt"})
	if result.Status != tool.StatusDenied || result.Code != tool.CodePolicyDenied || len(withoutSeparator.commands) != 1 {
		t.Fatalf("ambiguous file positional was not denied as a non-commit: result=%+v commands=%#v", result, withoutSeparator.commands)
	}

	withSeparator := &nativeRunner{}
	result = runInspection(t, root, withSeparator, "git", []string{"diff", "--", "visible.txt"})
	if result.Status != tool.StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "working-tree") {
		t.Fatalf("file path after -- was not inspected: %+v", result)
	}
	if len(withSeparator.commands) != 1 {
		t.Fatalf("path-only diff should not run revision preflights: %#v", withSeparator.commands)
	}
}

func TestRGDoesNotFollowSensitiveSymlinkAlias(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("UNIQUE_NEEDLE_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".env", filepath.Join(root, "visible-alias.txt")); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := trustedExecutable("rg", scope); err != nil {
		t.Skipf("trusted rg executable unavailable: %v", err)
	}
	runner := &nativeRunner{}
	result := runInspection(t, root, runner, "rg", []string{"UNIQUE_NEEDLE_VALUE", "."})
	if result.Status != tool.StatusCompleted || result.ExitCode == nil {
		t.Fatalf("safe rg inspection failed: %+v", result)
	}
	if strings.Contains(result.Stdout+result.Stderr, "UNIQUE_NEEDLE_VALUE") || strings.Contains(result.Stdout+result.Stderr, "visible-alias.txt") {
		t.Fatalf("rg followed a symlink to sensitive data: %+v", result)
	}
}

func TestRGRejectsBareRepositorySearchRoot(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "config"), []byte("[remote \"origin\"]\n\turl = https://credential@example.invalid/private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := planRG(t.Context(), []string{"url", "."}, root, scope); err == nil {
		t.Fatal("expected rg to reject a bare Git metadata root")
	}
}

func TestRGRejectsArbitrarilyNamedNestedBareRepository(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "ordinary-cache")
	if err := os.MkdirAll(filepath.Join(nested, "objects"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "config"), []byte("[remote \"origin\"]\n\turl = https://credential@example.invalid/private\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := planRG(t.Context(), []string{"url", "."}, root, scope); err == nil {
		t.Fatal("expected rg to reject an arbitrarily named nested bare repository")
	}
}

func TestRGSafetyPreflightFailsClosedAtEntryBudget(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"one.txt", "two.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("visible"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := rejectNestedBareRepositoriesWithin(t.Context(), root, scope, 1); err == nil {
		t.Fatal("rg safety preflight did not fail closed at its entry budget")
	}
}

func TestGitFilterConfigKeysAreIndependentlyDenied(t *testing.T) {
	for _, key := range []string{"filter.malicious.clean", "filter.malicious.process"} {
		t.Run(key, func(t *testing.T) {
			root := t.TempDir()
			gitRun(t, root, "init", "--quiet")
			gitRun(t, root, "config", key, filepath.Join(root, "untrusted-helper"))
			scope, err := tool.NewScope(root, nil)
			if err != nil {
				t.Fatal(err)
			}
			runner := &fakeRunner{}
			handler, err := NewRunCommand(t.Context(), scope, runner)
			if err != nil {
				t.Fatal(err)
			}
			result := tool.NewRegistry(nil, handler).Execute(t.Context(), model.ToolCall{
				Type: "function",
				Function: model.FunctionCall{
					Name:      "run_command",
					Arguments: `{"program":"git","args":["status","--short"],"cwd":"."}`,
				},
			})
			if result.Status != tool.StatusDenied || result.Code != tool.CodePolicyDenied {
				t.Fatalf("dangerous %s config was not denied: %+v", key, result)
			}
			if runner.runs != 0 {
				t.Fatalf("dangerous %s config reached the runner: runs=%d", key, runner.runs)
			}
		})
	}
}

func runInspection(t *testing.T, root string, runner sandbox.Runner, program string, args []string) tool.Result {
	t.Helper()
	scope, err := tool.NewScope(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewRunCommand(t.Context(), scope, runner)
	if err != nil {
		t.Fatal(err)
	}
	callArguments, err := jsonArguments(arguments{Program: program, Args: args, CWD: "."})
	if err != nil {
		t.Fatal(err)
	}
	return tool.NewRegistry(nil, handler).Execute(t.Context(), model.ToolCall{
		Type: "function",
		Function: model.FunctionCall{
			Name:      "run_command",
			Arguments: callArguments,
		},
	})
}

func jsonArguments(value arguments) (string, error) {
	data, err := json.Marshal(value)
	return string(data), err
}

func newGitHistory(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitRun(t, root, "init", "--quiet")
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOP_SECRET_VALUE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("visible-old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "-f", "--", ".env", "visible.txt")
	gitRun(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "initial")
	if err := os.WriteFile(filepath.Join(root, "visible.txt"), []byte("visible-new\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun(t, root, "add", "--", "visible.txt")
	gitRun(t, root, "-c", "user.name=Test", "-c", "user.email=test@example.invalid", "commit", "--quiet", "-m", "visible update")
	return root
}

func gitRun(t *testing.T, root string, args ...string) {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	command := exec.Command("/usr/bin/git", commandArgs...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
}

func gitOutput(t *testing.T, root string, args ...string) string {
	t.Helper()
	commandArgs := append([]string{"-C", root}, args...)
	output, err := exec.Command("/usr/bin/git", commandArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v failed: %v: %s", args, err, output)
	}
	return strings.TrimSpace(string(output))
}
