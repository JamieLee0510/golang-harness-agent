package process

import (
	"os/exec"
	"sync"
	"time"
)

type Status string

const (
	StatusRunning Status = "running"
	StatusExited  Status = "exited"
	StatusKilled  Status = "killed"
	StatusFailed  Status = "failed"
)

type Snapshot struct {
	ID         string
	Command    string
	Status     Status
	PID        int
	ExitCode   *int
	StartedAt  time.Time
	FinishedAt time.Time
}

// Task represents a single background process managed by Manager.
// Stdout and Stderr are exported so callers can read accumulated output via
// their String()/Total() methods; the underlying type stays unexported.
type Task struct {
	ID        string
	Command   string
	Stdout    *ringBuffer
	Stderr    *ringBuffer
	StartedAt time.Time

	cmd           *exec.Cmd
	mu            sync.Mutex
	status        Status
	exitCode      *int
	finishedAt    time.Time
	killRequested bool
}

func (t *Task) Status() Status {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.status
}

func (t *Task) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	pid := 0
	if t.cmd != nil && t.cmd.Process != nil {
		pid = t.cmd.Process.Pid
	}
	return Snapshot{
		ID:         t.ID,
		Command:    t.Command,
		Status:     t.status,
		PID:        pid,
		ExitCode:   t.exitCode,
		StartedAt:  t.StartedAt,
		FinishedAt: t.finishedAt,
	}
}
