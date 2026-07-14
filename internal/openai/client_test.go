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
	message, err := client.Complete(context.Background(), Request{Model: "test", Messages: []Message{{Role: "user", Content: "hi"}}})
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
	_, err := client.Complete(ctx, Request{Model: "test"})
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
	_, err := client.Complete(context.Background(), Request{Model: "test"})
	if err == nil || !strings.Contains(err.Error(), "401") || calls.Load() != 1 {
		t.Fatalf("unexpected result: %v calls=%d", err, calls.Load())
	}
}
