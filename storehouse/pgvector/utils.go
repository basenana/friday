package pgvector

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/utils/logger"
	"github.com/google/uuid"
	"go.uber.org/zap"
	"gorm.io/gorm"
	glogger "gorm.io/gorm/logger"
)

func jsonString(data any) string {
	js, _ := json.Marshal(data)
	return string(js)
}

func jsonData(str string, data any) {
	_ = json.Unmarshal([]byte(str), data)
}

func newChunkID() string {
	return uuid.New().String()
}

func defaultChunkSetup(chunk *storehouse.Chunk) {
	if chunk.Type == "" {
		chunk.Type = "default"
	}
}

type Logger struct {
	*zap.SugaredLogger
}

func (l *Logger) LogMode(level glogger.LogLevel) glogger.Interface {
	return l
}

func (l *Logger) Info(ctx context.Context, s string, i ...interface{}) {
	l.Infof(s, i...)
}

func (l *Logger) Warn(ctx context.Context, s string, i ...interface{}) {
	l.Warnf(s, i...)
}

func (l *Logger) Error(ctx context.Context, s string, i ...interface{}) {
	l.Errorf(s, i...)
}

func (l *Logger) Trace(ctx context.Context, begin time.Time, fc func() (sql string, rowsAffected int64), err error) {
	sqlContent, rows := fc()
	l.Debugw("trace sql", "sql", sqlContent, "rows", rows, "err", err)
	switch {
	case err != nil && !errors.Is(err, gorm.ErrRecordNotFound) && !errors.Is(err, context.Canceled):
		l.Warnw("trace error", "sql", sqlContent, "rows", rows, "err", err)
	case time.Since(begin) > time.Second:
		l.Infow("slow sql", "sql", sqlContent, "rows", rows, "cost", time.Since(begin).Seconds())
	}
}

func NewDbLogger() *Logger {
	return &Logger{SugaredLogger: logger.New("pgvector.sql")}
}
