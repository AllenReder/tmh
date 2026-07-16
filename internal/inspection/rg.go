package inspection

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/AllenReder/tmh/internal/tool"
)

const maxRGSafetyScanEntries = 100_000

func planRG(ctx context.Context, arguments []string, cwd string, scope *tool.Scope) ([]string, map[string]string, error) {
	exact := stringSet(
		"--files", "-l", "--files-with-matches", "--files-without-match", "-n", "--line-number", "-N", "--no-line-number",
		"--column", "-i", "--ignore-case", "-s", "--case-sensitive", "-S", "--smart-case", "-F", "--fixed-strings",
		"-w", "--word-regexp", "-x", "--line-regexp", "-v", "--invert-match", "-c", "--count", "--count-matches",
		"--stats", "--json", "--no-heading", "--heading", "-H", "--with-filename", "-I", "--no-filename", "-0", "--null",
		"--crlf", "-U", "--multiline", "--multiline-dotall", "--text", "-a", "--trim", "--passthru",
	)
	valueOptions := stringSet("-g", "--glob", "-t", "--type", "-T", "--type-not", "-m", "--max-count", "-A", "--after-context", "-B", "--before-context", "-C", "--context", "--max-depth", "--max-filesize", "-e", "--regexp")
	prefixes := []string{"--glob=", "--type=", "--type-not=", "--max-count=", "--after-context=", "--before-context=", "--context=", "--max-depth=", "--max-filesize=", "--regexp="}

	filesMode := false
	explicitPattern := false
	positionals := make([]string, 0)
	afterSeparator := false
	for index := 0; index < len(arguments); index++ {
		argument := arguments[index]
		if strings.IndexByte(argument, 0) >= 0 {
			return nil, nil, fmt.Errorf("rg arguments cannot contain NUL")
		}
		if tool.IsSensitiveArgument(argument) {
			return nil, nil, fmt.Errorf("rg argument targets sensitive data")
		}
		if afterSeparator {
			positionals = append(positionals, argument)
			continue
		}
		if argument == "--" {
			afterSeparator = true
			continue
		}
		if !strings.HasPrefix(argument, "-") || argument == "-" {
			positionals = append(positionals, argument)
			continue
		}
		if argument == "--files" {
			filesMode = true
		}
		if _, ok := exact[argument]; ok {
			continue
		}
		matched := false
		for _, prefix := range prefixes {
			if strings.HasPrefix(argument, prefix) && len(argument) > len(prefix) {
				if strings.HasPrefix(prefix, "--regexp") {
					explicitPattern = true
				}
				matched = true
				break
			}
		}
		if matched {
			continue
		}
		if _, ok := valueOptions[argument]; ok {
			index++
			if index >= len(arguments) {
				return nil, nil, fmt.Errorf("rg option %s requires a value", argument)
			}
			value := arguments[index]
			if strings.IndexByte(value, 0) >= 0 || tool.IsSensitiveArgument(value) {
				return nil, nil, fmt.Errorf("rg option %s targets sensitive data", argument)
			}
			if argument == "-e" || argument == "--regexp" {
				explicitPattern = true
			}
			continue
		}
		return nil, nil, fmt.Errorf("rg option %q is not allowed", argument)
	}

	pathStart := 0
	if !filesMode && !explicitPattern {
		if len(positionals) == 0 {
			return nil, nil, fmt.Errorf("rg search pattern is required")
		}
		pathStart = 1
	}
	searchPaths := positionals[pathStart:]
	if len(searchPaths) == 0 {
		searchPaths = []string{cwd}
	}
	for _, path := range searchPaths {
		if path == "-" {
			return nil, nil, fmt.Errorf("rg stdin is not allowed")
		}
		if unsafeRelativePath(path) {
			return nil, nil, fmt.Errorf("rg path escapes the working directory")
		}
		candidate := path
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(cwd, candidate)
		}
		resolved, err := scope.Resolve(candidate)
		if err != nil {
			return nil, nil, fmt.Errorf("rg path is outside the readable scope: %w", err)
		}
		if info, err := os.Stat(resolved); err != nil {
			return nil, nil, fmt.Errorf("inspect rg path: %w", err)
		} else if info.IsDir() && scope.IsSensitive(filepath.Join(resolved, "config")) {
			return nil, nil, fmt.Errorf("rg cannot search a bare Git metadata root")
		} else if info.IsDir() {
			if err := rejectNestedBareRepositories(ctx, resolved, scope); err != nil {
				return nil, nil, err
			}
		}
	}

	planned := []string{"--no-config", "--color=never", "--no-messages"}
	separator := len(arguments)
	for index, argument := range arguments {
		if argument == "--" {
			separator = index
			break
		}
	}
	planned = append(planned, arguments[:separator]...)
	// User globs are intentionally placed first. ripgrep applies glob rules in
	// order, so these mandatory exclusions must be the final matching rules and
	// must remain before a user-supplied -- option terminator.
	for _, pattern := range []string{
		"!**/.git/**", "!**/*.git/**", "!**/.gitmodules", "!**/.env", "!**/.env.*", "!**/.envrc", "!**/.ssh/**", "!**/.aws/**", "!**/.azure/**", "!**/.docker/**", "!**/.config/gcloud/**", "!**/.gnupg/**", "!**/.kube/**", "!**/.terraform.d/**",
		"!**/.netrc", "!**/.git-credentials", "!**/.npmrc", "!**/.pypirc", "!**/.vault-token", "!**/*secret*", "!**/*credential*", "!**/*password*", "!**/*token*",
		"!**/*.pem", "!**/*.key", "!**/*.p12", "!**/*.pfx",
	} {
		planned = append(planned, "--iglob="+pattern)
	}
	planned = append(planned, arguments[separator:]...)
	return planned, map[string]string{
		"RIPGREP_CONFIG_PATH": "/dev/null",
		"NO_COLOR":            "1",
	}, nil
}

// rejectNestedBareRepositories bounds the semantic preflight needed to keep
// recursive rg searches from reading a bare repository's root-level config.
// The walk never follows symlinks, stops at already-sensitive directories,
// honors cancellation, and fails closed before process launch if its fixed
// entry budget is exceeded.
func rejectNestedBareRepositories(ctx context.Context, searchRoot string, scope *tool.Scope) error {
	return rejectNestedBareRepositoriesWithin(ctx, searchRoot, scope, maxRGSafetyScanEntries)
}

func rejectNestedBareRepositoriesWithin(ctx context.Context, searchRoot string, scope *tool.Scope, maxEntries int) error {
	if maxEntries <= 0 {
		return fmt.Errorf("rg safety preflight entry budget must be positive")
	}
	stack := []string{searchRoot}
	entriesSeen := 0
	for len(stack) > 0 {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("rg safety preflight canceled: %w", err)
		}
		last := len(stack) - 1
		directory := stack[last]
		stack = stack[:last]
		if directory != searchRoot && scope.IsBareGitRoot(directory) {
			return fmt.Errorf("rg cannot recursively search nested bare Git metadata: %s", directory)
		}
		opened, err := os.Open(directory)
		if err != nil {
			return fmt.Errorf("inspect rg directory %q: %w", directory, err)
		}
		for {
			entries, readErr := opened.ReadDir(256)
			entriesSeen += len(entries)
			if entriesSeen > maxEntries {
				_ = opened.Close()
				return fmt.Errorf("rg safety preflight exceeded %d entries; use narrower search paths", maxEntries)
			}
			for _, entry := range entries {
				if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
					continue
				}
				child := filepath.Join(directory, entry.Name())
				if scope.IsSensitive(child) {
					continue
				}
				stack = append(stack, child)
			}
			if readErr == io.EOF {
				break
			}
			if readErr != nil {
				_ = opened.Close()
				return fmt.Errorf("inspect rg directory %q: %w", directory, readErr)
			}
		}
		if err := opened.Close(); err != nil {
			return fmt.Errorf("close rg directory %q: %w", directory, err)
		}
	}
	return nil
}
