package vm

import (
	"fmt"
	"maps"
	"math"
	"strconv"
	"strings"
)

//go:generate stringer -type=TypeShape -linecomment
type TypeShape byte

const (
	NullType   TypeShape = 1 << iota // null
	IntType                          // integer
	FloatType                        // float
	StringType                       // string
	ArrayType                        // array
	ObjectType                       // object
	BoolType                         // boolean
)

func Juggle(x, y TypeShape) TypeShape { return max(x, y) }

type Type struct {
	class Class
	shape TypeShape
}

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
	Cast(Context, TypeShape) Value
	Type() Type
	DebugInfo(Context) String
}

type Int int

func (i Int) IsRef() bool             { return false }
func (i Int) Type() Type              { return Type{shape: IntType} }
func (i Int) AsInt(Context) Int       { return i }
func (i Int) AsFloat(Context) Float   { return Float(i) }
func (i Int) AsBool(Context) Bool     { return i != 0 }
func (i Int) AsString(Context) String { return String(strconv.Itoa(int(i))) }
func (i Int) AsNull(Context) Null     { return Null{} }
func (i Int) AsArray(Context) *Array  { return NewArray(map[Value]Value{String("scalar"): i}) }
func (i Int) AsObject(ctx Context) *Object {
	return &Object{
		class: ctx.ClassByName("stdClass"),
		props: HashTable[String, Value]{
			internal: map[String]*htValue[Value]{"scalar": {i}},
		},
	}
}
func (i Int) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Errorf("cannot cast %s to %s", i.Type().shape.String(), t.String()))
	}
}
func (i Int) DebugInfo(Context) String { return "int(" + String(strconv.Itoa(int(i))) + ")" }

type Float float64

func (f Float) IsRef() bool             { return false }
func (f Float) Type() Type              { return Type{shape: FloatType} }
func (f Float) AsInt(Context) Int       { return Int(f) }
func (f Float) AsFloat(Context) Float   { return f }
func (f Float) AsBool(Context) Bool     { return f != 0 }
func (f Float) AsString(Context) String { return String(strconv.FormatFloat(float64(f), 'g', -1, 64)) }
func (f Float) AsNull(Context) Null     { return Null{} }
func (f Float) AsArray(Context) *Array  { return NewArray(map[Value]Value{String("scalar"): f}) }
func (f Float) AsObject(ctx Context) *Object {
	return &Object{
		class: ctx.ClassByName("stdClass"),
		props: HashTable[String, Value]{
			internal: map[String]*htValue[Value]{"scalar": {f}},
		},
	}
}
func (f Float) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", f.Type().shape.String(), t.String()))
	}
}
func (f Float) DebugInfo(Context) String {
	return "float(" + String(strconv.FormatFloat(float64(f), 'g', -1, 64)) + ")"
}

type Bool bool

func (b Bool) IsRef() bool { return false }
func (b Bool) Type() Type  { return Type{shape: BoolType} }
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
func (b Bool) AsArray(Context) *Array  { return NewArray(map[Value]Value{String("scalar"): b}) }
func (b Bool) AsObject(ctx Context) *Object {
	return &Object{
		class: ctx.ClassByName("stdClass"),
		props: HashTable[String, Value]{
			internal: map[String]*htValue[Value]{"scalar": {b}},
		},
	}
}
func (b Bool) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", b.Type().shape.String(), t.String()))
	}
}
func (b Bool) DebugInfo(Context) String { return "bool(" + String(strconv.FormatBool(bool(b))) + ")" }

type String string

func (s String) IsRef() bool { return false }
func (s String) Type() Type  { return Type{shape: StringType} }
func (s String) AsInt(Context) Int {
	if s == "" {
		return 0
	}

	v, err := strconv.Atoi(string(s))

	if err != nil {
		return 0
	}

	return Int(v)
}
func (s String) AsFloat(Context) Float {
	v, err := strconv.ParseFloat(string(s), 64)

	if err != nil {
		return 0
	}

	return Float(v)
}
func (s String) AsBool(Context) Bool     { return len(s) > 0 && s != "0" }
func (s String) AsString(Context) String { return s }
func (s String) AsNull(Context) Null     { return Null{} }
func (s String) AsArray(Context) *Array  { return NewArray(map[Value]Value{String("scalar"): s}) }
func (s String) AsObject(ctx Context) *Object {
	return &Object{
		class: ctx.ClassByName("stdClass"),
		props: HashTable[String, Value]{
			internal: map[String]*htValue[Value]{"scalar": {s}},
		},
	}
}
func (s String) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", s.Type().shape.String(), t.String()))
	}
}
func (s String) String() string           { return strconv.Quote(string(s)) }
func (s String) DebugInfo(Context) String { return "string(\"" + s + "\")" }

type Null struct{}

func (n Null) IsRef() bool             { return false }
func (n Null) Type() Type              { return Type{shape: NullType} }
func (n Null) AsInt(Context) Int       { return 0 }
func (n Null) AsFloat(Context) Float   { return 0 }
func (n Null) AsBool(Context) Bool     { return false }
func (n Null) AsString(Context) String { return "" }
func (n Null) AsNull(Context) Null     { return n }
func (n Null) AsArray(Context) *Array  { return NewArray(nil) }
func (n Null) AsObject(ctx Context) *Object {
	return &Object{
		class: ctx.ClassByName("stdClass"),
		props: HashTable[String, Value]{
			internal: map[String]*htValue[Value]{},
		},
	}
}
func (n Null) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", n.Type().shape.String(), t.String()))
	}
}
func (n Null) DebugInfo(Context) String { return "NULL" }

type Array struct {
	// Value type is Ref because assigning a value to map even in go stdlib is done through returning a pointer to new value in map.
	// We should do something similar and keep in mind the data evacuation in go maps
	hash HashTable[Value, Value]
	next Int

	iterator struct {
		i    int
		iter Iterator
	}
}

func (a *Array) GetIterator(ctx Context) Iterator {
	if a.iterator.iter == nil {
		keys := a.hash.keys(func(x, y Value) int { return int(compare(ctx, x, y)) })

		a.iterator.iter = &InternalIterator[*Array]{
			this:      a,
			nextFn:    func(ctx Context, array *Array) { array.iterator.i++ },
			currentFn: func(ctx Context, array *Array) Value { return array.hash.internal[keys[array.iterator.i]].v },
			keyFn:     func(ctx Context, array *Array) Value { return keys[array.iterator.i] },
			validFn:   func(ctx Context, array *Array) Bool { return array.iterator.i < len(keys) },
			rewindFn:  func(ctx Context, array *Array) { array.iterator.i = 0 },
		}
	}

	return a.iterator.iter
}

func (a *Array) Count(Context) Int { return Int(len(a.hash.internal)) }

func (a *Array) Copy() *Array {
	return &Array{
		hash: HashTable[Value, Value]{maps.Clone(a.hash.internal)},
		next: a.next,
	}
}
func (a *Array) access(key Value) (Ref, bool) {
	v, ok := a.hash.access(key)
	return NewRef(v), ok
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

	switch key.Type().shape {
	case IntType, FloatType:
		defer func() {
			key = key.AsInt(ctx)
			a.next = key.(Int) + 1
		}()
	}

	return NewRef(a.hash.assign(key))
}
func (a *Array) delete(key Value) { a.hash.delete(key) }

func NewArray(init map[Value]Value, next ...Int) *Array {
	if len(next) == 0 {
		next = []Int{math.MinInt}
	}

	arr := &Array{hash: HashTable[Value, Value]{make(map[Value]*htValue[Value], len(init))}, next: next[0]}

	for k, v := range init {
		arr.hash.internal[k] = &htValue[Value]{v}
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

func (a *Array) IsRef() bool { return false }
func (a *Array) Type() Type  { return Type{shape: ArrayType} }
func (a *Array) AsInt(Context) Int {
	if len(a.hash.internal) > 0 {
		return 1
	}

	return 0
}
func (a *Array) AsFloat(Context) Float {
	if len(a.hash.internal) > 0 {
		return 1
	}

	return 0
}
func (a *Array) AsBool(Context) Bool { return len(a.hash.internal) > 0 }
func (a *Array) AsString(ctx Context) String {
	ctx.Throw(NewThrowable("array to string conversion", EWarning))
	return "Array"
}
func (a *Array) AsNull(Context) Null    { return Null{} }
func (a *Array) AsArray(Context) *Array { return a }
func (a *Array) AsObject(ctx Context) *Object {
	props := make(map[String]Value, len(a.hash.internal))

	for k, v := range a.hash.internal {
		props[k.AsString(ctx)] = v.v
	}

	//return NewObject(ctx.ClassByName("stdClass"), props)
	return nil
}
func (a *Array) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", a.Type().shape.String(), t.String()))
	}
}
func (a *Array) NextKey() Value {
	if a.next == math.MinInt {
		return Int(0)
	}

	return a.next
}
func (a *Array) DebugInfo(ctx Context) String {
	var str strings.Builder
	str.WriteString(fmt.Sprintf("array(%d) {", len(a.hash.internal)))

	for _, key := range a.hash.keys(func(x, y Value) int { return int(compare(ctx, x, y)) }) {
		str.WriteString(stringIndent(fmt.Sprintf("\n[%v]=>\n", key)+string(a.hash.internal[key].v.DebugInfo(ctx)), 2))
	}

	str.WriteString("\n}")

	return String(str.String())
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
func (r Ref) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", r.Type().shape.String(), t.String()))
	}
}
func (r Ref) DebugInfo(ctx Context) String { return "&" + (*r.Deref()).DebugInfo(ctx) }

type Object struct {
	class Class
	props HashTable[String, Value]
}

func NewObject(class Class, init HashTable[String, Value]) *Object {
	return &Object{class: class, props: init}
}

func (o *Object) Count(ctx Context) Int {
	if _, ok := o.class.(Countable); ok {
		return o.class.(Countable).Count(ctx)
	}

	panic("does not implement countable")
}
func (o *Object) Invoke(ctx Context)          { o.class.Invoke(ctx, o) }
func (o *Object) IsRef() bool                 { return false }
func (o *Object) AsInt(Context) Int           { return 1 }
func (o *Object) AsFloat(Context) Float       { return 1 }
func (o *Object) AsBool(Context) Bool         { return true }
func (o *Object) AsString(ctx Context) String { return o.class.ToString(ctx, o) }
func (o *Object) AsNull(Context) Null         { return Null{} }
func (o *Object) Type() Type                  { return Type{class: o.class, shape: ObjectType} }
func (o *Object) AsArray(Context) *Array {
	arr := make(map[Value]Value, len(o.props.internal))

	for k, v := range o.props.internal {
		arr[k] = v.v
	}

	return NewArray(arr)
}
func (o *Object) AsObject(Context) *Object { return o }
func (o *Object) Cast(ctx Context, t TypeShape) Value {
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
		panic(fmt.Sprintf("cannot cast %s to %s", o.Type().shape.String(), t.String()))
	}
}
func (o *Object) DebugInfo(ctx Context) String { return o.class.DebugInfo(ctx, o) }

func stringIndent(s string, n int) string {
	return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", n))
}
