package shellinit

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	for _, shell := range []string{"zsh", "bash", "fish"} {
		for _, mode := range []BindMode{BindDefault, BindNone, BindForce} {
			script, err := Render(shell, mode)
			if err != nil {
				t.Fatalf("Render(%q, %q): %v", shell, mode, err)
			}
			if strings.Contains(script, "__TMH_BIND_MODE__") || !strings.Contains(script, string(mode)) {
				t.Fatalf("rendered %s script did not apply bind mode %q", shell, mode)
			}
		}
	}
}

func TestRenderRejectsUnknownValues(t *testing.T) {
	if _, err := Render("powershell", BindDefault); err == nil {
		t.Fatal("expected unsupported shell error")
	}
	if _, err := Render("zsh", "sometimes"); err == nil {
		t.Fatal("expected unsupported bind mode error")
	}
}
