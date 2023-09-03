package vm

import (
	"fmt"
	"math"
	"strconv"
)

//go:generate stringer -type=Type -linecomment
type Type byte

const (
	NullType   Type = iota // null
	IntType                // integer
	FloatType              // float
	StringType             // string
	ArrayType              // array
	ObjectType             // object
	BoolType               // boolean
)

func Juggle(x, y Type) Type { return max(x, y) }

type Value interface {
	AsInt(Context) Int
	AsFloat(Context) Float
	AsBool(Context) Bool
	AsString(Context) String
	AsNull(Context) Null
	AsArray(Context) Array
	Cast(Context, Type) Value
	Ref() Value
	Type() Type
}

type Int int

func (i Int) Type() Type              { return IntType }
func (i Int) AsInt(Context) Int       { return i }
func (i Int) AsFloat(Context) Float   { return Float(i) }
func (i Int) AsBool(Context) Bool     { return i != 0 }
func (i Int) AsString(Context) String { return String(strconv.Itoa(int(i))) }
func (i Int) AsNull(Context) Null     { return Null{} }
func (i Int) AsArray(Context) Array   { return Array{Int(0): i} }
func (i Int) Ref() Value              { return &i }
func (i Int) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return i
	case FloatType:
		return i.AsFloat(ctx)
	case BoolType:
		return i.AsBool(ctx)
	case StringType:
		return i.AsString(ctx)
	case NullType:
		return i.AsNull(ctx)
	case ArrayType:
		return i.AsArray(ctx)
	default:
		panic(fmt.Sprintf("cannot cast integer to %s", t.String()))
	}
}

type Float float64

func (f Float) Type() Type              { return FloatType }
func (f Float) AsInt(Context) Int       { return Int(f) }
func (f Float) AsFloat(Context) Float   { return f }
func (f Float) AsBool(Context) Bool     { return f != 0 }
func (f Float) AsString(Context) String { return String(strconv.FormatFloat(float64(f), 'g', -1, 64)) }
func (f Float) AsNull(Context) Null     { return Null{} }
func (f Float) AsArray(Context) Array   { return Array{Int(0): f} }
func (f Float) Ref() Value              { return &f }
func (f Float) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return f.AsInt(ctx)
	case FloatType:
		return f
	case BoolType:
		return f.AsBool(ctx)
	case StringType:
		return f.AsString(ctx)
	case NullType:
		return f.AsNull(ctx)
	case ArrayType:
		return f.AsArray(ctx)
	default:
		panic(fmt.Sprintf("cannot cast float to %s", t.String()))
	}
}

type Bool bool

func (b Bool) Type() Type { return BoolType }
func (b Bool) AsInt(Context) Int {
	if b {
		return 1
	}

	return 0
}
func (b Bool) AsFloat(Context) Float {
	if b {
		return 1
	}

	return 0
}
func (b Bool) AsBool(Context) Bool     { return b }
func (b Bool) AsString(Context) String { return String(strconv.FormatBool(bool(b))) }
func (b Bool) AsNull(Context) Null     { return Null{} }
func (b Bool) AsArray(Context) Array   { return Array{Int(0): b} }
func (b Bool) Ref() Value              { return &b }
func (b Bool) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return b.AsInt(ctx)
	case FloatType:
		return b.AsFloat(ctx)
	case BoolType:
		return b
	case StringType:
		return b.AsString(ctx)
	case NullType:
		return b.AsNull(ctx)
	case ArrayType:
		return b.AsArray(ctx)
	default:
		panic(fmt.Sprintf("cannot cast boolean to %s", t.String()))
	}
}

type String string

func (s String) Type() Type { return StringType }
func (s String) AsInt(ctx Context) Int {
	v, err := strconv.Atoi(string(s))

	if err != nil {
		ctx.Throw(err)
		return 0
	}

	return Int(v)
}
func (s String) AsFloat(ctx Context) Float {
	v, err := strconv.ParseFloat(string(s), 64)

	if err != nil {
		ctx.Throw(err)
		return 0
	}

	return Float(v)
}
func (s String) AsBool(Context) Bool     { return len(s) > 0 && s != "0" }
func (s String) AsString(Context) String { return s }
func (s String) AsNull(Context) Null     { return Null{} }
func (s String) AsArray(Context) Array   { return Array{Int(0): s} }
func (s String) Ref() Value              { return &s }
func (s String) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return s.AsInt(ctx)
	case FloatType:
		return s.AsFloat(ctx)
	case BoolType:
		return s.AsBool(ctx)
	case StringType:
		return s
	case NullType:
		return s.AsNull(ctx)
	case ArrayType:
		return s.AsArray(ctx)
	default:
		panic(fmt.Sprintf("cannot cast string to %s", t.String()))
	}
}

type Null struct{}

func (n Null) Type() Type              { return NullType }
func (n Null) AsInt(Context) Int       { return 0 }
func (n Null) AsFloat(Context) Float   { return 0 }
func (n Null) AsBool(Context) Bool     { return false }
func (n Null) AsString(Context) String { return "" }
func (n Null) AsNull(Context) Null     { return n }
func (n Null) AsArray(Context) Array   { return Array{} }
func (n Null) Ref() Value              { return &n }
func (n Null) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return n.AsInt(ctx)
	case FloatType:
		return n.AsFloat(ctx)
	case BoolType:
		return n.AsBool(ctx)
	case StringType:
		return n.AsString(ctx)
	case NullType:
		return n
	case ArrayType:
		return n.AsArray(ctx)
	default:
		panic(fmt.Sprintf("cannot cast string to %s", t.String()))
	}
}

type Array map[Value]Value

func (a Array) AsInt(Context) Int {
	if len(a) > 0 {
		return 1
	}

	return 0
}
func (a Array) AsFloat(Context) Float {
	if len(a) > 0 {
		return 1
	}

	return 0
}
func (a Array) AsBool(Context) Bool { return len(a) > 0 }
func (a Array) AsString(ctx Context) String {
	ctx.Throw(fmt.Errorf("array to string conversion"))
	return "Array"
}
func (a Array) AsNull(Context) Null   { return Null{} }
func (a Array) AsArray(Context) Array { return a }
func (a Array) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return a.AsInt(ctx)
	case FloatType:
		return a.AsFloat(ctx)
	case BoolType:
		return a.AsBool(ctx)
	case StringType:
		return a.AsString(ctx)
	case NullType:
		return a.AsNull(ctx)
	case ArrayType:
		return a
	default:
		panic(fmt.Sprintf("cannot cast array to %s", t.String()))
	}
}
func (a Array) Type() Type { return ArrayType }
func (a Array) Ref() Value { return &a }
func (a Array) NextKey() Value {
	for i := len(a); i > math.MinInt; i-- {
		if _, ok := a[Int(i)]; ok {
			return Int(i) + 1
		}
	}

	return Int(0)
}
