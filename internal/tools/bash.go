package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"time"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

type BashTool struct {
	workDir string
}

func NewBashTool(workDir string) *BashTool {
	return &BashTool{
		workDir: workDir,
	}
}

func (t *BashTool) Name() string {
	return "bash"
}

func (t *BashTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Executes any bash command in the current working directory. Supports command chaining (e.g., &&). Returns standard output (stdout) and standard error (stderr).",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{
					"type":        "string",
					"description": "The bash command to execute, such as: ls -la or go test ./...",
				},
			},
			"required": []string{"command"},
		},
	}
}

type bashArgs struct {
	Command string `json:"command"`
}

func (t *BashTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input bashArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("Failed to parse args: %w", err)
	}

	// [Harness bottomline 1]: Time Budgeting
	// give bash a maxium executing time, in case LLM block the process(ie. execute top command or keep monitor a web service)
	timeoutCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// On macOS/Linux, we wrap the command in `bash -c` to support env vars, pipes, and complex shell syntax like logical AND (&&).
	cmd := exec.CommandContext(timeoutCtx, "bash", "-c", input.Command)

	// [Harness bottomline 2]: set workDir as executing boundary
	// Ensure the command runs in the user-specified WorkDir by default, not the engine's startup absolute path.
	cmd.Dir = t.workDir

	// Execute and capture CombinedOutput (merging stdout and stderr)
	out, err := cmd.CombinedOutput()
	outputStr := string(out)

	// [Harness bottomline 3]: return error as-is (Self-Correction mechanism)
	// When bash errors out (err != nil), we must NOT return a Go error and abort the program!
	// We must concatenate err and outputStr into a string and return it, letting the LLM analyze the error using its self-correction ability!
	if err != nil {
		return fmt.Sprintf("execute error: %v\nconsole:\n%s", err, outputStr), nil
	}

	// If there is no terminal output (e.g., just ran mkdir), give the model an explicit success feedback
	if outputStr == "" {
		return "command executed successfully, no terminal output", nil
	}

	// [Harness bottomline 4]: length truncation protection (prevent OOM)
	const maxLen = 8000
	if len(outputStr) > maxLen {
		return fmt.Sprintf("%s\n\n...[terminal output too long, truncated to first %d bytes]...", outputStr[:maxLen], maxLen), nil
	}

	return outputStr, nil
}
