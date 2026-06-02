package engine

import (
	"context"
	"fmt"
	"strings"
)

// TerminalReporter 將引擎進度輸出到標準輸出，用於本機 CLI 模式。
type TerminalReporter struct{}

// NewTerminalReporter 建立 TerminalReporter。
func NewTerminalReporter() *TerminalReporter {
	return &TerminalReporter{}
}

func (r *TerminalReporter) OnThinking(ctx context.Context) {
	fmt.Printf("\n[🤔 思考中] 模型正在推理...\n")
}

func (r *TerminalReporter) OnToolCall(ctx context.Context, toolName string, args string) {
	fmt.Printf("[🛠️ 呼叫工具] %s\n", toolName)

	// 截斷過長的參數顯示，保持終端清爽。
	displayArgs := strings.ReplaceAll(args, "\n", "\\n")
	displayArgs = strings.ReplaceAll(displayArgs, "\r", "\\r")
	if len(displayArgs) > 150 {
		displayArgs = displayArgs[:150] + "... (已截斷)"
	}
	fmt.Printf(" 參數: %s\n", displayArgs)
}

func (r *TerminalReporter) OnToolResult(ctx context.Context, toolName string, result string, isError bool) {
	if isError {
		fmt.Printf("[❌ 執行失敗] %s\n", toolName)
		if result != "" {
			fmt.Printf(" 錯誤: %s\n", result)
		}
	} else {
		fmt.Printf("[✅ 執行成功] %s\n", toolName)
	}
}

func (r *TerminalReporter) OnMessage(ctx context.Context, content string) {
	if content == "" {
		return
	}
	fmt.Printf("\n🤖 Agent 回覆:\n%s\n\n", content)
}
