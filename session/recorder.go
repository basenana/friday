package session

import (
	"github.com/basenana/friday/storehouse"
	"github.com/basenana/friday/types"
	"go.uber.org/zap"
)

type Recorder struct {
	session *types.Session
	store   storehouse.Storehouse
	logger  *zap.SugaredLogger
}

func (m *Recorder) Record(message types.Message) {
	//TODO implement me
	panic("implement me")
}

func (m *Recorder) Close() error {
	return nil
}
