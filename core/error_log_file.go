package core

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bosbase/bosbase-enterprise/tools/logger"
)

// Error log files: the logs with level >= BOSBASE_ERROR_LOG_LEVEL (default ERROR) are also
// appended as JSON lines to {dataDir}/logs/errors-YYYY-MM-DD.jsonl so that they are available
// for troubleshooting even when the database logs are disabled or the database is unreachable.
//
// Environment variables:
//   - BOSBASE_ERROR_LOG_FILE=false disables the files
//   - BOSBASE_ERROR_LOG_LEVEL=DEBUG|INFO|WARN|ERROR (default ERROR)
//   - BOSBASE_ERROR_LOG_MAX_DAYS (default 30)
//   - BOSBASE_ERROR_LOG_MAX_MB per day (default 100)

const (
	// ErrorLogsDirName is the name of the data dir subdirectory with the error log files.
	ErrorLogsDirName = "logs"

	errorLogFilePrefix = "errors-"
	errorLogFileExt    = ".jsonl"
)

type errorLogFileWriter struct {
	mu          sync.Mutex
	dir         string
	level       slog.Level
	maxDays     int
	maxBytes    int64
	enabled     bool
	currentDay  string
	file        *os.File
	size        int64
	lastCleanup time.Time
}

func newErrorLogFileWriter(dataDir string) *errorLogFileWriter {
	w := &errorLogFileWriter{
		dir:      filepath.Join(dataDir, ErrorLogsDirName),
		level:    slog.LevelError,
		maxDays:  30,
		maxBytes: 100 << 20,
		enabled:  true,
	}

	if v := strings.ToLower(strings.TrimSpace(os.Getenv("BOSBASE_ERROR_LOG_FILE"))); v == "false" || v == "0" || v == "no" {
		w.enabled = false
	}
	switch strings.ToUpper(strings.TrimSpace(os.Getenv("BOSBASE_ERROR_LOG_LEVEL"))) {
	case "DEBUG":
		w.level = slog.LevelDebug
	case "INFO":
		w.level = slog.LevelInfo
	case "WARN", "WARNING":
		w.level = slog.LevelWarn
	}
	if v, err := strconv.Atoi(os.Getenv("BOSBASE_ERROR_LOG_MAX_DAYS")); err == nil && v > 0 {
		w.maxDays = v
	}
	if v, err := strconv.Atoi(os.Getenv("BOSBASE_ERROR_LOG_MAX_MB")); err == nil && v > 0 {
		w.maxBytes = int64(v) << 20
	}

	return w
}

// ErrorLogFilePath returns the path of the error log file of the provided day.
func ErrorLogFilePath(dataDir string, day time.Time) string {
	return filepath.Join(dataDir, ErrorLogsDirName, errorLogFilePrefix+day.UTC().Format("2006-01-02")+errorLogFileExt)
}

func (w *errorLogFileWriter) write(l *logger.Log) {
	if w == nil || !w.enabled || l.Level < w.level {
		return
	}

	entry := map[string]any{
		"time":    l.Time.UTC().Format(time.RFC3339Nano),
		"level":   l.Level.String(),
		"message": l.Message,
		"data":    l.Data,
	}
	line, err := json.Marshal(entry)
	if err != nil {
		return
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()

	day := l.Time.UTC().Format("2006-01-02")
	if w.file == nil || day != w.currentDay {
		if w.file != nil {
			w.file.Close()
			w.file = nil
		}
		if err := os.MkdirAll(w.dir, os.ModePerm); err != nil {
			return
		}
		f, err := os.OpenFile(filepath.Join(w.dir, errorLogFilePrefix+day+errorLogFileExt), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o640)
		if err != nil {
			return
		}
		info, _ := f.Stat()
		w.file, w.currentDay, w.size = f, day, 0
		if info != nil {
			w.size = info.Size()
		}
	}

	if w.size+int64(len(line)) > w.maxBytes {
		return
	}
	if n, err := w.file.Write(line); err == nil {
		w.size += int64(n)
	}

	if time.Since(w.lastCleanup) > time.Hour {
		w.lastCleanup = time.Now()
		w.cleanup()
	}
}

func (w *errorLogFileWriter) cleanup() {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -w.maxDays).Format("2006-01-02")
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, errorLogFilePrefix) || !strings.HasSuffix(name, errorLogFileExt) {
			continue
		}
		day := strings.TrimSuffix(strings.TrimPrefix(name, errorLogFilePrefix), errorLogFileExt)
		if day < cutoff {
			os.Remove(filepath.Join(w.dir, name))
		}
	}
}

func (w *errorLogFileWriter) close() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		w.file.Close()
		w.file = nil
	}
}
