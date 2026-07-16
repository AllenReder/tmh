package inspection

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/AllenReder/tmh/internal/tool"
)

const maxGitConfigBytes = 1024 * 1024

// validateLocalGitConfig rejects repository-controlled configuration that can
// launch helpers or redirect Git to unapproved files. Global and system
// configuration are disabled separately in the child environment.
func validateLocalGitConfig(cwd string, scope *tool.Scope) error {
	gitDir, err := discoverGitDir(cwd, scope)
	if err != nil || gitDir == "" {
		return err
	}
	commonDir := gitDir
	if path, ok, err := readGitDirPointer(filepath.Join(gitDir, "commondir"), gitDir, scope); err != nil {
		return err
	} else if ok {
		commonDir = path
	}
	paths := []string{filepath.Join(commonDir, "config")}
	if gitDir != commonDir {
		paths = append(paths, filepath.Join(gitDir, "config"))
	}
	paths = append(paths, filepath.Join(gitDir, "config.worktree"))
	seen := make(map[string]struct{})
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return fmt.Errorf("inspect Git config path: %w", err)
		}
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("Git config must be a regular non-symlink file")
		}
		canonical := filepath.Clean(path)
		if !scope.Contains(canonical) {
			return fmt.Errorf("Git config resolves outside the readable scope")
		}
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		if err := validateGitConfigFile(canonical); err != nil {
			return err
		}
	}
	return nil
}

func discoverGitDir(cwd string, scope *tool.Scope) (string, error) {
	for directory := cwd; ; directory = filepath.Dir(directory) {
		candidate := filepath.Join(directory, ".git")
		info, err := os.Lstat(candidate)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return "", fmt.Errorf(".git symlinks are not allowed for inspection")
			}
			if info.IsDir() {
				canonical, err := filepath.EvalSymlinks(candidate)
				if err != nil {
					return "", fmt.Errorf("resolve Git directory: %w", err)
				}
				if !scope.Contains(canonical) {
					return "", fmt.Errorf("Git directory is outside the readable scope")
				}
				return canonical, nil
			}
			if info.Mode().IsRegular() {
				gitDir, ok, err := readGitDirPointer(candidate, directory, scope)
				if err != nil {
					return "", err
				}
				if !ok {
					return "", fmt.Errorf("invalid .git pointer file")
				}
				return gitDir, nil
			}
			return "", fmt.Errorf(".git is not a directory or pointer file")
		}
		if err != nil && !os.IsNotExist(err) {
			return "", fmt.Errorf("inspect Git directory: %w", err)
		}

		// A bare repository has the config and object store at its root.
		if directory == cwd && scope.IsBareGitRoot(directory) {
			return directory, nil
		}
		parent := filepath.Dir(directory)
		if parent == directory || !scope.Contains(parent) {
			break
		}
	}
	return "", nil
}

func readGitDirPointer(path, relativeTo string, scope *tool.Scope) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("inspect Git directory pointer: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("Git directory pointer must be a regular non-symlink file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("read Git directory pointer: %w", err)
	}
	if len(data) > 4096 || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return "", false, fmt.Errorf("invalid Git directory pointer")
	}
	value := strings.TrimSpace(string(data))
	if strings.HasPrefix(strings.ToLower(value), "gitdir:") {
		value = strings.TrimSpace(value[len("gitdir:"):])
	}
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", false, fmt.Errorf("invalid Git directory pointer")
	}
	if !filepath.IsAbs(value) {
		value = filepath.Join(relativeTo, value)
	}
	value = filepath.Clean(value)
	if !scope.Contains(value) {
		return "", false, fmt.Errorf("Git directory pointer escapes the readable scope")
	}
	canonical, err := filepath.EvalSymlinks(value)
	if err != nil {
		return "", false, fmt.Errorf("resolve Git directory pointer: %w", err)
	}
	if !scope.Contains(canonical) {
		return "", false, fmt.Errorf("Git directory pointer escapes the readable scope")
	}
	info, err = os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return "", false, fmt.Errorf("Git directory pointer is not a directory")
	}
	return canonical, true, nil
}

func validateGitConfigFile(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("inspect Git config: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > maxGitConfigBytes {
		return fmt.Errorf("Git config is not a small regular file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read Git config: %w", err)
	}
	if !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
		return fmt.Errorf("Git config is not valid UTF-8 text")
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), maxGitConfigBytes)
	section := ""
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		// Reject continuation syntax rather than risk interpreting a physical
		// line differently from Git's parser.
		if strings.HasSuffix(line, "\\") {
			return fmt.Errorf("Git config line continuation is not allowed")
		}
		if strings.HasPrefix(line, "[") {
			closing := strings.IndexByte(line, ']')
			if closing <= 1 || strings.TrimSpace(line[closing+1:]) != "" {
				return fmt.Errorf("invalid Git config section at line %d", lineNumber)
			}
			section = baseGitConfigSection(line[1:closing])
			if section == "" {
				return fmt.Errorf("invalid Git config section at line %d", lineNumber)
			}
			if dangerousGitConfigSection(section) {
				return fmt.Errorf("Git config section %q is not allowed for inspection", section)
			}
			continue
		}
		if section == "" {
			return fmt.Errorf("Git config key outside a section at line %d", lineNumber)
		}
		key, value, ok := parseGitConfigEntry(line)
		if !ok {
			return fmt.Errorf("invalid Git config entry at line %d", lineNumber)
		}
		if dangerousGitConfigKey(section, key, value) {
			return fmt.Errorf("Git config key %q is not allowed for inspection", section+"."+key)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan Git config: %w", err)
	}
	return nil
}

func baseGitConfigSection(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if index := strings.IndexAny(value, ". \t\""); index >= 0 {
		value = value[:index]
	}
	for _, r := range value {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return ""
		}
	}
	return value
}

func parseGitConfigEntry(line string) (key, value string, ok bool) {
	index := strings.IndexAny(line, "= \t")
	if index < 0 {
		key = strings.ToLower(strings.TrimSpace(line))
		value = "true"
	} else {
		key = strings.ToLower(strings.TrimSpace(line[:index]))
		value = strings.TrimSpace(line[index:])
		value = strings.TrimSpace(strings.TrimPrefix(value, "="))
	}
	if key == "" {
		return "", "", false
	}
	for _, r := range key {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
			return "", "", false
		}
	}
	return key, value, true
}

func dangerousGitConfigSection(section string) bool {
	switch section {
	case "alias", "filter", "include", "includeif", "pager", "credential", "difftool", "mergetool", "browser", "web", "pretty":
		return true
	default:
		return false
	}
}

func dangerousGitConfigKey(section, key, value string) bool {
	compact := strings.ToLower(strings.ReplaceAll(key, "-", ""))
	for _, marker := range []string{
		"command", "program", "helper", "pager", "editor", "askpass", "fsmonitor", "textconv",
		"orderfile", "showsignature", "pretty", "hooks", "attributesfile", "worktree", "filter",
		"uploadpack", "receivepack", "gitproxy",
	} {
		if strings.Contains(compact, marker) {
			return true
		}
	}
	if section == "remote" && compact == "vcs" {
		return true
	}
	return strings.HasPrefix(strings.TrimLeft(strings.TrimSpace(value), "\"'"), "!")
}
