package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JamieLee0510/go-agent-harness/internal/process"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type TaskLogsTool struct {
	manager *process.Manager
}

func NewTaskLogsTool(manager *process.Manager) *TaskLogsTool {
	return &TaskLogsTool{manager: manager}
}

func (t *TaskLogsTool) Name() string {
	return "task_logs"
}

func (t *TaskLogsTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Reads the latest stdout/stderr from a background task (started by bash_background). The buffer keeps only the last ~8KB of output per stream; anything older is dropped.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_id": map[string]any{
					"type":        "string",
					"description": "The task_id returned by bash_background.",
				},
				"lines": map[string]any{
					"type":        "integer",
					"description": "Optional. If > 0, return only the last N lines from each stream.",
				},
			},
			"required": []string{"task_id"},
		},
	}
}

type taskLogsArgs struct {
	TaskID string `json:"task_id"`
	Lines  int    `json:"lines"`
}

func (t *TaskLogsTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input taskLogsArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("failed to parse args: %w", err)
	}

	task, ok := t.manager.Get(input.TaskID)
	if !ok {
		return "", fmt.Errorf("task %s not found", input.TaskID)
	}

	stdoutStr := task.Stdout.String()
	stderrStr := task.Stderr.String()
	if input.Lines > 0 {
		stdoutStr = lastNLines(stdoutStr, input.Lines)
		stderrStr = lastNLines(stderrStr, input.Lines)
	}

	snap := task.Snapshot()
	var sb strings.Builder
	fmt.Fprintf(&sb, "task_id: %s\nstatus: %s\n", snap.ID, snap.Status)
	if snap.ExitCode != nil {
		fmt.Fprintf(&sb, "exit_code: %d\n", *snap.ExitCode)
	}
	fmt.Fprintf(&sb, "command: %s\n", snap.Command)
	fmt.Fprintf(&sb, "\n--- stdout (%d bytes total written) ---\n%s\n", task.Stdout.Total(), stdoutStr)
	fmt.Fprintf(&sb, "\n--- stderr (%d bytes total written) ---\n%s\n", task.Stderr.Total(), stderrStr)
	return sb.String(), nil
}

func lastNLines(s string, n int) string {
	if n <= 0 || s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	if len(lines) <= n {
		return s
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}
