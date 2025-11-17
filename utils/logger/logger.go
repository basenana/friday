package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"os"
)

var (
	root  *zap.Logger
	atom  zap.AtomicLevel
	sugar *zap.SugaredLogger
)

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

func New(name string) *zap.SugaredLogger {
	return sugar.Named(name)
}

func Sync() {
	_ = root.Sync()
}
