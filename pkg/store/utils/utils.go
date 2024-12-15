/*
 Copyright 2024 Friday Author.

 Licensed under the Apache License, Version 2.0 (the "License");
 you may not use this file except in compliance with the License.
 You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

 Unless required by applicable law or agreed to in writing, software
 distributed under the License is distributed on an "AS IS" BASIS,
 WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 See the License for the specific language governing permissions and
 limitations under the License.
*/

package utils

import (
	"context"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	glogger "gorm.io/gorm/logger"

	"github.com/basenana/friday/pkg/models"
	"github.com/basenana/friday/pkg/utils/logger"
)

func SqlError2Error(err error) error {
	switch err {
	case gorm.ErrRecordNotFound:
		return models.ErrNotFound
	default:
		return err
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
	case err != nil && err != gorm.ErrRecordNotFound && err != context.Canceled:
		l.Warnw("trace error", "sql", sqlContent, "rows", rows, "err", err)
	case time.Since(begin) > time.Second:
		l.Infow("slow sql", "sql", sqlContent, "rows", rows, "cost", time.Since(begin).Seconds())
	}
}

func NewDbLogger() *Logger {
	return &Logger{SugaredLogger: logger.NewLog("database")}
}
