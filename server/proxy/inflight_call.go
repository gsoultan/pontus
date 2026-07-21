package proxy

import "sync"

type inflightCall struct {
	wg   sync.WaitGroup
	data []byte
	err  error
}
