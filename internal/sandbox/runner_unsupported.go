//go:build !darwin && !linux

package sandbox

import (
	"context"
	"fmt"
)

type unsupportedRunner struct{}

func New() Runner { return &unsupportedRunner{} }

func (*unsupportedRunner) Canary(context.Context, []string) error {
	return fmt.Errorf("inspection sandbox is unsupported on this platform")
}

func (*unsupportedRunner) Run(context.Context, Command) Result {
	return Result{Status: StatusFailed, Err: fmt.Errorf("inspection sandbox is unsupported on this platform")}
}
