package safeexec

import (
	"strings"
	"testing"
	"time"
)

func TestRunCapturesOutput(t *testing.T) {
	res := Run("echo run-ok", &Config{
		Timeout:   2 * time.Second,
		MaxOutput: 1024,
	})

	if res.Error != nil {
		t.Fatalf("unexpected error: %v", res.Error)
	}
	if res.Timeout {
		t.Fatalf("command timed out unexpectedly")
	}
	if strings.TrimSpace(res.Stdout) != "run-ok" {
		t.Fatalf("unexpected stdout: %q", res.Stdout)
	}
	if strings.TrimSpace(res.Combined) != "run-ok" {
		t.Fatalf("unexpected combined output: %q", res.Combined)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
}

func TestRunTimeout(t *testing.T) {
	res := Run("sleep 1", &Config{
		Timeout:   50 * time.Millisecond,
		MaxOutput: 1024,
	})

	if !res.Timeout {
		t.Fatalf("expected timeout, got none (err=%v)", res.Error)
	}
	if res.Error == nil || res.Error.Error() != "execution timeout" {
		t.Fatalf("expected execution timeout error, got %v", res.Error)
	}
}

func TestRunTruncatesOutput(t *testing.T) {
	res := Run("printf 'abcdefghijklmnopqrstuvwxyz'", &Config{
		Timeout:   2 * time.Second,
		MaxOutput: 20,
	})

	if !strings.Contains(res.Combined, "... (output truncated)") {
		t.Fatalf("expected combined output to be truncated, got %q", res.Combined)
	}
}

func TestWrapWithMemoryLimit(t *testing.T) {
	limited := wrapWithMemoryLimit("echo hi", 512*1024)
	if !strings.HasPrefix(limited, "ulimit -v 512;") || !strings.HasSuffix(limited, "echo hi") {
		t.Fatalf("unexpected wrapped command: %q", limited)
	}

	unchanged := wrapWithMemoryLimit("echo hi", 0)
	if unchanged != "echo hi" {
		t.Fatalf("expected command to remain unchanged when limit is zero, got %q", unchanged)
	}
}
