// safeexec/safeexec.go
package safeexec

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Config for a single execution
type Config struct {
	Timeout       time.Duration     // Execution timeout, default 15s
	MaxOutput     int64             // Max bytes to capture from stdout+stderr, default 8MB
	MaxMemory     uint64            // Max memory for child process (Linux only), default unlimited (env SCRIPT_MAX_MEMORY_MB/EXEC_MAX_MEMORY_MB)
	WorkingDir    string            // Working directory for the script
	ExtraEnv      map[string]string // Additional environment variables
	ConcurrentSem int               // Override global concurrency limit for this call only
}

var (
	// Global max concurrent executions
	// Controlled by any of these env vars (first found wins):
	// SCRIPT_MAX_CONCURRENT, MAX_CONCURRENT_SCRIPTS, EXEC_MAX_CONCURRENT
	maxConcurrent uint32 = 12

	// Global semaphore
	sem chan struct{}

	// defaultMaxMemoryBytes is loaded from env (MB). 0 = unlimited.
	defaultMaxMemoryBytes uint64 = 0
)

// init loads concurrency limit from environment and creates semaphore
func init() {
	loadConcurrentConfig()
	loadDefaultMemoryLimit()
	sem = make(chan struct{}, maxConcurrent)
}

// loadConcurrentConfig reads concurrency limit from environment
func loadConcurrentConfig() {
	defaultVal := 12
	envNames := []string{
		"SCRIPT_MAX_CONCURRENT",
		"MAX_CONCURRENT_SCRIPTS",
		"EXEC_MAX_CONCURRENT",
	}

	var val string
	for _, name := range envNames {
		if v := os.Getenv(name); v != "" {
			val = v
			break
		}
	}

	if val == "" {
		atomic.StoreUint32(&maxConcurrent, uint32(defaultVal))
		return
	}

	n, err := strconv.Atoi(val)
	if err != nil || n <= 0 {
		atomic.StoreUint32(&maxConcurrent, uint32(defaultVal))
		return
	}

	// Safety cap
	if n > 200 {
		n = 200
	}
	atomic.StoreUint32(&maxConcurrent, uint32(n))
}

func loadDefaultMemoryLimit() {
	envNames := []string{
		"SCRIPT_MAX_MEMORY_MB",
		"EXEC_MAX_MEMORY_MB",
	}

	for _, name := range envNames {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			mb, err := strconv.Atoi(v)
			if err != nil || mb < 0 {
				continue
			}
			defaultMaxMemoryBytes = uint64(mb) * 1024 * 1024
			return
		}
	}
}

// Monitoring helpers
func GetMaxConcurrent() int { return int(atomic.LoadUint32(&maxConcurrent)) }
func CurrentRunning() int   { return len(sem) }

// limitedWriter caps total written bytes
type limitedWriter struct {
	limit int64
	n     int64
	mu    sync.Mutex
}

func (w *limitedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.n >= w.limit {
		return 0, io.ErrShortWrite
	}
	remain := w.limit - w.n
	if int64(len(p)) > remain {
		p = p[:remain]
	}
	n := len(p)
	w.n += int64(n)
	return n, nil
}

// Result of an execution
type Result struct {
	Stdout   string
	Stderr   string
	Combined string // stdout + stderr, trimmed
	ExitCode int
	Timeout  bool
	Error    error
}

// Run executes a bash command safely
func Run(cmdText string, cfg *Config) *Result {
	if cfg == nil {
		cfg = &Config{}
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 15 * time.Second
	}
	if cfg.MaxOutput == 0 {
		cfg.MaxOutput = 8 * 1024 * 1024 // 8MB
	}
	if cfg.MaxMemory == 0 {
		cfg.MaxMemory = defaultMaxMemoryBytes // 0 = unlimited unless env override
	}

	// Determine concurrency limit for this call
	limit := atomic.LoadUint32(&maxConcurrent)
	if cfg.ConcurrentSem > 0 {
		limit = uint32(cfg.ConcurrentSem)
	}

	// Dynamically grow semaphore if limit increased
	if cap(sem) < int(limit) {
		newSem := make(chan struct{}, limit)
		// Migrate existing tokens
		for i := 0; i < cap(sem)-len(sem); i++ {
			newSem <- struct{}{}
		}
		sem = newSem
	}

	// Rate limiting: reject immediately if queue is full
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	default:
		return &Result{
			Error:    errors.New("too many concurrent script executions"),
			ExitCode: -1,
		}
	}

	// --- actual execution starts here ---
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", wrapWithMemoryLimit(cmdText, cfg.MaxMemory))

	// environment
	cmd.Env = os.Environ()
	for k, v := range cfg.ExtraEnv {
		cmd.Env = append(cmd.Env, fmt.Sprintf("%s=%s", k, v))
	}

	// working directory
	if cfg.WorkingDir != "" {
		cmd.Dir = cfg.WorkingDir
	}

	// fresh buffers every time → no output contamination ever
	var stdout, stderr bytes.Buffer
	limiter := &limitedWriter{limit: cfg.MaxOutput}
	cmd.Stdout = io.MultiWriter(&stdout, limiter)
	cmd.Stderr = io.MultiWriter(&stderr, limiter)

	// memory limit on Linux
	err := cmd.Run()

	// combine output with final safety truncation
	combined := strings.TrimSpace(stdout.String() + stderr.String())
	if int64(len(combined)) > cfg.MaxOutput/2 {
		combined = combined[:cfg.MaxOutput/2] + "\n... (output truncated)"
	}

	result := &Result{
		Stdout:   strings.TrimSpace(stdout.String()),
		Stderr:   strings.TrimSpace(stderr.String()),
		Combined: combined,
		Timeout:  ctx.Err() == context.DeadlineExceeded,
	}

	if err != nil {
		if result.Timeout {
			result.Error = errors.New("execution timeout")
		} else if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
			result.Error = fmt.Errorf("exited with code %d", result.ExitCode)
		} else {
			result.Error = err
		}
	} else {
		result.ExitCode = 0
	}

	return result
}

// QuickRun with sensible defaults (most common use case)
func QuickRun(cmdText string, env ...string) *Result {
	m := make(map[string]string)
	for i := 0; i < len(env)-1; i += 2 {
		if i+1 < len(env) {
			m[env[i]] = env[i+1]
		}
	}
	cfg := &Config{
		Timeout:   20 * time.Second,
		MaxOutput: 5 * 1024 * 1024,
		MaxMemory: defaultMaxMemoryBytes,
		ExtraEnv:  m,
	}
	return Run(cmdText, cfg)
}

// wrapWithMemoryLimit prefixes the command with a soft address space limit (KB) when requested.
func wrapWithMemoryLimit(cmd string, maxBytes uint64) string {
	if maxBytes == 0 {
		return cmd
	}

	limitKB := maxBytes / 1024
	if limitKB == 0 {
		limitKB = 1
	}

	return fmt.Sprintf("ulimit -v %d; %s", limitKB, cmd)
}
