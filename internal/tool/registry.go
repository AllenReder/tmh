package tool

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/AllenReder/tmh/internal/model"
)

type Invocation interface {
	Run(context.Context) Result
}

type InvocationFunc func(context.Context) Result

func (f InvocationFunc) Run(ctx context.Context) Result { return f(ctx) }

// Handler separates policy validation from side effects. Prepare must not
// launch processes or mutate external state. A non-zero Result means the call
// was denied before an Invocation could be created.
type Handler interface {
	Definition() model.ToolDefinition
	Prepare(context.Context, model.ToolCall) (Invocation, Result)
}

type AuditPhase string

const (
	AuditRequested AuditPhase = "requested"
	AuditAllowed   AuditPhase = "allowed"
	AuditBlocked   AuditPhase = "blocked"
	AuditStarted   AuditPhase = "started"
	AuditCompleted AuditPhase = "completed"
	AuditTimeout   AuditPhase = "timeout"
)

type AuditEvent struct {
	Phase      AuditPhase
	Tool       string
	Status     Status
	Code       string
	DurationMS int64
}

type AuditSink interface {
	Record(AuditEvent)
}

type AuditFunc func(AuditEvent)

func (f AuditFunc) Record(event AuditEvent) { f(event) }

type writerAudit struct {
	mu sync.Mutex
	w  io.Writer
}

func NewWriterAudit(w io.Writer) AuditSink {
	if w == nil {
		return AuditFunc(func(AuditEvent) {})
	}
	return &writerAudit{w: w}
}

func (a *writerAudit) Record(event AuditEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = fmt.Fprintf(a.w, "Inspect: tool=%s phase=%s", safeAuditField(event.Tool), safeAuditField(string(event.Phase)))
	if event.Status != "" {
		_, _ = fmt.Fprintf(a.w, " status=%s", safeAuditField(string(event.Status)))
	}
	if event.Code != "" {
		_, _ = fmt.Fprintf(a.w, " code=%s", safeAuditField(event.Code))
	}
	if event.DurationMS > 0 {
		_, _ = fmt.Fprintf(a.w, " duration_ms=%d", event.DurationMS)
	}
	_, _ = fmt.Fprintln(a.w)
}

type Registry struct {
	handlers map[string]Handler
	audit    AuditSink
	err      error
}

func NewRegistry(audit AuditSink, handlers ...Handler) *Registry {
	if audit == nil {
		audit = AuditFunc(func(AuditEvent) {})
	}
	r := &Registry{handlers: make(map[string]Handler), audit: audit}
	for _, handler := range handlers {
		if err := r.Register(handler); err != nil && r.err == nil {
			r.err = err
		}
	}
	return r
}

func (r *Registry) Register(handler Handler) error {
	if handler == nil {
		return fmt.Errorf("register nil tool handler")
	}
	name := handler.Definition().Function.Name
	if !validToolName(name) {
		return fmt.Errorf("register tool with invalid name %q", name)
	}
	if _, exists := r.handlers[name]; exists {
		return fmt.Errorf("tool %q is already registered", name)
	}
	r.handlers[name] = handler
	return nil
}

func (r *Registry) Err() error { return r.err }

func (r *Registry) Definitions() []model.ToolDefinition {
	if r == nil || r.err != nil {
		return nil
	}
	definitions := make([]model.ToolDefinition, 0, len(r.handlers))
	for _, handler := range r.handlers {
		definitions = append(definitions, handler.Definition())
	}
	// Deterministic ordering keeps requests and tests stable.
	for i := 0; i < len(definitions); i++ {
		for j := i + 1; j < len(definitions); j++ {
			if definitions[j].Function.Name < definitions[i].Function.Name {
				definitions[i], definitions[j] = definitions[j], definitions[i]
			}
		}
	}
	return definitions
}

func (r *Registry) Execute(ctx context.Context, call model.ToolCall) Result {
	name := call.Function.Name
	auditName := r.auditToolName(call)
	r.audit.Record(AuditEvent{Phase: AuditRequested, Tool: auditName})
	if r.err != nil {
		result := Denied(CodePolicyDenied, "tool registry is not available")
		r.audit.Record(AuditEvent{Phase: AuditBlocked, Tool: auditName, Status: result.Status, Code: result.Code})
		return result
	}
	handler, ok := r.handlers[name]
	if !ok || call.Type != "function" {
		result := Denied(CodeUnknownTool, "tool is not registered")
		r.audit.Record(AuditEvent{Phase: AuditBlocked, Tool: auditName, Status: result.Status, Code: result.Code})
		return result
	}
	invocation, rejected := handler.Prepare(ctx, call)
	if rejected.Status != "" {
		r.audit.Record(AuditEvent{Phase: AuditBlocked, Tool: auditName, Status: rejected.Status, Code: rejected.Code})
		return rejected
	}
	if invocation == nil {
		result := Failed(CodeExecutionFailed, "tool produced no invocation")
		r.audit.Record(AuditEvent{Phase: AuditBlocked, Tool: auditName, Status: result.Status, Code: result.Code})
		return result
	}
	r.audit.Record(AuditEvent{Phase: AuditAllowed, Tool: auditName})
	r.audit.Record(AuditEvent{Phase: AuditStarted, Tool: auditName})
	started := time.Now()
	result := invocation.Run(ctx)
	if result.DurationMS == 0 {
		result.DurationMS = durationMillis(time.Since(started))
	}
	phase := AuditCompleted
	if result.Status == StatusTimeout {
		phase = AuditTimeout
	}
	r.audit.Record(AuditEvent{
		Phase: phase, Tool: auditName, Status: result.Status, Code: result.Code, DurationMS: result.DurationMS,
	})
	return result
}

// RecordDenied records a call rejected by a session-wide policy (for example,
// an atomic budget reservation) without invoking or preparing its handler.
func (r *Registry) RecordDenied(call model.ToolCall, result Result) {
	if r == nil {
		return
	}
	name := r.auditToolName(call)
	r.audit.Record(AuditEvent{Phase: AuditRequested, Tool: name})
	r.audit.Record(AuditEvent{Phase: AuditBlocked, Tool: name, Status: result.Status, Code: result.Code})
}

func (r *Registry) auditToolName(call model.ToolCall) string {
	if call.Type != "function" {
		return "<invalid>"
	}
	if _, exists := r.handlers[call.Function.Name]; !exists {
		return "<invalid>"
	}
	return call.Function.Name
}

func validToolName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func safeAuditField(value string) string {
	if value == "<invalid>" {
		return value
	}
	if value == "" || len(value) > 64 || strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.')
	}) >= 0 {
		return "<invalid>"
	}
	return value
}
