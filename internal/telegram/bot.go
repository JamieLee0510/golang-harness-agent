package telegram

import (
	"context"
	"fmt"
	"log"
	"os"
	"strconv"

	"github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"

	"github.com/JamieLee0510/go-agent-harness/internal/engine"
	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// TelegramBot 封裝 Telegram 機器人的配置與核心業務流。
type TelegramBot struct {
	b      *bot.Bot
	engine *engine.AgentEngine
}

// NewTelegramBot 從環境變數 TELEGRAM_BOT_TOKEN 讀取憑證並建立 TelegramBot。
func NewTelegramBot(eng *engine.AgentEngine) *TelegramBot {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("請設定 TELEGRAM_BOT_TOKEN（向 @BotFather 申請）")
	}

	tb := &TelegramBot{engine: eng}

	b, err := bot.New(token, bot.WithDefaultHandler(tb.defaultHandler))
	if err != nil {
		log.Fatalf("Telegram Bot 初始化失敗: %v", err)
	}
	tb.b = b

	return tb
}

// Start 啟動長輪詢，阻塞直到 ctx 被取消。
// 不像 Feishu 需要外掛 HTTP server，Telegram bot 自行管理 polling loop。
func (b *TelegramBot) Start(ctx context.Context) {
	log.Printf("[Telegram] 開始監聽訊息...")
	b.b.Start(ctx)
}

// defaultHandler 是所有未匹配特定指令的訊息入口。
func (b *TelegramBot) defaultHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}

	chatID := update.Message.Chat.ID
	text := update.Message.Text
	log.Printf("[Telegram] 收到會話 %d 訊息: %s", chatID, text)

	// 先判斷這句是不是審批回覆（approve/reject <TaskID>）。
	// 若是，喚醒對應的被掛起工具協程，而非開啟一輪新的 Agent 推理。
	if taskID, allowed, ok := ParseApprovalReply(text); ok {
		reporter := &TelegramReporter{b: b.b, chatID: chatID}
		if GlobalApprovalMgr.ResolveApproval(taskID, allowed, "人類於 Telegram 做出決定") {
			if allowed {
				reporter.sendMsg(fmt.Sprintf("✅ 已放行任務 %s。", taskID))
			} else {
				reporter.sendMsg(fmt.Sprintf("⛔ 已拒絕任務 %s。", taskID))
			}
		} else {
			reporter.sendMsg(fmt.Sprintf("⚠️ 找不到任務 %s（可能已逾時或處理完畢）。", taskID))
		}
		return
	}

	// 每條訊息獨立 goroutine，避免長任務阻塞下一條。
	go b.handleAgentRun(chatID, text)
}

// handleAgentRun 連接 Telegram 與底層引擎：建立會話、注入 reporter，並執行一輪 Agent。
func (b *TelegramBot) handleAgentRun(chatID int64, prompt string) {
	reporter := &TelegramReporter{
		b:      b.b,
		chatID: chatID,
	}

	// 以 chatID 為 key 維持每個會話獨立的對話歷史
	workDir, _ := os.Getwd()
	workDir += "/workspace"
	session := engine.GlobalSessionMgr.GetOrCreate(strconv.FormatInt(chatID, 10), workDir)

	// Run 只負責讀取 working memory，所以必須先把使用者輸入追加成一條 user 訊息
	session.Append(schema.Message{
		Role:    schema.RoleUser,
		Content: prompt,
	})

	// 把該會話的 reporter 注入 context，讓審批中間件與 SubAgent 能把訊息送回這個 chatID。
	ctx := WithReporter(context.Background(), reporter)
	err := b.engine.Run(ctx, session, reporter)
	if err != nil {
		reporter.sendMsg(fmt.Sprintf("❌ Agent 執行崩潰: %v", err))
	}
}

// TelegramReporter 將引擎輸出格式化後發送到指定 chatID。
type TelegramReporter struct {
	b      *bot.Bot
	chatID int64
}

// sendMsg 以純文字發送訊息，避免 Markdown 解析失敗導致整條訊息發不出去。
// Telegram 的 MarkdownV2 對 . _ * [ ( 等字元要求嚴格跳脫，錯一個就 400；
// 工具輸出常含路徑與特殊符號，純文字最穩。若需排版，可改用 ParseMode = "HTML"
// 並把內容包成 <b>...</b> / <code>...</code>，HTML 模式的跳脫需求較小。
func (r *TelegramReporter) sendMsg(text string) {
	_, err := r.b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: r.chatID,
		Text:   text,
	})
	if err != nil {
		log.Printf("[Telegram] 發送訊息失敗: %v", err)
	}
}

func (r *TelegramReporter) OnThinking(ctx context.Context) {
	r.sendMsg("🤔 模型正在慢思考 (Thinking)...")
}

func (r *TelegramReporter) OnToolCall(ctx context.Context, toolName string, args string) {
	r.sendMsg(fmt.Sprintf("🛠️ 正在執行工具: %s\n參數: %s", toolName, args))
}

func (r *TelegramReporter) OnToolResult(ctx context.Context, toolName string, result string, isError bool) {
	if isError {
		r.sendMsg(fmt.Sprintf("⚠️ 執行報錯 (%s):\n%s", toolName, result))
	} else {
		// 成功時僅彙報成功，不刷全量日誌，避免 Telegram 單條 4096 字元上限被打爆
		r.sendMsg(fmt.Sprintf("✅ 執行成功 (%s)", toolName))
	}
}

func (r *TelegramReporter) OnMessage(ctx context.Context, content string) {
	r.sendMsg(content)
}

// 編譯時類型檢查：確保 TelegramReporter 實作了 Reporter 介面
var _ engine.Reporter = (*TelegramReporter)(nil)
