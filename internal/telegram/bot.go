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

// TelegramBot wraps the Telegram bot's configuration and core business flow.
type TelegramBot struct {
	b      *bot.Bot
	engine *engine.AgentEngine
}

// NewTelegramBot reads the credential from the TELEGRAM_BOT_TOKEN environment variable and creates a TelegramBot.
func NewTelegramBot(eng *engine.AgentEngine) *TelegramBot {
	token := os.Getenv("TELEGRAM_BOT_TOKEN")
	if token == "" {
		log.Fatal("Please set TELEGRAM_BOT_TOKEN (request one from @BotFather)")
	}

	tb := &TelegramBot{engine: eng}

	b, err := bot.New(token, bot.WithDefaultHandler(tb.defaultHandler))
	if err != nil {
		log.Fatalf("Telegram Bot initialization failed: %v", err)
	}
	tb.b = b

	return tb
}

// Start launches long polling and blocks until ctx is cancelled.
// Unlike Feishu, which needs a separate HTTP server, the Telegram bot manages its own polling loop.
func (b *TelegramBot) Start(ctx context.Context) {
	log.Printf("[Telegram] Started listening for messages...")
	b.b.Start(ctx)
}

// defaultHandler is the entry point for all messages that don't match a specific command.
func (b *TelegramBot) defaultHandler(ctx context.Context, _ *bot.Bot, update *models.Update) {
	if update.Message == nil || update.Message.Text == "" {
		return
	}

	chatID := update.Message.Chat.ID
	text := update.Message.Text
	log.Printf("[Telegram] Received message from chat %d: %s", chatID, text)

	// First determine whether this message is an approval reply (approve/reject <TaskID>).
	// If so, wake up the corresponding suspended tool goroutine instead of starting a new round of Agent reasoning.
	if taskID, allowed, ok := ParseApprovalReply(text); ok {
		reporter := &TelegramReporter{b: b.b, chatID: chatID}
		if GlobalApprovalMgr.ResolveApproval(taskID, allowed, "Human made a decision via Telegram") {
			if allowed {
				reporter.sendMsg(fmt.Sprintf("✅ Approved task %s.", taskID))
			} else {
				reporter.sendMsg(fmt.Sprintf("⛔ Rejected task %s.", taskID))
			}
		} else {
			reporter.sendMsg(fmt.Sprintf("⚠️ Task %s not found (it may have timed out or already been processed).", taskID))
		}
		return
	}

	// Each message gets its own goroutine, so a long task doesn't block the next one.
	go b.handleAgentRun(chatID, text)
}

// handleAgentRun connects Telegram with the underlying engine: creates the session, injects the reporter, and runs one round of the Agent.
func (b *TelegramBot) handleAgentRun(chatID int64, prompt string) {
	reporter := &TelegramReporter{
		b:      b.b,
		chatID: chatID,
	}

	// Use chatID as the key to keep a separate conversation history per session
	workDir, _ := os.Getwd()
	workDir += "/workspace"
	session := engine.GlobalSessionMgr.GetOrCreate(strconv.FormatInt(chatID, 10), workDir)

	// Run only reads working memory, so we must first append the user input as a user message
	session.Append(schema.Message{
		Role:    schema.RoleUser,
		Content: prompt,
	})

	// Inject this session's reporter into the context so the approval middleware and SubAgent can send messages back to this chatID.
	ctx := WithReporter(context.Background(), reporter)
	err := b.engine.Run(ctx, session, reporter)
	if err != nil {
		reporter.sendMsg(fmt.Sprintf("❌ Agent execution crashed: %v", err))
	}
}

// TelegramReporter formats engine output and sends it to a specified chatID.
type TelegramReporter struct {
	b      *bot.Bot
	chatID int64
}

// sendMsg sends messages as plain text, avoiding Markdown parse failures that would prevent the whole message from being sent.
// Telegram's MarkdownV2 requires strict escaping of characters like . _ * [ ( ; one mistake yields a 400;
// tool output often contains paths and special symbols, so plain text is the most reliable. For formatting, switch to ParseMode = "HTML"
// and wrap content in <b>...</b> / <code>...</code>, since HTML mode has lighter escaping requirements.
func (r *TelegramReporter) sendMsg(text string) {
	_, err := r.b.SendMessage(context.Background(), &bot.SendMessageParams{
		ChatID: r.chatID,
		Text:   text,
	})
	if err != nil {
		log.Printf("[Telegram] Failed to send message: %v", err)
	}
}

func (r *TelegramReporter) OnThinking(ctx context.Context) {
	r.sendMsg("🤔 Model is thinking...")
}

func (r *TelegramReporter) OnToolCall(ctx context.Context, toolName string, args string) {
	r.sendMsg(fmt.Sprintf("🛠️ Executing tool: %s\nArgs: %s", toolName, args))
}

func (r *TelegramReporter) OnToolResult(ctx context.Context, toolName string, result string, isError bool) {
	if isError {
		r.sendMsg(fmt.Sprintf("⚠️ Execution error (%s):\n%s", toolName, result))
	} else {
		// On success, only report success rather than dumping the full log, to avoid blowing past Telegram's 4096-character per-message limit
		r.sendMsg(fmt.Sprintf("✅ Execution succeeded (%s)", toolName))
	}
}

func (r *TelegramReporter) OnMessage(ctx context.Context, content string) {
	r.sendMsg(content)
}

// Compile-time type check: ensures TelegramReporter implements the Reporter interface
var _ engine.Reporter = (*TelegramReporter)(nil)
