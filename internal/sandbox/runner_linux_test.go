//go:build linux

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/elastic/go-seccomp-bpf/arch"
)

const (
	linuxMetadataProbeEnvironment = "TMH_INTERNAL_LINUX_METADATA_PROBE_V1"
	linuxMetadataProbeTarget      = "TMH_INTERNAL_LINUX_METADATA_TARGET_V1"
	linuxMetadataProbeSymlink     = "TMH_INTERNAL_LINUX_METADATA_SYMLINK_V1"
)

func TestLinuxSeccompPolicyCoversMetadataMutationFamilies(t *testing.T) {
	architecture, err := arch.GetInfo("")
	if err != nil {
		t.Fatal(err)
	}
	names, err := inspectionDeniedSyscalls(architecture)
	if err != nil {
		t.Fatal(err)
	}
	denied := make(map[string]struct{}, len(names))
	for _, name := range names {
		denied[name] = struct{}{}
	}
	for _, family := range inspectionDeniedSyscallFamilies {
		found := false
		for _, candidate := range family.candidates {
			if _, exists := denied[candidate]; exists {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("seccomp policy omitted syscall family %q: %v", family.name, names)
		}
	}
}

func TestLinuxSandboxBlocksMetadataMutations(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, "metadata-target")
	symlink := filepath.Join(root, "metadata-symlink")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, symlink); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	linkBefore, err := os.Lstat(symlink)
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
		linuxMetadataProbeEnvironment: "1",
		linuxMetadataProbeTarget:      target,
		linuxMetadataProbeSymlink:     symlink,
	})
	result := runner.Run(t.Context(), Command{
		Program:     executable,
		Args:        []string{"-test.run=^TestLinuxMetadataChildProbe$"},
		Dir:         root,
		Env:         environment,
		Roots:       []string{root},
		Secrets:     secrets,
		StdoutLimit: 1024,
		StderrLimit: 1024,
	})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "metadata-mutations-blocked") {
		t.Fatalf("metadata mutation probe failed: %+v", result)
	}

	after, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	linkAfter, err := os.Lstat(symlink)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode() != after.Mode() || before.ModTime() != after.ModTime() || statOwner(before) != statOwner(after) {
		t.Fatalf("target metadata changed: before=%+v after=%+v", before, after)
	}
	if statOwner(linkBefore) != statOwner(linkAfter) {
		t.Fatalf("symlink ownership changed: before=%+v after=%+v", linkBefore, linkAfter)
	}
	if _, err := syscall.Getxattr(target, "user.tmh_probe", nil); err == nil {
		t.Fatal("sandbox created an extended attribute")
	}
}

func TestLinuxMetadataChildProbe(t *testing.T) {
	if os.Getenv(linuxMetadataProbeEnvironment) != "1" {
		return
	}
	target := os.Getenv(linuxMetadataProbeTarget)
	symlink := os.Getenv(linuxMetadataProbeSymlink)
	file, err := os.Open(target)
	if err != nil {
		t.Fatalf("open metadata target: %v", err)
	}
	defer file.Close()
	operations := []struct {
		name string
		run  func() error
	}{
		{name: "chmod", run: func() error { return os.Chmod(target, 0o777) }},
		{name: "fchmod", run: func() error { return file.Chmod(0o777) }},
		{name: "chown", run: func() error { return os.Chown(target, os.Getuid(), os.Getgid()) }},
		{name: "fchown", run: func() error { return file.Chown(os.Getuid(), os.Getgid()) }},
		{name: "lchown", run: func() error { return os.Lchown(symlink, os.Getuid(), os.Getgid()) }},
		{name: "setxattr", run: func() error { return syscall.Setxattr(target, "user.tmh_probe", []byte("bad"), 0) }},
		{name: "removexattr", run: func() error { return syscall.Removexattr(target, "user.tmh_probe") }},
		{name: "utimensat", run: func() error { return os.Chtimes(target, time.Unix(1, 0), time.Unix(1, 0)) }},
	}
	for _, operation := range operations {
		if err := operation.run(); !sandboxPermissionDenied(err) {
			t.Fatalf("%s was not denied by seccomp: %v", operation.name, err)
		}
	}
	_, _ = os.Stdout.WriteString("metadata-mutations-blocked")
}

func statOwner(info os.FileInfo) [2]uint32 {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return [2]uint32{}
	}
	return [2]uint32{stat.Uid, stat.Gid}
}
