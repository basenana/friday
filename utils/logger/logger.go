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

// InitWithFile initializes the logger writing to the specified file
func InitWithFile(logPath string) {
	atom = zap.NewAtomicLevel()
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.RFC3339TimeEncoder

	// Ensure log directory exists
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		// Fall back to stdout if directory creation fails
		Init()
		return
	}

	// Open log file for appending
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		// Fall back to stdout if file open fails
		Init()
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
		logFile.Close()
	}
}

// CoreLogger returns a logger that implements core/logger.Logger interface
func CoreLogger() *coreLoggerAdapter {
	if coreAdapter == nil {
		coreAdapter = &coreLoggerAdapter{sugar: sugar}
	}
	return coreAdapter
}
