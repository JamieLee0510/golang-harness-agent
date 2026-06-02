package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JamieLee0510/go-agent-harness/internal/process"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type TaskKillTool struct {
	manager *process.Manager
}

func NewTaskKillTool(manager *process.Manager) *TaskKillTool {
	return &TaskKillTool{manager: manager}
}

func (t *TaskKillTool) Name() string {
	return "task_kill"
}

func (t *TaskKillTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Stops a running background task. Sends SIGTERM first, then SIGKILL after a 5-second grace period if the process is still alive. The signal is sent to the entire process group, so child processes spawned by the task are also terminated.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The task_id returned by bash_background.",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

type taskKillArgs struct {
	TaskID string `json:"task_id"`
}

func (t *TaskKillTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input taskKillArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("failed to parse args: %w", err)
	}

	if err := t.manager.Kill(input.TaskID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Sent SIGTERM to task %s. Call task_logs or task_list to verify it exited.", input.TaskID), nil
}
