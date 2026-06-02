package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/agentctx"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// AgentRunner 抽象出主引擎拉起子智能體的能力，避免 tools 反向依賴 engine。
type AgentRunner interface {
	RunSub(ctx context.Context, taskPrompt string, readOnlyRegistry Registry, reporter any) (string, error)
}

// SubagentTool 讓主 Agent 委派一個唯讀的探索型子智能體。
type SubagentTool struct {
	runner AgentRunner
	// readOnlyRegistry 是給子智能體的受限唯讀工具註冊表。
	readOnlyRegistry Registry
}

// NewSubagentTool 建立 SubagentTool。
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
		Description: "派出一個專門用於深度探索（Exploration）的子智能體。當你需要閱讀大量程式碼、跨檔案查找邏輯時請呼叫此工具。它在探索完畢後，會給你回傳一份極度精煉的摘要報告。",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"task_prompt": map[string]any{
					"type":        "string",
					"description": "給子智能體下達的明確指令",
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
		return "", fmt.Errorf("解析參數失敗： %w", err)
	}

	log.Printf("[Subagent] 🚀 主 Agent 發起委派！正在拉起探路者: [%s]...\n", input.TaskPrompt)

	// 從 ctx 取出發起本次呼叫的那個會話的 reporter。
	// reporter 是 per-chat、請求進來時才建立的，不能在建構工具時注入；
	// 由上層（如 telegram.handleAgentRun）用 WithReporter 塞進 ctx，這裡再取出。
	// 取不到（如 CLI 模式）會是 nil，RunSub 內部有 nil 判斷，會略過進度回報。
	reporter := agentctx.ReporterFromCtx(ctx)

	// 拉起一個物理隔離的子迴圈，僅提供 readOnlyRegistry，子智能體只能讀檔或執行唯讀的 bash。
	summary, err := t.runner.RunSub(ctx, input.TaskPrompt, t.readOnlyRegistry, reporter)

	if err != nil {
		return fmt.Errorf("子智能體執行失敗: %v", err).Error(), nil
	}

	log.Printf("[Subagent] ✅ 子智能體任務結束。報告回傳給主幹...")

	// 幾萬字的程式碼探索，最終化作這一段輕量摘要回傳給主 Agent。
	return fmt.Sprintf("【子智能體探索報告】:\n%s", summary), nil
}
