//go:build darwin

package sandbox

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

const (
	darwinCanaryEnvironment = "TMH_INTERNAL_SEATBELT_CANARY_V1"
	darwinCanaryArgument    = "--tmh-internal-seatbelt-canary-v1"
)

type darwinRunner struct {
	mu       sync.RWMutex
	readyKey string
}

func New() Runner { return &darwinRunner{} }

func (r *darwinRunner) Canary(ctx context.Context, roots []string) error {
	r.mu.Lock()
	r.readyKey = ""
	defer r.mu.Unlock()
	if len(roots) == 0 {
		return fmt.Errorf("macOS sandbox canary requires at least one read root")
	}
	canonicalRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		canonical, err := filepath.EvalSymlinks(filepath.Clean(root))
		if err != nil {
			return fmt.Errorf("canonicalize sandbox root %q: %w", root, err)
		}
		canonicalRoots = append(canonicalRoots, canonical)
	}
	roots = canonicalRoots
	sandboxExec := "/usr/bin/sandbox-exec"
	if info, err := os.Stat(sandboxExec); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("macOS sandbox-exec is unavailable")
	}
	executable, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve sandbox canary executable: %w", err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return fmt.Errorf("canonicalize sandbox canary executable: %w", err)
	}
	profile, err := seatbeltProfile(roots, executable)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(roots)
	if err != nil {
		return fmt.Errorf("encode sandbox canary: %w", err)
	}
	environment, _ := CleanEnvironment(map[string]string{
		darwinCanaryEnvironment: base64.RawStdEncoding.EncodeToString(payload),
	})
	dir := roots[0]
	for _, root := range roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			dir = root
			break
		}
	}
	result := runProcess(ctx, sandboxExec, []string{"-p", profile, executable, darwinCanaryArgument}, dir, environment, Command{})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 {
		exitCode := -1
		if result.ExitCode != nil {
			exitCode = *result.ExitCode
		}
		return fmt.Errorf("macOS sandbox canary failed (status=%s exit=%d): %s", result.Status, exitCode, resultFailureDetail(result))
	}
	r.readyKey = rootsKey(roots)
	return nil
}

func (r *darwinRunner) Run(ctx context.Context, command Command) Result {
	if err := validateCommand(command); err != nil {
		return Result{Status: StatusFailed, Err: err}
	}
	r.mu.RLock()
	ready := r.readyKey == rootsKey(command.Roots)
	r.mu.RUnlock()
	if !ready {
		return Result{Status: StatusFailed, Err: fmt.Errorf("macOS sandbox canary has not passed for these roots")}
	}
	profile, err := seatbeltProfile(command.Roots, command.Program)
	if err != nil {
		return Result{Status: StatusFailed, Err: err}
	}
	return runProcess(ctx, "/usr/bin/sandbox-exec", append([]string{"-p", profile, command.Program}, command.Args...), command.Dir, command.Env, command)
}

func seatbeltProfile(roots []string, executable string) (string, error) {
	paths := append([]string(nil), roots...)
	paths = append(paths,
		"/System",
		"/Library/Apple/System/Library",
		"/usr",
		"/bin",
		"/sbin",
		"/private/etc",
		"/dev/null",
		executable,
	)
	if executable == "/usr/bin/git" {
		paths = append(paths,
			"/Library/Developer",
			"/Applications/Xcode.app/Contents",
			"/private/var/select",
		)
	}
	if pathWithin(executable, "/Library/Developer") {
		paths = append(paths, "/Library/Developer")
	}
	if pathWithin(executable, "/Applications/Xcode.app/Contents") {
		paths = append(paths, "/Applications/Xcode.app/Contents")
	}
	if pathWithin(executable, "/opt/homebrew") {
		paths = append(paths, "/opt/homebrew")
	}
	var profile strings.Builder
	profile.WriteString("(version 1)\n")
	profile.WriteString("(deny default)\n")
	// Apple's system profile contains the process bootstrap, dyld, libc and
	// inherited-fd allowances required by ordinary command-line binaries. It
	// does not grant arbitrary user-file access. The explicit network deny and
	// canary below prevent its narrow system IPC allowances from weakening the
	// inspection boundary.
	profile.WriteString("(import \"system.sb\")\n")
	profile.WriteString("(deny network*)\n")
	profile.WriteString("(deny file-write* (require-not (literal \"/dev/null\")))\n")
	profile.WriteString("(allow process*)\n")
	profile.WriteString("(deny process-exec (require-not (literal ")
	profile.WriteString(strconv.Quote(filepath.Clean(executable)))
	profile.WriteString(")))\n")
	profile.WriteString("(allow signal (target self))\n")
	profile.WriteString("(allow sysctl-read)\n")
	for _, path := range paths {
		if strings.IndexFunc(path, func(r rune) bool { return r == 0 || r == '\n' || r == '\r' }) >= 0 {
			return "", fmt.Errorf("sandbox path contains a control character")
		}
		quoted := strconv.Quote(filepath.Clean(path))
		profile.WriteString("(allow file-read* (literal ")
		profile.WriteString(quoted)
		profile.WriteString("))\n")
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			profile.WriteString("(allow file-read* (subpath ")
			profile.WriteString(quoted)
			profile.WriteString("))\n")
		}
	}
	profile.WriteString("(allow file-write* (literal \"/dev/null\"))\n")
	return profile.String(), nil
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func init() {
	encoded := os.Getenv(darwinCanaryEnvironment)
	if encoded == "" || len(os.Args) != 2 || os.Args[1] != darwinCanaryArgument {
		return
	}
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "invalid sandbox canary payload")
		os.Exit(125)
	}
	var roots []string
	if err := json.Unmarshal(payload, &roots); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "invalid sandbox canary roots")
		os.Exit(125)
	}
	if err := runCanaryChecks(roots); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(125)
	}
	os.Exit(0)
}
