package engine

import (
	"sync"
	"time"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// Session holds the complete conversation history of a single session (user input, model replies, tool results).
type Session struct {
	ID        string
	WorkDir   string
	CreatedAt time.Time
	UpdatedAt time.Time

	history []schema.Message
	mu      sync.RWMutex // prevents data races from concurrent reads/writes of history
}

// NewSession builds an empty Session.
func NewSession(id string, workDir string) *Session {
	return &Session{
		ID:        id,
		WorkDir:   workDir,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		history:   make([]schema.Message, 0),
	}
}

// Append appends one or more messages to the Session in a thread-safe manner.
//
// Persistence placeholder: an industrial-grade implementation (like Claude Code) would here append
// the history in JSONL form to workDir/.claw/sessions/xxx.jsonl.
func (s *Session) Append(msgs ...schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.history = append(s.history, msgs...)
	s.UpdatedAt = time.Now()
}

// GetWorkingMemory takes the most recent limit messages from the tail of the history to form the Agent's short-term working memory;
// when limit <= 0 or the history is insufficient it returns the full set (all deep copies, to prevent external modification).
//
// The LLM API requires history messages to be contiguous: if the first message after truncation is an "orphan" tool response
// (RoleUser with a ToolCallId, but whose corresponding ToolCall has been truncated away) the API returns 400,
// so this discards leading orphan tool responses one by one, advancing to the next normal message.
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

// SessionManager manages multiple sessions, used for multi-user / multi-terminal isolation.
type SessionManager struct {
	sessions map[string]*Session
	mu       sync.RWMutex
}

// GlobalSessionMgr is the globally shared session manager.
var GlobalSessionMgr = &SessionManager{
	sessions: make(map[string]*Session),
}

// GetOrCreate returns the existing Session for the given id, or creates a new one if it does not exist.
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
