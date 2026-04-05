package logger

// FieldType identifies the type of field value
// This avoids interface{} boxing for common types - critical for perf
type FieldType uint8

const (
	StringType FieldType = iota
	IntType
	Int64Type
	Float64Type
	BoolType
	ErrorType
	AnyType // fallback for complex types
)

// Field represents a structured key-value pair
// Design choice: Using a union-like struct instead of interface{}
// This avoids heap allocations for primitive types
type Field struct {
	Key       string
	Type      FieldType
	StringVal string
	IntVal    int64
	FloatVal  float64
	BoolVal   bool
	AnyVal    any // only used for complex types
}

// Constructor functions - these are zero-allocation for primitives
func String(key, val string) Field {
	return Field{Key: key, Type: StringType, StringVal: val}
}

func Int(key string, val int) Field {
	return Field{Key: key, Type: IntType, IntVal: int64(val)}
}

func Int64(key string, val int64) Field {
	return Field{Key: key, Type: Int64Type, IntVal: val}
}

func Float64(key string, val float64) Field {
	return Field{Key: key, Type: Float64Type, FloatVal: val}
}

func Bool(key string, val bool) Field {
	return Field{Key: key, Type: BoolType, BoolVal: val}
}

func Err(err error) Field {
	return Field{Key: "error", Type: ErrorType, AnyVal: err}
}

func Any(key string, val any) Field {
	return Field{Key: key, Type: AnyType, AnyVal: val}
}
