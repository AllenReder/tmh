package command

import "context"

type bashAdapter struct{}

func (bashAdapter) guidance() string {
	return "Generate exactly one Bash command compatible with Bash 3.2 or newer. Use Bash syntax; do not emit zsh- or fish-only syntax."
}

func (bashAdapter) check(ctx context.Context, target Target, command string) error {
	// BASH_ENV and ENV are also pinned to /dev/null by syntaxEnvironment.
	return runSyntaxCheck(ctx, target, "--noprofile", "--norc", "-n", "-c", command)
}
