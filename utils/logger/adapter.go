package logger

import (
	corelogger "github.com/basenana/friday/core/logger"

	"go.uber.org/zap"
)

// coreLoggerAdapter adapts zap.SugaredLogger to core/logger.Logger interface
type coreLoggerAdapter struct {
	sugar *zap.SugaredLogger
	name  string
}

// Named returns a new logger with the given name
func (a *coreLoggerAdapter) Named(name string) corelogger.Logger {
	newName := name
	if a.name != "" {
		newName = a.name + "." + name
	}
	return &coreLoggerAdapter{
		sugar: a.sugar.Named(name),
		name:  newName,
	}
}

// With returns a new logger with additional key-value pairs
func (a *coreLoggerAdapter) With(keysAndValues ...interface{}) corelogger.Logger {
	return &coreLoggerAdapter{
		sugar: a.sugar.With(keysAndValues...),
		name:  a.name,
	}
}

// Info logs a message at INFO level
func (a *coreLoggerAdapter) Info(args ...interface{}) {
	a.sugar.Info(args...)
}

// Warn logs a message at WARN level
func (a *coreLoggerAdapter) Warn(args ...interface{}) {
	a.sugar.Warn(args...)
}

// Error logs a message at ERROR level
func (a *coreLoggerAdapter) Error(args ...interface{}) {
	a.sugar.Error(args...)
}

// Infof logs a formatted message at INFO level
func (a *coreLoggerAdapter) Infof(template string, args ...interface{}) {
	a.sugar.Infof(template, args...)
}

// Warnf logs a formatted message at WARN level
func (a *coreLoggerAdapter) Warnf(template string, args ...interface{}) {
	a.sugar.Warnf(template, args...)
}

// Errorf logs a formatted message at ERROR level
func (a *coreLoggerAdapter) Errorf(template string, args ...interface{}) {
	a.sugar.Errorf(template, args...)
}

// Infow logs a message with key-value pairs at INFO level
func (a *coreLoggerAdapter) Infow(msg string, keysAndValues ...interface{}) {
	a.sugar.Infow(msg, keysAndValues...)
}

// Warnw logs a message with key-value pairs at WARN level
func (a *coreLoggerAdapter) Warnw(msg string, keysAndValues ...interface{}) {
	a.sugar.Warnw(msg, keysAndValues...)
}

// Errorw logs a message with key-value pairs at ERROR level
func (a *coreLoggerAdapter) Errorw(msg string, keysAndValues ...interface{}) {
	a.sugar.Errorw(msg, keysAndValues...)
}
