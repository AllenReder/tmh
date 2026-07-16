package tool

import (
	"fmt"
	"sync"
	"time"

	"github.com/AllenReder/tmh/internal/model"
)

type Limits struct {
	MaxTurns                int
	MaxToolCalls            int
	MaxRunCommands          int
	MaxCommandDuration      time.Duration
	MaxTotalCommandDuration time.Duration
	MaxCommandOutputBytes   int64
}

func DefaultLimits() Limits {
	return Limits{
		MaxTurns:                8,
		MaxToolCalls:            12,
		MaxRunCommands:          4,
		MaxCommandDuration:      5 * time.Second,
		MaxTotalCommandDuration: 15 * time.Second,
		MaxCommandOutputBytes:   96 * 1024,
	}
}

type Budget struct {
	mu sync.Mutex

	limits          Limits
	toolCalls       int
	runCommands     int
	commandDuration time.Duration
	commandOutput   int64
}

type BudgetSnapshot struct {
	ToolCalls       int
	RunCommands     int
	CommandDuration time.Duration
	CommandOutput   int64
}

func NewBudget(limits Limits) *Budget {
	defaults := DefaultLimits()
	if limits.MaxTurns <= 0 {
		limits.MaxTurns = defaults.MaxTurns
	}
	if limits.MaxToolCalls <= 0 {
		limits.MaxToolCalls = defaults.MaxToolCalls
	}
	if limits.MaxRunCommands <= 0 {
		limits.MaxRunCommands = defaults.MaxRunCommands
	}
	if limits.MaxCommandDuration <= 0 {
		limits.MaxCommandDuration = defaults.MaxCommandDuration
	}
	if limits.MaxTotalCommandDuration <= 0 {
		limits.MaxTotalCommandDuration = defaults.MaxTotalCommandDuration
	}
	if limits.MaxCommandOutputBytes <= 0 {
		limits.MaxCommandOutputBytes = defaults.MaxCommandOutputBytes
	}
	return &Budget{limits: limits}
}

// ReserveBatch is atomic: either every tool call in a model response is
// reserved, or none of them is.
func (b *Budget) ReserveBatch(calls []model.ToolCall) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	runCommands := 0
	for _, call := range calls {
		if call.Function.Name == "run_command" {
			runCommands++
		}
	}
	if b.toolCalls+len(calls) > b.limits.MaxToolCalls {
		return fmt.Errorf("tool call budget exhausted")
	}
	if b.runCommands+runCommands > b.limits.MaxRunCommands {
		return fmt.Errorf("command execution budget exhausted")
	}
	b.toolCalls += len(calls)
	b.runCommands += runCommands
	return nil
}

func (b *Budget) CommandTimeout() (time.Duration, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err := b.commandBudgetErrorLocked(); err != nil {
		return 0, err
	}
	remaining := b.limits.MaxTotalCommandDuration - b.commandDuration
	return min(b.limits.MaxCommandDuration, remaining), nil
}

func (b *Budget) RemainingCommandOutput() (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limits.MaxCommandOutputBytes - b.commandOutput
	if remaining <= 0 {
		return 0, fmt.Errorf("command output budget exhausted")
	}
	return remaining, nil
}

// CommandBudgetError reports whether accumulated command execution has
// consumed a hard output or wall-clock budget. Callers use this after each
// result so the next model turn can be a no-tools finalization rather than a
// tool-enabled turn that can only be rejected.
func (b *Budget) CommandBudgetError() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.commandBudgetErrorLocked()
}

func (b *Budget) commandBudgetErrorLocked() error {
	if b.commandOutput >= b.limits.MaxCommandOutputBytes {
		return fmt.Errorf("command output budget exhausted")
	}
	if b.commandDuration >= b.limits.MaxTotalCommandDuration {
		return fmt.Errorf("command execution time budget exhausted")
	}
	return nil
}

func (b *Budget) Record(name string, result Result) {
	if name != "run_command" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.commandDuration += time.Duration(result.DurationMS) * time.Millisecond
	b.commandOutput += result.OutputBytes()
}

func (b *Budget) Snapshot() BudgetSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	return BudgetSnapshot{
		ToolCalls:       b.toolCalls,
		RunCommands:     b.runCommands,
		CommandDuration: b.commandDuration,
		CommandOutput:   b.commandOutput,
	}
}
