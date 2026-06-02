package process

import "sync"

// ringBuffer is a thread-safe io.Writer that retains only the last maxSize bytes.
// Used to cap memory usage when long-running processes produce unbounded output.
type ringBuffer struct {
	mu      sync.Mutex
	buf     []byte
	maxSize int
	total   int64
}

func newRingBuffer(maxSize int) *ringBuffer {
	return &ringBuffer{maxSize: maxSize}
}

func (r *ringBuffer) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.total += int64(len(p))
	r.buf = append(r.buf, p...)
	if len(r.buf) > r.maxSize {
		r.buf = r.buf[len(r.buf)-r.maxSize:]
	}
	return len(p), nil
}

func (r *ringBuffer) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return string(r.buf)
}

func (r *ringBuffer) Total() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.total
}
