package command

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseShell(t *testing.T) {
	for input, want := range map[string]Shell{
		"auto":  Auto,
		" ZSH ": Zsh,
		"Bash":  Bash,
		"fish":  Fish,
	} {
		got, err := ParseShell(input)
		if err != nil || got != want {
			t.Fatalf("ParseShell(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := ParseShell("powershell"); err == nil {
		t.Fatal("expected unsupported shell error")
	}
}

func TestResolvePrecedence(t *testing.T) {
	lookPath := func(name string) (string, error) {
		return filepath.Join("/resolved", name), nil
	}
	tests := []struct {
		name      string
		selection Selection
		wantName  Shell
		requested Shell
		source    Source
	}{
		{
			name: "cli",
			selection: Selection{
				CLI: "fish", Environment: "bash", Config: "zsh", LoginShell: "/bin/zsh", LookPath: lookPath,
			},
			wantName: Fish, requested: Fish, source: SourceCLI,
		},
		{
			name: "environment",
			selection: Selection{
				Environment: "bash", Config: "zsh", LoginShell: "/bin/fish", LookPath: lookPath,
			},
			wantName: Bash, requested: Bash, source: SourceEnvironment,
		},
		{
			name: "config",
			selection: Selection{
				Config: "zsh", LoginShell: "/bin/fish", LookPath: lookPath,
			},
			wantName: Zsh, requested: Zsh, source: SourceConfig,
		},
		{
			name: "explicit auto does not fall through",
			selection: Selection{
				CLI: "auto", Environment: "fish", Config: "zsh", LoginShell: "/bin/bash", LookPath: lookPath,
			},
			wantName: Bash, requested: Auto, source: SourceCLI,
		},
		{
			name: "default auto",
			selection: Selection{
				LoginShell: "/usr/local/bin/fish", LookPath: lookPath,
			},
			wantName: Fish, requested: Auto, source: SourceDefault,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			target, err := Resolve(test.selection)
			if err != nil {
				t.Fatal(err)
			}
			if target.Name != test.wantName || target.Requested != test.requested || target.Source != test.source {
				t.Fatalf("unexpected target: %+v", target)
			}
			if target.Executable != filepath.Join("/resolved", string(test.wantName)) {
				t.Fatalf("unexpected executable: %s", target.Executable)
			}
			if !strings.Contains(strings.ToLower(target.Guidance()), string(test.wantName)) {
				t.Fatalf("guidance does not identify %s: %q", test.wantName, target.Guidance())
			}
		})
	}
}

func TestResolveRejectsUnknownOrMissingShell(t *testing.T) {
	lookPath := func(string) (string, error) {
		return "", errors.New("not found")
	}
	if _, err := Resolve(Selection{CLI: "pwsh", LoginShell: "/bin/zsh", LookPath: lookPath}); err == nil || !strings.Contains(err.Error(), "--shell") {
		t.Fatalf("expected CLI shell error, got %v", err)
	}
	if _, err := Resolve(Selection{Config: "auto", LoginShell: "/bin/sh", LookPath: lookPath}); err == nil || !strings.Contains(err.Error(), "$SHELL") {
		t.Fatalf("expected auto detection error, got %v", err)
	}
	if _, err := Resolve(Selection{CLI: "bash", LookPath: lookPath}); err == nil || !strings.Contains(err.Error(), "executable is required") {
		t.Fatalf("expected missing executable error, got %v", err)
	}
}

func TestResolveAutoUsesAbsoluteLoginShellWithoutPATHLookup(t *testing.T) {
	loginShell := "/bin/bash"
	if _, err := os.Stat(loginShell); err != nil {
		t.Skipf("%s is not installed: %v", loginShell, err)
	}
	t.Setenv("PATH", t.TempDir())
	target, err := Resolve(Selection{Config: "auto", LoginShell: loginShell})
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(loginShell)
	if err != nil {
		t.Fatal(err)
	}
	if target.Name != Bash || target.Executable != canonical {
		t.Fatalf("auto shell did not use absolute $SHELL: %+v", target)
	}
}

func TestResolveRejectsWorkspaceShellFromPATH(t *testing.T) {
	root := t.TempDir()
	oldDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDirectory) })
	marker := filepath.Join(root, "executed")
	fake := filepath.Join(root, "bash")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\ntouch \""+marker+"\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	_, err = Resolve(Selection{CLI: "bash"})
	if err == nil || !strings.Contains(err.Error(), "untrusted bash executable") {
		t.Fatalf("expected untrusted shell error, got %v", err)
	}
	if _, statErr := os.Stat(marker); !os.IsNotExist(statErr) {
		t.Fatalf("fake shell was executed during resolution: %v", statErr)
	}
}

func TestResolveRejectsSiblingShellFromPATH(t *testing.T) {
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "repo", "subdir")
	fakeDirectory := filepath.Join(root, "repo", "bin")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(fakeDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(fakeDirectory, "bash")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDirectory) })
	t.Setenv("PATH", fakeDirectory)
	if _, err := Resolve(Selection{CLI: "bash"}); err == nil || !strings.Contains(err.Error(), "untrusted bash executable") {
		t.Fatalf("expected sibling shell to be rejected, got %v", err)
	}
}

func TestResolveRejectsUserManagedSymlinkToUntrustedShell(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	workspace := t.TempDir()
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(workspace, "fish")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(bin, "fish")
	if err := os.Symlink(fake, linked); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	if _, err := Resolve(Selection{CLI: "fish"}); err == nil || !strings.Contains(err.Error(), "untrusted fish executable") {
		t.Fatalf("expected user-managed symlink target to be rejected, got %v", err)
	}
}

func TestTrustedSystemShellLocationWorksFromFilesystemRoot(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	canonical, err := filepath.EvalSymlinks(bashPath)
	if err != nil {
		t.Fatal(err)
	}
	if !trustedSystemShellLocation(Bash, bashPath, canonical) {
		t.Skipf("bash is outside the recognized system locations: %s", bashPath)
	}
	oldDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir("/"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDirectory) })
	target, err := Resolve(Selection{CLI: "bash"})
	if err != nil || target.Executable != canonical {
		t.Fatalf("system Bash was not resolved from /: %+v %v", target, err)
	}
}

func TestTrustedSystemShellLocationRejectsMisdirectedSymlink(t *testing.T) {
	if trustedSystemShellLocation(Bash, "/usr/local/bin/bash", "/usr/local/src/project/bash") {
		t.Fatal("system bin candidate trusted a canonical target outside executable or package roots")
	}
	if trustedSystemShellLocation(Bash, "/usr/local/bin/bash", "/usr/local/bin/python3") {
		t.Fatal("system bin candidate trusted a different executable identity")
	}
	if !trustedSystemShellLocation(Fish, "/opt/homebrew/bin/fish", "/opt/homebrew/Cellar/fish/4.0.0/bin/fish") {
		t.Fatal("Homebrew fish target was not trusted")
	}
}

func TestResolveAllowsUserManagedShellFromHomeDirectory(t *testing.T) {
	home := t.TempDir()
	bin := filepath.Join(home, ".local", "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	fish := filepath.Join(bin, "fish")
	if err := os.WriteFile(fish, []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	oldDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldDirectory) })
	t.Setenv("HOME", home)
	t.Setenv("PATH", bin)
	target, err := Resolve(Selection{CLI: "fish"})
	canonicalFish, canonicalErr := filepath.EvalSymlinks(fish)
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	if err != nil || target.Executable != canonicalFish {
		t.Fatalf("user-managed Fish was not resolved: %+v %v", target, err)
	}
}

func TestParseFishVersion(t *testing.T) {
	for _, test := range []struct {
		input      string
		major      int
		minor      int
		recognized bool
	}{
		{"fish, version 3.6.0", 3, 6, true},
		{"fish, version 4.1.2", 4, 1, true},
		{"fish 3.6", 0, 0, false},
	} {
		major, minor, ok := parseFishVersion(test.input)
		if major != test.major || minor != test.minor || ok != test.recognized {
			t.Fatalf("parseFishVersion(%q) = %d, %d, %v", test.input, major, minor, ok)
		}
	}
}
