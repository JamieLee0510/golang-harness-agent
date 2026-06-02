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

// approvalTimeout is the maximum time to wait for human approval.
// The original Feishu version had no timeout, but in the Telegram scenario, if the user never replies the tool Goroutine would block forever and leak,
// so we add a safeguard here: timeout is treated as rejection, releasing the suspended engine goroutine.
const approvalTimeout = 5 * time.Minute

// ApprovalResult is the approval result bundle
type ApprovalResult struct {
	Allowed bool
	Reason  string
}

// ApprovalManager centrally manages tasks currently awaiting human approval.
type ApprovalManager struct {
	mu sync.RWMutex
	// pendingTasks is keyed by the approval ticket TaskID, with the value being the channel that receives the approval result.
	pendingTasks map[string]chan ApprovalResult
}

// GlobalApprovalMgr is a global singleton that lets the Registry middleware and the Telegram message handler share state.
var GlobalApprovalMgr = &ApprovalManager{
	pendingTasks: make(map[string]chan ApprovalResult),
}

// taskCounter is used to generate short, easy-to-type, collision-free TaskIDs (the user must enter them manually in the chat)
var taskCounter atomic.Uint64

// NextTaskID generates a short approval ticket number (T1, T2…).
// We don't use the model's ToolCallID as the ticket number because Telegram requires the user to "type" approve <id> by hand,
// and a ToolCallID (e.g. call_x7Vz3ab…) is too long to be convenient to enter.
func NextTaskID() string {
	return fmt.Sprintf("T%d", taskCounter.Add(1))
}

// The Reporter is passed down through the context: each chatID has its own *TelegramReporter,
// carried along the context to the middleware and SubAgent so messages can be sent back to the chat that initiated the call.
// The underlying context key lives in the neutral agentctx package so the tools package can read it too,
// avoiding the telegram → engine → tools import cycle.

// WithReporter injects the session's reporter into the context before entering the engine.
func WithReporter(ctx context.Context, r *TelegramReporter) context.Context {
	return agentctx.WithReporter(ctx, r)
}

// ReporterFromCtx extracts the reporter from the context; returns nil in CLI/terminal mode where it isn't present.
// The middleware closure (in main.go) relies on it to obtain the reporter for "the chat that initiated this tool call",
// with no shared mutable state throughout, so concurrent chats won't cross wires.
func ReporterFromCtx(ctx context.Context) *TelegramReporter {
	r, _ := agentctx.ReporterFromCtx(ctx).(*TelegramReporter)
	return r
}

// WaitForApproval sends a Telegram notification and blocks the current goroutine while waiting for the reply result
func (m *ApprovalManager) WaitForApproval(ctx context.Context, taskID, toolName, args string, reporter *TelegramReporter) (bool, string) {
	// 1. Create the channel used to block the current engine goroutine (capacity 1, to prevent deadlock on the ResolveApproval side)
	ch := make(chan ApprovalResult, 1)

	m.mu.Lock()
	m.pendingTasks[taskID] = ch
	m.mu.Unlock()

	// Ensure the pending record is cleaned up on any exit path to avoid memory leaks
	defer func() {
		m.mu.Lock()
		delete(m.pendingTasks, taskID)
		m.mu.Unlock()
	}()

	// 2. Send the approval request to Telegram via the Reporter
	noticeMsg := fmt.Sprintf(`⚠️ High-risk operation approval request
The Agent is attempting to perform the following action:
- Tool: %s
- Args: %s

Task ID: %s

👉 Please reply "approve %s" to allow, or "reject %s" to reject.`,
		toolName, args, taskID, taskID, taskID)

	if reporter != nil {
		reporter.sendMsg(noticeMsg)
	} else {
		// Fall back to terminal printing (compatible with local CLI mode)
		fmt.Printf("\n\033[31m[Approval required, TaskID: %s]\033[0m %s\n", taskID, noticeMsg)
	}

	log.Printf("[Approval] Approval request sent (TaskID: %s), goroutine suspended and waiting...\n", taskID)

	// 3. Block waiting for the Telegram message handler to wake us up; on timeout or session cancellation, auto-reject.
	select {
	case result := <-ch:
		return result.Allowed, result.Reason
	case <-time.After(approvalTimeout):
		log.Printf("[Approval] TaskID %s approval timed out (%s), automatically rejected\n", taskID, approvalTimeout)
		if reporter != nil {
			reporter.sendMsg(fmt.Sprintf("⏰ Task %s approval timed out and has been automatically rejected.", taskID))
		}
		return false, fmt.Sprintf("Approval timed out (no reply within %s); automatically rejected based on the safe default.", approvalTimeout)
	case <-ctx.Done():
		// When the session is cancelled (e.g. the user presses Ctrl+C), exit cleanly
		return false, "Session cancelled; operation aborted."
	}
}

// ResolveApproval is triggered by the Telegram message handler, sending a signal to the channel to unblock
func (m *ApprovalManager) ResolveApproval(taskID string, allowed bool, reason string) bool {
	m.mu.RLock()
	ch, exists := m.pendingTasks[taskID]
	m.mu.RUnlock()

	if exists {
		log.Printf("[Approval] Received Telegram approval result (TaskID: %s, Allowed: %v)\n", taskID, allowed)
		// The channel has capacity 1, so this won't block; use select+default as an extra safeguard against panic on duplicate replies
		select {
		case ch <- ApprovalResult{Allowed: allowed, Reason: reason}:
		default:
		}
		return true
	}

	log.Printf("[Approval] No matching TaskID found: %s, it may have timed out or already been processed\n", taskID)
	return false
}

// approvalReplyRe parses replies like "approve T12" / "reject T12" (case-insensitive, tolerant of leading/trailing whitespace)
var approvalReplyRe = regexp.MustCompile(`(?i)^\s*(approve|reject)\s+(\S+)\s*$`)

// ParseApprovalReply attempts to interpret a chat message as an approval command.
// Returns (taskID, allowed, ok); ok being false means this is not an approval command and should go through the normal Agent flow.
func ParseApprovalReply(text string) (taskID string, allowed bool, ok bool) {
	m := approvalReplyRe.FindStringSubmatch(text)
	if m == nil {
		return "", false, false
	}
	return m[2], strings.EqualFold(m[1], "approve"), true
}

// IsDangerousCommand uses a simple blacklist to decide whether a tool call needs human approval
func IsDangerousCommand(toolName, args string) bool {
	// For read-only tools, default to YOLO mode and let everything through
	if toolName != "bash" && toolName != "write_file" && toolName != "edit_file" {
		return false
	}

	// Match against high-risk patterns for bash
	if toolName == "bash" {
		dangerousPatterns := []string{
			`rm\s+-r`, // cascading deletion
			`sudo\s+`, // privilege escalation
			`drop\s+`, // database deletion
			`>.*\.go`, // malicious overwrite of source code
		}
		for _, p := range dangerousPatterns {
			if matched, _ := regexp.MatchString(p, args); matched {
				return true
			}
		}
		// Other bash commands are not intercepted for now (consistent with the original Feishu version)
		return false
	}

	// write_file / edit_file are always treated as high-risk (they modify disk) and require human confirmation
	return true
}
