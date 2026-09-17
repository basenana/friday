package logger

import (
	"os"
	"path/filepath"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	root        *zap.Logger
	atom        zap.AtomicLevel
	sugar       *zap.SugaredLogger
	logFile     *os.File
	coreAdapter *coreLoggerAdapter
)

// Init initializes the logger writing to stdout only
func Init() {
	logFile = nil
	coreAdapter = nil
	atom = zap.NewAtomicLevel()
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.RFC3339TimeEncoder

	root = zap.New(zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.Lock(os.Stdout),
		atom,
	), zap.AddCaller())
	sugar = root.Sugar()
}

func initDiscard() {
	logFile = nil
	coreAdapter = nil
	atom = zap.NewAtomicLevel()
	root = zap.NewNop()
	sugar = root.Sugar()
}

// InitWithFile initializes the logger writing to the specified file
func InitWithFile(logPath string) {
	logFile = nil
	coreAdapter = nil
	atom = zap.NewAtomicLevel()
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.RFC3339TimeEncoder

	// Ensure log directory exists
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		// Never fall back to a terminal writer: this logger is also used by the
		// full-screen TUI, where an unexpected write would corrupt the display.
		initDiscard()
		return
	}

	// Open log file for appending
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		initDiscard()
		return
	}
	logFile = f

	// Create core for file only
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(logFile),
		atom,
	)

	root = zap.New(fileCore, zap.AddCaller())
	sugar = root.Sugar()
}

// New creates a named sugared logger
func New(name string) *zap.SugaredLogger {
	return sugar.Named(name)
}

// Sync flushes the logger
func Sync() {
	_ = root.Sync()
}

// Close closes the log file
func Close() {
	Sync()
	if logFile != nil {
		_ = logFile.Close()
		logFile = nil
	}
}

// IsFileBacked reports whether the active logger writes to a file.
func IsFileBacked() bool {
	return logFile != nil
}

// CoreLogger returns a logger that implements core/logger.Logger interface
func CoreLogger() *coreLoggerAdapter {
	if coreAdapter == nil {
		coreAdapter = &coreLoggerAdapter{sugar: sugar}
	}
	return coreAdapter
}
