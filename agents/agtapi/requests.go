package agtapi

import (
	"bytes"
	"context"
	"github.com/basenana/friday/types"

	"github.com/basenana/friday/memory"
)

type Request struct {
	UserMessage string
	ImageURLs   []string // 图片URL列表，用于多模态分析
	SessionID   string
	Memory      *memory.Memory

	ExtraKV           []string
	OverwriteToolArgs map[string]string
}

type Response struct {
	e   chan types.Event
	err chan error
}

func (r *Response) Events() <-chan types.Event {
	return r.e
}

func (r *Response) Fail(err error) {
	r.err <- err
}
func (r *Response) Error() <-chan error {
	return r.err
}

func (r *Response) Close() {
	close(r.e)
	close(r.err)
}

func NewResponse() *Response {
	return &Response{e: make(chan types.Event, 5), err: make(chan error, 0)}
}

func SendEvent(req *Request, resp *Response, evt *types.Event, extraKV ...string) {
	var (
		ev = make(map[string]string)
	)
	if len(req.ExtraKV) > 1 {
		for i := 1; i < len(req.ExtraKV); i += 2 {
			ev[req.ExtraKV[i-1]] = req.ExtraKV[i]
		}
	}
	if len(extraKV) > 1 {
		for i := 1; i < len(extraKV); i += 2 {
			ev[extraKV[i-1]] = extraKV[i]
		}
	}
	if len(ev) > 0 {
		evt.ExtraValue = ev
	}
	resp.e <- *evt
}

func ReadAllContent(ctx context.Context, resp *Response) (string, error) {
	var (
		msgBuf = &bytes.Buffer{}
	)

WaitingRun:
	for {
		select {
		case <-ctx.Done():
			return msgBuf.String(), ctx.Err()
		case err := <-resp.Error():
			if err != nil {
				msgBuf = &bytes.Buffer{}
			}
		case evt, ok := <-resp.Events():
			if !ok {
				break WaitingRun
			}
			msg := evt.Delta
			if msg == nil {
				continue
			}
			if msg.Content != "" {
				msgBuf.WriteString(msg.Content)
			}
		}
	}

	return msgBuf.String(), nil
}
