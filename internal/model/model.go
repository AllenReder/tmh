// Package model defines the provider-neutral messages exchanged with a model.
package model

import "context"

type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

type FunctionCall struct {
	Name      string
	Arguments string
}

type ToolCall struct {
	ID       string
	Type     string
	Function FunctionCall
}

type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall
	ToolCallID string
}

type FunctionDefinition struct {
	Name        string
	Description string
	Parameters  map[string]any
}

type ToolDefinition struct {
	Type     string
	Function FunctionDefinition
}

type ToolChoice string

const (
	ToolChoiceAuto ToolChoice = "auto"
	ToolChoiceNone ToolChoice = "none"
)

type Request struct {
	Model      string
	Messages   []Message
	Tools      []ToolDefinition
	ToolChoice ToolChoice
}

type Client interface {
	Complete(context.Context, Request) (Message, error)
}
