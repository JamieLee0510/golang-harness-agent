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
	"github.com/JamieLee0510/go-agent-harness/internal/mcp"
	"github.com/JamieLee0510/go-agent-harness/internal/provider"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/telegram"
	"github.com/JamieLee0510/go-agent-harness/internal/tools"
	"github.com/joho/godotenv"
)

func main() {
	// Load .env from the current directory if present, so the binary works
	// without a prior `source .env`. Real env vars already set in the shell
	// take precedence (godotenv.Load does not overwrite existing vars).
	_ = godotenv.Load()

	// Two mutually exclusive modes: default Telegram bot, or -p one-shot CLI run.
	printMode := flag.Bool("p", false, "run one task non-interactively and exit")
	workdirFlag := flag.String("workdir", "", "sandbox directory (default: ./workspace)")
	mcpConfigFlag := flag.String("mcp-config", "mcp.json", "path to the MCP server config")
	mcpListFlag := flag.Bool("mcp-list", false, "connect to configured MCP servers, print their tools, and exit")
	flag.Parse()

	// Diagnostic mode: list tools from configured MCP servers and exit. This
	// needs no LLM, so it runs before the OPENAI_API_KEY check.
	if *mcpListFlag {
		runMCPList(*mcpConfigFlag)
		return
	}

	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("please set OPENAI_API_KEY in .env first")
	}

	// Sandbox root: -workdir if given, otherwise the current working directory.
	// File tools resolve every path against this root and reject escapes
	// (see internal/tools/resolvePath), so the root no longer needs to be a
	// dedicated "/workspace" subdir to stay safe.
	workDir := *workdirFlag
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	if *printMode {
		runPrint(workDir, strings.TrimSpace(strings.Join(flag.Args(), " ")), *mcpConfigFlag)
		return
	}
	runTelegram(workDir, *mcpConfigFlag)
}

// buildEngine wires the provider, tools and sub-agent shared by both modes.
// interactive adds the human-in-the-loop approval middleware, which belongs to
// the interactive front-end and is wired only when that front-end runs.
//
// It also connects to any configured MCP servers and registers their tools into
// the main registry. The returned *mcp.Manager owns those connections; the
// caller MUST Close it at shutdown. The Manager is non-nil even when MCP is
// disabled, so callers can always defer Close unconditionally.
func buildEngine(ctx context.Context, workDir string, interactive bool, mcpConfigPath string) (*engine.AgentEngine, *mcp.Manager) {
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

	// Connect to MCP servers (static, one-shot discovery) and register their
	// tools into the main registry. MCP tools are intentionally NOT added to the
	// read-only subagent registry. A failed/absent config yields an empty
	// Manager and leaves the local tools untouched.
	mcpCfg, err := mcp.LoadConfig(mcpConfigPath)
	if err != nil {
		log.Printf("[MCP] ⚠️ ignoring MCP config %q: %v", mcpConfigPath, err)
		return eng, &mcp.Manager{}
	}
	mcpMgr, _ := mcp.Start(ctx, mcpCfg, registry)
	return eng, mcpMgr
}

// runTelegram starts the interactive bot and blocks until shutdown.
func runTelegram(workDir, mcpConfigPath string) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eng, mcpMgr := buildEngine(ctx, workDir, true, mcpConfigPath)
	defer mcpMgr.Close()

	tb := telegram.NewTelegramBot(eng)

	log.Printf("🚀 golang-harness-agent Telegram service has started")
	tb.Start(ctx)
}

// runPrint runs a single task against the workspace and exits.
func runPrint(workDir, task, mcpConfigPath string) {
	if task == "" {
		log.Fatal("print mode (-p): no task provided")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	eng, mcpMgr := buildEngine(ctx, workDir, false, mcpConfigPath)

	session := engine.NewSession("cli", workDir)
	session.Append(schema.Message{Role: schema.RoleUser, Content: task})

	reporter := engine.NewTerminalReporter()
	runErr := eng.Run(agentctx.WithReporter(ctx, reporter), session, reporter)

	// Close MCP connections explicitly before any os.Exit: os.Exit skips
	// deferred calls, so a deferred Close would otherwise leak the subprocesses
	// on the error path. This is the single-shot "clean teardown" semantics
	// flagged in .self-note/17 (缺陷 2).
	if err := mcpMgr.Close(); err != nil {
		log.Printf("[MCP] close error: %v", err)
	}

	if runErr != nil {
		fmt.Printf("agent run failed: %v\n", runErr)
		os.Exit(1)
	}
}

// runMCPList loads the MCP config, connects to each server, prints the tools it
// exposes, and exits. A diagnostic for verifying MCP connectivity (step 2-3);
// it does not start the agent loop.
func runMCPList(configPath string) {
	cfg, err := mcp.LoadConfig(configPath)
	if err != nil {
		log.Fatalf("[MCP] failed to load config %q: %v", configPath, err)
	}
	if len(cfg.Servers) == 0 {
		log.Printf("[MCP] no servers configured in %q (nothing to list)", configPath)
		return
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	for name, srv := range cfg.Servers {
		log.Printf("[MCP] connecting to %q (%s)...", name, srv.Transport)
		session, err := mcp.Connect(ctx, srv)
		if err != nil {
			// One bad server must not abort the rest (design §10).
			log.Printf("[MCP] ⚠️ %q connect failed: %v", name, err)
			continue
		}

		toolList, err := mcp.DiscoverTools(ctx, session)
		if err != nil {
			log.Printf("[MCP] ⚠️ %q list tools failed: %v", name, err)
			_ = session.Close()
			continue
		}

		fmt.Printf("\n%s — %d tool(s):\n", name, len(toolList))
		for _, t := range toolList {
			fmt.Printf("  • %s — %s\n", t.Name, t.Description)
		}
		_ = session.Close()
	}
}
