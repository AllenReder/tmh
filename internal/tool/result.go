package tool

import (
	"encoding/json"
	"time"
)

type Status string

const (
	StatusCompleted       Status = "completed"
	StatusDenied          Status = "denied"
	StatusFailed          Status = "failed"
	StatusTimeout         Status = "timeout"
	StatusCanceled        Status = "canceled"
	StatusBudgetExhausted Status = "budget_exhausted"
)

const (
	CodeInvalidArguments = "invalid_arguments"
	CodeUnknownTool      = "unknown_tool"
	CodePolicyDenied     = "policy_denied"
	CodeOutsideScope     = "outside_scope"
	CodeSensitivePath    = "sensitive_path"
	CodeSandboxRequired  = "sandbox_required"
	CodeSandboxFailure   = "sandbox_failure"
	CodeExecutionFailed  = "execution_failed"
	CodeTimeout          = "timeout"
	CodeCanceled         = "canceled"
	CodeBudgetExhausted  = "budget_exhausted"
)

// Result is the stable envelope returned to the model for every tool call.
// ExitCode is absent when no child process was started.
type Result struct {
	Status          Status `json:"status"`
	Code            string `json:"code,omitempty"`
	Message         string `json:"message,omitempty"`
	ExitCode        *int   `json:"exit_code,omitempty"`
	Stdout          string `json:"stdout"`
	Stderr          string `json:"stderr"`
	DurationMS      int64  `json:"duration_ms"`
	StdoutTruncated bool   `json:"stdout_truncated"`
	StderrTruncated bool   `json:"stderr_truncated"`
	Data            any    `json:"data,omitempty"`
}

func (r Result) JSON() string {
	encoded, err := json.Marshal(r)
	if err != nil {
		fallback, _ := json.Marshal(Result{
			Status:  StatusFailed,
			Code:    CodeExecutionFailed,
			Message: "encode tool result",
		})
		return string(fallback)
	}
	return string(encoded)
}

func (r Result) OutputBytes() int64 {
	return int64(len(r.Stdout) + len(r.Stderr))
}

func durationMillis(d time.Duration) int64 {
	if d <= 0 {
		return 0
	}
	return d.Milliseconds()
}

func Denied(code, message string) Result {
	return Result{Status: StatusDenied, Code: code, Message: message}
}

func Failed(code, message string) Result {
	return Result{Status: StatusFailed, Code: code, Message: message}
}

func Exhausted(message string) Result {
	return Result{Status: StatusBudgetExhausted, Code: CodeBudgetExhausted, Message: message}
}
