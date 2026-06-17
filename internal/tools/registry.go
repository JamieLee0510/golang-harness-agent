package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"

	"github.com/JamieLee0510/go-agent-harness/internal/observability"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// MiddlewareFunc is the signature for an interceptor that runs before a tool executes.
// The returned allowed indicates whether to permit execution, and rejectReason is the reason returned to the model when intercepted.
type MiddlewareFunc func(ctx context.Context, call schema.ToolCall) (allowed bool, rejectReason string)

// BaseTool is the common interface that all tools must implement.
type BaseTool interface {
	// Name returns the globally unique tool name (the model calls the tool by this name).
	Name() string

	// Definition returns the metadata and parameter JSON schema submitted to the model.
	Definition() schema.ToolDefinition

	// Execute receives the JSON parameters output by the model and executes the actual functional logic.
	Execute(ctx context.Context, args json.RawMessage) (string, error)
}

// Closer is an OPTIONAL interface a tool (or any subsystem) may implement when
// it owns resources that must be released at shutdown — e.g. an MCP client's
// subprocess or long-lived connection. Stateless local tools need not implement
// it. Owners are responsible for invoking Close during teardown.
type Closer interface {
	Close() error
}

// Registry manages tool registration, middleware mounting, and routed execution.
type Registry interface {
	// Register mounts a new tool into the system.
	Register(tool BaseTool)

	// Use mounts a global middleware.
	Use(mw MiddlewareFunc)

	// GetAvailableTools returns the schemas of all mounted tools for the main loop to submit to the Provider.
	GetAvailableTools() []schema.ToolDefinition

	// Execute routes and executes the tool call requested by the model.
	Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult
}

// registryImpl is the default implementation of Registry.
type registryImpl struct {
	// tools is keyed by tool name, providing O(1) route lookup.
	tools       map[string]BaseTool
	middlewares []MiddlewareFunc
}

// NewRegistry creates an empty Registry.
func NewRegistry() Registry {
	return &registryImpl{
		tools:       make(map[string]BaseTool),
		middlewares: make([]MiddlewareFunc, 0),
	}
}

func (r *registryImpl) Register(tool BaseTool) {
	name := tool.Name()
	if _, exists := r.tools[name]; exists {
		log.Printf("[Warning] tool %s has been already registered; will be overwritten.\n", name)
	}
	r.tools[name] = tool
	log.Printf("[Registry] mount tool successfully: %s\n", name)
}

func (r *registryImpl) Use(mw MiddlewareFunc) {
	r.middlewares = append(r.middlewares, mw)
}

func (r *registryImpl) GetAvailableTools() []schema.ToolDefinition {
	var defs []schema.ToolDefinition
	for _, tool := range r.tools {
		defs = append(defs, tool.Definition())
	}
	return defs
}

func (r *registryImpl) Execute(ctx context.Context, call schema.ToolCall) schema.ToolResult {
	// Open one span per tool execution. This is the single source of truth for tool tracing:
	// both the main loop and the subagent loop route through here, so every execution — including
	// middleware interceptions and panics — is captured in one place.
	_, span := observability.StartSpan(ctx, "Tool."+call.Name)
	span.AddAttribute("tool_name", call.Name)
	span.AddAttribute("arguments", string(call.Arguments))
	defer span.EndSpan()

	// 1. Route lookup; not found usually means the model hallucinated a nonexistent tool.
	tool, exists := r.tools[call.Name]
	if !exists {
		errMsg := fmt.Sprintf("Error: the tool '%s' doesn't exist in system", call.Name)
		span.AddAttribute("error", "tool not found")
		return schema.ToolResult{
			ToolCallId: call.ID,
			Output:     errMsg,
			IsError:    true,
		}
	}

	// 2. Before executing the tool, pass through all middlewares in order for security gatekeeping.
	for _, mw := range r.middlewares {
		allowed, rejectReason := mw(ctx, call)
		if !allowed {
			log.Printf("[Registry] ⚠️ Tool %s intercepted by Middleware: %s\n", call.Name, rejectReason)
			span.AddAttribute("intercepted", true)
			span.AddAttribute("reject_reason", rejectReason)
			return schema.ToolResult{
				ToolCallId: call.ID,
				Output:     fmt.Sprintf("Execution intercepted by the system. Reason: %s", rejectReason),
				IsError:    true,
			}
		}
	}

	// 3. Execute the tool.
	// safeExecute wraps the foreign tool call in a panic safety net, so a single buggy tool
	// (nil deref, slice out of range, failed type assertion, ...) cannot crash the whole agent process.
	output, err := safeExecute(ctx, tool, call.Arguments)

	// 4. Wrap the result and return it to the main loop.
	if err != nil {
		errMsg := fmt.Sprintf("Error executing %s: %v", call.Name, err)
		span.AddAttribute("error", err.Error())
		return schema.ToolResult{ToolCallId: call.ID, Output: errMsg, IsError: true}
	}

	// Store only a short preview of the output to keep the trace file from bloating.
	span.AddAttribute("output_preview", truncate(output, 100))

	return schema.ToolResult{
		ToolCallId: call.ID,
		Output:     output,
		IsError:    false,
	}
}

// truncate caps a string at max runes for safe inclusion in trace attributes.
// Counting runes (not bytes) avoids slicing through a multi-byte character — important
// for non-ASCII output, which would otherwise produce invalid UTF-8 in the trace.
func truncate(s string, max int) string {
	rs := []rune(s)
	if len(rs) > max {
		return string(rs[:max]) + "..."
	}
	return s
}

// safeExecute runs a single tool's Execute and converts any panic into an error,
func safeExecute(ctx context.Context, tool BaseTool, args json.RawMessage) (output string, err error) {
	defer func() {
		if r := recover(); r != nil {
			// Log the full stack trace for debugging (the process no longer crashes, so it
			// would otherwise be lost), but hand the model only a concise message.
			log.Printf("[Registry] 🔥 tool '%s' panicked: %v\n%s", tool.Name(), r, debug.Stack())
			err = fmt.Errorf("tool crashed internally (panic): %v", r)
		}
	}()
	return tool.Execute(ctx, args)
}
