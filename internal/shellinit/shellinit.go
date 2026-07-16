package shellinit

import (
	"embed"
	"fmt"
	"strings"
)

type BindMode string

const (
	BindDefault BindMode = "default"
	BindNone    BindMode = "none"
	BindForce   BindMode = "force"
)

//go:embed scripts/*
var scripts embed.FS

func Render(shell string, mode BindMode) (string, error) {
	if mode == "" {
		mode = BindDefault
	}
	if mode != BindDefault && mode != BindNone && mode != BindForce {
		return "", fmt.Errorf("unsupported bind mode %q", mode)
	}
	var name string
	switch shell {
	case "zsh":
		name = "scripts/tmh.zsh"
	case "bash":
		name = "scripts/tmh.bash"
	case "fish":
		name = "scripts/tmh.fish"
	default:
		return "", fmt.Errorf("unsupported shell %q", shell)
	}
	data, err := scripts.ReadFile(name)
	if err != nil {
		return "", fmt.Errorf("read embedded %s init: %w", shell, err)
	}
	return strings.ReplaceAll(string(data), "__TMH_BIND_MODE__", string(mode)), nil
}
