package schema

import "encoding/json"

type Role string

const (
	RoleSystem    Role = "system"    // systemPrompt: build agent personality and red line
	RoleUser      Role = "user"      // user input / result from tool execution
	RoleAssistant Role = "assistant" // model output: including Reasoning or ToolCall
)

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`     // input tokens
	CompletionTokens int `json:"completion_tokens"` // output tokens
}

type Message struct {
	Role       Role       `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallId string     `json:"tool_call_id,omitempty"`
	Usage      *Usage     `json:"usage,omitempty"`
}

type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

type ToolResult struct {
	ToolCallId string `json:"tool_call_id"`
	Output     string `json:"output"`
	IsError    bool   `json:"is_error"`
}

type ToolDefinition struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"input_schema"`
}
