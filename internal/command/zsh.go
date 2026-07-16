package command

import "context"

type zshAdapter struct{}

func (zshAdapter) guidance() string {
	return "Generate exactly one zsh command. Use zsh syntax and built-ins; do not emit Bash- or fish-only syntax."
}

func (zshAdapter) check(ctx context.Context, target Target, command string) error {
	// -f disables user and global startup files after the unavoidable zshenv
	// bootstrap; -n parses without executing the command.
	return runSyntaxCheck(ctx, target, "-f", "-n", "-c", command)
}
