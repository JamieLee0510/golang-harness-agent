package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

type OpenAIProvider struct {
	client openai.Client
	model  string
}

func NewOpenAIProvider(model string) *OpenAIProvider {
	apiKey := os.Getenv("OPENAI_API_KEY")

	return &OpenAIProvider{client: openai.NewClient(option.WithAPIKey(apiKey)), model: model}
}

func (p *OpenAIProvider) Generate(ctx context.Context, msgs []schema.Message, availTools []schema.ToolDefinition) (*schema.Message, error) {
	var openaiMsgs []openai.ChatCompletionMessageParamUnion

	// 1. translate context messages
	for _, msg := range msgs {
		switch msg.Role {
		case schema.RoleSystem:
			openaiMsgs = append(openaiMsgs, openai.SystemMessage(msg.Content))
		case schema.RoleUser:
			if msg.ToolCallId != "" {
				// openai v3 order is (content, toolCallID)
				openaiMsgs = append(openaiMsgs, openai.ToolMessage(msg.Content, msg.ToolCallId))
			} else {
				openaiMsgs = append(openaiMsgs, openai.UserMessage(msg.Content))
			}
		case schema.RoleAssistant:
			astParam := openai.ChatCompletionAssistantMessageParam{}

			if msg.Content != "" {
				astParam.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(msg.Content),
				}
			}

			// if there are ToolCalls in history, we should put those back to keep model logic chain
			if len(msg.ToolCalls) > 0 {
				var toolCalls []openai.ChatCompletionMessageToolCallUnionParam
				for _, tc := range msg.ToolCalls {
					// OfFunction corresponds to GetFunction(); the field type is strictly required to be a pointer
					toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallUnionParam{
						OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
							ID:   tc.ID,
							Type: "function",
							Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
								Name:      tc.Name,
								Arguments: string(tc.Arguments),
							},
						},
					})
				}
				astParam.ToolCalls = toolCalls
			}

			openaiMsgs = append(openaiMsgs, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &astParam,
			})
		}

	}

	// 2， translate tool definition
	var openaiTools []openai.ChatCompletionToolUnionParam
	for _, toolDef := range availTools {
		var params shared.FunctionParameters

		// Try direct type assertion first; if it fails, fall back to JSON round-trip serialization to ensure type matching
		if m, ok := toolDef.InputSchema.(map[string]any); ok {
			params = shared.FunctionParameters(m)
		} else {
			b, _ := json.Marshal(toolDef.InputSchema)
			_ = json.Unmarshal(b, &params)
		}

		openaiTools = append(openaiTools, openai.ChatCompletionFunctionTool(
			shared.FunctionDefinitionParam{
				Name:        toolDef.Name,
				Description: openai.String(toolDef.Description),
				Parameters:  params,
			},
		))
	}

	// 3. build the request and send
	params := openai.ChatCompletionNewParams{
		Model:    p.model,
		Messages: openaiMsgs,
	}

	// [Supporting deep thinking] only when availableTools existed, mounts tools
	if len(openaiTools) > 0 {
		params.Tools = openaiTools
	}

	// sync request
	resp, err := p.client.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, fmt.Errorf("OpenAI API request failed: %w", err)
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI API return empty Choices")
	}

	// 4. Turn API Response into internal schema.Message
	choice := resp.Choices[0].Message
	resultMsg := &schema.Message{
		Role:    schema.RoleAssistant,
		Content: choice.Content,
	}

	// Usage info
	if resp.Usage.PromptTokens > 0 || resp.Usage.CompletionTokens > 0 {
		resultMsg.Usage = &schema.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
		}
	}

	for _, tc := range choice.ToolCalls {
		if tc.Type == "function" {
			resultMsg.ToolCalls = append(resultMsg.ToolCalls, schema.ToolCall{
				ID:        tc.ID,
				Name:      tc.Function.Name,
				Arguments: []byte(tc.Function.Arguments), // Extract the JSON string bytes
			})
		}
	}

	return resultMsg, nil
}
