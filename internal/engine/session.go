package engine

import (
	"sync"
	"time"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// Session 保存單一會話的完整對話歷史（使用者輸入、模型回覆、工具結果）。
type Session struct {
	ID        string
	WorkDir   string
	CreatedAt time.Time
	UpdatedAt time.Time

	history []schema.Message
	mu      sync.RWMutex // 防止併發讀寫 history 造成 data race
}

// NewSession 建立一個空的 Session。
func NewSession(id string, workDir string) *Session {
	return &Session{
		ID:        id,
		WorkDir:   workDir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		history:   make([]schema.Message, 0),
	}
}

// Append 以線程安全的方式向 Session 追加一條或多條訊息。
//
// 持久化預留點：工業級實作（如 Claude Code）會在此將 history 以 JSONL 形式
// 追加寫入 workDir/.claw/sessions/xxx.jsonl。
func (s *Session) Append(msgs ...schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, msgs...)
	s.UpdatedAt = time.Now()
}

// GetWorkingMemory 從歷史尾端截取最近的 limit 條訊息，形成 Agent 的短期工作記憶；
// limit <= 0 或歷史不足時回傳全量（皆為深拷貝，避免外部修改）。
//
// 大模型 API 要求歷史訊息連續：若截斷後的首條是「孤兒」工具回應
// （RoleUser 且含 ToolCallId，但對應的 ToolCall 已被截掉）會回 400，
// 因此這裡會將開頭的孤兒工具回應逐一捨棄，順延到下一條正常訊息。
func (s *Session) GetWorkingMemory(limit int) []schema.Message {
	s.mu.RLock()
	defer s.mu.RUnlock()

	total := len(s.history)
	if total <= limit || limit <= 0 {
		res := make([]schema.Message, total)
		copy(res, s.history)
		return res
	}

	res := make([]schema.Message, limit)
	copy(res, s.history[total-limit:])

	for len(res) > 0 {
		if res[0].Role == schema.RoleUser && res[0].ToolCallId != "" {
			res = res[1:]
		} else {
			break
		}
	}
	return res
}

// SessionManager 管理多會話，用於多使用者／多終端隔離。
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// GlobalSessionMgr 是全域共享的會話管理器。
var GlobalSessionMgr = &SessionManager{
	sessions: make(map[string]*Session),
}

// GetOrCreate 依 id 取得既有 Session，不存在則建立一個新的。
func (sm *SessionManager) GetOrCreate(id string, workDir string) *Session {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sess, exists := sm.sessions[id]; exists {
		return sess
	}

	sess := NewSession(id, workDir)
	sm.sessions[id] = sess

	return sess
}
