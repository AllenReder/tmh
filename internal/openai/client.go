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
	"unicode"
	"unicode/utf8"

	"github.com/AllenReder/tmh/internal/model"
)

type chatFunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type chatToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function chatFunctionCall `json:"function"`
}

type chatMessage struct {
	Role       string         `json:"role"`
	Content    string         `json:"content,omitempty"`
	ToolCalls  []chatToolCall `json:"tool_calls,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
}

type chatFunctionDefinition struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type chatToolDefinition struct {
	Type     string                 `json:"type"`
	Function chatFunctionDefinition `json:"function"`
}

type chatRequest struct {
	Model      string               `json:"model"`
	Messages   []chatMessage        `json:"messages"`
	Tools      []chatToolDefinition `json:"tools,omitempty"`
	ToolChoice string               `json:"tool_choice,omitempty"`
}

type chatResponse struct {
	Choices []struct {
		Message chatMessage `json:"message"`
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

func (c *Client) Complete(ctx context.Context, request model.Request) (model.Message, error) {
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
			return model.Message{}, ctx.Err()
		case <-timer.C:
		}
	}
	return model.Message{}, lastErr
}

func (c *Client) completeOnce(ctx context.Context, request model.Request) (model.Message, bool, error) {
	body, err := json.Marshal(toChatRequest(request))
	if err != nil {
		return model.Message{}, false, fmt.Errorf("encode model request: %w", err)
	}
	endpoint := strings.TrimRight(c.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return model.Message{}, false, fmt.Errorf("create model request: %w", err)
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
			return model.Message{}, false, err
		}
		return model.Message{}, true, fmt.Errorf("call model endpoint: %w", err)
	}
	defer resp.Body.Close()

	limited := io.LimitReader(resp.Body, 1<<20)
	data, err := io.ReadAll(limited)
	if err != nil {
		return model.Message{}, true, fmt.Errorf("read model response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		text := truncateErrorBody(sanitizeErrorBody(data, c.APIKey), 4096)
		httpErr := &HTTPError{StatusCode: resp.StatusCode, Body: text}
		return model.Message{}, resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500, httpErr
	}

	var decoded chatResponse
	if err := json.Unmarshal(data, &decoded); err != nil {
		return model.Message{}, false, fmt.Errorf("decode model response: %w", err)
	}
	if decoded.Error != nil {
		message := truncateErrorBody(sanitizeErrorBody([]byte(decoded.Error.Message), c.APIKey), 4096)
		return model.Message{}, false, fmt.Errorf("model error: %s", message)
	}
	if len(decoded.Choices) == 0 {
		return model.Message{}, false, fmt.Errorf("model response contained no choices")
	}
	return fromChatMessage(decoded.Choices[0].Message), false, nil
}

func toChatRequest(request model.Request) chatRequest {
	converted := chatRequest{
		Model:      request.Model,
		Messages:   make([]chatMessage, 0, len(request.Messages)),
		Tools:      make([]chatToolDefinition, 0, len(request.Tools)),
		ToolChoice: string(request.ToolChoice),
	}
	for _, message := range request.Messages {
		converted.Messages = append(converted.Messages, toChatMessage(message))
	}
	for _, definition := range request.Tools {
		converted.Tools = append(converted.Tools, chatToolDefinition{
			Type: definition.Type,
			Function: chatFunctionDefinition{
				Name:        definition.Function.Name,
				Description: definition.Function.Description,
				Parameters:  definition.Function.Parameters,
			},
		})
	}
	return converted
}

func toChatMessage(message model.Message) chatMessage {
	converted := chatMessage{
		Role:       string(message.Role),
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
		ToolCalls:  make([]chatToolCall, 0, len(message.ToolCalls)),
	}
	for _, call := range message.ToolCalls {
		converted.ToolCalls = append(converted.ToolCalls, chatToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: chatFunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return converted
}

func fromChatMessage(message chatMessage) model.Message {
	converted := model.Message{
		Role:       model.Role(message.Role),
		Content:    message.Content,
		ToolCallID: message.ToolCallID,
		ToolCalls:  make([]model.ToolCall, 0, len(message.ToolCalls)),
	}
	for _, call := range message.ToolCalls {
		converted.ToolCalls = append(converted.ToolCalls, model.ToolCall{
			ID:   call.ID,
			Type: call.Type,
			Function: model.FunctionCall{
				Name:      call.Function.Name,
				Arguments: call.Function.Arguments,
			},
		})
	}
	return converted
}

func sanitizeErrorBody(data []byte, secrets ...string) string {
	text := strings.ToValidUTF8(string(data), "�")
	text = strings.Map(func(r rune) rune {
		if unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return -1
		}
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	for _, secret := range secrets {
		if len(secret) >= 4 {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	return strings.TrimSpace(text)
}

func truncateErrorBody(text string, limit int) string {
	if limit <= 0 || len(text) <= limit {
		return text
	}
	end := limit
	for end > 0 && !utf8.ValidString(text[:end]) {
		end--
	}
	return text[:end] + "..."
}
