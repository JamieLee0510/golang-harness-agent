package engine

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// ReminderInjector monitors the context at runtime and dynamically injects a strong interrupting message when the model falls into a fixation (repeated failures).
type ReminderInjector struct {
	// consecutiveFailures records the consecutive failure count, keyed by the tool call fingerprint (hash(ToolName + Arguments)).
	consecutiveFailures map[string]int
}

// NewReminderInjector builds a ReminderInjector.
func NewReminderInjector() *ReminderInjector {
	return &ReminderInjector{
		consecutiveFailures: make(map[string]int),
	}
}

// generateFingerprint produces a unique fingerprint for a tool call, used to determine whether the model is repeating the same action.
func generateFingerprint(toolName string, args []byte) string {
	hasher := md5.New()
	hasher.Write([]byte(toolName))
	hasher.Write(args)
	return hex.EncodeToString(hasher.Sum(nil))
}

// CheckAndInject analyzes this turn's execution result and decides whether to append a Reminder at the tail of the context.
// When the same fingerprint fails more than 3 times consecutively, it returns a RoleUser message (highest recency-effect weight) to force the model to change strategy;
// once a tool succeeds it clears all failure counts; in all other cases it returns nil.
func (r *ReminderInjector) CheckAndInject(lastToolCall schema.ToolCall, lastResult schema.ToolResult) *schema.Message {
	fingerpring := generateFingerprint(lastToolCall.Name, lastToolCall.Arguments)

	// A successful tool execution means this path worked, so clear all failure counters.
	if !lastResult.IsError {
		r.consecutiveFailures = make(map[string]int)
		return nil
	}

	// On execution failure, increment the failure count for this fingerprint.
	r.consecutiveFailures[fingerpring]++
	failCount := r.consecutiveFailures[fingerpring]

	// Dead-loop interruption mechanism: tripping on the same spot 3 times in a row forcibly breaks the model's local fixation.
	if failCount > 3 {
		log.Println("[Reminder] ⚠️ dead-loop intervention triggered! Injecting strong corrective instructions.")

		nudgeMsg := fmt.Sprintf(`[SYSTEM REMINDER 警告]
你似乎陷入了死迴圈。你剛剛連續 %d 次使用相同的參數呼叫了 '%s' 工具，並且都失敗了。
請立即停止這種無效的重試！你的注意力被當前的報錯過度吸引了。
你需要：
1. 停止猜測參數。跳出當前的局部思維。
2. 徹底改變你的策略。
3. 如果你確實無法透過系統工具解決當前問題，請直接結束任務並向使用者說明你需要什麼人工協助，而不是繼續盲目消耗 API 資源嘗試。
`, failCount, lastToolCall.Name)

		return &schema.Message{
			Role:    schema.RoleUser, // Must be RoleUser to ensure the highest recency-effect weight in the next API request
			Content: nudgeMsg,
		}
	}
	return nil
}
