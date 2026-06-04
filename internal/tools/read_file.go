package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/utils"
)

// ReadFileTool implement real local file tool
type ReadFileTool struct {
	workDir string
}

func NewReadFileTool(workDir string) *ReadFileTool {
	return &ReadFileTool{
		workDir: workDir,
	}
}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

// Definition: Clearly describe the purpose and parameter format of this tool to LLM.
func (t *ReadFileTool) Definition() schema.ToolDefinition {
	return schema.ToolDefinition{
		Name:        t.Name(),
		Description: "Read the contents of a file at the specified path. Please provide the path relative to the workspace.",

		InputSchema: map[string]any{
			"type": "object",
			// follow JSON Schema
			"properties": map[string]any{
				"path": map[string]any{
					"type":        "string",
					"description": "The file path to read, such as cmd/claw/main.go",
				},
			},
			"required": []string{"path"},
		},
	}
}

type readFileArgs struct {
	Path string `json:"path"`
}

func (t *ReadFileTool) Execute(ctx context.Context, args json.RawMessage) (string, error) {
	// 1. Lazy parsing: parse the JSON arguments from the LLM into a strongly-typed struct
	var input readFileArgs
	if err := json.Unmarshal(args, &input); err != nil {
		// Returning an error will be caught by the Registry and passed to the LLM, so it knows it got the JSON format wrong
		return "", fmt.Errorf("Failed to parse args: %w", err)
	}

	// 2. Resolve the path against the sandbox root. utils.ResolvePath handles
	// both relative and absolute inputs and rejects anything that escapes
	// workDir (path traversal, e.g. ../../etc/passwd).
	fullPath, err := utils.ResolvePath(t.workDir, input.Path)
	if err != nil {
		return "", err
	}

	// 3. execute IO operation
	file, err := os.Open(fullPath)
	if err != nil {
		return "", fmt.Errorf("Failed to open file: %w", err)
	}
	defer file.Close()

	content, err := io.ReadAll(file)
	if err != nil {
		return "", fmt.Errorf("Failed to read the file: %w", err)
	}

	// 4. [Core Defense] length truncation protection
	// To prevent the LLM from reading hundreds of MBs of log files and causing context explosion (OOM),
	// we perform a physical truncation directly inside the tool.
	const maxLen = 8000
	if len(content) > maxLen {
		truncateMsg := fmt.Sprintf("%s\n\n...[content too long, truncated by the system to the first %d bytes]...", string(content[:maxLen]), maxLen)
		return truncateMsg, nil
	}

	return string(content), nil
}

/*
 * Tool Call Offloading:
 * The mainstream approach for industrial-grade Harnesses is to implement an output offloading strategy at the tool execution layer.
 * When a file or command output exceeds a threshold (typically thousands to tens of thousands of characters),
 * the Harness automatically writes the full content to a temporary directory on disk and returns a summary message
 * to the model containing "head preview + tail preview + file path reference",
 * e.g., "File too long (5000 lines total, offloaded to <path>). Below is a head/tail preview; call read_file('<path>') if you need full content."
 * This preserves the model's decision-making context while forcing it to read selectively on demand.
 *
 * Combined with global Context Compaction:
 * Even though we relax read limits within a single tool through offloading, at the engine's global level,
 * industrial-grade Harnesses still have a context-window monitoring mechanism in the Main Loop. When token usage
 * approaches a preset threshold of the model's context window (typically 75%~98%), the Harness triggers Compaction —
 * compressing historical conversations (using various strategies such as smart summarization), preserving high-value
 * information like architectural decisions and unresolved bugs while trimming redundant tool outputs, so the Agent
 * can continue running long-term without losing critical context. This is the global-level ultimate defense against OOM.
 */
