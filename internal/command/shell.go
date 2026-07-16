package command

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Shell identifies a supported command language.
type Shell string

const (
	Auto Shell = "auto"
	Zsh  Shell = "zsh"
	Bash Shell = "bash"
	Fish Shell = "fish"
)

// Source identifies the setting that selected a target shell.
type Source string

const (
	SourceCLI         Source = "--shell"
	SourceEnvironment Source = "TMH_SHELL"
	SourceConfig      Source = "config"
	SourceDefault     Source = "default"
)

// Selection contains shell settings in descending precedence order. An
// explicitly selected "auto" wins over lower-precedence values and detects
// the concrete shell from LoginShell.
type Selection struct {
	CLI         string
	Environment string
	Config      string
	LoginShell  string

	// LookPath is intended for tests and embedders. A nil value uses
	// exec.LookPath.
	LookPath func(string) (string, error)
}

// Target is a resolved shell executable and its command-language adapter.
type Target struct {
	Name       Shell
	Requested  Shell
	Executable string
	Source     Source
}

// ParseShell parses a supported shell name.
func ParseShell(value string) (Shell, error) {
	shell := Shell(strings.ToLower(strings.TrimSpace(value)))
	switch shell {
	case Auto, Zsh, Bash, Fish:
		return shell, nil
	default:
		return "", fmt.Errorf("unsupported shell %q; expected auto, zsh, bash, or fish", value)
	}
}

// Resolve applies CLI > TMH_SHELL > config > default precedence, resolves
// "auto" from $SHELL, and verifies that the selected executable exists in a
// trusted system, package-manager, or per-user shell location.
func Resolve(selection Selection) (Target, error) {
	requested, source, err := selectRequested(selection)
	if err != nil {
		return Target{}, err
	}

	name := requested
	if requested == Auto {
		name, err = detectLoginShell(selection.LoginShell)
		if err != nil {
			return Target{}, fmt.Errorf("resolve auto shell selected by %s: %w", source, err)
		}
	}

	lookPath := selection.LookPath
	verifyExecutable := false
	var executable string
	// An absolute $SHELL is authoritative for real auto selection. This avoids
	// validating with a different same-named shell found earlier on PATH.
	if lookPath == nil && requested == Auto && filepath.IsAbs(strings.TrimSpace(selection.LoginShell)) {
		executable = strings.TrimSpace(selection.LoginShell)
		verifyExecutable = true
	} else {
		if lookPath == nil {
			lookPath = exec.LookPath
			verifyExecutable = true
		}
		executable, err = lookPath(string(name))
		if err != nil {
			return Target{}, fmt.Errorf("%s executable is required to generate and validate %s commands: %w", name, name, err)
		}
	}
	if !filepath.IsAbs(executable) {
		executable, err = filepath.Abs(executable)
		if err != nil {
			return Target{}, fmt.Errorf("resolve %s executable path: %w", name, err)
		}
	}
	if verifyExecutable {
		executable, err = trustedShellExecutable(name, executable)
		if err != nil {
			return Target{}, err
		}
	}

	return Target{
		Name:       name,
		Requested:  requested,
		Executable: filepath.Clean(executable),
		Source:     source,
	}, nil
}

func trustedShellExecutable(shell Shell, executable string) (string, error) {
	clean := filepath.Clean(executable)
	canonical, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s executable: %w", shell, err)
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", fmt.Errorf("inspect %s executable: %w", shell, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("%s executable is not a regular executable file: %s", shell, canonical)
	}
	if !trustedSystemShellLocation(shell, clean, canonical) && !trustedUserShellLocation(shell, clean, canonical) {
		return "", fmt.Errorf("refusing untrusted %s executable location: %s", shell, clean)
	}
	return canonical, nil
}

func trustedUserShellLocation(shell Shell, candidate, canonical string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	home = filepath.Clean(home)
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return false
	}
	directories := []string{
		filepath.Join(home, ".local", "bin"),
		filepath.Join(home, "bin"),
		filepath.Join(home, ".asdf", "shims"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".nix-profile", "bin"),
		filepath.Join(home, ".local", "state", "nix", "profile", "bin"),
	}
	candidateAllowed := false
	for _, directory := range directories {
		if candidate == filepath.Join(directory, string(shell)) {
			candidateAllowed = true
			break
		}
	}
	installPrefixes := []string{
		filepath.Join(home, ".asdf", "installs"),
		filepath.Join(home, ".local", "share", "mise", "installs"),
	}
	if !candidateAllowed && filepath.Base(candidate) == string(shell) {
		for _, prefix := range installPrefixes {
			if pathWithin(candidate, prefix) {
				candidateAllowed = true
				break
			}
		}
	}
	if !candidateAllowed {
		return false
	}

	canonicalDirectories := []string{
		filepath.Join(canonicalHome, ".local", "bin"),
		filepath.Join(canonicalHome, "bin"),
		filepath.Join(canonicalHome, ".asdf", "shims"),
		filepath.Join(canonicalHome, ".local", "share", "mise", "shims"),
		filepath.Join(canonicalHome, ".nix-profile", "bin"),
		filepath.Join(canonicalHome, ".local", "state", "nix", "profile", "bin"),
	}
	for _, directory := range canonicalDirectories {
		if canonical == filepath.Join(directory, string(shell)) {
			return true
		}
	}
	for _, prefix := range []string{
		filepath.Join(canonicalHome, ".asdf", "installs"),
		filepath.Join(canonicalHome, ".local", "share", "mise", "installs"),
	} {
		if filepath.Base(canonical) == string(shell) && pathWithin(canonical, prefix) {
			return true
		}
	}
	return trustedSystemShellTarget(shell, canonical)
}

func trustedSystemShellLocation(shell Shell, candidate, canonical string) bool {
	trustedCandidate := false
	for _, directory := range []string{
		"/bin", "/usr/bin", "/usr/local/bin", "/opt/homebrew/bin", "/opt/local/bin", "/sw/bin",
		"/home/linuxbrew/.linuxbrew/bin", "/run/current-system/sw/bin", "/nix/var/nix/profiles/default/bin",
	} {
		if candidate == filepath.Join(directory, string(shell)) {
			trustedCandidate = true
			break
		}
	}
	if !trustedCandidate {
		return false
	}
	return trustedSystemShellTarget(shell, canonical)
}

func trustedSystemShellTarget(shell Shell, canonical string) bool {
	if filepath.Base(canonical) != string(shell) {
		return false
	}
	for _, prefix := range []string{
		"/bin",
		"/usr/bin",
		"/usr/local/bin",
		"/usr/local/Cellar",
		"/opt/homebrew/bin",
		"/opt/homebrew/Cellar",
		"/opt/local/bin",
		"/sw/bin",
		"/home/linuxbrew/.linuxbrew/bin",
		"/home/linuxbrew/.linuxbrew/Cellar",
		"/nix/store",
	} {
		if pathWithin(canonical, prefix) {
			return true
		}
	}
	return false
}

func pathWithin(path, root string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func selectRequested(selection Selection) (Shell, Source, error) {
	candidates := []struct {
		value  string
		source Source
	}{
		{selection.CLI, SourceCLI},
		{selection.Environment, SourceEnvironment},
		{selection.Config, SourceConfig},
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.value) == "" {
			continue
		}
		shell, err := ParseShell(candidate.value)
		if err != nil {
			return "", candidate.source, fmt.Errorf("invalid shell from %s: %w", candidate.source, err)
		}
		return shell, candidate.source, nil
	}
	return Auto, SourceDefault, nil
}

func detectLoginShell(value string) (Shell, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("$SHELL is empty; select a shell with --shell, TMH_SHELL, or config")
	}
	name := strings.TrimPrefix(filepath.Base(value), "-")
	shell, err := ParseShell(name)
	if err != nil || shell == Auto {
		return "", fmt.Errorf("cannot detect a supported shell from $SHELL=%q; select zsh, bash, or fish explicitly", value)
	}
	return shell, nil
}

func adapterFor(shell Shell) (adapter, error) {
	switch shell {
	case Zsh:
		return zshAdapter{}, nil
	case Bash:
		return bashAdapter{}, nil
	case Fish:
		return fishAdapter{}, nil
	default:
		return nil, fmt.Errorf("target shell %q is not resolved", shell)
	}
}
