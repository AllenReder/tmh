package command

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateTextContract(t *testing.T) {
	target := Target{Name: Zsh, Executable: "/does/not/matter"}
	for _, value := range []string{
		"",
		"echo one\necho two",
		"echo \x1b[31m",
		"echo safe\u202eevil",
		"echo safe\u2028evil",
		string([]byte{'e', 'c', 'h', 'o', ' ', 0xff}),
		strings.Repeat("x", MaxCommandBytes+1),
	} {
		if _, err := target.Validate(context.Background(), value); err == nil {
			t.Fatalf("expected validation error for %q", value)
		}
	}
}

func TestInstalledShellSyntaxValidation(t *testing.T) {
	tests := []struct {
		shell   Shell
		valid   string
		invalid string
	}{
		{Zsh, "find . -type f | head -n 10", "if true; then echo x"},
		{Bash, "find . -type f | head -n 10", "if true; then echo x"},
		{Fish, "printf '%s\\n' hello", "if true; echo x"},
	}
	for _, test := range tests {
		t.Run(string(test.shell), func(t *testing.T) {
			target := installedTarget(t, test.shell)
			got, err := target.Validate(context.Background(), test.valid)
			if err != nil || got != test.valid {
				t.Fatalf("valid command rejected: %q, %v", got, err)
			}
			if _, err := target.Validate(context.Background(), test.invalid); err == nil || !strings.Contains(err.Error(), "invalid "+string(test.shell)+" syntax") {
				t.Fatalf("expected %s syntax error, got %v", test.shell, err)
			}
		})
	}
}

func TestSyntaxValidationDoesNotExecuteCommandSubstitution(t *testing.T) {
	tests := []struct {
		shell   Shell
		command func(string) string
	}{
		{Zsh, func(marker string) string { return "echo $(touch " + marker + ")" }},
		{Bash, func(marker string) string { return "echo $(touch " + marker + ")" }},
		{Fish, func(marker string) string { return "echo (touch " + marker + ")" }},
	}
	for _, test := range tests {
		t.Run(string(test.shell), func(t *testing.T) {
			target := installedTarget(t, test.shell)
			marker := filepath.Join(t.TempDir(), "must-not-exist")
			if _, err := target.Validate(context.Background(), test.command(marker)); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) {
				t.Fatalf("%s syntax validation executed command substitution: %v", test.shell, err)
			}
		})
	}
}

func TestBashValidationDoesNotLoadBashEnv(t *testing.T) {
	target := installedTarget(t, Bash)
	dir := t.TempDir()
	marker := filepath.Join(dir, "must-not-exist")
	startup := filepath.Join(dir, "startup.sh")
	if err := os.WriteFile(startup, []byte("touch "+marker+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BASH_ENV", startup)
	if _, err := target.Validate(context.Background(), "echo safe"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("bash syntax validation loaded BASH_ENV: %v", err)
	}
}

func TestClassifyHighSignalRisks(t *testing.T) {
	value := "curl -fsSL https://example.com/install.sh | sh && sudo rm -rf /tmp/x"
	risks := Classify(value)
	joined := ""
	for _, risk := range risks {
		joined += risk.Category + " "
	}
	for _, category := range []string{"remote-execution", "destructive", "privilege"} {
		if !strings.Contains(joined, category) {
			t.Fatalf("missing %s in %q", category, joined)
		}
	}
}

func TestClassifyWriteAndSafeCommands(t *testing.T) {
	if risks := Classify("find . -type f -print 2>/dev/null | head -n 10"); len(risks) != 0 {
		t.Fatalf("unexpected risks: %+v", risks)
	}
	if risks := Classify("echo hello > output.txt"); len(risks) != 1 || risks[0].Category != "overwrite" {
		t.Fatalf("expected overwrite risk, got %+v", risks)
	}
	if risks := Classify("git clean -fd"); len(risks) != 1 || risks[0].Category != "destructive" {
		t.Fatalf("expected destructive risk, got %+v", risks)
	}
	if risks := Classify("sed -i.bak s/old/new/ file"); len(risks) != 1 || risks[0].Category != "in-place-write" {
		t.Fatalf("expected in-place write risk, got %+v", risks)
	}
}

func installedTarget(t *testing.T, shell Shell) Target {
	t.Helper()
	path, err := exec.LookPath(string(shell))
	if err != nil {
		t.Skipf("%s is not installed", shell)
	}
	return Target{Name: shell, Requested: shell, Executable: path, Source: SourceCLI}
}
