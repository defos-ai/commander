package fakes

import (
	"bytes"
	"io"
	"sync"
	"sync/atomic"
)

type HeadlessOutput struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (o *HeadlessOutput) Write(data []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.Write(data)
}

func (o *HeadlessOutput) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.data.String()
}

type HeadlessInput struct {
	reads atomic.Int32
}

func (i *HeadlessInput) Read([]byte) (int, error) {
	i.reads.Add(1)
	return 0, io.EOF
}

func (i *HeadlessInput) Reads() int32 { return i.reads.Load() }
