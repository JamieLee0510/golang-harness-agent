package context

import (
	"context"
	"sync"
	"time"

	"github.com/JamieLee0510/go-agent-harness/internal/schema"
)

// sessionKey is the private context key for the Session. A private type prevents collisions with keys from other packages.
type sessionKey struct{}

// WithSession binds a Session to the context so layers below the engine that can't receive it as a parameter
// (e.g. the observability CostTracker, which wraps the provider's Generate) can still reach the current request's session.
// The engine injects this once at the top of Run; callers normally don't need to call it directly.
func WithSession(ctx context.Context, session *Session) context.Context {
	return context.WithValue(ctx, sessionKey{}, session)
}

// SessionFromCtx retrieves the Session bound to the context, returning nil when none is present
// (e.g. CLI paths that never bound one, or a non-*Session value).
func SessionFromCtx(ctx context.Context) *Session {
	sess, _ := ctx.Value(sessionKey{}).(*Session)
	return sess
}

// Session holds the complete conversation history of a single session (user input, model replies, tool results).
//
// It lives in the context package alongside the other context-management components (compactor, composer, recovery, skill),
// so that everything governing what the model "sees" and "remembers" is managed in one place. The engine and the
// observability layer both import this package and pass the *Session explicitly.
type Session struct {
	ID        string
	WorkDir   string
	CreatedAt time.Time
	UpdatedAt time.Time

	TotalPromptTokens     int
	TotalCompletionTokens int
	TotalCostUSD          float64

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
// the history in JSONL form to workDir/.agent/sessions/xxx.jsonl.
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

func (s *Session) RecordUsage(prompt int, completion int, cost float64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.TotalPromptTokens += prompt
	s.TotalCompletionTokens += completion
	s.TotalCostUSD = cost
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
