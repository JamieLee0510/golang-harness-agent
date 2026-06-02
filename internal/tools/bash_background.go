package tools

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/JamieLee0510/go-agent-harness/internal/process"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type BashBackgroundTool struct {
	workDir string
	manager *process.Manager
}

func NewBashBackgroundTool(workDir string, manager *process.Manager) *BashBackgroundTool {
	return &BashBackgroundTool{workDir: workDir, manager: manager}
}

func (t *BashBackgroundTool) Name() string {
	return "bash_background"
}

func (t *BashBackgroundTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Starts a long-running bash command in the background (e.g., 'npm run dev', 'python server.py'). Returns immediately with a task_id; the process keeps running across turns. Use task_logs to read its output, task_list to see all background tasks, and task_kill to stop one. Do NOT use this for one-off commands — use the 'bash' tool for those.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to run in the background, e.g., 'npm run dev' or 'python -m http.server 8000'.",
				},
			},
			"required": []string{"command"},
		},
	}
}

type bashBackgroundArgs struct {
	Command string `json:"command"`
}

func (t *BashBackgroundTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input bashBackgroundArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("failed to parse args: %w", err)
	}

	// Intentionally ignore ctx: a background task must outlive the current turn's context.
	task, err := t.manager.Spawn(input.Command, t.workDir)
	if err != nil {
		return "", err
	}

	snap := task.Snapshot()
	return fmt.Sprintf(
		"Background task started.\ntask_id: %s\npid: %d\ncommand: %s\n\nUse task_logs(task_id=%q) to read output, task_kill(task_id=%q) to stop it.",
		snap.ID, snap.PID, input.Command, snap.ID, snap.ID,
	), nil
}
