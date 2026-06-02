package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/JamieLee0510/go-agent-harness/internal/process"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type TaskListTool struct {
	manager *process.Manager
}

func NewTaskListTool(manager *process.Manager) *TaskListTool {
	return &TaskListTool{manager: manager}
}

func (t *TaskListTool) Name() string {
	return "task_list"
}

func (t *TaskListTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Lists all background tasks (both running and exited) with their task_id, status, runtime, and command.",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{},
		},
	}
}

func (t *TaskListTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	tasks := t.manager.List()
	if len(tasks) == 0 {
		return "No background tasks.", nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%-12s %-8s %-10s %s\n", "TASK_ID", "STATUS", "RUNTIME", "COMMAND")
	for _, task := range tasks {
		snap := task.Snapshot()
		var runtime time.Duration
		if snap.FinishedAt.IsZero() {
			runtime = time.Since(snap.StartedAt)
		} else {
			runtime = snap.FinishedAt.Sub(snap.StartedAt)
		}
		cmd := snap.Command
		if len(cmd) > 60 {
			cmd = cmd[:57] + "..."
		}
		fmt.Fprintf(&sb, "%-12s %-8s %-10s %s\n",
			snap.ID, snap.Status, runtime.Truncate(time.Second), cmd)
	}
	return sb.String(), nil
}
