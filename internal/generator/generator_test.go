package generator

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/AllenReder/tmh/internal/agenttools"
	"github.com/AllenReder/tmh/internal/openai"
)

type scriptedCompleter struct {
	responses []openai.Message
	requests  []openai.Request
}

func (s *scriptedCompleter) Complete(_ context.Context, request openai.Request) (openai.Message, error) {
	s.requests = append(s.requests, request)
	if len(s.responses) == 0 {
		return openai.Message{}, errors.New("no scripted response")
	}
	response := s.responses[0]
	s.responses = s.responses[1:]
	return response, nil
}

func TestDirectRepairsInvalidOutputOnce(t *testing.T) {
	client := &scriptedCompleter{responses: []openai.Message{
		{Role: "assistant", Content: "```zsh\necho hi\n```"},
		{Role: "assistant", Content: `{"command":"echo hi","explanation":"Prints hi."}`},
	}}
	gen := &Generator{Client: client, Model: "test"}
	result, err := gen.Direct(context.Background(), "say hi", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if result.Command != "echo hi" || len(client.requests) != 2 {
		t.Fatalf("unexpected result: %+v requests=%d", result, len(client.requests))
	}
}

func TestAgentUsesToolThenReturnsCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/package.json", []byte(`{"scripts":{"start":"vite"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	client := &scriptedCompleter{responses: []openai.Message{
		{Role: "assistant", ToolCalls: []openai.ToolCall{{ID: "call-1", Type: "function", Function: openai.FunctionCall{Name: "read_text_file", Arguments: `{"path":"package.json"}`}}}},
		{Role: "assistant", Content: `{"command":"npm run start","explanation":"Uses the project's start script."}`},
	}}
	service, err := agenttools.New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen := &Generator{Client: client, Model: "test"}
	result, err := gen.Agent(context.Background(), "start this project", root, service)
	if err != nil {
		t.Fatal(err)
	}
	if result.Command != "npm run start" || len(client.requests) != 2 {
		t.Fatalf("unexpected result: %+v requests=%d", result, len(client.requests))
	}
	messages := client.requests[1].Messages
	if len(messages) == 0 || messages[len(messages)-1].Role != "tool" {
		t.Fatalf("tool result missing from follow-up: %+v", messages)
	}
}

func TestAgentEnforcesToolCallBudget(t *testing.T) {
	root := t.TempDir()
	calls := make([]openai.ToolCall, 0, 9)
	for i := 0; i < 9; i++ {
		calls = append(calls, openai.ToolCall{ID: string(rune('a' + i)), Type: "function", Function: openai.FunctionCall{Name: "list_directory", Arguments: `{"path":"."}`}})
	}
	client := &scriptedCompleter{responses: []openai.Message{{Role: "assistant", ToolCalls: calls}}}
	service, err := agenttools.New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen := &Generator{Client: client, Model: "test"}
	_, err = gen.Agent(context.Background(), "inspect", root, service)
	if err == nil {
		t.Fatal("expected tool budget error")
	}
}

func TestAgentCanReportInsufficientContext(t *testing.T) {
	client := &scriptedCompleter{responses: []openai.Message{{Role: "assistant", Content: `{"command":"","explanation":"A required file is unavailable."}`}}}
	root := t.TempDir()
	service, err := agenttools.New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	gen := &Generator{Client: client, Model: "test"}
	_, err = gen.Agent(context.Background(), "start it", root, service)
	var insufficient *InsufficientContextError
	if !errors.As(err, &insufficient) {
		t.Fatalf("expected insufficient context error, got %v", err)
	}
}

func TestAgentWithHTTPClientReportsInsufficientContext(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(root+"/.env", []byte("TOKEN=secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant", "tool_calls": []any{map[string]any{
					"id": "call-sensitive", "type": "function", "function": map[string]any{"name": "read_text_file", "arguments": `{"path":".env"}`},
				}},
			}}}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]any{
			"role": "assistant", "content": `{"command":"","explanation":"The required file is blocked by policy."}`,
		}}}})
	}))
	defer server.Close()
	service, err := agenttools.New(root, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &openai.Client{BaseURL: server.URL, HTTPClient: server.Client()}
	gen := &Generator{Client: client, Model: "test"}
	result, err := gen.Agent(context.Background(), "inspect", root, service)
	var insufficient *InsufficientContextError
	if !errors.As(err, &insufficient) {
		t.Fatalf("expected insufficient context error, got result=%+v err=%v calls=%d", result, err, calls.Load())
	}
}
