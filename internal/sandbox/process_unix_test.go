//go:build darwin || linux

package sandbox

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunProcessDoesNotStartWithCanceledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	result := runProcess(ctx, "/tmh-test-program-that-does-not-exist", nil, t.TempDir(), nil, Command{})
	if result.Status != StatusCanceled || !errors.Is(result.Err, context.Canceled) {
		t.Fatalf("pre-canceled process result = %+v, want canceled", result)
	}
}

func TestRunProcessReportsSignalTermination(t *testing.T) {
	result := runProcess(t.Context(), "/bin/sh", []string{"-c", "kill -KILL $$"}, t.TempDir(), nil, Command{})
	if result.Status != StatusFailed {
		t.Fatalf("signal-terminated process status = %s, want failed: %+v", result.Status, result)
	}
	if result.Err == nil || !strings.Contains(strings.ToLower(result.Err.Error()), "signal") {
		t.Fatalf("signal-terminated process error = %v, want signal context", result.Err)
	}
}

func TestRunProcessKillsRemainingProcessGroupMembers(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// The outer shell exits promptly on SIGTERM. Its child deliberately ignores
	// SIGTERM and closes inherited output descriptors, so waiting only for the
	// group leader would leave the child running.
	script := `
trap 'exit 0' TERM
(trap '' TERM; exec sleep 30) </dev/null >/dev/null 2>&1 &
printf '%d\n' "$!"
wait
`
	result := runProcess(ctx, "/bin/sh", []string{"-c", script}, t.TempDir(), nil, Command{})
	if result.Status != StatusTimeout || !errors.Is(result.Err, context.DeadlineExceeded) {
		t.Fatalf("timed-out process result = %+v, want timeout", result)
	}
	pidText := strings.TrimSpace(result.Stdout)
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatalf("parse child pid from %q: %v", pidText, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	deadline := time.Now().Add(time.Second)
	for processPIDExists(pid) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if processPIDExists(pid) {
		t.Fatalf("child process %d survived cancellation", pid)
	}
}

func processPIDExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func TestRunProcessDrainsOutputAfterLimits(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	result := runProcess(ctx, "/bin/sh", []string{"-c", `
i=0
while [ "$i" -lt 20000 ]; do
  printf '0123456789abcdef'
  printf 'fedcba9876543210' >&2
  i=$((i + 1))
done
`}, t.TempDir(), nil, Command{StdoutLimit: 128, StderrLimit: 128})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 {
		t.Fatalf("large-output process failed: %+v", result)
	}
	if !result.StdoutTruncated || !result.StderrTruncated {
		t.Fatalf("large output was not marked truncated: %+v", result)
	}
	if len(result.Stdout) > 128 || len(result.Stderr) > 128 {
		t.Fatalf("output exceeded limits: stdout=%d stderr=%d", len(result.Stdout), len(result.Stderr))
	}
}
