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

	// Defensive sandbox: prepare a restricted read-only registry for the subagent.
	readOnlyRegistry := tools.NewRegistry()
	readOnlyRegistry.Register(tools.NewReadFileTool(workDir))
	readOnlyRegistry.Register(tools.NewBashTool(workDir)) // allow simple search operations such as grep

	registry := tools.NewRegistry()

	// mount minimal tool set
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))
	registry.Register(tools.NewEditFileTool(workDir))

	// [Core injection] Register the security interception Middleware (human-in-the-loop)
	// Same closure structure as the Feishu version; the difference lies in two trade-offs for the Telegram scenario:
	//   1. reporter is taken from ctx (telegram.ReporterFromCtx) rather than a single field bound to the bot,
	//      so that with "one bot, multiple concurrent chat rooms" the approval card won't be sent to the wrong person.
	//   2. taskID uses a short code (telegram.NextTaskID) rather than the model's call.ID, because the user has to type approve <id> manually.
	registry.Use(func(ctx context.Context, call schema.ToolCall) (bool, string) {
		argsStr := string(call.Arguments)

		// Only intercept on a hit in the high-risk signature database; otherwise YOLO let it through
		if telegram.IsDangerousCommand(call.Name, argsStr) {
			taskID := telegram.NextTaskID()
			reporter := telegram.ReporterFromCtx(ctx)

			// Suspend the current goroutine, send a message to Telegram, and wait relentlessly for human approval!
			allowed, reason := telegram.GlobalApprovalMgr.WaitForApproval(ctx, taskID, call.Name, argsStr, reporter)
			if !allowed {
				return false, reason // reject, pass the reason back to the LLM
			}
			return true, "" // approve, let the underlying tool through
		}

		// No blacklist hit, let it through directly
		return true, ""
	})

	eng := engine.NewAgentEngine(llmProvider, registry, false, false)

	// Note: reporter is per-chat and only created when a request arrives, so there is no reporter here (at startup).
	// SubAgent retrieves the current chat room's reporter from ctx during Execute (agentctx.ReporterFromCtx),
	// which telegram.handleAgentRun puts into ctx via WithReporter.
	registry.Register(tools.NewSubagentTool(eng, readOnlyRegistry))

	// init telegram bot
	tb := telegram.NewTelegramBot(eng)

	// Set up graceful shutdown: cancel ctx on Ctrl+C / SIGTERM to let polling exit cleanly
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Start long polling, blocking until ctx is cancelled
	log.Printf("🚀 golang-harness-agent Telegram service has started")
	tb.Start(ctx)
}
