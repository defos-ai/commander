package fakes

import (
	"io"
	"sync/atomic"
)

type CountingReader struct{ reads atomic.Int32 }

func (r *CountingReader) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, io.EOF
}

func (r *CountingReader) ReadCount() int32 { return r.reads.Load() }
