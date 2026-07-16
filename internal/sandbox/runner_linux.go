//go:build linux

package sandbox

import (
	"context"
	"debug/elf"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	seccomp "github.com/elastic/go-seccomp-bpf"
	"github.com/elastic/go-seccomp-bpf/arch"
	"github.com/landlock-lsm/go-landlock/landlock"
	ll "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

const (
	linuxHelperEnvironment = "TMH_INTERNAL_LINUX_SANDBOX_HELPER_V1"
	linuxHelperArgument    = "--tmh-internal-linux-sandbox-helper-v1"
	linuxHelperErrorPrefix = "tmh sandbox helper: "
)

type linuxHelperSpec struct {
	Kind    string  `json:"kind"`
	Command Command `json:"command"`
}

type linuxRunner struct {
	mu       sync.RWMutex
	readyKey string
}

func New() Runner { return &linuxRunner{} }

func (r *linuxRunner) Canary(ctx context.Context, roots []string) error {
	r.mu.Lock()
	r.readyKey = ""
	defer r.mu.Unlock()
	dir, err := existingDirectory(roots)
	if err != nil {
		return err
	}
	result := runLinuxHelper(ctx, linuxHelperSpec{Kind: "canary", Command: Command{Dir: dir, Roots: roots}}, Command{})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 {
		exitCode := -1
		if result.ExitCode != nil {
			exitCode = *result.ExitCode
		}
		return fmt.Errorf("Linux sandbox canary failed (status=%s exit=%d): %s", result.Status, exitCode, resultFailureDetail(result))
	}
	r.readyKey = rootsKey(roots)
	return nil
}

func (r *linuxRunner) Run(ctx context.Context, command Command) Result {
	if err := validateCommand(command); err != nil {
		return Result{Status: StatusFailed, Err: err}
	}
	r.mu.RLock()
	ready := r.readyKey == rootsKey(command.Roots)
	r.mu.RUnlock()
	if !ready {
		return Result{Status: StatusFailed, Err: fmt.Errorf("Linux sandbox canary has not passed for these roots")}
	}
	result := runLinuxHelper(ctx, linuxHelperSpec{Kind: "command", Command: command}, command)
	if result.ExitCode != nil && *result.ExitCode == 125 && strings.HasPrefix(strings.TrimSpace(result.Stderr), linuxHelperErrorPrefix) {
		result.Status = StatusFailed
		result.Err = fmt.Errorf("sandbox helper failed")
	}
	return result
}

func runLinuxHelper(ctx context.Context, spec linuxHelperSpec, limits Command) Result {
	executable, err := os.Executable()
	if err != nil {
		return Result{Status: StatusFailed, Err: fmt.Errorf("resolve sandbox helper executable: %w", err)}
	}
	payload, err := json.Marshal(spec)
	if err != nil {
		return Result{Status: StatusFailed, Err: fmt.Errorf("encode sandbox helper request: %w", err)}
	}
	environment, _ := CleanEnvironment(map[string]string{
		linuxHelperEnvironment: base64.RawStdEncoding.EncodeToString(payload),
	})
	return runProcess(ctx, executable, []string{linuxHelperArgument}, spec.Command.Dir, environment, limits)
}

func init() {
	encoded := os.Getenv(linuxHelperEnvironment)
	if encoded == "" || len(os.Args) != 2 || os.Args[1] != linuxHelperArgument {
		return
	}
	fail := func(err error) {
		_, _ = fmt.Fprintln(os.Stderr, linuxHelperErrorPrefix+err.Error())
		os.Exit(125)
	}
	payload, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil {
		fail(fmt.Errorf("invalid payload"))
	}
	var spec linuxHelperSpec
	if err := json.Unmarshal(payload, &spec); err != nil {
		fail(fmt.Errorf("invalid request"))
	}
	if spec.Kind != "canary" && spec.Kind != "command" {
		fail(fmt.Errorf("invalid helper kind"))
	}
	if spec.Kind == "command" {
		if err := validateCommand(spec.Command); err != nil {
			fail(err)
		}
	}
	if err := applyLinuxRestrictions(spec.Command.Roots, spec.Command.Program); err != nil {
		fail(err)
	}
	if spec.Kind == "canary" {
		if err := runCanaryChecks(spec.Command.Roots); err != nil {
			fail(err)
		}
		os.Exit(0)
	}
	if err := syscall.Exec(spec.Command.Program, append([]string{spec.Command.Program}, spec.Command.Args...), spec.Command.Env); err != nil {
		fail(fmt.Errorf("exec inspected program: %w", err))
	}
	panic("unreachable")
}

func applyLinuxRestrictions(roots []string, program string) error {
	if len(roots) == 0 {
		return fmt.Errorf("Landlock requires at least one read root")
	}
	paths := append([]string(nil), roots...)
	paths = append(paths, "/usr", "/bin", "/lib", "/lib64", "/etc")
	if strings.HasPrefix(program, "/home/linuxbrew/.linuxbrew/") {
		paths = append(paths, "/home/linuxbrew/.linuxbrew")
	}
	rules := make([]landlock.Rule, 0, len(paths)+5)
	readAccess := landlock.AccessFSSet(ll.AccessFSReadFile | ll.AccessFSReadDir)
	executeAccess := landlock.AccessFSSet(ll.AccessFSExecute | ll.AccessFSReadFile)
	seen := make(map[string]struct{})
	for _, path := range paths {
		canonical, err := filepath.EvalSymlinks(filepath.Clean(path))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("resolve Landlock path %q: %w", path, err)
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		info, err := os.Stat(canonical)
		if err != nil {
			return fmt.Errorf("inspect Landlock path %q: %w", canonical, err)
		}
		if info.IsDir() {
			rules = append(rules, landlock.PathAccess(readAccess, canonical))
		} else if info.Mode().IsRegular() {
			rules = append(rules, landlock.PathAccess(landlock.AccessFSSet(ll.AccessFSReadFile), canonical))
		}
	}
	for _, path := range []string{"/dev/null", "/dev/urandom", "/dev/random"} {
		if _, err := os.Stat(path); err == nil {
			rules = append(rules, landlock.PathAccess(landlock.AccessFSSet(ll.AccessFSReadFile), path))
		}
	}
	if _, err := os.Stat("/dev/null"); err == nil {
		rules = append(rules, landlock.RWFiles("/dev/null"))
	}
	if program != "" {
		executables, err := linuxExecutableChain(program)
		if err != nil {
			return err
		}
		for _, executable := range executables {
			rules = append(rules, landlock.PathAccess(executeAccess, executable))
		}
	}
	// V3 (Linux 6.2 / backports) is mandatory because it mediates truncate.
	// BestEffort is intentionally not used: missing support disables execution.
	if err := landlock.V3.RestrictPaths(rules...); err != nil {
		return fmt.Errorf("install Landlock ABI v3 policy: %w", err)
	}
	if err := installInspectionSeccomp(); err != nil {
		return fmt.Errorf("install read-only/no-network seccomp policy: %w", err)
	}
	return nil
}

func linuxExecutableChain(program string) ([]string, error) {
	canonical, err := filepath.EvalSymlinks(filepath.Clean(program))
	if err != nil {
		return nil, fmt.Errorf("resolve approved executable: %w", err)
	}
	executables := []string{canonical}
	file, err := elf.Open(canonical)
	if err != nil {
		return nil, fmt.Errorf("approved executable is not an ELF binary: %w", err)
	}
	defer file.Close()
	for _, segment := range file.Progs {
		if segment.Type != elf.PT_INTERP {
			continue
		}
		data, err := io.ReadAll(io.LimitReader(segment.Open(), 4096))
		if err != nil {
			return nil, fmt.Errorf("read ELF interpreter: %w", err)
		}
		interpreter := strings.TrimSpace(strings.TrimRight(string(data), "\x00"))
		if interpreter == "" || !filepath.IsAbs(interpreter) {
			return nil, fmt.Errorf("approved executable has an invalid ELF interpreter")
		}
		interpreter, err = filepath.EvalSymlinks(interpreter)
		if err != nil {
			return nil, fmt.Errorf("resolve ELF interpreter: %w", err)
		}
		executables = append(executables, interpreter)
		break
	}
	return executables, nil
}

type syscallFamily struct {
	name       string
	candidates []string
}

var inspectionDeniedSyscallFamilies = []syscallFamily{
	{
		name: "network",
		candidates: []string{
			"socket", "socketpair", "connect", "bind", "listen", "accept", "accept4",
			"sendto", "sendmsg", "sendmmsg", "recvfrom", "recvmsg", "recvmmsg", "shutdown",
			"getsockname", "getpeername", "setsockopt", "getsockopt", "socketcall",
			// io_uring can create and operate on sockets without invoking the
			// traditional socket syscalls, so disable it entirely in inspection.
			"io_uring_setup",
		},
	},
	{
		name:       "file mode mutation",
		candidates: []string{"chmod", "fchmod", "fchmodat", "fchmodat2"},
	},
	{
		name:       "file ownership mutation",
		candidates: []string{"chown", "fchown", "lchown", "fchownat"},
	},
	{
		name:       "extended attribute mutation",
		candidates: []string{"setxattr", "lsetxattr", "fsetxattr"},
	},
	{
		name:       "extended attribute removal",
		candidates: []string{"removexattr", "lremovexattr", "fremovexattr"},
	},
	{
		name:       "file timestamp mutation",
		candidates: []string{"utime", "utimes", "futimesat", "utimensat", "utimensat_time64"},
	},
}

func installInspectionSeccomp() error {
	if !seccomp.Supported() {
		return fmt.Errorf("seccomp-BPF is unavailable")
	}
	architecture, err := arch.GetInfo("")
	if err != nil {
		return err
	}
	names, err := inspectionDeniedSyscalls(architecture)
	if err != nil {
		return err
	}
	return seccomp.LoadFilter(seccomp.Filter{
		NoNewPrivs: true,
		Flag:       seccomp.FilterFlagTSync,
		Policy: seccomp.Policy{
			DefaultAction: seccomp.ActionAllow,
			Syscalls: []seccomp.SyscallGroup{{
				Names:  names,
				Action: seccomp.ActionErrno,
			}},
		},
	})
}

func inspectionDeniedSyscalls(architecture *arch.Info) ([]string, error) {
	if architecture == nil {
		return nil, fmt.Errorf("seccomp architecture information is required")
	}
	seen := make(map[string]struct{})
	names := make([]string, 0)
	for _, family := range inspectionDeniedSyscallFamilies {
		familyCount := 0
		for _, name := range family.candidates {
			if _, exists := architecture.SyscallNames[name]; !exists {
				continue
			}
			familyCount++
			if _, exists := seen[name]; exists {
				continue
			}
			seen[name] = struct{}{}
			names = append(names, name)
		}
		if familyCount == 0 {
			return nil, fmt.Errorf("architecture %s exposes no recognized %s syscalls", architecture.Name, family.name)
		}
	}
	return names, nil
}

func existingDirectory(roots []string) (string, error) {
	for _, root := range roots {
		if info, err := os.Stat(root); err == nil && info.IsDir() {
			return root, nil
		}
	}
	return "", fmt.Errorf("sandbox requires an existing directory root")
}
