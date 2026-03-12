package logger

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// RotatingFileWriter implements daily log rotation with retention
type RotatingFileWriter struct {
	mu             sync.Mutex
	basePath       string
	maxDays        int
	currentDate    string
	currentFile    *os.File
	cleanupRunning atomic.Bool
}

// NewRotatingFileWriter creates a new rotating file writer
func NewRotatingFileWriter(basePath string, maxDays int) *RotatingFileWriter {
	return &RotatingFileWriter{
		basePath: basePath,
		maxDays:  maxDays,
	}
}

// Write implements io.Writer
func (w *RotatingFileWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	today := time.Now().Format("2006-01-02")

	// Rotate if date changed or file not opened
	if w.currentDate != today || w.currentFile == nil {
		if w.currentFile != nil {
			w.currentFile.Close()
		}

		// Ensure directory exists
		if err := os.MkdirAll(w.basePath, 0755); err != nil {
			return 0, err
		}

		// Open new file: YYYY-MM-DD.log
		filename := filepath.Join(w.basePath, today+".log")
		f, err := os.OpenFile(filename, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return 0, err
		}

		w.currentFile = f
		w.currentDate = today

		// Clean up old logs asynchronously (only if not already running)
		go w.cleanupOldLogs()
	}

	return w.currentFile.Write(p)
}

// cleanupOldLogs removes logs older than maxDays
func (w *RotatingFileWriter) cleanupOldLogs() {
	// Only run one cleanup at a time
	if !w.cleanupRunning.CompareAndSwap(false, true) {
		return
	}
	defer w.cleanupRunning.Store(false)

	entries, err := os.ReadDir(w.basePath)
	if err != nil {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -w.maxDays)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		name := entry.Name()
		if len(name) < 10 {
			continue
		}

		// Parse date from filename (YYYY-MM-DD.log)
		dateStr := name[:10]
		fileDate, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			continue
		}

		if fileDate.Before(cutoff) {
			os.Remove(filepath.Join(w.basePath, name))
		}
	}
}

// Sync flushes the current file
func (w *RotatingFileWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile != nil {
		return w.currentFile.Sync()
	}
	return nil
}

// Close closes the current file
func (w *RotatingFileWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.currentFile != nil {
		err := w.currentFile.Close()
		w.currentFile = nil
		return err
	}
	return nil
}
