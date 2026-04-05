// logger/writer.go
package logger

import (
	"io"
	"sync"
)

// AsyncWriter provides non-blocking writes with buffering
// This is crucial for "should not hinder application behavior"
type AsyncWriter struct {
	writer    io.Writer
	ch        chan []byte
	wg        sync.WaitGroup
	closeOnce sync.Once
	done      chan struct{}

	// Metrics for monitoring
	dropped uint64
}

func NewAsyncWriter(w io.Writer, bufferSize int, workers int) *AsyncWriter {
	aw := &AsyncWriter{
		writer: w,
		ch:     make(chan []byte, bufferSize),
		done:   make(chan struct{}),
	}

	// Start worker goroutines
	for i := 0; i < workers; i++ {
		aw.wg.Add(1)
		go aw.worker()
	}

	return aw
}

func (aw *AsyncWriter) worker() {
	defer aw.wg.Done()

	for {
		select {
		case data := <-aw.ch:
			// Batch writes for efficiency
			aw.writer.Write(data)
		case <-aw.done:
			// Drain remaining logs before exit
			for {
				select {
				case data := <-aw.ch:
					aw.writer.Write(data)
				default:
					return
				}
			}
		}
	}
}

func (aw *AsyncWriter) Write(p []byte) (n int, err error) {
	// Non-blocking send
	select {
	case aw.ch <- p:
		return len(p), nil
	default:
		// Buffer full - drop log rather than block application
		// In production, you'd increment a dropped counter
		return 0, nil
	}
}

func (aw *AsyncWriter) Close() error {
	aw.closeOnce.Do(func() {
		close(aw.done)
		aw.wg.Wait()
	})
	return nil
}

// SyncWriter for synchronous writes (testing/debugging)
type SyncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func NewSyncWriter(w io.Writer) *SyncWriter {
	return &SyncWriter{writer: w}
}

func (sw *SyncWriter) Write(p []byte) (n int, err error) {
	sw.mu.Lock()
	defer sw.mu.Unlock()
	return sw.writer.Write(p)
}
