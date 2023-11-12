package vm

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"strconv"
	"strings"
)

//go:generate stringer -type=Type -linecomment
type Type byte

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

type Countable interface {
	Count(Context) Int
}

type ArrayAccess interface {
	OffsetGet(Context, Value) Value
	OffsetSet(Context, Value, Value)
	OffsetIsSet(Context, Value) Bool
	OffsetUnset(Context, Value)
}

type Value interface {
	IsRef() bool
	AsInt(Context) Int
	AsFloat(Context) Float
	AsBool(Context) Bool
	AsString(Context) String
	AsNull(Context) Null
	AsArray(Context) *Array
	AsObject(Context) *Object
	Cast(Context, Type) Value
	Type() Type
	DebugInfo(Context, int) string
}

type Int int

func (i Int) IsRef() bool              { return false }
func (i Int) Type() Type               { return IntType }
func (i Int) AsInt(Context) Int        { return i }
func (i Int) AsFloat(Context) Float    { return Float(i) }
func (i Int) AsBool(Context) Bool      { return i != 0 }
func (i Int) AsString(Context) String  { return String(strconv.Itoa(int(i))) }
func (i Int) AsNull(Context) Null      { return Null{} }
func (i Int) AsArray(Context) *Array   { return NewArray(map[Value]Value{String("scalar"): i}) }
func (i Int) AsObject(Context) *Object { return &Object{props: map[String]Value{"scalar": i}} }
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
	case ObjectType:
		return i.AsObject(ctx)
	default:
		ctx.Throw(fmt.Errorf("cannot cast %s to %s", i.Type().String(), t.String()))
		return nil
	}
}
func (i Int) DebugInfo(_ Context, level int) string {
	return fmt.Sprintf("%sint(%d)", strings.Repeat(" ", level<<1), i)
}

type Float float64

func (f Float) IsRef() bool              { return false }
func (f Float) Type() Type               { return FloatType }
func (f Float) AsInt(Context) Int        { return Int(f) }
func (f Float) AsFloat(Context) Float    { return f }
func (f Float) AsBool(Context) Bool      { return f != 0 }
func (f Float) AsString(Context) String  { return String(strconv.FormatFloat(float64(f), 'g', -1, 64)) }
func (f Float) AsNull(Context) Null      { return Null{} }
func (f Float) AsArray(Context) *Array   { return NewArray(map[Value]Value{String("scalar"): f}) }
func (f Float) AsObject(Context) *Object { return &Object{props: map[String]Value{"scalar": f}} }
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
	case ObjectType:
		return f.AsObject(ctx)
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", f.Type().String(), t.String()))
	}
}
func (f Float) DebugInfo(_ Context, level int) string {
	return fmt.Sprintf("%sfloat(%f)", strings.Repeat(" ", level<<1), f)
}

type Bool bool

func (b Bool) IsRef() bool { return false }
func (b Bool) Type() Type  { return BoolType }
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
func (b Bool) AsBool(Context) Bool      { return b }
func (b Bool) AsString(Context) String  { return String(strconv.FormatBool(bool(b))) }
func (b Bool) AsNull(Context) Null      { return Null{} }
func (b Bool) AsArray(Context) *Array   { return NewArray(map[Value]Value{String("scalar"): b}) }
func (b Bool) AsObject(Context) *Object { return &Object{props: map[String]Value{"scalar": b}} }
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
	case ObjectType:
		return b.AsObject(ctx)
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", b.Type().String(), t.String()))
	}
}
func (b Bool) DebugInfo(_ Context, level int) string {
	return fmt.Sprintf("%sbool(%t)", strings.Repeat(" ", level<<1), b)
}

type String string

func (s String) IsRef() bool { return false }
func (s String) Type() Type  { return StringType }
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
func (s String) AsBool(Context) Bool      { return len(s) > 0 && s != "0" }
func (s String) AsString(Context) String  { return s }
func (s String) AsNull(Context) Null      { return Null{} }
func (s String) AsArray(Context) *Array   { return NewArray(map[Value]Value{String("scalar"): s}) }
func (s String) AsObject(Context) *Object { return &Object{props: map[String]Value{"scalar": s}} }
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
	case ObjectType:
		return s.AsObject(ctx)
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", s.Type().String(), t.String()))
	}
}
func (s String) String() string { return strconv.Quote(string(s)) }
func (s String) DebugInfo(_ Context, level int) string {
	return fmt.Sprintf("%sstring(%s)", strings.Repeat(" ", level<<1), s)
}

type Null struct{}

func (n Null) IsRef() bool              { return false }
func (n Null) Type() Type               { return NullType }
func (n Null) AsInt(Context) Int        { return 0 }
func (n Null) AsFloat(Context) Float    { return 0 }
func (n Null) AsBool(Context) Bool      { return false }
func (n Null) AsString(Context) String  { return "" }
func (n Null) AsNull(Context) Null      { return n }
func (n Null) AsArray(Context) *Array   { return NewArray(nil) }
func (n Null) AsObject(Context) *Object { return &Object{props: map[String]Value{}} }
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
	case ObjectType:
		return n.AsObject(ctx)
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", n.Type().String(), t.String()))
	}
}
func (n Null) DebugInfo(_ Context, level int) string { return strings.Repeat(" ", level<<1) + "NULL" }

type Array struct {
	hash map[Value]Ref
	next Int
}

func (a *Array) GetIterator(ctx Context) Iterator {
	keys := a.Keys(ctx)
	i := 0

	return InternalIterator[*Array]{
		this:      a,
		nextFn:    func(ctx Context, array *Array) { i++ },
		currentFn: func(ctx Context, array *Array) Value { return array.hash[keys[i]] },
		keyFn:     func(ctx Context, array *Array) Value { return keys[i] },
		validFn:   func(ctx Context, array *Array) Bool { return i < len(keys) },
		rewindFn:  func(ctx Context, array *Array) { i = 0 },
	}
}

func (a *Array) Count(Context) Int { return Int(len(a.hash)) }

func (a *Array) Copy() *Array {
	return &Array{
		hash: maps.Clone(a.hash),
		next: a.next,
	}
}
func (a *Array) access(key Value) (v Ref, ok bool) {
	v, ok = a.hash[key]
	return
}
func (a *Array) assign(ctx Context, key Value) Ref {
	if key == nil {
		if a.next == math.MinInt {
			a.next = 0
		}

		key = a.next
	}

	if ref, ok := a.access(key); ok {
		return ref
	}

	switch key.Type() {
	case IntType, FloatType:
		key = key.AsInt(ctx)
		a.next = key.(Int) + 1
	}

	a.hash[key] = NewRef(nil)
	return a.hash[key]
}
func (a *Array) delete(key Value) { delete(a.hash, key) }

func NewArray(init map[Value]Value, next ...Int) *Array {
	if next == nil {
		next = []Int{math.MinInt}
	}

	arr := &Array{hash: make(map[Value]Ref, len(init)), next: next[0]}

	for k, v := range init {
		r := v
		arr.hash[k] = NewRef(&r)
	}

	return arr
}

func (a *Array) OffsetGet(_ Context, key Value) Value {
	if ref, ok := a.access(key); ok {
		return *ref.Deref()
	}

	return Null{}
}
func (a *Array) OffsetSet(ctx Context, key Value, value Value) { *a.assign(ctx, key).Deref() = value }
func (a *Array) OffsetIsSet(_ Context, key Value) Bool {
	_, ok := a.access(key)
	return Bool(ok)
}
func (a *Array) OffsetUnset(_ Context, key Value) { a.delete(key) }
func (a *Array) IsRef() bool                      { return false }
func (a *Array) Type() Type                       { return ArrayType }
func (a *Array) AsInt(Context) Int {
	if len(a.hash) > 0 {
		return 1
	}

	return 0
}
func (a *Array) AsFloat(Context) Float {
	if len(a.hash) > 0 {
		return 1
	}

	return 0
}
func (a *Array) AsBool(Context) Bool { return len(a.hash) > 0 }
func (a *Array) AsString(ctx Context) String {
	ctx.Throw(fmt.Errorf("array to string conversion"))
	return "Array"
}
func (a *Array) AsNull(Context) Null    { return Null{} }
func (a *Array) AsArray(Context) *Array { return a }
func (a *Array) AsObject(ctx Context) *Object {
	props := make(map[String]Value, len(a.hash))

	for k, v := range a.hash {
		props[k.AsString(ctx)] = v
	}

	return &Object{props: props}
}
func (a *Array) Cast(ctx Context, t Type) Value {
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
	case ObjectType:
		return a.AsObject(ctx)
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", a.Type().String(), t.String()))
	}
}
func (a *Array) NextKey() Value {
	if a.next == math.MinInt {
		return Int(0)
	}

	return a.next
}
func (a *Array) DebugInfo(ctx Context, level int) string {
	var str strings.Builder
	str.WriteString(fmt.Sprintf("%sarray(%d) {\n", strings.Repeat(" ", level<<1), len(a.hash)))
	level++
	spaces := strings.Repeat(" ", level<<1)

	for _, key := range a.Keys(ctx) {
		str.WriteString(fmt.Sprintf("%s[%v]=>\n%s\n", spaces, key, (*a.hash[key].Deref()).DebugInfo(ctx, level)))
	}

	str.WriteString(strings.Repeat(" ", (level-1)<<1))
	str.WriteByte('}')

	return str.String()
}
func (a *Array) Keys(ctx Context) []Value {
	keys := make([]Value, 0, len(a.hash))
	for k := range a.hash {
		keys = append(keys, k)
	}
	slices.SortFunc(keys, func(a, b Value) int { return int(compare(ctx, a, b)) })
	return keys
}

type Ref struct{ ref *Value }

func NewRef(v *Value) Ref {
	if v == nil {
		n := Value(Null{})
		v = &n
	}

	return Ref{v}
}

func (r Ref) IsRef() bool                  { return true }
func (r Ref) Deref() *Value                { return r.ref }
func (r Ref) Type() Type                   { return (*r.ref).Type() }
func (r Ref) AsInt(ctx Context) Int        { return (*r.Deref()).AsInt(ctx) }
func (r Ref) AsFloat(ctx Context) Float    { return (*r.Deref()).AsFloat(ctx) }
func (r Ref) AsBool(ctx Context) Bool      { return (*r.Deref()).AsBool(ctx) }
func (r Ref) AsString(ctx Context) String  { return (*r.Deref()).AsString(ctx) }
func (r Ref) AsNull(ctx Context) Null      { return (*r.Deref()).AsNull(ctx) }
func (r Ref) AsArray(ctx Context) *Array   { return (*r.Deref()).AsArray(ctx) }
func (r Ref) AsObject(ctx Context) *Object { return (*r.Deref()).AsObject(ctx) }
func (r Ref) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return r.AsInt(ctx)
	case FloatType:
		return r.AsFloat(ctx)
	case BoolType:
		return r.AsBool(ctx)
	case StringType:
		return r.AsString(ctx)
	case NullType:
		return r.AsNull(ctx)
	case ArrayType:
		return r.AsArray(ctx)
	case ObjectType:
		return r.AsObject(ctx)
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", r.Type().String(), t.String()))
	}
}
func (r Ref) DebugInfo(ctx Context, level int) string {
	return fmt.Sprintf("%s&%s", strings.Repeat(" ", level<<1), (*r.Deref()).DebugInfo(ctx, 0))
}

type Object struct {
	props map[String]Value
}

func (o *Object) IsRef() bool             { return false }
func (o *Object) AsInt(Context) Int       { return 1 }
func (o *Object) AsFloat(Context) Float   { return 1 }
func (o *Object) AsBool(Context) Bool     { return true }
func (o *Object) AsString(Context) String { panic("cannot be converted to string") }
func (o *Object) AsNull(Context) Null     { return Null{} }
func (o *Object) Type() Type              { return ObjectType }
func (o *Object) AsArray(Context) *Array {
	arr := make(map[Value]Value, len(o.props))

	for k, v := range o.props {
		arr[k] = v
	}

	return NewArray(arr)
}
func (o *Object) AsObject(Context) *Object { return o }
func (o *Object) Cast(ctx Context, t Type) Value {
	switch t {
	case IntType:
		return o.AsInt(ctx)
	case FloatType:
		return o.AsFloat(ctx)
	case BoolType:
		return o.AsBool(ctx)
	case StringType:
		return o.AsString(ctx)
	case NullType:
		return o.AsNull(ctx)
	case ArrayType:
		return o.AsArray(ctx)
	case ObjectType:
		return o
	default:
		panic(fmt.Sprintf("cannot cast %s to %s", o.Type().String(), t.String()))
	}
}
func (o *Object) Keys() []String {
	keys := make([]String, 0, len(o.props))
	for k := range o.props {
		keys = append(keys, k)
	}
	return keys
}
func (o *Object) DebugInfo(ctx Context, level int) string {
	var str strings.Builder
	str.WriteString(fmt.Sprintf("%sobject(stdClass)#%d (%d) {\n", strings.Repeat(" ", level<<1), 1, len(o.props)))
	level++
	spaces := strings.Repeat(" ", level<<1)

	for _, key := range o.Keys() {
		str.WriteString(fmt.Sprintf("%s[%v]=>\n%s\n", spaces, key, o.props[key].DebugInfo(ctx, level)))
	}

	str.WriteString(strings.Repeat(" ", (level-1)<<1))
	str.WriteByte('}')

	return str.String()
}
