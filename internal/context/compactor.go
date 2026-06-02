package context

import (
	"fmt"
	"log"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// Compactor monitors and compresses the context memory to prevent exceeding the LLM's token window.
type Compactor struct {
	MaxChars       int // Character-count watermark that triggers compaction (can reference the token window of the LLM in use)
	RetainLastMsgs int // Working Memory protected zone: the most recent N messages are not compressed
}

// NewCompactor creates a Compactor.
func NewCompactor(maxChars int, retainLastMsgs int) *Compactor {
	return &Compactor{
		MaxChars:       maxChars,
		RetainLastMsgs: retainLastMsgs,
	}
}

// Compact compresses msgs when the total length exceeds the watermark: early tool outputs are fully masked,
// recent oversized content is head-and-tail trimmed, while the System Prompt and all ToolCalls are fully preserved.
// Returns unchanged when not over the limit.
func (c *Compactor) Compact(msgs []schema.Message) []schema.Message {
	currentLength := c.estimateLength(msgs)

	if currentLength < c.MaxChars {
		return msgs
	}

	log.Printf("[Compactor] ⚠️ Memory alert: current context length (%d chars) exceeds threshold (%d), triggering compaction cleanup...\n", currentLength, c.MaxChars)

	var compacted []schema.Message
	msgCount := len(msgs)

	// Compute the start index of the protected Working Memory.
	protectStartIndex := max(0, msgCount-c.RetainLastMsgs)

	for i, msg := range msgs {

		// Keep the System Prompt as-is.
		if msg.Role == schema.RoleSystem {
			compacted = append(compacted, msg)
			continue
		}

		// Copy into a new message to avoid mutating the original reference and polluting underlying data in concurrent environments.
		newMsg := msg

		isInWorkingMemory := i >= protectStartIndex

		if msg.Role == schema.RoleUser && msg.ToolCallId != "" {
			if !isInWorkingMemory {
				// First line of defense (distant history): fully mask early tool outputs.
				if len(msg.Content) > 200 {
					newMsg.Content = fmt.Sprintf("...[Earlier tool output was force-cleared to save memory. Original length: %d bytes]...", len(msg.Content))
				}
			} else {
				// Second line of defense (recent memory): even in the protected zone, an oversized single message still needs head-and-tail trimming to prevent OOM.
				// Keep the first 500 and last 500 characters (the model usually only needs the opening error and the closing summary).
				const maxKeep = 1000
				if len(msg.Content) > maxKeep {
					head := msg.Content[:500]
					tail := msg.Content[len(msg.Content)-500:]
					newMsg.Content = fmt.Sprintf("%s\n\n...[Content too long; the middle %d bytes were truncated by the system]...\n\n %s", head, len(msg.Content)-maxKeep, tail)
				}
			}
		} else if msg.Role == schema.RoleAssistant && msg.Content != "" {
			// The model's verbose thinking trace: collapse the early ones.
			if !isInWorkingMemory && len(msg.Content) > 200 {
				newMsg.Content = "...[Earlier reasoning trace has been collapsed]..."
			}
		}

		// Never touch msg.ToolCalls; it is the evidence of the model's actions and the key to maintaining the logic chain.
		compacted = append(compacted, newMsg)
	}

	newLength := c.estimateLength(compacted)
	log.Printf("[Compactor] ✅ Compaction complete. Context length reduced from %d to %d chars.\n", currentLength, newLength)

	return compacted
}

// estimateLength roughly estimates the total character length of the context (including ToolCalls' names and arguments).
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
