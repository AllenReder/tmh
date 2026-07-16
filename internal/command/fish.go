package command

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

const minimumFishMinor = 6

var fishVersionPattern = regexp.MustCompile("(?i)\\bfish,\\s+version\\s+(\\d+)\\.(\\d+)(?:\\.\\d+)?\\b")

type fishAdapter struct{}

func (fishAdapter) guidance() string {
	return "Generate exactly one fish command compatible with fish 3.6 or newer. Use fish syntax; do not emit POSIX, Bash, or zsh syntax where fish differs."
}

func (fishAdapter) check(ctx context.Context, target Target, command string) error {
	if err := checkFishVersion(ctx, target); err != nil {
		return err
	}
	return runSyntaxCheck(ctx, target, "--no-config", "--no-execute", "-c", command)
}

func checkFishVersion(ctx context.Context, target Target) error {
	checkCtx, cancel := context.WithTimeout(ctx, syntaxCheckTimeout)
	defer cancel()
	cmd := exec.CommandContext(checkCtx, target.Executable, "--version")
	cmd.Env = syntaxEnvironment()
	output, err := cmd.CombinedOutput()
	if checkCtx.Err() != nil {
		return fmt.Errorf("check fish version: %w", checkCtx.Err())
	}
	if err != nil {
		return fmt.Errorf("check fish version: %w", err)
	}
	major, minor, ok := parseFishVersion(strings.TrimSpace(string(output)))
	if !ok {
		return fmt.Errorf("check fish version: unrecognized output %q", strings.TrimSpace(string(output)))
	}
	if major < 3 || (major == 3 && minor < minimumFishMinor) {
		return fmt.Errorf("fish 3.6 or newer is required; found %d.%d", major, minor)
	}
	return nil
}

func parseFishVersion(value string) (int, int, bool) {
	match := fishVersionPattern.FindStringSubmatch(value)
	if len(match) != 3 {
		return 0, 0, false
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	return major, minor, majorErr == nil && minorErr == nil
}
