// logger/encoder.go
package logger

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// Encoder serializes log entries
type Encoder interface {
	Encode(e *Entry) []byte
}

// JSONEncoder produces structured JSON output
type JSONEncoder struct {
	// Buffer pool for encoding
	bufPool sync.Pool
}

func NewJSONEncoder() *JSONEncoder {
	return &JSONEncoder{
		bufPool: sync.Pool{
			New: func() any {
				b := make([]byte, 0, 512)
				return &b
			},
		},
	}
}

func (enc *JSONEncoder) Encode(e *Entry) []byte {
	bufPtr := enc.bufPool.Get().(*[]byte)
	buf := (*bufPtr)[:0]

	// Manual JSON construction is faster than json.Marshal
	// and avoids reflection overhead
	buf = append(buf, '{')

	// Timestamp - RFC3339 is standard for logs
	buf = append(buf, `"ts":"`...)
	buf = e.Time.AppendFormat(buf, time.RFC3339Nano)
	buf = append(buf, `",`...)

	// Level
	buf = append(buf, `"level":"`...)
	buf = append(buf, e.Level.String()...)
	buf = append(buf, `",`...)

	// Message
	buf = append(buf, `"msg":`...)
	buf = appendJSONString(buf, e.Message)

	// Caller if present
	if e.Caller != "" {
		buf = append(buf, `,"caller":"`...)
		buf = append(buf, e.Caller...)
		buf = append(buf, '"')
	}

	// Fields
	for _, f := range e.Fields {
		buf = append(buf, ',', '"')
		buf = append(buf, f.Key...)
		buf = append(buf, `":`...)
		buf = appendFieldValue(buf, f)
	}

	// Stack trace if present
	if e.Stack != "" {
		buf = append(buf, `,"stack":`...)
		buf = appendJSONString(buf, e.Stack)
	}

	buf = append(buf, '}', '\n')

	// Return buffer to pool for reuse
	*bufPtr = buf
	enc.bufPool.Put(bufPtr)

	// Return a copy since the buffer will be reused
	result := make([]byte, len(buf))
	copy(result, buf)
	return result
}

func appendJSONString(buf []byte, s string) []byte {
	buf = append(buf, '"')
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"':
			buf = append(buf, '\\', '"')
		case '\\':
			buf = append(buf, '\\', '\\')
		case '\n':
			buf = append(buf, '\\', 'n')
		case '\r':
			buf = append(buf, '\\', 'r')
		case '\t':
			buf = append(buf, '\\', 't')
		default:
			if c < 0x20 {
				buf = append(buf, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xf])
			} else {
				buf = append(buf, c)
			}
		}
	}
	buf = append(buf, '"')
	return buf
}

var hexDigits = "0123456789abcdef"

func appendFieldValue(buf []byte, f Field) []byte {
	switch f.Type {
	case StringType:
		return appendJSONString(buf, f.StringVal)
	case IntType, Int64Type:
		return appendInt64(buf, f.IntVal)
	case Float64Type:
		return append(buf, fmt.Sprintf("%g", f.FloatVal)...)
	case BoolType:
		if f.BoolVal {
			return append(buf, "true"...)
		}
		return append(buf, "false"...)
	case ErrorType:
		if err, ok := f.AnyVal.(error); ok && err != nil {
			return appendJSONString(buf, err.Error())
		}
		return append(buf, "null"...)
	default:
		// Fallback to json.Marshal for complex types
		b, _ := json.Marshal(f.AnyVal)
		return append(buf, b...)
	}
}

func appendInt64(buf []byte, n int64) []byte {
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
