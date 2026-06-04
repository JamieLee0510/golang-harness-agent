package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/utils"
)

type WriteFileTool struct {
	workDir string
}

func NewWriteFileTool(workDir string) *WriteFileTool {
	return &WriteFileTool{
		workDir: workDir,
	}
}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Creates or overwrites a file. It will be created automatically if the directory does not exist. Please provide a relative path to the workspace.",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path to write to, such as src/main.go",
				},
				"content": map[string]any{
					"type":        "string",
					"description": "The complete file content to be written",
				},
			},
			"required": []string{"path", "content"},
		},
	}
}

type writeFileArgs struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (t *WriteFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	var input writeFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		return "", fmt.Errorf("Failed to parse Args: %w", err)
	}

	// [Security Defense]: resolve within workDir and reject sandbox escapes,
	// preventing the LLM from writing to system-level files.
	fullPath, err := utils.ResolvePath(t.workDir, input.Path)
	if err != nil {
		return "", err
	}

	// Automatically create any missing parent directories
	if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
		return "", fmt.Errorf("Failed to create parent folder: %w", err)
	}

	// Write the file content with permission 0644
	if err := os.WriteFile(fullPath, []byte(input.Content), 0644); err != nil {
		return "", fmt.Errorf("Failed to write file: %w", err)
	}

	return fmt.Sprintf("Write content into file successfully: %s", input.Path), nil
}
