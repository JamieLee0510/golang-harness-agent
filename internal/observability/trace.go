package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// saving Span Key from Context
type traceKey struct{}

type Span struct {
	Name       string         `json:"name"`
	StartTime  time.Time      `json:"start_time"`
	EndTime    time.Time      `json:"end_time"`
	DurationMs int64          `json:"duration_ms"`
	Attributes map[string]any `json:"attributes,omitempty"`
	Children   []*Span        `json:"children,omitempty"`

	mu sync.Mutex // protect Children parallel writing
}

func StartSpan(ctx context.Context, name string) (context.Context, *Span) {
	span := &Span{
		Name:       name,
		StartTime:  time.Now(),
		Attributes: make(map[string]any),
	}

	if parent, ok := ctx.Value(traceKey{}).(*Span); ok {
		parent.mu.Lock()
		parent.Children = append(parent.Children, span)
		parent.mu.Unlock()
	}

	newCtx := context.WithValue(ctx, traceKey{}, span)
	return newCtx, span
}

func (s *Span) EndSpan() {
	s.EndTime = time.Now()
	s.DurationMs = s.EndTime.Sub(s.StartTime).Milliseconds()
}

func (s *Span) AddAttribute(key string, value any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Attributes[key] = value
}

func ExportTraceToFile(rootSpan *Span, workDir string, sessionID string) error {
	traceDir := filepath.Join(workDir, ".agent", "traces")
	os.MkdirAll(traceDir, 0755)

	filename := filepath.Join(traceDir, fmt.Sprintf("trace_%s_%d.json", sessionID, time.Now().Unix()))

	// prettier JSON export for reading
	data, err := json.MarshalIndent(rootSpan, "", " ")
	if err != nil {
		return err
	}

	return os.WriteFile(filename, data, 0644)
}
