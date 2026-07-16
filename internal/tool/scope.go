package tool

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Scope is the canonical set of filesystem roots visible to tools.
type Scope struct {
	cwd   string
	roots []string
}

func NewScope(cwd string, allowedPaths []string) (*Scope, error) {
	requestedCWD, canonicalCWD, err := scopeRootPaths(cwd)
	if err != nil {
		return nil, fmt.Errorf("resolve current directory: %w", err)
	}
	if isSensitiveRoot(requestedCWD) || isSensitiveRoot(canonicalCWD) {
		return nil, fmt.Errorf("current directory is a sensitive path: %s", requestedCWD)
	}
	roots := []string{canonicalCWD}
	for _, path := range allowedPaths {
		requested, canonical, err := scopeRootPaths(path)
		if err != nil {
			return nil, fmt.Errorf("resolve allowed path %q: %w", path, err)
		}
		if isSensitiveRoot(requested) || isSensitiveRoot(canonical) {
			return nil, fmt.Errorf("allowed path is sensitive: %s", requested)
		}
		if !containsPath(roots, canonical) {
			roots = append(roots, canonical)
		}
	}
	sort.Strings(roots)
	return &Scope{cwd: canonicalCWD, roots: roots}, nil
}

func scopeRootPaths(path string) (requested, canonical string, err error) {
	requested, err = filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", "", err
	}
	canonical, err = canonicalExisting(requested)
	if err != nil {
		return "", "", err
	}
	return requested, canonical, nil
}

func (s *Scope) CWD() string { return s.cwd }

func (s *Scope) Roots() []string {
	return append([]string(nil), s.roots...)
}

func (s *Scope) Resolve(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("path is empty")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.cwd, path)
	}
	requested, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	// Check the requested spelling before following symlinks. Otherwise a
	// bare-repository config symlink could shed its Git-metadata identity when
	// canonicalized to an ordinary-looking file elsewhere in the same scope.
	if s.IsSensitive(requested) {
		return "", fmt.Errorf("sensitive paths are not readable: %s", requested)
	}
	canonical, err := canonicalExisting(requested)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	if !s.Contains(canonical) {
		return "", fmt.Errorf("path is outside the allowed roots: %s", canonical)
	}
	if s.IsSensitive(canonical) {
		return "", fmt.Errorf("sensitive paths are not readable: %s", canonical)
	}
	return canonical, nil
}

// IsSensitive evaluates only the path below an allowed root. A root name (or
// one of its ancestors) containing a word such as "token" must not make the
// entire explicitly-authorized tree unusable.
func (s *Scope) IsSensitive(path string) bool {
	for _, root := range s.roots {
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return rel != "." && (IsSensitivePath(rel) || IsSensitivePath(path) || isBareGitMetadataPath(path))
	}
	return IsSensitivePath(path)
}

func (s *Scope) ResolveDirectory(path string) (string, error) {
	resolved, err := s.Resolve(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("not a directory: %s", resolved)
	}
	return resolved, nil
}

func (s *Scope) Contains(path string) bool {
	for _, root := range s.roots {
		rel, err := filepath.Rel(root, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// IsBareGitRoot reports whether path is an in-scope bare Git repository root.
// Scope owns this detection because bare metadata sensitivity is shared by
// file tools and multiple inspection adapters.
func (s *Scope) IsBareGitRoot(path string) bool {
	path = filepath.Clean(path)
	return s.Contains(path) && looksLikeBareGitRepository(path)
}

func IsSensitivePath(path string) bool {
	clean := strings.ToLower(filepath.ToSlash(filepath.Clean(path)))
	parts := strings.FieldsFunc(clean, func(r rune) bool {
		return r == '/' || r == ':' || r == '\\'
	})
	for i, part := range parts {
		if sensitiveDirectory(part) {
			return true
		}
		if i > 0 && parts[i-1] == ".docker" && part == "config.json" {
			return true
		}
		if i > 0 && parts[i-1] == ".config" && part == "gcloud" {
			return true
		}
		if i == len(parts)-1 && sensitiveFileName(part) {
			return true
		}
	}
	return false
}

// IsSensitiveArgument is deliberately conservative because command output is
// sent to an untrusted model. It also catches Git object syntax such as
// HEAD:.env and rg patterns explicitly looking for secrets.
func IsSensitiveArgument(argument string) bool {
	argument = strings.ToLower(strings.TrimSpace(argument))
	if argument == "" {
		return false
	}
	parts := strings.FieldsFunc(argument, func(r rune) bool {
		switch r {
		case '/', '\\', ':', '=', ',', ' ', '\t', '{', '}', '[', ']', '(', ')':
			return true
		default:
			return false
		}
	})
	for _, part := range parts {
		part = strings.Trim(part, "!*?\"'")
		if sensitiveDirectory(part) || sensitiveFileName(part) {
			return true
		}
	}
	return false
}

func sensitiveDirectory(part string) bool {
	if part == "" {
		return false
	}
	switch part {
	case ".git", ".ssh", ".aws", ".azure", ".docker", ".gnupg", ".kube", ".terraform.d", "secrets", ".secrets", "credentials", ".credentials":
		return true
	}
	return false
}

func sensitiveFileName(part string) bool {
	if part == "" {
		return false
	}
	switch part {
	case ".git", ".gitmodules", ".env", ".envrc", ".netrc", ".git-credentials", ".npmrc", ".pypirc", ".vault-token", "id_rsa", "id_ed25519":
		return true
	}
	if strings.HasPrefix(part, ".env.") || strings.Contains(part, "credential") || strings.Contains(part, "secret") || strings.Contains(part, "password") || strings.Contains(part, "token") {
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(part, suffix) {
			return true
		}
	}
	return false
}

// isBareGitMetadataPath recognizes Git metadata that lives at the root of a
// bare repository rather than below a directory named .git. The repository
// root itself remains usable as a run_command cwd, while every descendant is
// treated as sensitive even when that descendant was separately passed as an
// allow-path root.
func isBareGitMetadataPath(path string) bool {
	path = filepath.Clean(path)
	directory := path
	if info, err := os.Lstat(directory); err == nil && !info.IsDir() {
		directory = filepath.Dir(directory)
	}
	for {
		if looksLikeBareGitRepository(directory) {
			rel, err := filepath.Rel(directory, path)
			if err == nil && rel != "." && rel != "" {
				return true
			}
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			return false
		}
		directory = parent
	}
}

func looksLikeBareGitRepository(directory string) bool {
	head, err := os.Lstat(filepath.Join(directory, "HEAD"))
	if err != nil || !(head.Mode().IsRegular() || head.Mode()&os.ModeSymlink != 0) {
		return false
	}
	config, err := os.Lstat(filepath.Join(directory, "config"))
	if err != nil || !(config.Mode().IsRegular() || config.Mode()&os.ModeSymlink != 0) {
		return false
	}
	objects, err := os.Lstat(filepath.Join(directory, "objects"))
	return err == nil && (objects.IsDir() || objects.Mode()&os.ModeSymlink != 0)
}

func isSensitiveRoot(path string) bool {
	clean := filepath.Clean(path)
	info, err := os.Stat(clean)
	if err == nil && !info.IsDir() {
		return IsSensitivePath(clean) || isBareGitMetadataPath(clean)
	}
	parts := strings.Split(strings.ToLower(filepath.ToSlash(clean)), "/")
	for index, part := range parts {
		if sensitiveDirectory(part) {
			return true
		}
		if index > 0 && parts[index-1] == ".config" && part == "gcloud" {
			return true
		}
	}
	base := ""
	if len(parts) > 0 {
		base = parts[len(parts)-1]
	}
	switch base {
	case ".gitmodules", ".env", ".envrc", ".netrc", ".git-credentials", ".npmrc", ".pypirc", ".vault-token", "id_rsa", "id_ed25519":
		return true
	}
	for _, suffix := range []string{".pem", ".key", ".p12", ".pfx"} {
		if strings.HasSuffix(base, suffix) {
			return true
		}
	}
	return isBareGitMetadataPath(clean)
}

func canonicalExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(filepath.Clean(abs))
}

func containsPath(paths []string, candidate string) bool {
	for _, path := range paths {
		if path == candidate {
			return true
		}
	}
	return false
}
