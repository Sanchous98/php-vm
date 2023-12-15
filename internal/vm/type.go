package vm

//go:generate stringer -type=Type -linecomment
type Type uint8

const (
	NullType   Type = 1 << iota // null
	IntType                     // integer
	FloatType                   // float
	StringType                  // string
	ArrayType                   // array
	ObjectType                  // object
	BoolType                    // boolean
)

func Juggle(x, y Type) Type { return max(x, y) }

type typeHint func(*FunctionContext, Value) bool

func IsInt(_ *FunctionContext, v Value) bool    { return v.Type() == IntType }
func IsFloat(_ *FunctionContext, v Value) bool  { return v.Type() == FloatType }
func IsBool(_ *FunctionContext, v Value) bool   { return v.Type() == BoolType }
func IsArray(_ *FunctionContext, v Value) bool  { return v.Type() == ArrayType }
func IsObject(_ *FunctionContext, v Value) bool { return v.Type() == ObjectType }
func IsString(_ *FunctionContext, v Value) bool { return v.Type() == StringType }
func IsIterable(ctx *FunctionContext, v Value) bool {
	switch v.(type) {
	case *Array:
		return true
	default:
		return false
	}
}
func IsInstanceOf(className String) typeHint {
	return func(ctx *FunctionContext, v Value) bool {
		switch v.(type) {
		default:
			return false
		}
	}
}
