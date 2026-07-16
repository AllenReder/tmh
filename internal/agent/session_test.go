package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/AllenReder/tmh/internal/model"
	"github.com/AllenReder/tmh/internal/tool"
)

type scriptedClient struct {
	responses []model.Message
	requests  []model.Request
}

func (c *scriptedClient) Complete(_ context.Context, request model.Request) (model.Message, error) {
	c.requests = append(c.requests, request)
	if len(c.responses) == 0 {
		return model.Message{}, fmt.Errorf("no scripted response")
	}
	response := c.responses[0]
	c.responses = c.responses[1:]
	return response, nil
}

type countingHandler struct {
	name   string
	runs   int
	result tool.Result
}

func (h *countingHandler) Definition() model.ToolDefinition {
	return model.ToolDefinition{Type: "function", Function: model.FunctionDefinition{Name: h.name}}
}

func (h *countingHandler) Prepare(context.Context, model.ToolCall) (tool.Invocation, tool.Result) {
	return tool.InvocationFunc(func(context.Context) tool.Result {
		h.runs++
		if h.result.Status != "" {
			return h.result
		}
		return tool.Result{Status: tool.StatusCompleted}
	}), tool.Result{}
}

func TestSessionRepairsWithToolsExplicitlyDisabled(t *testing.T) {
	client := &scriptedClient{responses: []model.Message{
		{Role: model.RoleAssistant, Content: "invalid"},
		{Role: model.RoleAssistant, Content: "valid"},
	}}
	session := New(client, "test", nil)
	response, err := session.Run(context.Background(), []model.Message{{Role: model.RoleUser, Content: "request"}}, func(_ context.Context, content string) error {
		if content != "valid" {
			return fmt.Errorf("not valid")
		}
		return nil
	})
	if err != nil || response.Content != "valid" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if len(client.requests) != 2 || client.requests[1].ToolChoice != model.ToolChoiceNone || len(client.requests[1].Tools) != 0 {
		t.Fatalf("repair did not disable tools: %+v", client.requests)
	}
}

func TestSessionReservesOversizedBatchBeforeExecuting(t *testing.T) {
	handler := &countingHandler{name: "test"}
	registry := tool.NewRegistry(nil, handler)
	calls := make([]model.ToolCall, 13)
	for index := range calls {
		calls[index] = model.ToolCall{
			ID:       fmt.Sprintf("call-%d", index),
			Type:     "function",
			Function: model.FunctionCall{Name: "test", Arguments: `{}`},
		}
	}
	client := &scriptedClient{responses: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: calls},
		{Role: model.RoleAssistant, Content: "final"},
	}}
	response, err := New(client, "test", registry).Run(context.Background(), nil, nil)
	if err != nil || response.Content != "final" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if handler.runs != 0 {
		t.Fatalf("part of rejected batch executed: %d", handler.runs)
	}
	if len(client.requests) != 2 || client.requests[1].ToolChoice != model.ToolChoiceNone {
		t.Fatalf("budget exhaustion did not force finalization: %+v", client.requests)
	}
	for _, message := range client.requests[1].Messages {
		if message.Role == model.RoleTool && !contains(message.Content, `"status":"budget_exhausted"`) {
			t.Fatalf("unexpected budget result: %s", message.Content)
		}
	}
}

func TestSessionUsesLastTurnForFinalization(t *testing.T) {
	handler := &countingHandler{name: "test"}
	registry := tool.NewRegistry(nil, handler)
	client := &scriptedClient{}
	for index := 0; index < 7; index++ {
		client.responses = append(client.responses, model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:       fmt.Sprintf("call-%d", index),
				Type:     "function",
				Function: model.FunctionCall{Name: "test", Arguments: `{}`},
			}},
		})
	}
	client.responses = append(client.responses, model.Message{Role: model.RoleAssistant, Content: "final"})
	limits := tool.DefaultLimits()
	limits.MaxToolCalls = 20
	response, err := New(client, "test", registry, WithLimits(limits)).Run(context.Background(), nil, nil)
	if err != nil || response.Content != "final" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if len(client.requests) != 8 || client.requests[7].ToolChoice != model.ToolChoiceNone {
		t.Fatalf("last turn was not reserved for finalization: %d %+v", len(client.requests), client.requests[len(client.requests)-1])
	}
}

func TestSessionFinalizesImmediatelyWhenCommandOutputBudgetIsConsumed(t *testing.T) {
	handler := &countingHandler{
		name: "run_command",
		result: tool.Result{
			Status: tool.StatusCompleted,
			Stdout: "1234",
		},
	}
	registry := tool.NewRegistry(nil, handler)
	client := &scriptedClient{responses: []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:       "inspect-1",
				Type:     "function",
				Function: model.FunctionCall{Name: "run_command", Arguments: `{}`},
			}},
		},
		{Role: model.RoleAssistant, Content: "final"},
	}}
	limits := tool.DefaultLimits()
	limits.MaxCommandOutputBytes = 4
	response, err := New(client, "test", registry, WithLimits(limits)).Run(context.Background(), nil, nil)
	if err != nil || response.Content != "final" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if handler.runs != 1 {
		t.Fatalf("unexpected command executions: %d", handler.runs)
	}
	if len(client.requests) != 2 || client.requests[1].ToolChoice != model.ToolChoiceNone || len(client.requests[1].Tools) != 0 {
		t.Fatalf("consumed output budget did not force no-tools finalization: %+v", client.requests)
	}
}

func TestSessionDoesNotExecuteRemainingBatchAfterDynamicBudgetExhaustion(t *testing.T) {
	commandHandler := &countingHandler{
		name: "run_command",
		result: tool.Result{
			Status: tool.StatusCompleted,
			Stdout: "1234",
		},
	}
	fileHandler := &countingHandler{name: "read_text_file"}
	registry := tool.NewRegistry(nil, commandHandler, fileHandler)
	client := &scriptedClient{responses: []model.Message{
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "inspect-1", Type: "function", Function: model.FunctionCall{Name: "run_command", Arguments: `{}`}},
				{ID: "read-1", Type: "function", Function: model.FunctionCall{Name: "read_text_file", Arguments: `{}`}},
			},
		},
		{Role: model.RoleAssistant, Content: "final"},
	}}
	limits := tool.DefaultLimits()
	limits.MaxCommandOutputBytes = 4
	response, err := New(client, "test", registry, WithLimits(limits)).Run(context.Background(), nil, nil)
	if err != nil || response.Content != "final" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if commandHandler.runs != 1 || fileHandler.runs != 0 {
		t.Fatalf("calls after exhaustion executed: command=%d file=%d", commandHandler.runs, fileHandler.runs)
	}
	request := client.requests[1]
	if request.ToolChoice != model.ToolChoiceNone {
		t.Fatalf("expected no-tools finalization: %+v", request)
	}
	foundExhausted := false
	for _, message := range request.Messages {
		if message.ToolCallID == "read-1" && contains(message.Content, `"status":"budget_exhausted"`) {
			foundExhausted = true
		}
	}
	if !foundExhausted {
		t.Fatalf("remaining call did not receive a budget result: %+v", request.Messages)
	}
}

func TestSessionCapsCumulativeCommandOutput(t *testing.T) {
	handler := &countingHandler{
		name: "run_command",
		result: tool.Result{
			Status: tool.StatusCompleted,
			Stdout: strings.Repeat("x", 30*1024),
		},
	}
	registry := tool.NewRegistry(nil, handler)
	client := &scriptedClient{}
	for index := 0; index < 4; index++ {
		client.responses = append(client.responses, model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:       fmt.Sprintf("command-%d", index),
				Type:     "function",
				Function: model.FunctionCall{Name: "run_command", Arguments: `{}`},
			}},
		})
	}
	client.responses = append(client.responses, model.Message{Role: model.RoleAssistant, Content: "final"})
	limits := tool.DefaultLimits()
	budget := tool.NewBudget(limits)
	response, err := New(client, "test", registry, WithBudget(budget)).Run(context.Background(), nil, nil)
	if err != nil || response.Content != "final" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if snapshot := budget.Snapshot(); snapshot.CommandOutput != limits.MaxCommandOutputBytes {
		t.Fatalf("output budget not enforced: %+v", snapshot)
	}
	var fourth tool.Result
	for _, message := range client.requests[4].Messages {
		if message.ToolCallID == "command-3" {
			if err := json.Unmarshal([]byte(message.Content), &fourth); err != nil {
				t.Fatal(err)
			}
		}
	}
	if len(fourth.Stdout) != 6*1024 || !fourth.StdoutTruncated {
		t.Fatalf("fourth command was not capped to remaining budget: bytes=%d truncated=%v", len(fourth.Stdout), fourth.StdoutTruncated)
	}
}

func TestSessionContinuesAfterDeniedAndNonzeroToolResults(t *testing.T) {
	denied := &countingHandler{
		name:   "denied_tool",
		result: tool.Denied(tool.CodePolicyDenied, "blocked"),
	}
	nonzero := 2
	commandHandler := &countingHandler{
		name: "run_command",
		result: tool.Result{
			Status:   tool.StatusCompleted,
			ExitCode: &nonzero,
			Stderr:   "not a repository",
		},
	}
	registry := tool.NewRegistry(nil, denied, commandHandler)
	client := &scriptedClient{responses: []model.Message{
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID: "denied-1", Type: "function", Function: model.FunctionCall{Name: "denied_tool", Arguments: `{}`},
		}}},
		{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
			ID: "command-1", Type: "function", Function: model.FunctionCall{Name: "run_command", Arguments: `{}`},
		}}},
		{Role: model.RoleAssistant, Content: "final"},
	}}
	response, err := New(client, "test", registry).Run(context.Background(), nil, nil)
	if err != nil || response.Content != "final" {
		t.Fatalf("unexpected result: %+v %v", response, err)
	}
	if denied.runs != 1 || commandHandler.runs != 1 || len(client.requests) != 3 {
		t.Fatalf("agent did not continue after recoverable results: denied=%d command=%d requests=%d", denied.runs, commandHandler.runs, len(client.requests))
	}
}

func contains(value, fragment string) bool {
	for index := 0; index+len(fragment) <= len(value); index++ {
		if value[index:index+len(fragment)] == fragment {
			return true
		}
	}
	return false
}
