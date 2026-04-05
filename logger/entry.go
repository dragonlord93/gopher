package logger

import (
	"runtime"
	"sync"
	"time"
)

// Entry represents a single log record
// Pooled to reduce GC pressure
type Entry struct {
	Time    time.Time
	Level   Level
	Message string
	Fields  []Field
	Stack   string // populated only for errors when configured
	Caller  string // file:line
}

// Entry pool - critical for low latency
// sync.Pool automatically handles cleanup during GC
var entryPool = sync.Pool{
	New: func() any {
		return &Entry{
			Fields: make([]Field, 0, 8), // pre-allocate for common case
		}
	},
}

func getEntry() *Entry {
	e := entryPool.Get().(*Entry)
	e.Fields = e.Fields[:0] // reset slice but keep capacity
	e.Stack = ""
	e.Caller = ""
	return e
}

func putEntry(e *Entry) {
	// Don't pool entries with large field slices (memory leak prevention)
	if cap(e.Fields) <= 32 {
		entryPool.Put(e)
	}
}

// captureStack captures the stack trace for error logging
func captureStack(skip int) string {
	// Use a pooled buffer for stack capture
	buf := make([]byte, 4096)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

// captureCaller gets file:line of the log call
func captureCaller(skip int) string {
	_, file, line, ok := runtime.Caller(skip)
	if !ok {
		return "unknown"
	}
	// Extract just filename, not full path
	short := file
	for i := len(file) - 1; i > 0; i-- {
		if file[i] == '/' {
			short = file[i+1:]
			break
		}
	}
	// Format as file:line
	buf := make([]byte, 0, len(short)+10)
	buf = append(buf, short...)
	buf = append(buf, ':')
	buf = appendInt(buf, line)
	return string(buf)
}

func appendInt(buf []byte, n int) []byte {
	if n < 0 {
		buf = append(buf, '-')
		n = -n
	}
	if n < 10 {
		return append(buf, byte('0'+n))
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	return append(buf, digits[i:]...)
}
