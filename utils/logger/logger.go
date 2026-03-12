package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var (
	root           *zap.Logger
	atom           zap.AtomicLevel
	sugar          *zap.SugaredLogger
	rotatingWriter *RotatingFileWriter
	coreAdapter    *coreLoggerAdapter
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

// InitWithFile initializes the logger writing to file only
func InitWithFile(logPath string, maxDays int) {
	atom = zap.NewAtomicLevel()
	encoderCfg := zap.NewProductionEncoderConfig()
	encoderCfg.TimeKey = "timestamp"
	encoderCfg.EncodeTime = zapcore.RFC3339TimeEncoder

	// Ensure log directory exists
	if err := os.MkdirAll(logPath, 0755); err != nil {
		// Fall back to stdout if directory creation fails
		Init()
		return
	}

	// Create rotating file writer
	rotatingWriter = NewRotatingFileWriter(logPath, maxDays)

	// Create core for file only
	fileCore := zapcore.NewCore(
		zapcore.NewJSONEncoder(encoderCfg),
		zapcore.AddSync(rotatingWriter),
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

// Close closes the rotating file writer
func Close() {
	if rotatingWriter != nil {
		rotatingWriter.Close()
	}
}

// CoreLogger returns a logger that implements core/logger.Logger interface
func CoreLogger() *coreLoggerAdapter {
	if coreAdapter == nil {
		coreAdapter = &coreLoggerAdapter{sugar: sugar}
	}
	return coreAdapter
}
