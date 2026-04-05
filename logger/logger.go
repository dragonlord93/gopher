// logger/logger.go
package logger

import (
	"context"
	"io"
	"os"
	"time"
)

// Logger is the main logging interface
type Logger struct {
	level      Level
	encoder    Encoder
	writer     io.Writer
	sampler    Sampler
	fields     []Field // inherited fields
	addCaller  bool
	addStack   bool // for error level
	callerSkip int
}

// Config for creating a new logger
type Config struct {
	Level      Level
	Output     io.Writer
	Encoder    Encoder
	Sampler    Sampler
	AddCaller  bool
	AddStack   bool // add stack for error level
	Async      bool
	BufferSize int
	Workers    int
}

func DefaultConfig() Config {
	return Config{
		Level:      InfoLevel,
		Output:     os.Stdout,
		Encoder:    NewJSONEncoder(),
		Sampler:    &NoopSampler{},
		AddCaller:  true,
		AddStack:   true,
		Async:      true,
		BufferSize: 4096,
		Workers:    2,
	}
}

func New(cfg Config) *Logger {
	var writer io.Writer = cfg.Output
	if cfg.Async {
		writer = NewAsyncWriter(cfg.Output, cfg.BufferSize, cfg.Workers)
	} else {
		writer = NewSyncWriter(cfg.Output)
	}

	return &Logger{
		level:      cfg.Level,
		encoder:    cfg.Encoder,
		writer:     writer,
		sampler:    cfg.Sampler,
		addCaller:  cfg.AddCaller,
		addStack:   cfg.AddStack,
		callerSkip: 3,
	}
}

// With creates a child logger with additional fields
// This is the key to contextual logging
func (l *Logger) With(fields ...Field) *Logger {
	newFields := make([]Field, len(l.fields), len(l.fields)+len(fields))
	copy(newFields, l.fields)
	newFields = append(newFields, fields...)

	return &Logger{
		level:      l.level,
		encoder:    l.encoder,
		writer:     l.writer,
		sampler:    l.sampler,
		fields:     newFields,
		addCaller:  l.addCaller,
		addStack:   l.addStack,
		callerSkip: l.callerSkip,
	}
}

// WithContext extracts fields from context
// Common pattern: store request ID, user ID, trace ID in context
func (l *Logger) WithContext(ctx context.Context) *Logger {
	fields := extractContextFields(ctx)
	if len(fields) == 0 {
		return l
	}
	return l.With(fields...)
}

// Context key types for type safety
type contextKey int

const (
	fieldsKey contextKey = iota
	requestIDKey
	traceIDKey
)

// ContextWithFields adds logging fields to context
func ContextWithFields(ctx context.Context, fields ...Field) context.Context {
	existing := ctx.Value(fieldsKey)
	if existing != nil {
		fields = append(existing.([]Field), fields...)
	}
	return context.WithValue(ctx, fieldsKey, fields)
}

func extractContextFields(ctx context.Context) []Field {
	if ctx == nil {
		return nil
	}

	fields, _ := ctx.Value(fieldsKey).([]Field)
	return fields
}

// Core logging methods
func (l *Logger) log(level Level, msg string, fields []Field) {
	// Level check - fast path
	if level < l.level {
		return
	}

	// Sampling check
	if !l.sampler.Sample(level, msg) {
		return
	}
	// Get entry from pool
	entry := getEntry()
	entry.Time = time.Now()
	entry.Level = level
	entry.Message = msg

	// Add inherited fields
	entry.Fields = append(entry.Fields, l.fields...)
	// Add call-site fields
	entry.Fields = append(entry.Fields, fields...)

	// Capture caller info
	if l.addCaller {
		entry.Caller = captureCaller(l.callerSkip)
	}

	// Capture stack for errors
	if l.addStack && level >= ErrorLevel {
		entry.Stack = captureStack(l.callerSkip)
	}

	// Encode and write
	data := l.encoder.Encode(entry)
	l.writer.Write(data)

	// Return entry to pool
	putEntry(entry)
}

// Public API methods
func (l *Logger) Debug(msg string, fields ...Field) {
	l.log(DebugLevel, msg, fields)
}

func (l *Logger) Info(msg string, fields ...Field) {
	l.log(InfoLevel, msg, fields)
}

func (l *Logger) Warn(msg string, fields ...Field) {
	l.log(WarnLevel, msg, fields)
}

func (l *Logger) Error(msg string, fields ...Field) {
	l.log(ErrorLevel, msg, fields)
}

func (l *Logger) Fatal(msg string, fields ...Field) {
	l.log(FatalLevel, msg, fields)
	os.Exit(1)
}

// Close flushes any buffered logs
func (l *Logger) Close() error {
	if closer, ok := l.writer.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

// Global logger for convenience
var globalLogger = New(DefaultConfig())

func SetGlobal(l *Logger) {
	globalLogger = l
}

func Debug(msg string, fields ...Field) { globalLogger.Debug(msg, fields...) }
func Info(msg string, fields ...Field)  { globalLogger.Info(msg, fields...) }
func Warn(msg string, fields ...Field)  { globalLogger.Warn(msg, fields...) }
func Error(msg string, fields ...Field) { globalLogger.Error(msg, fields...) }
func Fatal(msg string, fields ...Field) { globalLogger.Fatal(msg, fields...) }
func With(fields ...Field) *Logger      { return globalLogger.With(fields...) }
