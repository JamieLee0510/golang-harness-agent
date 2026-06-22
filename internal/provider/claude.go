package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type ClaudeProvider struct {
	client anthropic.Client
	model  string
}

func NewClaudeProvider(model string) *ClaudeProvider {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	return &ClaudeProvider{client: anthropic.NewClient(option.WithAPIKey(apiKey)), model: model}
}

// toStringSlice coerces a schema "required" value into []string. It accepts the
// native []string (local tools) as well as []interface{} of strings (MCP tools
// or any JSON-decoded schema), returning nil for anything else.
func toStringSlice(v any) []string {
	switch s := v.(type) {
	case []string:
		return s
	case []any:
		out := make([]string, 0, len(s))
		for _, item := range s {
			if str, ok := item.(string); ok {
				out = append(out, str)
			}
		}
		return out
	default:
		return nil
	}
}

// The official Anthropic SDK strictly separates the tool's Properties and Required fields into dedicated struct types
func (p *ClaudeProvider) Generate(ctx context.Context, msgs []schema.Message, availTools []schema.ToolDefinition) (*schema.Message, error) {
	var anthropicMsgs []anthropic.MessageParam
	var systemPrompt string

	// 1. translate context messages
	for _, msg := range msgs {
		switch msg.Role {
		case schema.RoleSystem:
			systemPrompt = msg.Content
		case schema.RoleUser:
			if msg.ToolCallId != "" {
				anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
					anthropic.NewToolResultBlock(msg.ToolCallId, msg.Content, false),
				))
			} else {
				anthropicMsgs = append(anthropicMsgs, anthropic.NewUserMessage(
					anthropic.NewTextBlock(msg.Content),
				))
			}
		case schema.RoleAssistant:
			var blocks []anthropic.ContentBlockParamUnion

			if msg.Content != "" {
				blocks = append(blocks, anthropic.NewTextBlock(msg.Content))
			}

			// Convert historical tool calls back to Claude's specific ToolUseBlockParam
			for _, tc := range msg.ToolCalls {
				var inputMap map[string]any
				_ = json.Unmarshal(tc.Arguments, &inputMap)
				blocks = append(blocks, anthropic.ContentBlockParamUnion{
					OfToolUse: &anthropic.ToolUseBlockParam{
						ID:    tc.ID,
						Name:  tc.Name,
						Input: inputMap,
					},
				})
			}
			if len(blocks) > 0 {
				anthropicMsgs = append(anthropicMsgs, anthropic.NewAssistantMessage(blocks...))
			}
		}

	}

	// 2， translate tool definition
	var anthropicTools []anthropic.ToolUnionParam
	for _, toolDef := range availTools {
		// ToolInputSchemaParam is a struct that requires precise filling via the Properties field
		var properties map[string]any
		var required []string

		// Normalize the schema into a map[string]any first. A direct assertion
		// only works for locally-defined tools; MCP tools (and any schema that
		// has been JSON round-tripped) arrive as map[string]interface{} with
		// required as []interface{}, so we fall back to a JSON round-trip and
		// then read the fields type-tolerantly.
		m, ok := toolDef.InputSchema.(map[string]any)
		if !ok {
			b, _ := json.Marshal(toolDef.InputSchema)
			_ = json.Unmarshal(b, &m)
		}

		if p, ok := m["properties"].(map[string]any); ok {
			properties = p
		}
		required = toStringSlice(m["required"])

		tp := anthropic.ToolParam{
			Name:        toolDef.Name,
			Description: anthropic.String(toolDef.Description),
			InputSchema: anthropic.ToolInputSchemaParam{
				Properties: properties,
				Required:   required,
			},
		}
		anthropicTools = append(anthropicTools, anthropic.ToolUnionParam{OfTool: &tp})
	}

	// 3. build the request and send
	params := anthropic.MessageNewParams{
		Model:     anthropic.Model(p.model),
		MaxTokens: 4096,
		Messages:  anthropicMsgs,
	}

	if systemPrompt != "" {
		params.System = []anthropic.TextBlockParam{
			{Text: systemPrompt},
		}
	}

	if len(anthropicTools) > 0 {
		params.Tools = anthropicTools
	}

	// sync request
	resp, err := p.client.Messages.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("Claude API request failed: %w", err)
	}
	if len(resp.Content) == 0 {
		return nil, fmt.Errorf("Claude API return empty Choices")
	}

	// 4. Turn API Response into internal schema.Message
	resultMsg := &schema.Message{
		Role: schema.RoleAssistant,
	}

	// Usage info
	if resp.Usage.InputTokens > 0 || resp.Usage.OutputTokens > 0 {
		resultMsg.Usage = &schema.Usage{
			PromptTokens:     int(resp.Usage.InputTokens),
			CompletionTokens: int(resp.Usage.OutputTokens),
		}
	}

	for _, block := range resp.Content {
		switch block.Type {
		case "text":
			resultMsg.Content += block.Text
		case "tool_use":
			argBytes, _ := json.Marshal(block.Input)
			resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: argBytes,
			})
		}
	}

	return resultMsg, nil
}
