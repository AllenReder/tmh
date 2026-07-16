package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AllenReder/tmh/internal/model"
)

func TestClientRetriesTransientFailureAndSendsAuth(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Fatalf("missing auth header")
		}
		if calls.Add(1) == 1 {
			http.Error(w, "temporary", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer server.Close()

	client := &Client{BaseURL: server.URL + "/v1", APIKey: "secret", HTTPClient: server.Client()}
	message, err := client.Complete(context.Background(), model.Request{Model: "test", Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if message.Content != "ok" || calls.Load() != 2 {
		t.Fatalf("unexpected response/calls: %+v %d", message, calls.Load())
	}
}

func TestClientHonorsContextDeadline(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := client.Complete(ctx, model.Request{Model: "test"})
	close(release)
	if err == nil || ctx.Err() == nil {
		t.Fatalf("expected deadline error, got %v", err)
	}
}

func TestClientDoesNotRetryAuthenticationFailure(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	_, err := client.Complete(context.Background(), model.Request{Model: "test"})
	if err == nil || !strings.Contains(err.Error(), "401") || calls.Load() != 1 {
		t.Fatalf("unexpected result: %v calls=%d", err, calls.Load())
	}
}

func TestClientSanitizesSecretsAndControlsInHTTPError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad\x1b]8;;https://example.invalid\x07 secret-value"))
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, APIKey: "secret-value", HTTPClient: server.Client()}
	_, err := client.Complete(context.Background(), model.Request{Model: "test"})
	if err == nil || strings.Contains(err.Error(), "secret-value") || strings.Contains(err.Error(), "\x1b") {
		t.Fatalf("unsafe HTTP error: %q", err)
	}
	if !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("expected redaction marker: %q", err)
	}
}

func TestClientSanitizesSuccessfulErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{
				"message": "bad\nInjected: secret-value\x1b[31m" + strings.Repeat("x", 5000),
			},
		})
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, APIKey: "secret-value", HTTPClient: server.Client()}
	_, err := client.Complete(context.Background(), model.Request{Model: "test"})
	if err == nil {
		t.Fatal("expected model error envelope to fail")
	}
	message := err.Error()
	if strings.Contains(message, "secret-value") || strings.ContainsAny(message, "\r\n\x1b") {
		t.Fatalf("unsafe successful error envelope: %q", message)
	}
	if !strings.Contains(message, "[REDACTED]") || !strings.HasSuffix(message, "...") || len(message) > 4120 {
		t.Fatalf("error envelope was not redacted and bounded: len=%d %q", len(message), message)
	}
}

func TestClientSendsExplicitNoToolsChoice(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request["tool_choice"] != "none" {
			t.Fatalf("tool_choice = %#v, want none", request["tool_choice"])
		}
		if _, exists := request["tools"]; exists {
			t.Fatalf("no-tools request unexpectedly included tools: %#v", request)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": "ok"}}},
		})
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	if _, err := client.Complete(context.Background(), model.Request{Model: "test", ToolChoice: model.ToolChoiceNone}); err != nil {
		t.Fatal(err)
	}
}

func TestClientMapsDomainToolsToAndFromChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		tools, ok := request["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("unexpected wire tools: %#v", request["tools"])
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id": "call-1", "type": "function", "function": map[string]any{
						"name": "read_text_file", "arguments": `{"path":"README.md"}`,
					},
				}},
			}}},
		})
	}))
	defer server.Close()
	client := &Client{BaseURL: server.URL, HTTPClient: server.Client()}
	message, err := client.Complete(context.Background(), model.Request{
		Model:    "test",
		Messages: []model.Message{{Role: model.RoleUser, Content: "inspect"}},
		Tools: []model.ToolDefinition{{
			Type: "function",
			Function: model.FunctionDefinition{
				Name: "read_text_file",
				Parameters: map[string]any{
					"type": "object",
				},
			},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(message.ToolCalls) != 1 || message.ToolCalls[0].Function.Name != "read_text_file" || message.ToolCalls[0].Function.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected domain message: %+v", message)
	}
}
