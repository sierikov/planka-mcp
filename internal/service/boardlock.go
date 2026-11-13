package service

import (
	"context"
	"sync"
)

// boardLocks serializes the writes on each board. The MCP SDK runs tool calls
// concurrently, and a write reads the board before it writes what it worked
// out from it: a label or checklist to create, the next position, the
// stopwatch total. Two such writes on one board at once could create a label
// twice, or a second "Tasks" checklist. The zero value is ready to use.
type boardLocks struct {
	mu    sync.Mutex
	locks map[string]chan struct{} // board ID -> a one-slot semaphore
}

// lock waits until no other write holds the board, or until ctx ends. The
// caller calls unlock when its last write is done. Locks are never removed:
// there is one per board written to, and boards are few.
func (b *boardLocks) lock(ctx context.Context, boardID string) (unlock func(), err error) {
	b.mu.Lock()
	sem, ok := b.locks[boardID]
	if !ok {
		if b.locks == nil {
			b.locks = map[string]chan struct{}{}
		}
		sem = make(chan struct{}, 1)
		b.locks[boardID] = sem
	}
	b.mu.Unlock()
	select {
	case sem <- struct{}{}:
		return func() { <-sem }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
