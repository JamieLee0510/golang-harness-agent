package provider

import (
	"context"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type LLMProvider interface {
	// Generate receive current context history, available tool list, and then generate a llm reasoning
	Generate(ctx context.Context, messages []schema.Message, availabletools []schema.ToolDefinition) (*schema.Message, error)
}
