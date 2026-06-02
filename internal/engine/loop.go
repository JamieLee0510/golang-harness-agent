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

// AgentEngine 驅動 ReAct 主迴圈，串接 Provider、工具註冊表、上下文壓縮與自癒機制。
type AgentEngine struct {
	provider       provider.LLMProvider
	registry       tools.Registry
	EnableThinking bool
	PlanMode       bool
	compactor      *ctxpkg.Compactor
	recovery       *ctxpkg.RecoveryManager
	injector       *ReminderInjector
}

// NewAgentEngine 建立 AgentEngine。compactor 的水位線設為 3000 字元、保護最近 6 條訊息
// （約兩輪 Turn 的互動），以便在小上下文視窗下也能觀察壓縮行為。
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

// Run 喚醒指定 session 並執行 ReAct 主迴圈，直到模型不再請求工具為止。
// reporter 可為 nil（純後端模式），此時略過所有進度回報。
func (e *AgentEngine) Run(ctx context.Context, session *Session, reporter Reporter) error {
	log.Printf("[Engine] awake session [%s], and lock workspace: %s\n", session.ID, session.WorkDir)

	// 動態組裝 System Prompt：每個 session 綁定自己的 WorkDir，
	// composer 依此載入該專案的 AGENTS.md 與 skills。
	composer := ctxpkg.NewPromptComposer(session.WorkDir, e.PlanMode)
	systemMsg := composer.Build()

	turnCount := 0

	for {
		turnCount++
		log.Printf("======== [Turn %d] start =======\n", turnCount)

		availableTools := e.registry.GetAvailableTools()

		// 從 Session 取出近期的 Working Memory（最近 20 條，給壓縮器留下足夠的判斷空間）。
		workingMemory := session.GetWorkingMemory(20)

		var contextHistory []schema.Message
		contextHistory = append(contextHistory, systemMsg)
		contextHistory = append(contextHistory, workingMemory...)

		// 推理前先過一遍壓縮器：總字元數超標時，早期日誌會被遮罩、超大日誌會被掐頭去尾。
		compactedContext := e.compactor.Compact(contextHistory)

		// Phase 1：思考階段。剝奪工具，強制模型先規劃。
		if e.EnableThinking {
			log.Printf("[Enging][Phase 1] deprivate tools and force to thinking stage")
			if reporter != nil {
				reporter.OnThinking(ctx)
			}
			thinkResp, err := e.provider.Generate(ctx, compactedContext, nil)
			if err != nil {
				return fmt.Errorf("Thinking stage failed: %w", err)
			}

			// 若模型輸出了思考內容，持久化為一條 Assistant 訊息，並併入本輪臨時上下文供 Action 階段使用。
			if thinkResp.Content != "" {
				fmt.Printf("[internal thinking trace]: %s\n", thinkResp.Content)
				session.Append(*thinkResp)
				compactedContext = append(compactedContext, *thinkResp)
			}
		}

		// Phase 2：行動階段。恢復工具，等待模型依規劃發起動作。
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

		// 終止條件：模型不再請求工具，代表任務完成。
		if len(actionResp.ToolCalls) == 0 {
			log.Printf("[Engine] finish task, break the loop.")
			break
		}

		log.Printf("[Engine] model request to execute %d tools ... \n", len(actionResp.ToolCalls))

		// 併發執行工具呼叫（目前皆為讀寫混合，靠 registry 內的中間件把關）。
		// 預分配固定長度的切片，每個 goroutine 只寫自己的索引，因此無需 Mutex。
		observationMsgs := make([]schema.Message, len(actionResp.ToolCalls))
		var wg sync.WaitGroup

		// 收集本輪最後一個工具供 Reminder 探測器分析；多工具併發時這裡簡化為取第一個（idx == 0）。
		var lastToolCall schema.ToolCall
		var lastToolResult schema.ToolResult

		for i, toolCall := range actionResp.ToolCalls {
			wg.Add(1)

			// 將 idx 與 call 作為參數傳入，避免閉包變數捕獲陷阱。
			go func(idx int, call schema.ToolCall) {
				defer wg.Done()

				log.Printf(" --> [GO-%d] 觸發並行執行: %s\n", idx, call.Name)

				if reporter != nil {
					reporter.OnToolCall(ctx, call.Name, string(call.Arguments))
				}

				result := e.registry.Execute(ctx, call)

				// 執行出錯時，交由 RecoveryManager 診斷並注入修正建議。
				finalOutput := result.Output
				if result.IsError {
					finalOutput = e.recovery.AnalyzeAndInject(call.Name, result.Output)
					log.Printf(" -> [Go-%d] ❌ 注入救援指南: %s\n", idx, finalOutput)
				} else {
					log.Printf(" -> [Go-%d] ✅ 工具執行成功 (返回 %d 字節)\n", idx, len(result.Output))
				}

				if reporter != nil {
					// 回報給人類時截斷顯示，避免過長訊息；送回模型的 observationMsgs 仍為完整資料。
					displayOutput := result.Output
					if len(displayOutput) > 200 {
						displayOutput = displayOutput[:200] + "... (已截斷)"
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

		// 阻塞等待所有併發工具執行完畢。
		wg.Wait()
		log.Printf("[Engine]: 所有併發執行完畢，開始聚合觀察結果（Observation）...")

		// 將所有工具執行結果持久化到 Session，開啟下一輪推理。
		session.Append(observationMsgs...)

		// 若觸發干預規則，將提醒作為 User 訊息追加到 Session 尾端，
		// 讓模型下一輪第一眼就看到，藉此打破局部執念。
		reminderMsg := e.injector.CheckAndInject(lastToolCall, lastToolResult)
		if reminderMsg != nil {
			session.Append(*reminderMsg)
		}
	}
	return nil
}

// RunSub 拉起一個物理隔離的子智能體迴圈，只提供 readOnlyRegistry（唯讀工具），
// 探索完畢後回傳一段精煉摘要。reporter 為 any，由本函式斷言回 Reporter 後使用；
// 為 nil 時略過進度回報。
func (e *AgentEngine) RunSub(ctx context.Context, taskPrompt string, readOnlyRegistry tools.Registry, reporter any) (string, error) {
	// 子智能體容易偷懶，System Prompt 必須嚴格要求它依靠工具求證。
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
			return "", fmt.Errorf("子智能體探索過於深入，超過 %d 輪被強制召回，請主 Agent 給它更明確的指令", maxSubTurns)
		}

		// 子智能體僅能取用傳入的唯讀工具註冊表。
		availableTools := readOnlyRegistry.GetAvailableTools()

		compactedContext := e.compactor.Compact(contextHistory)

		// 子任務要求快速回應，強制關閉慢思考，直接預測行動。
		actionResp, err := e.provider.Generate(ctx, compactedContext, availableTools)
		if err != nil {
			return "", fmt.Errorf("子智能體推理失敗: %w", err)
		}

		contextHistory = append(contextHistory, *actionResp)

		// 退出條件：子智能體不再呼叫工具，代表它已完成總結匯報。
		if len(actionResp.ToolCalls) == 0 {
			return actionResp.Content, nil
		}

		observationMsgs := make([]schema.Message, len(actionResp.ToolCalls))
		var wg sync.WaitGroup

		for i, toolCall := range actionResp.ToolCalls {
			wg.Add(1)
			go func(idx int, call schema.ToolCall) {
				defer wg.Done()

				// 讓終端使用者看到子智能體正在做什麼。
				var r Reporter
				if reporter != nil {
					r = reporter.(Reporter)
					r.OnToolCall(ctx, fmt.Sprintf("[Subagent] %s", call.Name), string(call.Arguments))
				}

				result := readOnlyRegistry.Execute(ctx, call)

				finalOutput := result.Output
				if result.IsError {
					finalOutput = e.recovery.AnalyzeAndInject(call.Name, result.Output)
				}

				if reporter != nil {
					display := finalOutput
					if len(display) > 200 {
						display = display[:200] + "... (已截斷)"
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
