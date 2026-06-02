package process

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

const (
	defaultBufferSize = 8 * 1024
	defaultMaxTasks   = 5
	killGracePeriod   = 5 * time.Second
)

// Manager owns the lifecycle of background bash processes.
// It is safe for concurrent use.
type Manager struct {
	mu       sync.RWMutex
	tasks    map[string]*Task
	maxTasks int
}

func NewManager(maxTasks int) *Manager {
	if maxTasks <= 0 {
		maxTasks = defaultMaxTasks
	}
	return &Manager{
		tasks:    make(map[string]*Task),
		maxTasks: maxTasks,
	}
}

func (m *Manager) Spawn(command, workDir string) (*Task, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	running := 0
	for _, t := range m.tasks {
		if t.Status() == StatusRunning {
			running++
		}
	}
	if running >= m.maxTasks {
		return nil, fmt.Errorf("too many background tasks running (max %d); kill one with task_kill before spawning more", m.maxTasks)
	}

	task := &Task{
		ID:        newID(),
		Command:   command,
		Stdout:    newRingBuffer(defaultBufferSize),
		Stderr:    newRingBuffer(defaultBufferSize),
		StartedAt: time.Now(),
		status:    StatusRunning,
	}

	cmd := exec.Command("bash", "-c", command)
	cmd.Dir = workDir
	cmd.Stdout = task.Stdout
	cmd.Stderr = task.Stderr
	// Setpgid: put the child in its own process group so we can later
	// signal the entire tree (npm/python often fork sub-processes).
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	task.cmd = cmd

	if err := cmd.Start(); err != nil {
		task.status = StatusFailed
		task.finishedAt = time.Now()
		m.tasks[task.ID] = task
		return task, fmt.Errorf("failed to start command: %w", err)
	}

	m.tasks[task.ID] = task
	go m.reap(task)

	return task, nil
}

// reap blocks on cmd.Wait until the process exits, then records the result.
// cmd.Wait also flushes the stdout/stderr copy goroutines, so the ring buffer
// is fully populated by the time we mark the task as exited.
func (m *Manager) reap(task *Task) {
	_ = task.cmd.Wait()

	task.mu.Lock()
	defer task.mu.Unlock()

	task.finishedAt = time.Now()
	if task.cmd.ProcessState != nil {
		code := task.cmd.ProcessState.ExitCode()
		task.exitCode = &code
	}
	if task.killRequested {
		task.status = StatusKilled
	} else {
		task.status = StatusExited
	}
}

func (m *Manager) Get(id string) (*Task, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	return t, ok
}

func (m *Manager) List() []*Task {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*Task, 0, len(m.tasks))
	for _, t := range m.tasks {
		out = append(out, t)
	}
	return out
}

func (m *Manager) Kill(id string) error {
	m.mu.RLock()
	task, ok := m.tasks[id]
	m.mu.RUnlock()
	if !ok {
		return fmt.Errorf("task %s not found", id)
	}

	task.mu.Lock()
	if task.status != StatusRunning {
		status := task.status
		task.mu.Unlock()
		return fmt.Errorf("task %s is already %s", id, status)
	}
	task.killRequested = true
	pid := task.cmd.Process.Pid
	task.mu.Unlock()

	pgid, err := syscall.Getpgid(pid)
	if err != nil {
		pgid = pid
	}

	// negative pgid: signal the whole process group
	_ = syscall.Kill(-pgid, syscall.SIGTERM)

	go func() {
		time.Sleep(killGracePeriod)
		if task.Status() == StatusRunning {
			_ = syscall.Kill(-pgid, syscall.SIGKILL)
		}
	}()

	return nil
}

func (m *Manager) KillAll() {
	m.mu.RLock()
	ids := make([]string, 0, len(m.tasks))
	for id := range m.tasks {
		ids = append(ids, id)
	}
	m.mu.RUnlock()

	for _, id := range ids {
		_ = m.Kill(id)
	}
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return "bg-" + hex.EncodeToString(b)
}
