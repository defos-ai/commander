package fakes

import (
	"context"
	"io"
	"sync"
	"sync/atomic"
)

type TrackedBody struct {
	reader io.Reader
	closed atomic.Bool
	read   atomic.Int64
}

func (body *TrackedBody) Read(data []byte) (int, error) {
	n, err := body.reader.Read(data)
	body.read.Add(int64(n))
	return n, err
}

func (body *TrackedBody) Close() error { body.closed.Store(true); return nil }

type ErrorReader struct{ Err error }

func (reader ErrorReader) Read([]byte) (int, error) { return 0, reader.Err }

type BlockingBody struct {
	ctx             context.Context
	started, closed chan struct{}
	readOnce        sync.Once
	closeOnce       sync.Once
}

func (body *BlockingBody) Read([]byte) (int, error) {
	body.readOnce.Do(func() { close(body.started) })
	select {
	case <-body.ctx.Done():
		return 0, body.ctx.Err()
	case <-body.closed:
		return 0, io.ErrClosedPipe
	}
}

func (body *BlockingBody) Close() error {
	body.closeOnce.Do(func() { close(body.closed) })
	return nil
}

func NewTrackedBody(reader io.Reader) *TrackedBody { return &TrackedBody{reader: reader} }
func (body *TrackedBody) IsClosed() bool           { return body.closed.Load() }
func (body *TrackedBody) ReadCount() int64         { return body.read.Load() }
func NewBlockingBody(ctx context.Context) *BlockingBody {
	return &BlockingBody{ctx: ctx, started: make(chan struct{}), closed: make(chan struct{})}
}
func (body *BlockingBody) Started() <-chan struct{} { return body.started }
func (body *BlockingBody) Closed() <-chan struct{}  { return body.closed }
