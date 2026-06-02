package engine

import (
	"context"
	"fmt"
	"strings"
)

// TerminalReporter outputs engine progress to standard output, used for local CLI mode.
type TerminalReporter struct{}

// NewTerminalReporter builds a TerminalReporter.
func NewTerminalReporter() *TerminalReporter {
	return &TerminalReporter{}
}

func (r *TerminalReporter) OnThinking(ctx context.Context) {
	fmt.Printf("\n[🤔 Thinking] Model is reasoning...\n")
}

func (r *TerminalReporter) OnToolCall(ctx context.Context, toolName string, args string) {
	fmt.Printf("[🛠️ Calling tool] %s\n", toolName)

	// Truncate overly long argument displays to keep the terminal clean.
	displayArgs := strings.ReplaceAll(args, "\n", "\\n")
	displayArgs = strings.ReplaceAll(displayArgs, "\r", "\\r")
	if len(displayArgs) > 150 {
		displayArgs = displayArgs[:150] + "... (truncated)"
	}
	fmt.Printf(" Args: %s\n", displayArgs)
}

func (r *TerminalReporter) OnToolResult(ctx context.Context, toolName string, result string, isError bool) {
	if isError {
		fmt.Printf("[❌ Execution failed] %s\n", toolName)
		if result != "" {
			fmt.Printf(" Error: %s\n", result)
		}
	} else {
		fmt.Printf("[✅ Execution succeeded] %s\n", toolName)
	}
}

func (r *TerminalReporter) OnMessage(ctx context.Context, content string) {
	if content == "" {
		return
	}
	fmt.Printf("\n🤖 Agent reply:\n%s\n\n", content)
}
