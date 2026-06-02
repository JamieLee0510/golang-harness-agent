package context

import (
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// Compactor 監控並壓縮上下文記憶，防止超出大模型的 token 視窗。
type Compactor struct {
	MaxChars       int // 觸發壓縮的字元數水位線（可參考所用大模型的 token window）
	RetainLastMsgs int // Working Memory 保護區：最近 N 條訊息不壓縮
}

// NewCompactor 建立 Compactor。
func NewCompactor(maxChars int, retainLastMsgs int) *Compactor {
	return &Compactor{
		MaxChars:       maxChars,
		RetainLastMsgs: retainLastMsgs,
	}
}

// Compact 在總長度超過水位線時壓縮 msgs：早期工具輸出整段遮罩、近期超大內容掐頭去尾，
// 並完整保留 System Prompt 與所有 ToolCalls。未超標時原樣返回。
func (c *Compactor) Compact(msgs []schema.Message) []schema.Message {
	currentLength := c.estimateLength(msgs)

	if currentLength < c.MaxChars {
		return msgs
	}

	log.Printf("[Compactor] ⚠️ 記憶體告警：當前上下文長度 (%d 字元) 超過閾值 (%d)，觸發壓縮清理...\n", currentLength, c.MaxChars)

	var compacted []schema.Message
	msgCount := len(msgs)

	// 計算受保護的 Working Memory 起始索引。
	protectStartIndex := max(0, msgCount-c.RetainLastMsgs)

	for i, msg := range msgs {

		// System Prompt 直接保留。
		if msg.Role == schema.RoleSystem {
			compacted = append(compacted, msg)
			continue
		}

		// 拷貝一份新訊息，避免併發環境下直接修改原引用污染底層資料。
		newMsg := msg

		isInWorkingMemory := i >= protectStartIndex

		if msg.Role == schema.RoleUser && msg.ToolCallId != "" {
			if !isInWorkingMemory {
				// 第一道防線（遠期歷史）：早期工具輸出整段遮罩。
				if len(msg.Content) > 200 {
					newMsg.Content = fmt.Sprintf("...[為了節省記憶體，早期的工具輸出已被系統強制清理。原始長度: %d 字節]...", len(msg.Content))
				}
			} else {
				// 第二道防線（近期記憶）：即使在保護區，單條內容過大仍需掐頭去尾防 OOM。
				// 保留前 500 與後 500 字元（模型通常只需開頭的報錯與結尾的總結）。
				const maxKeep = 1000
				if len(msg.Content) > maxKeep {
					head := msg.Content[:500]
					tail := msg.Content[len(msg.Content)-500:]
					newMsg.Content = fmt.Sprintf("%s\n\n...[內容過長，中間 %d 字節已被系統截斷]...\n\n %s", head, len(msg.Content)-maxKeep, tail)
				}
			}
		} else if msg.Role == schema.RoleAssistant && msg.Content != "" {
			// 模型冗長的思考軌跡（Thinking Trace）：早期的折疊。
			if !isInWorkingMemory && len(msg.Content) > 200 {
				newMsg.Content = "...[早期的推理思考過程已折疊]..."
			}
		}

		// 絕不更動 msg.ToolCalls，那是模型行動的證據，是維繫邏輯鏈的關鍵。
		compacted = append(compacted, newMsg)
	}

	newLength := c.estimateLength(compacted)
	log.Printf("[Compactor] ✅ 壓縮完成。上下文長度從 %d 降至 %d 字元。\n", currentLength, newLength)

	return compacted
}

// estimateLength 粗略估算上下文的總字元長度（含 ToolCalls 的名稱與參數）。
func (c *Compactor) estimateLength(msgs []schema.Message) int {
	length := 0
	for _, msg := range msgs {
		length += len(msg.Content)
		for _, tc := range msg.ToolCalls {
			length += len(tc.Name) + len(tc.Arguments)
		}
	}

	return length
}
