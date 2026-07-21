package buffer

import (
	"sync"
)

const DefaultBufferSize = 32 * 1024 // 32KB

var pool = sync.Pool{
	New: func() any {
		return make([]byte, DefaultBufferSize)
	},
}

// Get returns a buffer from the pool.
func Get() []byte {
	return pool.Get().([]byte)
}

// Put returns a buffer to the pool.
func Put(b []byte) {
	if len(b) != DefaultBufferSize {
		return
	}
	pool.Put(b)
}
