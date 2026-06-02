package engine

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// ReminderInjector 於 runtime 監控上下文，在模型陷入執念（重複失敗）時動態注入強力打斷訊息。
type ReminderInjector struct {
	// consecutiveFailures 以工具呼叫指紋（hash(ToolName + Arguments)）為鍵，記錄連續失敗次數。
	consecutiveFailures map[string]int
}

// NewReminderInjector 建立 ReminderInjector。
func NewReminderInjector() *ReminderInjector {
	return &ReminderInjector{
		consecutiveFailures: make(map[string]int),
	}
}

// generateFingerprint 產生工具呼叫的唯一指紋，用於判斷模型是否在重複相同的動作。
func generateFingerprint(toolName string, args []byte) string {
	hasher := md5.New()
	hasher.Write([]byte(toolName))
	hasher.Write(args)
	return hex.EncodeToString(hasher.Sum(nil))
}

// CheckAndInject 分析本輪執行結果，決定是否在上下文尾端追加 Reminder。
// 同一指紋連續失敗超過 3 次時，回傳一條 RoleUser 訊息（最高近因效應權重）強制模型改變策略；
// 工具一旦成功則清空所有失敗計數；其餘情況回傳 nil。
func (r *ReminderInjector) CheckAndInject(lastToolCall schema.ToolCall, lastResult schema.ToolResult) *schema.Message {
	fingerpring := generateFingerprint(lastToolCall.Name, lastToolCall.Arguments)

	// 工具執行成功代表此路徑走通了，清空所有失敗計數器。
	if !lastResult.IsError {
		r.consecutiveFailures = make(map[string]int)
		return nil
	}

	// 執行失敗則累加該指紋的失敗次數。
	r.consecutiveFailures[fingerpring]++
	failCount := r.consecutiveFailures[fingerpring]

	// 死迴圈打斷機制：連續 3 次在同一處跌倒，強行打斷模型的局部執念。
	if failCount > 3 {
		log.Println("[Reminder] ⚠️ 觸發死迴圈干預！注入強力修正指令。")

		nudgeMsg := fmt.Sprintf(`[SYSTEM REMINDER 警告]
你似乎陷入了死迴圈。你剛剛連續 %d 次使用相同的參數呼叫了 '%s' 工具，並且都失敗了。
請立即停止這種無效的重試！你的注意力被當前的報錯過度吸引了。
你需要：
1. 停止猜測參數。跳出當前的局部思維。
2. 徹底改變你的策略。
3. 如果你確實無法透過系統工具解決當前問題，請直接結束任務並向使用者說明你需要什麼人工協助，而不是繼續盲目消耗 API 資源嘗試。
`, failCount, lastToolCall.Name)

		return &schema.Message{
			Role:    schema.RoleUser, // 必須是 RoleUser，確保在下一次 API 請求中擁有最高的近因效應權重
			Content: nudgeMsg,
		}
	}
	return nil
}
