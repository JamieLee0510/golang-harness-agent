package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/JamieLee0510/go-agent-harness/internal/engine"
	"github.com/JamieLee0510/go-agent-harness/internal/provider"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/telegram"
	"github.com/JamieLee0510/go-agent-harness/internal/tools"
)

func main() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("please set OPENAI_API_KEY in .env first")
	}

	workDir, _ := os.Getwd()
	workDir += "/workspace"

	llmProvider := provider.NewOpenAIProvider("gpt-5-nano")

	// 防禦沙箱：為子智能體準備受限的唯讀註冊表。
	readOnlyRegistry := tools.NewRegistry()
	readOnlyRegistry.Register(tools.NewReadFileTool(workDir))
	readOnlyRegistry.Register(tools.NewBashTool(workDir)) // 允許簡單的 grep 等搜尋操作

	registry := tools.NewRegistry()

	// mount minimal tool set
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))
	registry.Register(tools.NewEditFileTool(workDir))

	// 【核心注入】註冊安全攔截 Middleware（human-in-the-loop）
	// 與飛書版同樣的閉包結構，差別在 Telegram 場景下的兩個取捨：
	//   1. reporter 從 ctx 取（telegram.ReporterFromCtx），而非綁在 bot 上的單一欄位，
	//      這樣「一個 bot、多個聊天室並發」時不會把審批卡片發錯人。
	//   2. taskID 用短碼（telegram.NextTaskID），而非模型的 call.ID，因為使用者要手打 approve <id>。
	registry.Use(func(ctx context.Context, call schema.ToolCall) (bool, string) {
		argsStr := string(call.Arguments)

		// 命中高危特徵庫才攔截，否則 YOLO 放行
		if telegram.IsDangerousCommand(call.Name, argsStr) {
			taskID := telegram.NextTaskID()
			reporter := telegram.ReporterFromCtx(ctx)

			// 掛起當前協程，發訊息給 Telegram，死死等待人類審批！
			allowed, reason := telegram.GlobalApprovalMgr.WaitForApproval(ctx, taskID, call.Name, argsStr, reporter)
			if !allowed {
				return false, reason // 拒絕，將理由傳回給大模型
			}
			return true, "" // 同意，放行底層工具
		}

		// 沒命中黑名單，直接放行
		return true, ""
	})

	eng := engine.NewAgentEngine(llmProvider, registry, false, false)

	// 注意：reporter 是 per-chat、請求進來才建立的，這裡（啟動期）還沒有 reporter。
	// SubAgent 會在 Execute 時從 ctx 取當前聊天室的 reporter（agentctx.ReporterFromCtx），
	// 由 telegram.handleAgentRun 透過 WithReporter 塞進 ctx。
	registry.Register(tools.NewSubagentTool(eng, readOnlyRegistry))

	// init telegram bot
	tb := telegram.NewTelegramBot(eng)

	// 設置優雅關閉：Ctrl+C / SIGTERM 時取消 ctx，讓 polling 乾淨退出
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// 啟動長輪詢，阻塞直到 ctx 取消
	log.Printf("🚀 golang-harness-agent Telegram 服務已啟動")
	tb.Start(ctx)
}
