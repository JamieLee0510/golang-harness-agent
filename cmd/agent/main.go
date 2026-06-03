package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JamieLee0510/go-agent-harness/internal/agentctx"
	"github.com/JamieLee0510/go-agent-harness/internal/engine"
	"github.com/JamieLee0510/go-agent-harness/internal/provider"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/telegram"
	"github.com/JamieLee0510/go-agent-harness/internal/tools"
)

func main() {
	// Two mutually exclusive modes: default Telegram bot, or -p one-shot CLI run.
	printMode := flag.Bool("p", false, "run one task non-interactively and exit")
	workdirFlag := flag.String("workdir", "", "sandbox directory (default: ./workspace)")
	flag.Parse()

	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("please set OPENAI_API_KEY in .env first")
	}

	workDir := *workdirFlag
	if workDir == "" {
		cwd, _ := os.Getwd()
		workDir = cwd + "/workspace"
	}

	if *printMode {
		runPrint(workDir, strings.TrimSpace(strings.Join(flag.Args(), " ")))
		return
	}
	runTelegram(workDir)
}

// buildEngine wires the provider, tools and sub-agent shared by both modes.
// interactive adds the human-in-the-loop approval middleware, which belongs to
// the interactive front-end and is wired only when that front-end runs.
func buildEngine(workDir string, interactive bool) *engine.AgentEngine {
	llmProvider := provider.NewOpenAIProvider("gpt-5-nano")

	// Read-only registry for the subagent (grep/read only).
	readOnlyRegistry := tools.NewRegistry()
	readOnlyRegistry.Register(tools.NewReadFileTool(workDir))
	readOnlyRegistry.Register(tools.NewBashTool(workDir))

	registry := tools.NewRegistry()
	registry.Register(tools.NewReadFileTool(workDir))
	registry.Register(tools.NewWriteFileTool(workDir))
	registry.Register(tools.NewBashTool(workDir))
	registry.Register(tools.NewEditFileTool(workDir))

	if interactive {
		// Intercept dangerous calls and block on human approval via Telegram.
		registry.Use(func(ctx context.Context, call schema.ToolCall) (bool, string) {
			argsStr := string(call.Arguments)
			if telegram.IsDangerousCommand(call.Name, argsStr) {
				taskID := telegram.NextTaskID()
				reporter := telegram.ReporterFromCtx(ctx)
				allowed, reason := telegram.GlobalApprovalMgr.WaitForApproval(ctx, taskID, call.Name, argsStr, reporter)
				if !allowed {
					return false, reason
				}
				return true, ""
			}
			return true, ""
		})
	}

	eng := engine.NewAgentEngine(llmProvider, registry, false, false)
	registry.Register(tools.NewSubagentTool(eng, readOnlyRegistry))
	return eng
}

// runTelegram starts the interactive bot and blocks until shutdown.
func runTelegram(workDir string) {
	eng := buildEngine(workDir, true)
	tb := telegram.NewTelegramBot(eng)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("🚀 golang-harness-agent Telegram service has started")
	tb.Start(ctx)
}

// runPrint runs a single task against the workspace and exits.
func runPrint(workDir, task string) {
	if task == "" {
		log.Fatal("print mode (-p): no task provided")
	}

	eng := buildEngine(workDir, false)

	session := engine.NewSession("cli", workDir)
	session.Append(schema.Message{Role: schema.RoleUser, Content: task})

	reporter := engine.NewTerminalReporter()
	ctx := agentctx.WithReporter(context.Background(), reporter)

	if err := eng.Run(ctx, session, reporter); err != nil {
		fmt.Printf("agent run failed: %v\n", err)
		os.Exit(1)
	}
}
