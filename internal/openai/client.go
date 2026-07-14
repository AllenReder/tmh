package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
}

type FunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type ToolDefinition struct {
	Type     string             `json:"type"`
	Function FunctionDefinition `json:"function"`
}

type Request struct {
	Model    string           `json:"model"`
	Messages []Message        `json:"messages"`
	Tools    []ToolDefinition `json:"tools,omitempty"`
}

type response struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

type HTTPError struct {
	StatusCode int
	Body       string
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("model endpoint returned HTTP %d: %s", e.StatusCode, e.Body)
}

type Client struct {
	BaseURL     string
	APIKey      string
	HTTPClient  *http.Client
	Debug       bool
	DebugWriter io.Writer
	Version     string
}

func (c *Client) Complete(ctx context.Context, request Request) (Message, error) {
	if c.HTTPClient == nil {
		c.HTTPClient = &http.Client{}
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		message, retry, err := c.completeOnce(ctx, request)
		if err == nil {
			return message, nil
		}
		lastErr = err
		if !retry || attempt == 1 || ctx.Err() != nil {
			break
		}
		if c.Debug && c.DebugWriter != nil {
			fmt.Fprintf(c.DebugWriter, "Debug: transient model error; retrying once: %v\n", err)
		}
		timer := time.NewTimer(250 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return Message{}, ctx.Err()
		case <-timer.C:
		}
	}
	return Message{}, lastErr
}

func (c *Client) completeOnce(ctx context.Context, request Request) (Message, bool, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return Message{}, false, fmt.Errorf("encode model request: %w", err)
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Message{}, false, fmt.Errorf("create model request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	version := c.Version
	if version == "" {
		version = "dev"
	}
	req.Header.Set("User-Agent", "tmh/"+version)
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.Debug && c.DebugWriter != nil {
		fmt.Fprintf(c.DebugWriter, "Debug: POST %s model=%q messages=%d tools=%d\n", endpoint, request.Model, len(request.Messages), len(request.Tools))
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			return Message{}, false, err
		}
		return Message{}, true, fmt.Errorf("call model endpoint: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, 1<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return Message{}, true, fmt.Errorf("read model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := strings.TrimSpace(string(data))
		if len(text) > 4096 {
			text = text[:4096] + "..."
		}
		httpErr := &HTTPError{StatusCode: resp.StatusCode, Body: text}
		return Message{}, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, httpErr
	}

	var decoded response
	if err := json.Unmarshal(data, &decoded); err != nil {
		return Message{}, false, fmt.Errorf("decode model response: %w", err)
	}
	if decoded.Error != nil {
		return Message{}, false, fmt.Errorf("model error: %s", decoded.Error.Message)
	}
	if len(decoded.Choices) == 0 {
		return Message{}, false, fmt.Errorf("model response contained no choices")
	}
	return decoded.Choices[0].Message, false, nil
}
