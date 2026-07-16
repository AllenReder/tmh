package command

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

const syntaxCheckTimeout = 2 * time.Second

type adapter interface {
	guidance() string
	check(context.Context, Target, string) error
}

// Guidance returns prompt guidance for generating commands in the resolved
// target language.
func (target Target) Guidance() string {
	adapter, err := adapterFor(target.Name)
	if err != nil {
		return ""
	}
	return adapter.guidance()
}

func runSyntaxCheck(ctx context.Context, target Target, args ...string) error {
	checkCtx, cancel := context.WithTimeout(ctx, syntaxCheckTimeout)
	defer cancel()

	cmd := exec.CommandContext(checkCtx, target.Executable, args...)
	cmd.Stdin = nil
	cmd.Env = syntaxEnvironment()
	output, err := cmd.CombinedOutput()
	if checkCtx.Err() != nil {
		return fmt.Errorf("%s syntax validation did not complete: %w", target.Name, checkCtx.Err())
	}
	if err == nil {
		return nil
	}
	message := strings.TrimSpace(string(output))
	if message == "" {
		message = err.Error()
	}
	return fmt.Errorf("invalid %s syntax: %s", target.Name, message)
}

func syntaxEnvironment() []string {
	path := os.Getenv("PATH")
	if strings.TrimSpace(path) == "" {
		path = "/usr/bin:/bin"
	}
	return []string{
		"PATH=" + path,
		"HOME=/dev/null",
		"ZDOTDIR=/dev/null",
		"BASH_ENV=/dev/null",
		"ENV=/dev/null",
		"XDG_CONFIG_HOME=/dev/null",
		"LC_ALL=C",
		"TERM=dumb",
	}
}
