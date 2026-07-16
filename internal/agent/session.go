// Package agent implements the provider-neutral model/tool state machine.
package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/AllenReder/tmh/internal/model"
	"github.com/AllenReder/tmh/internal/tool"
)

type Validator func(context.Context, string) error

type Option func(*Session)

type Session struct {
	client   model.Client
	model    string
	registry *tool.Registry
	limits   tool.Limits
	budget   *tool.Budget

	finalizationInstruction string
	repairInstruction       string
}

func New(client model.Client, modelName string, registry *tool.Registry, options ...Option) *Session {
	session := &Session{
		client:   client,
		model:    modelName,
		registry: registry,
		limits:   tool.DefaultLimits(),
		finalizationInstruction: "Tool access is now disabled. Using only the context already gathered, return the final response now. " +
			"Do not request tools and do not claim to have performed the generated command.",
		repairInstruction: "Your response failed local validation: %s. Return one corrected final response. " +
			"Do not use markdown and do not call tools.",
	}
	for _, option := range options {
		option(session)
	}
	return session
}

func WithLimits(limits tool.Limits) Option {
	return func(session *Session) { session.limits = normalizedLimits(limits) }
}

// WithBudget is primarily useful to expose budget state to a composition
// root. A Session should not be reused concurrently when this option is used.
func WithBudget(budget *tool.Budget) Option {
	return func(session *Session) { session.budget = budget }
}

func WithFinalizationInstruction(instruction string) Option {
	return func(session *Session) {
		if strings.TrimSpace(instruction) != "" {
			session.finalizationInstruction = instruction
		}
	}
}

func WithRepairInstruction(instruction string) Option {
	return func(session *Session) {
		if strings.TrimSpace(instruction) != "" {
			session.repairInstruction = instruction
		}
	}
}

func (s *Session) Run(ctx context.Context, initial []model.Message, validate Validator) (model.Message, error) {
	if s == nil || s.client == nil {
		return model.Message{}, fmt.Errorf("agent model client is required")
	}
	if strings.TrimSpace(s.model) == "" {
		return model.Message{}, fmt.Errorf("agent model name is required")
	}
	if s.registry != nil && s.registry.Err() != nil {
		return model.Message{}, fmt.Errorf("initialize agent tools: %w", s.registry.Err())
	}

	messages := append([]model.Message(nil), initial...)
	limits := normalizedLimits(s.limits)
	budget := s.budget
	if budget == nil {
		budget = tool.NewBudget(limits)
	}
	turns := 0

	for {
		if turns >= limits.MaxTurns {
			return model.Message{}, fmt.Errorf("agent exceeded the %d model turn limit", limits.MaxTurns)
		}
		request := model.Request{Model: s.model, Messages: messages}
		if s.registry != nil {
			request.Tools = s.registry.Definitions()
		}
		response, err := s.client.Complete(ctx, request)
		if err != nil {
			return model.Message{}, err
		}
		turns++
		if len(response.ToolCalls) == 0 {
			return s.validateOrRepair(ctx, messages, response, validate, &turns, limits.MaxTurns, true)
		}
		if s.registry == nil || len(request.Tools) == 0 {
			return model.Message{}, fmt.Errorf("model requested tools when none are available")
		}
		messages = append(messages, response)

		if err := budget.ReserveBatch(response.ToolCalls); err != nil {
			messages = appendBudgetResults(messages, response.ToolCalls, err.Error(), s.registry)
			return s.finalize(ctx, messages, validate, &turns, limits.MaxTurns, false)
		}

		budgetExhausted := false
		for _, call := range response.ToolCalls {
			var result tool.Result
			if budgetExhausted {
				result = tool.Exhausted("agent tool budget was exhausted by an earlier call in this batch")
				s.registry.RecordDenied(call, result)
			} else if call.Function.Name == "run_command" {
				timeout, err := budget.CommandTimeout()
				if err != nil {
					result = tool.Exhausted(err.Error())
					s.registry.RecordDenied(call, result)
					budgetExhausted = true
				} else {
					outputAllowance, allowanceErr := budget.RemainingCommandOutput()
					if allowanceErr != nil {
						result = tool.Exhausted(allowanceErr.Error())
						s.registry.RecordDenied(call, result)
						budgetExhausted = true
					} else {
						callCtx, cancel := context.WithTimeout(ctx, timeout)
						result = s.registry.Execute(callCtx, call)
						cancel()
						result = capCommandOutput(result, outputAllowance)
					}
				}
			} else {
				result = s.registry.Execute(ctx, call)
			}
			budget.Record(call.Function.Name, result)
			if result.Status == tool.StatusBudgetExhausted || (call.Function.Name == "run_command" && budget.CommandBudgetError() != nil) {
				budgetExhausted = true
			}
			messages = append(messages, model.Message{
				Role:       model.RoleTool,
				ToolCallID: call.ID,
				Content:    result.JSON(),
			})
		}

		// Preserve one model turn for a no-tools final response. Once an
		// execution budget is exhausted, no further tool-enabled turn is made.
		if budgetExhausted || turns >= limits.MaxTurns-1 {
			return s.finalize(ctx, messages, validate, &turns, limits.MaxTurns, false)
		}
	}
}

func capCommandOutput(result tool.Result, allowance int64) tool.Result {
	if allowance < 0 {
		allowance = 0
	}
	stdoutAllowance := min(int64(len(result.Stdout)), allowance)
	result.Stdout, result.StdoutTruncated = truncateUTF8(result.Stdout, stdoutAllowance, result.StdoutTruncated)
	allowance -= int64(len(result.Stdout))
	result.Stderr, result.StderrTruncated = truncateUTF8(result.Stderr, allowance, result.StderrTruncated)
	return result
}

func truncateUTF8(value string, limit int64, alreadyTruncated bool) (string, bool) {
	if int64(len(value)) <= limit {
		return value, alreadyTruncated
	}
	if limit <= 0 {
		return "", true
	}
	end := int(limit)
	for end > 0 && value[end]&0xc0 == 0x80 {
		end--
	}
	return value[:end], true
}

func (s *Session) finalize(
	ctx context.Context,
	messages []model.Message,
	validate Validator,
	turns *int,
	maxTurns int,
	allowRepair bool,
) (model.Message, error) {
	if *turns >= maxTurns {
		return model.Message{}, fmt.Errorf("agent has no model turn available for finalization")
	}
	messages = append(messages, model.Message{Role: model.RoleUser, Content: s.finalizationInstruction})
	response, err := s.client.Complete(ctx, model.Request{
		Model:      s.model,
		Messages:   messages,
		ToolChoice: model.ToolChoiceNone,
	})
	if err != nil {
		return model.Message{}, err
	}
	*turns++
	if len(response.ToolCalls) != 0 {
		return model.Message{}, fmt.Errorf("model requested tools during no-tools finalization")
	}
	return s.validateOrRepair(ctx, messages, response, validate, turns, maxTurns, allowRepair)
}

func (s *Session) validateOrRepair(
	ctx context.Context,
	prior []model.Message,
	response model.Message,
	validate Validator,
	turns *int,
	maxTurns int,
	allowRepair bool,
) (model.Message, error) {
	if validate == nil {
		return response, nil
	}
	validationErr := validate(ctx, response.Content)
	if validationErr == nil {
		return response, nil
	}
	if !allowRepair || *turns >= maxTurns {
		return model.Message{}, fmt.Errorf("model output failed local validation: %w", validationErr)
	}

	messages := append([]model.Message(nil), prior...)
	messages = append(messages,
		response,
		model.Message{Role: model.RoleUser, Content: fmt.Sprintf(s.repairInstruction, validationErr)},
	)
	repaired, err := s.client.Complete(ctx, model.Request{
		Model:      s.model,
		Messages:   messages,
		ToolChoice: model.ToolChoiceNone,
	})
	if err != nil {
		return model.Message{}, fmt.Errorf("repair invalid model output: %w", err)
	}
	*turns++
	if len(repaired.ToolCalls) != 0 {
		return model.Message{}, fmt.Errorf("model requested tools during no-tools repair")
	}
	if err := validate(ctx, repaired.Content); err != nil {
		return model.Message{}, fmt.Errorf("model output remained invalid after one repair: %w", err)
	}
	return repaired, nil
}

func appendBudgetResults(messages []model.Message, calls []model.ToolCall, reason string, registry *tool.Registry) []model.Message {
	result := tool.Exhausted(reason)
	for _, call := range calls {
		registry.RecordDenied(call, result)
		messages = append(messages, model.Message{
			Role:       model.RoleTool,
			ToolCallID: call.ID,
			Content:    result.JSON(),
		})
	}
	return messages
}

func normalizedLimits(limits tool.Limits) tool.Limits {
	defaults := tool.DefaultLimits()
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
	return limits
}
