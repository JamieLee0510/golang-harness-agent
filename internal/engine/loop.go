package engine

import (
	"context"
	"fmt"
	"log"
	"sync"

	ctxpkg "github.com/JamieLee0510/go-agent-harness/internal/context"
	"github.com/JamieLee0510/go-agent-harness/internal/provider"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
	"github.com/JamieLee0510/go-agent-harness/internal/tools"
)

// AgentEngine drives the ReAct main loop, wiring together the Provider, tool registry, context compaction and self-healing mechanisms.
type AgentEngine struct {
	provider       provider.LLMProvider
	registry       tools.Registry
	EnableThinking bool
	PlanMode       bool
	compactor      *ctxpkg.Compactor
	recovery       *ctxpkg.RecoveryManager
	injector       *ReminderInjector
}

// NewAgentEngine builds an AgentEngine. The compactor's watermark is set to 3000 characters and protects the most recent 6 messages
// (roughly two turns of interaction), so compaction behavior can be observed even within a small context window.
func NewAgentEngine(p provider.LLMProvider, r tools.Registry, enableThinking bool, planMode bool) *AgentEngine {
	return &AgentEngine{
		provider:       p,
		registry:       r,
		EnableThinking: enableThinking,
		PlanMode:       planMode,
		compactor:      ctxpkg.NewCompactor(3000, 6),
		recovery:       ctxpkg.NewRevocerManager(),
		injector:       NewReminderInjector(),
	}
}

// Run wakes the specified session and executes the ReAct main loop until the model no longer requests tools.
// reporter may be nil (pure backend mode), in which case all progress reporting is skipped.
func (e *AgentEngine) Run(ctx context.Context, session *ctxpkg.Session, reporter Reporter) error {
	// Bind the session to ctx so layers below the engine (the cost tracker wrapping the provider, and the
	// subagent loop spawned via RunSub which inherits this ctx) can attribute token usage to this session.
	ctx = ctxpkg.WithSession(ctx, session)

	log.Printf("[Engine] awake session [%s], and lock workspace: %s\n", session.ID, session.WorkDir)

	// Dynamically assemble the System Prompt: each session is bound to its own WorkDir,
	// and the composer loads that project's AGENTS.md and skills accordingly.
	composer := ctxpkg.NewPromptComposer(session.WorkDir, e.PlanMode)
	systemMsg := composer.Build()

	turnCount := 0

	for {
		turnCount++
		log.Printf("======== [Turn %d] start =======\n", turnCount)

		availableTools := e.registry.GetAvailableTools()

		// Pull recent Working Memory from the Session (the latest 20 messages, leaving the compactor enough room to make decisions).
		workingMemory := session.GetWorkingMemory(20)

		var contextHistory []schema.Message
		contextHistory = append(contextHistory, systemMsg)
		contextHistory = append(contextHistory, workingMemory...)

		// Run through the compactor before inference: when total character count exceeds the limit, early logs are masked and oversized logs are trimmed at both ends.
		compactedContext := e.compactor.Compact(contextHistory)

		// Phase 1: thinking stage. Strip tools to force the model to plan first.
		if e.EnableThinking {
			log.Printf("[Enging][Phase 1] deprivate tools and force to thinking stage")
			if reporter != nil {
				reporter.OnThinking(ctx)
			}
			thinkResp, err := e.provider.Generate(ctx, compactedContext, nil)
			if err != nil {
				return fmt.Errorf("Thinking stage failed: %w", err)
			}

			// If the model produced thinking content, persist it as an Assistant message and merge it into this turn's temporary context for the Action stage to use.
			if thinkResp.Content != "" {
				fmt.Printf("[internal thinking trace]: %s\n", thinkResp.Content)
				session.Append(*thinkResp)
				compactedContext = append(compactedContext, *thinkResp)
			}
		}

		// Phase 2: action stage. Restore tools and wait for the model to act according to its plan.
		log.Printf("[Enging][Phase 2] recover tools and wait for model actions")
		actionResp, err := e.provider.Generate(ctx, compactedContext, availableTools)
		if err != nil {
			return fmt.Errorf("Model generate failed: %w", err)
		}

		session.Append(*actionResp)
		compactedContext = append(compactedContext, *actionResp)

		if actionResp.Content != "" && reporter != nil {
			reporter.OnMessage(ctx, actionResp.Content)
			fmt.Printf("Model: %s\n", actionResp.Content)
		}

		// Termination condition: the model no longer requests tools, meaning the task is complete.
		if len(actionResp.ToolCalls) == 0 {
			log.Printf("[Engine] finish task, break the loop.")
			break
		}

		log.Printf("[Engine] model request to execute %d tools ... \n", len(actionResp.ToolCalls))

		// Execute tool calls concurrently (currently all read/write mixed, gated by middleware inside the registry).
		// Preallocate a fixed-length slice so each goroutine only writes its own index, avoiding the need for a Mutex.
		observationMsgs := make([]schema.Message, len(actionResp.ToolCalls))
		var wg sync.WaitGroup

		// Collect this turn's last tool for the Reminder detector to analyze; with multiple concurrent tools this is simplified to taking the first (idx == 0).
		var lastToolCall schema.ToolCall
		var lastToolResult schema.ToolResult

		for i, toolCall := range actionResp.ToolCalls {
			wg.Add(1)

			// Pass idx and call as arguments to avoid the closure variable capture trap.
			go func(idx int, call schema.ToolCall) {
				defer wg.Done()

				log.Printf(" --> [GO-%d] triggered parallel execution: %s\n", idx, call.Name)

				if reporter != nil {
					reporter.OnToolCall(ctx, call.Name, string(call.Arguments))
				}

				result := e.registry.Execute(ctx, call)

				// On execution error, hand off to the RecoveryManager to diagnose and inject corrective suggestions.
				finalOutput := result.Output
				if result.IsError {
					finalOutput = e.recovery.AnalyzeAndInject(call.Name, result.Output)
					log.Printf(" -> [Go-%d] ❌ injected rescue guide: %s\n", idx, finalOutput)
				} else {
					log.Printf(" -> [Go-%d] ✅ tool executed successfully (returned %d bytes)\n", idx, len(result.Output))
				}

				if reporter != nil {
					// Truncate the display when reporting to humans to avoid overly long messages; the observationMsgs sent back to the model still contain the full data.
					displayOutput := result.Output
					if len(displayOutput) > 200 {
						displayOutput = displayOutput[:200] + "... (truncated)"
					}
					reporter.OnToolResult(ctx, call.Name, displayOutput, result.IsError)
				}

				observationMsgs[idx] = schema.Message{
					Role:       schema.RoleUser,
					Content:    finalOutput,
					ToolCallId: call.ID,
				}

				if idx == 0 {
					lastToolCall = call
					lastToolResult = result
				}
			}(i, toolCall)
		}

		// Block and wait for all concurrent tools to finish executing.
		wg.Wait()
		log.Printf("[Engine]: all concurrent executions finished, aggregating observations...")

		// Persist all tool execution results to the Session, starting the next round of inference.
		session.Append(observationMsgs...)

		// If an intervention rule triggers, append the reminder as a User message at the tail of the Session,
		// so the model sees it first thing next turn, thereby breaking its local fixation.
		reminderMsg := e.injector.CheckAndInject(lastToolCall, lastToolResult)
		if reminderMsg != nil {
			session.Append(*reminderMsg)
		}
	}
	return nil
}

// RunSub spins up a physically isolated subagent loop, providing only the readOnlyRegistry (read-only tools),
// and returns a refined summary once exploration is complete. reporter is an any, asserted back to Reporter by this function before use;
// when nil, progress reporting is skipped.
func (e *AgentEngine) RunSub(ctx context.Context, taskPrompt string, readOnlyRegistry tools.Registry, reporter any) (string, error) {
	// Subagents tend to be lazy, so the System Prompt must strictly require it to verify via tools.
	contextHistory := []schema.Message{
		{
			Role: schema.RoleSystem,
			Content: `
			你是一個專門負責深度探索的探路者 (Explorer Subagent)。
			你的任務是根據主架構師的指令，在當前工作區內仔細閱讀程式碼、查閱日誌，蒐集足夠的資訊。
			【核心紀律】
			1. 你必須、且只能依靠內建工具（如 bash 的 find/grep，或 read_file）去尋找答案。絕對不允許憑空捏造或猜測！
			2. 如果你沒有找到確切的答案，你必須繼續使用工具深入搜尋。
			3. 當且僅當你找到了確切的線索後，停止呼叫工具，直接輸出一段純文字作為你的終極匯報。主架構師會根據你的匯報來做下一步決策。
			`,
		},
		{
			Role:    schema.RoleUser,
			Content: taskPrompt,
		},
	}

	const maxSubTurns = 10
	turnCount := 0

	for {
		turnCount++
		if turnCount > maxSubTurns {
			return "", fmt.Errorf("subagent explored too deeply and was force-recalled after exceeding %d turns; please have the main Agent give it clearer instructions", maxSubTurns)
		}

		// The subagent can only access the read-only tool registry passed in.
		availableTools := readOnlyRegistry.GetAvailableTools()

		compactedContext := e.compactor.Compact(contextHistory)

		// Subtasks require fast responses, so slow thinking is forcibly disabled and actions are predicted directly.
		actionResp, err := e.provider.Generate(ctx, compactedContext, availableTools)
		if err != nil {
			return "", fmt.Errorf("subagent inference failed: %w", err)
		}

		contextHistory = append(contextHistory, *actionResp)

		// Exit condition: the subagent no longer calls tools, meaning it has finished its summary report.
		if len(actionResp.ToolCalls) == 0 {
			return actionResp.Content, nil
		}

		observationMsgs := make([]schema.Message, len(actionResp.ToolCalls))
		var wg sync.WaitGroup

		for i, toolCall := range actionResp.ToolCalls {
			wg.Add(1)
			go func(idx int, call schema.ToolCall) {
				defer wg.Done()

				// Let the terminal user see what the subagent is doing.
				var r Reporter
				// Use comma-ok: reporter is typed as any all the way down (agentctx.WithReporter),
				// so a non-Reporter value must not panic here — fall back to no reporting instead.
				if rep, ok := reporter.(Reporter); ok {
					r = rep
					r.OnToolCall(ctx, fmt.Sprintf("[Subagent] %s", call.Name), string(call.Arguments))
				}

				result := readOnlyRegistry.Execute(ctx, call)

				finalOutput := result.Output
				if result.IsError {
					finalOutput = e.recovery.AnalyzeAndInject(call.Name, result.Output)
				}

				// Guard on r (not reporter): r is only set when the comma-ok assertion above succeeded.
				if r != nil {
					display := finalOutput
					if len(display) > 200 {
						display = display[:200] + "... (truncated)"
					}
					r.OnToolResult(ctx, fmt.Sprintf("[Subagent] %s", call.Name), display, result.IsError)
				}

				observationMsgs[idx] = schema.Message{
					Role:       schema.RoleUser,
					Content:    finalOutput,
					ToolCallId: call.ID,
				}
			}(i, toolCall)
		}

		wg.Wait()
		contextHistory = append(contextHistory, observationMsgs...)
	}
}
