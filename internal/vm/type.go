package vm

import (
	"strings"
)

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

type TypeHint = func(*FunctionContext, Value) bool

func IsInt(_ *FunctionContext, v Value) bool    { return v.Type() == IntType }
func IsFloat(_ *FunctionContext, v Value) bool  { return v.Type() == FloatType }
func IsBool(_ *FunctionContext, v Value) bool   { return v.Type() == BoolType }
func IsArray(_ *FunctionContext, v Value) bool  { return v.Type() == ArrayType }
func IsObject(_ *FunctionContext, v Value) bool { return v.Type() == ObjectType }
func IsString(_ *FunctionContext, v Value) bool { return v.Type() == StringType }
func IsCallable(ctx *FunctionContext, v Value) bool {
	switch v := v.(type) {
	case String:
		return ctx.FunctionByName(v) != nil
	case *Object:
		return false
	case *Array:
		switch len(v.hash.values) {
		case 2:
			var class Class
			switch x := v.hash.values[0].(type) {
			case *Object:
				class = x.impl
			case String:
				class = ctx.ClassByName(x)
			default:
				return false
			}

			_ = class

			return false
		case 1:
			classAndMethod := strings.Split(string(v.hash.values[0].(String)), "::")

			switch len(classAndMethod) {
			case 2:
				class := ctx.ClassByName(String(classAndMethod[0]))
				return class != nil
			case 1:
				return ctx.FunctionByName(String(classAndMethod[0])) != nil
			default:
				return false
			}
		}
	}
	return false
}
func IsIterable(ctx *FunctionContext, v Value) bool {
	switch v.(type) {
	case *Array:
		return true
	case *Object:
		return false
	default:
		return false
	}
}
func IsInstanceOf(className String) TypeHint {
	return func(ctx *FunctionContext, v Value) bool {
		switch v.(type) {
		case *Object:
			return false
		default:
			return false
		}
	}
}
