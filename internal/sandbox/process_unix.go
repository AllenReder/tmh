//go:build darwin || linux

package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultOutputLimit     = 16 * 1024
	terminationGracePeriod = 150 * time.Millisecond
)

var ansiEscape = regexp.MustCompile(`\x1b(?:\[[0-?]*[ -/]*[@-~]|\][^\x07]*(?:\x07|\x1b\\))`)

type cappedWriter struct {
	mu           sync.Mutex
	buffer       bytes.Buffer
	limit        int
	captureLimit int
	written      int64
	truncated    bool
}

func newCappedWriter(limit int, secrets []string) *cappedWriter {
	maxSecretLength := 0
	for _, secret := range secrets {
		if len(secret) > maxSecretLength {
			maxSecretLength = len(secret)
		}
	}
	captureLimit := limit
	if maxSecretLength > 1 {
		captureLimit += maxSecretLength - 1
	}
	return &cappedWriter{limit: limit, captureLimit: captureLimit}
}

func (w *cappedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	remaining := w.captureLimit - w.buffer.Len()
	if remaining > 0 {
		_, _ = w.buffer.Write(p[:min(len(p), remaining)])
	}
	w.written += int64(len(p))
	if w.written > int64(w.limit) {
		w.truncated = true
	}
	// Claim the entire write so io.Copy keeps draining the pipe.
	return len(p), nil
}

func (w *cappedWriter) result() ([]byte, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]byte(nil), w.buffer.Bytes()...), w.truncated
}

func runProcess(ctx context.Context, program string, args []string, dir string, env []string, limits Command) Result {
	started := time.Now()
	if err := ctx.Err(); err != nil {
		return canceledProcessResult(started, err)
	}
	stdoutLimit := limits.StdoutLimit
	if stdoutLimit <= 0 {
		stdoutLimit = defaultOutputLimit
	}
	stderrLimit := limits.StderrLimit
	if stderrLimit <= 0 {
		stderrLimit = defaultOutputLimit
	}
	stdout := newCappedWriter(stdoutLimit, limits.Secrets)
	stderr := newCappedWriter(stderrLimit, limits.Secrets)

	devNull, err := os.Open("/dev/null")
	if err != nil {
		return Result{Status: StatusFailed, Err: fmt.Errorf("open /dev/null: %w", err)}
	}
	defer devNull.Close()

	command := exec.Command(program, args...)
	command.Dir = dir
	command.Env = append([]string(nil), env...)
	command.Stdin = devNull
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	// Bound Wait even if an unexpected descendant escapes the process group
	// while retaining one of the output pipes.
	command.WaitDelay = terminationGracePeriod
	if err := command.Start(); err != nil {
		return Result{Status: StatusFailed, Err: fmt.Errorf("start sandboxed process: %w", err)}
	}

	waited := make(chan error, 1)
	go func() { waited <- command.Wait() }()
	var waitErr error
	var contextErr error
	select {
	case waitErr = <-waited:
	case <-ctx.Done():
		contextErr = ctx.Err()
		waitErr = stopProcessGroup(command.Process.Pid, waited)
	}

	stdoutBytes, stdoutTruncated := stdout.result()
	stderrBytes, stderrTruncated := stderr.result()
	stdoutValue, stdoutSanitizedTruncated := finalizeOutput(stdoutBytes, limits.Secrets, stdoutLimit, stdoutTruncated)
	stderrValue, stderrSanitizedTruncated := finalizeOutput(stderrBytes, limits.Secrets, stderrLimit, stderrTruncated)
	result := Result{
		Status:          StatusCompleted,
		Stdout:          stdoutValue,
		Stderr:          stderrValue,
		DurationMS:      time.Since(started).Milliseconds(),
		StdoutTruncated: stdoutTruncated || stdoutSanitizedTruncated,
		StderrTruncated: stderrTruncated || stderrSanitizedTruncated,
	}
	if command.ProcessState != nil {
		exitCode := command.ProcessState.ExitCode()
		result.ExitCode = &exitCode
	}
	if contextErr != nil {
		result.Err = contextErr
		if errors.Is(contextErr, context.DeadlineExceeded) {
			result.Status = StatusTimeout
		} else {
			result.Status = StatusCanceled
		}
		return result
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if errors.As(waitErr, &exitErr) {
			if waitStatus, ok := exitErr.Sys().(syscall.WaitStatus); ok && waitStatus.Signaled() {
				result.Status = StatusFailed
				result.Err = fmt.Errorf("sandboxed process terminated by signal %s", waitStatus.Signal())
			}
		} else {
			result.Status = StatusFailed
			result.Err = fmt.Errorf("wait for sandboxed process: %w", waitErr)
		}
	}
	return result
}

func canceledProcessResult(started time.Time, err error) Result {
	status := StatusCanceled
	if errors.Is(err, context.DeadlineExceeded) {
		status = StatusTimeout
	}
	return Result{Status: status, DurationMS: time.Since(started).Milliseconds(), Err: err}
}

func stopProcessGroup(pid int, waited <-chan error) error {
	terminateProcessGroup(pid)
	timer := time.NewTimer(terminationGracePeriod)
	defer timer.Stop()

	waitChannel := waited
	var waitErr error
	for {
		select {
		case waitErr = <-waitChannel:
			waitChannel = nil
			if !processGroupExists(pid) {
				return waitErr
			}
		case <-timer.C:
			killProcessGroup(pid)
			if waitChannel != nil {
				waitErr = <-waitChannel
			}
			return waitErr
		}
	}
}

func terminateProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
}

func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

func processGroupExists(pid int) bool {
	err := syscall.Kill(-pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func sanitizeOutput(data []byte, secrets []string) string {
	value := cleanOutput(data)
	for _, secret := range normalizedSecrets(secrets) {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func normalizedSecrets(secrets []string) []string {
	normalizedSecrets := make([]string, 0, len(secrets))
	seen := make(map[string]struct{}, len(secrets))
	for _, secret := range secrets {
		normalized := cleanOutput([]byte(secret))
		if len(normalized) < 4 {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		normalizedSecrets = append(normalizedSecrets, normalized)
	}
	sort.Slice(normalizedSecrets, func(i, j int) bool {
		return len(normalizedSecrets[i]) > len(normalizedSecrets[j])
	})
	return normalizedSecrets
}

func cleanOutput(data []byte) string {
	if !utf8.Valid(data) {
		data = bytes.ToValidUTF8(data, []byte("�"))
	}
	value := ansiEscape.ReplaceAllString(string(data), "")
	var output strings.Builder
	output.Grow(len(value))
	for _, r := range value {
		switch r {
		case '\n', '\r', '\t':
			output.WriteRune(r)
		default:
			if !unicode.IsControl(r) && !unicode.In(r, unicode.Cf) {
				output.WriteRune(r)
			}
		}
	}
	return output.String()
}

func finalizeOutput(data []byte, secrets []string, limit int, sourceTruncated bool) (string, bool) {
	value := sanitizeOutput(data, secrets)
	if sourceTruncated {
		value = redactTrailingSecretPrefix(value, normalizedSecrets(secrets))
	}
	if len(value) <= limit {
		return value, false
	}
	end := limit
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end], true
}

func redactTrailingSecretPrefix(value string, secrets []string) string {
	bestLength := 0
	for _, secret := range secrets {
		maximum := min(len(secret)-1, len(value))
		for length := maximum; length >= 4; length-- {
			prefix := secret[:length]
			if utf8.ValidString(prefix) && strings.HasSuffix(value, prefix) {
				bestLength = max(bestLength, length)
				break
			}
		}
	}
	if bestLength == 0 {
		return value
	}
	return value[:len(value)-bestLength] + "[REDACTED]"
}

var _ io.Writer = (*cappedWriter)(nil)
