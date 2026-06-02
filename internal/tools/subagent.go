package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/agentctx"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// AgentRunner abstracts the main engine's ability to spawn subagents, avoiding a reverse dependency from tools to engine.
type AgentRunner interface {
	RunSub(ctx context.Context, taskPrompt string, readOnlyRegistry Registry, reporter any) (string, error)
}

// SubagentTool lets the main Agent delegate to a read-only exploration subagent.
type SubagentTool struct {
	runner AgentRunner
	// readOnlyRegistry is the restricted read-only tool registry for the subagent.
	readOnlyRegistry Registry
}

// NewSubagentTool creates a SubagentTool.
func NewSubagentTool(runner AgentRunner, readOnlyRegistry Registry) *SubagentTool {
	return &SubagentTool{
		runner:           runner,
		readOnlyRegistry: readOnlyRegistry,
	}
}

func (t *SubagentTool) Name() string {
	return "spawn_subagent"
}

func (t *SubagentTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Dispatches a sub-agent dedicated to deep exploration. Call this tool when you need to read large amounts of code or trace logic across multiple files. Once it finishes exploring, it returns a highly condensed summary report.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_prompt": map[string]any{
					"type":        "string",
					"description": "The explicit instruction to give the sub-agent",
				},
			},
			"required": []string{"task_prompt"},
		},
	}
}

type subagenArgs struct {
	TaskPrompt string `json:"task_prompt"`
}

func (t *SubagentTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input subagenArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("failed to parse arguments: %w", err)
	}

	log.Printf("[Subagent] 🚀 Main Agent is delegating! Spawning the pathfinder: [%s]...\n", input.TaskPrompt)

	// Retrieve the reporter of the session that initiated this call from ctx.
	// reporter is per-chat and only created when a request arrives, so it cannot be injected when constructing the tool;
	// the upper layer (e.g. telegram.handleAgentRun) puts it into ctx via WithReporter, and we retrieve it here.
	// If it cannot be retrieved (e.g. CLI mode) it will be nil; RunSub has an internal nil check and will skip progress reporting.
	reporter := agentctx.ReporterFromCtx(ctx)

	// Spawn a physically isolated sub-loop, providing only readOnlyRegistry, so the subagent can only read files or run read-only bash.
	summary, err := t.runner.RunSub(ctx, input.TaskPrompt, t.readOnlyRegistry, reporter)

	if err != nil {
		return fmt.Errorf("subagent execution failed: %v", err).Error(), nil
	}

	log.Printf("[Subagent] ✅ Subagent task finished. Returning report to the main trunk...")

	// Tens of thousands of words of code exploration are finally distilled into this lightweight summary returned to the main Agent.
	return fmt.Sprintf("[Subagent Exploration Report]:\n%s", summary), nil
}
