package core

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

var (
	logIdOnce     sync.Once
	logIdSequence atomic.Int64
)

func initLogIdSequence() {
	seed := time.Now().UTC().UnixNano()
	if seed <= 0 {
		seed = time.Now().UTC().UnixNano() + 1
	}
	logIdSequence.Store(seed)
}

// generateSequentialLogId returns a zero-padded, monotonically increasing identifier
// that can be safely stored as text while still preserving chronological ordering.
func generateSequentialLogId() string {
	logIdOnce.Do(initLogIdSequence)
	next := logIdSequence.Add(1)
	return fmt.Sprintf("%020d", next)
}
