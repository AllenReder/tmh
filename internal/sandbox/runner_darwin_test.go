//go:build darwin

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSeatbeltRunsDirectAppleGit(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitExecutable := "/Library/Developer/CommandLineTools/usr/bin/git"
	developerDirectory := "/Library/Developer/CommandLineTools"
	if info, err := os.Stat(gitExecutable); err != nil || !info.Mode().IsRegular() {
		gitExecutable = "/Applications/Xcode.app/Contents/Developer/usr/bin/git"
		developerDirectory = "/Applications/Xcode.app/Contents/Developer"
	}
	if info, err := os.Stat(gitExecutable); err != nil || !info.Mode().IsRegular() {
		t.Skip("Apple developer tools are not installed")
	}
	runner := New()
	if err := runner.Canary(t.Context(), []string{root}); err != nil {
		t.Fatal(err)
	}
	environment, secrets := CleanEnvironment(map[string]string{"DEVELOPER_DIR": developerDirectory})
	result := runner.Run(t.Context(), Command{
		Program:     gitExecutable,
		Args:        []string{"--version"},
		Dir:         root,
		Env:         environment,
		Roots:       []string{root},
		Secrets:     secrets,
		StdoutLimit: 1024,
		StderrLimit: 1024,
	})
	if result.Status != StatusCompleted || result.ExitCode == nil || *result.ExitCode != 0 || !strings.Contains(result.Stdout, "git version") {
		t.Fatalf("Apple git failed in Seatbelt: %+v", result)
	}
}

func TestSeatbeltProfileScopesOptionalRuntimePrefixes(t *testing.T) {
	regular, err := seatbeltProfile([]string{t.TempDir()}, "/usr/bin/rg")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/opt/homebrew", "/Library/Developer", "/Applications/Xcode.app/Contents", "/private/var/select"} {
		if strings.Contains(regular, path) {
			t.Fatalf("regular profile unexpectedly grants optional runtime path %q", path)
		}
	}

	homebrew, err := seatbeltProfile([]string{t.TempDir()}, "/opt/homebrew/Cellar/ripgrep/15.1.0/bin/rg")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(homebrew, "/opt/homebrew") {
		t.Fatal("Homebrew profile does not grant its runtime prefix")
	}

	appleGit, err := seatbeltProfile([]string{t.TempDir()}, "/usr/bin/git")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/Library/Developer", "/Applications/Xcode.app/Contents", "/private/var/select"} {
		if !strings.Contains(appleGit, path) {
			t.Fatalf("Apple git profile is missing runtime path %q", path)
		}
	}
}
