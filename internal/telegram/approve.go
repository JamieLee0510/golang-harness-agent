package telegram

import (
	"context"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/JamieLee0510/go-agent-harness/internal/agentctx"
)

// approvalTimeout 是人類審批的最長等待時間。
// 飛書原版沒有逾時，但 Telegram 場景下若使用者一直不回覆，工具 Goroutine 會永久阻塞造成洩漏，
// 因此這裡加上保護：逾時即視為拒絕，釋放被掛起的引擎協程。
const approvalTimeout = 5 * time.Minute

// ApprovalResult 審批結果包
type ApprovalResult struct {
	Allowed bool
	Reason  string
}

// ApprovalManager 統一管理當前正在等待人類審批的任務。
type ApprovalManager struct {
	mu sync.RWMutex
	// pendingTasks 以審批單號 TaskID 為鍵，值為接收審批結果的 channel。
	pendingTasks map[string]chan ApprovalResult
}

// GlobalApprovalMgr 是全域單例，供 Registry 中間件與 Telegram 訊息處理器共享狀態。
var GlobalApprovalMgr = &ApprovalManager{
	pendingTasks: make(map[string]chan ApprovalResult),
}

// taskCounter 用於產生短、好打字、且不會碰撞的 TaskID（使用者要在聊天室裡手動輸入）
var taskCounter atomic.Uint64

// NextTaskID 產生一個短審批單號（T1、T2…）。
// 不用模型的 ToolCallID 當單號，是因為 Telegram 要使用者「手打」approve <id>，
// 而 ToolCallID（如 call_x7Vz3ab…）太長不利輸入。
func NextTaskID() string {
	return fmt.Sprintf("T%d", taskCounter.Add(1))
}

// Reporter 透過 context 往下傳遞：每個 chatID 各有一個 *TelegramReporter，
// 隨 context 傳給中間件與 SubAgent，才能把訊息送回發起呼叫的那個聊天室。
// 底層 context key 放在中性套件 agentctx，讓 tools 套件也能讀到，
// 避免 telegram → engine → tools 的 import 循環。

// WithReporter 在進入引擎前，把該會話的 reporter 塞進 context。
func WithReporter(ctx context.Context, r *TelegramReporter) context.Context {
	return agentctx.WithReporter(ctx, r)
}

// ReporterFromCtx 從 context 取出 reporter；CLI/終端機模式下取不到會回傳 nil。
// 中間件閉包（在 main.go）靠它拿到「發起該次工具呼叫的那個聊天室」的 reporter，
// 全程無共享可變狀態，多聊天室並發也不會串台。
func ReporterFromCtx(ctx context.Context) *TelegramReporter {
	r, _ := agentctx.ReporterFromCtx(ctx).(*TelegramReporter)
	return r
}

// WaitForApproval 發送 Telegram 通知，並阻塞當前協程等待回覆結果
func (m *ApprovalManager) WaitForApproval(ctx context.Context, taskID, toolName, args string, reporter *TelegramReporter) (bool, string) {
	// 1. 建立用於阻塞當前引擎協程的 channel（容量 1，防止 ResolveApproval 端死鎖）
	ch := make(chan ApprovalResult, 1)

	m.mu.Lock()
	m.pendingTasks[taskID] = ch
	m.mu.Unlock()

	// 確保任何路徑離開時都清理掉 pending 記錄，避免記憶體洩漏
	defer func() {
		m.mu.Lock()
		delete(m.pendingTasks, taskID)
		m.mu.Unlock()
	}()

	// 2. 透過 Reporter 向 Telegram 發送審批請求
	noticeMsg := fmt.Sprintf(`⚠️ 高危操作審批請求
Agent 試圖執行以下動作：
- 工具：%s
- 參數：%s

任務 ID：%s

👉 請直接回覆「approve %s」放行，或「reject %s」拒絕。`,
		toolName, args, taskID, taskID, taskID)

	if reporter != nil {
		reporter.sendMsg(noticeMsg)
	} else {
		// 回退到終端機列印（相容本機 CLI 模式）
		fmt.Printf("\n\033[31m[需要審批 TaskID: %s]\033[0m %s\n", taskID, noticeMsg)
	}

	log.Printf("[Approval] 已發送審批請求 (TaskID: %s)，協程掛起等待...\n", taskID)

	// 3. 阻塞等待 Telegram 訊息處理器喚醒；逾時或會話取消則自動拒絕。
	select {
	case result := <-ch:
		return result.Allowed, result.Reason
	case <-time.After(approvalTimeout):
		log.Printf("[Approval] TaskID %s 審批逾時（%s），自動拒絕\n", taskID, approvalTimeout)
		if reporter != nil {
			reporter.sendMsg(fmt.Sprintf("⏰ 任務 %s 審批逾時，已自動拒絕。", taskID))
		}
		return false, fmt.Sprintf("審批逾時（超過 %s 未回覆），基於安全預設自動拒絕。", approvalTimeout)
	case <-ctx.Done():
		// 會話被取消（例如使用者 Ctrl+C）時，乾淨退出
		return false, "會話已取消，操作中止。"
	}
}

// ResolveApproval 由 Telegram 訊息處理器觸發，向 channel 發送信號解開阻塞
func (m *ApprovalManager) ResolveApproval(taskID string, allowed bool, reason string) bool {
	m.mu.RLock()
	ch, exists := m.pendingTasks[taskID]
	m.mu.RUnlock()

	if exists {
		log.Printf("[Approval] 收到 Telegram 審批結果 (TaskID: %s, Allowed: %v)\n", taskID, allowed)
		// channel 容量為 1，這裡不會阻塞；用 select+default 多一層保險防止重複回覆時 panic
		select {
		case ch <- ApprovalResult{Allowed: allowed, Reason: reason}:
		default:
		}
		return true
	}

	log.Printf("[Approval] 找不到對應的 TaskID: %s，可能已逾時或處理完畢\n", taskID)
	return false
}

// approvalReplyRe 解析「approve T12」/「reject T12」這類回覆（大小寫不敏感、容忍前後空白）
var approvalReplyRe = regexp.MustCompile(`(?i)^\s*(approve|reject)\s+(\S+)\s*$`)

// ParseApprovalReply 嘗試把一句聊天訊息解讀成審批指令。
// 回傳 (taskID, allowed, ok)；ok 為 false 表示這不是審批指令，應走正常 Agent 流程。
func ParseApprovalReply(text string) (taskID string, allowed bool, ok bool) {
	m := approvalReplyRe.FindStringSubmatch(text)
	if m == nil {
		return "", false, false
	}
	return m[2], strings.EqualFold(m[1], "approve"), true
}

// IsDangerousCommand 用簡單的黑名單判斷該工具呼叫是否需要人類審批
func IsDangerousCommand(toolName, args string) bool {
	// 對於純讀取的工具，預設 YOLO 模式，全部放行
	if toolName != "bash" && toolName != "write_file" && toolName != "edit_file" {
		return false
	}

	// 針對 bash 的高危模式比對
	if toolName == "bash" {
		dangerousPatterns := []string{
			`rm\s+-r`, // 級聯刪除
			`sudo\s+`, // 提權
			`drop\s+`, // 資料庫刪除
			`>.*\.go`, // 惡意覆寫原始碼
		}
		for _, p := range dangerousPatterns {
			if matched, _ := regexp.MatchString(p, args); matched {
				return true
			}
		}
		// 其餘 bash 指令暫不攔截（與飛書原版一致）
		return false
	}

	// write_file / edit_file 一律視為高危（會改動磁碟），需要人類確認
	return true
}
