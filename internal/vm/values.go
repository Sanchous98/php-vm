package vm

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

type Value interface {
	IsRef() bool
	AsInt(Context) Int
	AsFloat(Context) Float
	AsBool(Context) Bool
	AsString(Context) String
	AsNull(Context) Null
	AsArray(Context) *Array
	Cast(Context, Type) Value
	Type() Type
	DebugInfo(Context) String
}

func intSign[T ~int | ~byte](x T) T { return (x >> 63) | T(uint(-x)>>63) }

func compare(ctx Context, x, y Value) Int {
	typeX := x.Type()
	typeY := y.Type()

	if typeX != typeY {
		if typeX == ObjectType {
			return +1
		} else if typeY == ObjectType {
			return -1
		} else if typeX == ArrayType {
			return +1
		} else if typeY == ArrayType {
			return -1
		}
	}

	switch Juggle(typeX, typeY) {
	case IntType:
		return intSign(x.AsInt(ctx) - y.AsInt(ctx))
	case FloatType:
		return intSign(Int(x.AsFloat(ctx) - y.AsFloat(ctx)))
	case StringType:
		return Int(strings.Compare(string(x.AsString(ctx)), string(y.AsString(ctx))))
	case NullType:
		// Is possible if only both are null, so always equal
	case BoolType:
		return x.AsBool(ctx).AsInt(ctx) - y.AsBool(ctx).AsInt(ctx)
	case ArrayType:
		return hashCompare(ctx, x.AsArray(ctx).hash, y.AsArray(ctx).hash)
	}

	return 0
}

type Int int

func (i Int) ShiftLeft(ctx Context, v Value) Int  { return i << v.AsInt(ctx) }
func (i Int) ShiftRight(ctx Context, v Value) Int { return i >> v.AsInt(ctx) }
func (i Int) BwAnd(ctx Context, v Value) Int      { return i & v.AsInt(ctx) }
func (i Int) BwOr(ctx Context, v Value) Int       { return i | v.AsInt(ctx) }
func (i Int) BwXor(ctx Context, v Value) Int      { return i ^ v.AsInt(ctx) }
func (i Int) BwNot(Context) Int                   { return ^i }
func (i Int) Identical(v Value) Bool              { return i == v }
func (i Int) Compare(ctx Context, v Value) Int    { return compare(ctx, i, v) }
func (i Int) Equal(ctx Context, v Value) Bool {
	if i.Identical(v) {
		return true
	}

	as := Juggle(IntType, v.Type())
	return i.Cast(ctx, as).(Comparable).Compare(ctx, v.Cast(ctx, as)) == 0
}
func (i Int) Add(ctx Context, v Value) Value {
	if v.Type() == FloatType {
		return Float(i) + v.AsFloat(ctx)
	}

	return i + v.AsInt(ctx)
}
func (i Int) Sub(ctx Context, v Value) Value {
	if v.Type() == FloatType {
		return Float(i) - v.AsFloat(ctx)
	}

	return i - v.AsInt(ctx)
}
func (i Int) Mul(ctx Context, v Value) Value {
	if v.Type() == FloatType {
		return Float(i) * v.AsFloat(ctx)
	}

	return i * v.AsInt(ctx)
}
func (i Int) Div(ctx Context, v Value) Value {
	res := Float(i) / v.AsFloat(ctx)

	if v.Type() == FloatType || Float(Int(res)) != res {
		return res
	}

	return Int(res)
}
func (i Int) Mod(ctx Context, v Value) Value {
	return Float(math.Mod(float64(i), float64(v.AsFloat(ctx)))).Cast(ctx, Juggle(IntType, v.Type()))
}
func (i Int) Pow(ctx Context, v Value) Value {
	return Float(math.Pow(float64(i), float64(v.AsFloat(ctx)))).Cast(ctx, Juggle(IntType, v.Type()))
}

func (i Int) IsRef() bool             { return false }
func (i Int) Type() Type              { return IntType }
func (i Int) AsInt(Context) Int       { return i }
func (i Int) AsFloat(Context) Float   { return Float(i) }
func (i Int) AsBool(Context) Bool     { return i != 0 }
func (i Int) AsString(Context) String { return String(strconv.Itoa(int(i))) }
func (i Int) AsNull(Context) Null     { return Null{} }
func (i Int) AsArray(Context) *Array  { return NewArray(map[Value]Value{String("scalar"): i}) }
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
		panic(fmt.Errorf("cannot cast %s to %s", i.Type().String(), t.String()))
	}
}
func (i Int) DebugInfo(Context) String { return "int(" + String(strconv.Itoa(int(i))) + ")" }

type Float float64

func (f Float) ShiftLeft(ctx Context, v Value) Int  { return f.AsInt(ctx) << v.AsInt(ctx) }
func (f Float) ShiftRight(ctx Context, v Value) Int { return f.AsInt(ctx) >> v.AsInt(ctx) }
func (f Float) BwAnd(ctx Context, v Value) Int      { return f.AsInt(ctx) & v.AsInt(ctx) }
func (f Float) BwOr(ctx Context, v Value) Int       { return f.AsInt(ctx) | v.AsInt(ctx) }
func (f Float) BwXor(ctx Context, v Value) Int      { return f.AsInt(ctx) ^ v.AsInt(ctx) }
func (f Float) BwNot(ctx Context) Int               { return ^f.AsInt(ctx) }
func (f Float) Identical(v Value) Bool              { return f == v }
func (f Float) Compare(ctx Context, v Value) Int    { return compare(ctx, f, v) }
func (f Float) Equal(ctx Context, v Value) Bool {
	if f.Identical(v) {
		return true
	}

	as := Juggle(IntType, v.Type())
	return f.Cast(ctx, as).(Comparable).Compare(ctx, v.Cast(ctx, as)) == 0
}
func (f Float) Add(ctx Context, v Value) Value { return f + v.AsFloat(ctx) }
func (f Float) Sub(ctx Context, v Value) Value { return f - v.AsFloat(ctx) }
func (f Float) Mul(ctx Context, v Value) Value { return f * v.AsFloat(ctx) }
func (f Float) Div(ctx Context, v Value) Value { return f / v.AsFloat(ctx) }
func (f Float) Mod(ctx Context, v Value) Value {
	return Float(math.Mod(float64(f), float64(v.AsFloat(ctx))))
}
func (f Float) Pow(ctx Context, v Value) Value {
	return Float(math.Pow(float64(f), float64(v.AsFloat(ctx))))
}

func (f Float) IsRef() bool           { return false }
func (f Float) Type() Type            { return FloatType }
func (f Float) AsInt(Context) Int     { return Int(f) }
func (f Float) AsFloat(Context) Float { return f }
func (f Float) AsBool(Context) Bool   { return f != 0 }
func (f Float) AsString(Context) String {
	return String(strconv.FormatFloat(float64(f), 'g', -1, 64))
}
func (f Float) AsNull(Context) Null    { return Null{} }
func (f Float) AsArray(Context) *Array { return NewArray(map[Value]Value{String("scalar"): f}) }
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
		panic(fmt.Sprintf("cannot cast %s to %s", f.Type().String(), t.String()))
	}
}
func (f Float) DebugInfo(Context) String {
	return "float(" + String(strconv.FormatFloat(float64(f), 'g', -1, 64)) + ")"
}

type Bool bool

func (b Bool) Add(ctx Context, v Value) Value   { return b.AsInt(ctx).Add(ctx, v) }
func (b Bool) Sub(ctx Context, v Value) Value   { return b.AsInt(ctx).Sub(ctx, v) }
func (b Bool) Mul(ctx Context, v Value) Value   { return b.AsInt(ctx).Mul(ctx, v) }
func (b Bool) Div(ctx Context, v Value) Value   { return b.AsInt(ctx).Div(ctx, v) }
func (b Bool) Mod(ctx Context, v Value) Value   { return b.AsInt(ctx).Mod(ctx, v) }
func (b Bool) Pow(ctx Context, v Value) Value   { return b.AsInt(ctx).Pow(ctx, v) }
func (b Bool) Identical(v Value) Bool           { return b == v }
func (b Bool) Compare(ctx Context, v Value) Int { return compare(ctx, b, v) }
func (b Bool) Equal(ctx Context, v Value) Bool {
	if b.Identical(v) {
		return true
	}

	as := Juggle(BoolType, v.Type())
	return b.Cast(ctx, as).(Comparable).Compare(ctx, v.Cast(ctx, as)) == 0
}

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
func (b Bool) AsBool(Context) Bool     { return b }
func (b Bool) AsString(Context) String { return String(strconv.FormatBool(bool(b))) }
func (b Bool) AsNull(Context) Null     { return Null{} }
func (b Bool) AsArray(Context) *Array  { return NewArray(map[Value]Value{String("scalar"): b}) }
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
		panic(fmt.Sprintf("cannot cast %s to %s", b.Type().String(), t.String()))
	}
}
func (b Bool) DebugInfo(Context) String {
	return "bool(" + String(strconv.FormatBool(bool(b))) + ")"
}

type String string

func (s String) Identical(v Value) Bool           { return s == v }
func (s String) Compare(ctx Context, v Value) Int { return compare(ctx, s, v) }
func (s String) Equal(ctx Context, v Value) Bool {
	if s.Identical(v) {
		return true
	}

	as := Juggle(StringType, v.Type())
	return s.Cast(ctx, as).(Comparable).Compare(ctx, v.Cast(ctx, as)) == 0
}

func (s String) IsRef() bool { return false }
func (s String) Type() Type  { return StringType }
func (s String) AsInt(Context) Int {
	v, _ := strconv.Atoi(string(s))
	return Int(v)
}
func (s String) AsFloat(Context) Float {
	v, _ := strconv.ParseFloat(string(s), 64)
	return Float(v)
}
func (s String) AsBool(Context) Bool             { return len(s) > 0 && s != "0" }
func (s String) AsString(Context) String         { return s }
func (s String) AsNull(Context) Null             { return Null{} }
func (s String) AsArray(Context) *Array          { return NewArray(map[Value]Value{String("scalar"): s}) }
func (s String) AsCallable(ctx Context) Callable { return ctx.FunctionByName(s) }
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
		panic(fmt.Sprintf("cannot cast %s to %s", s.Type().String(), t.String()))
	}
}
func (s String) String() string           { return strconv.Quote(string(s)) }
func (s String) DebugInfo(Context) String { return "string(\"" + s + "\")" }

type Null struct{}

func (n Null) Identical(v Value) Bool           { return n == v }
func (n Null) Compare(ctx Context, v Value) Int { return compare(ctx, n, v) }
func (n Null) Equal(ctx Context, v Value) Bool {
	if n.Identical(v) {
		return true
	}

	as := Juggle(NullType, v.Type())
	return n.Cast(ctx, as).(Comparable).Compare(ctx, v.Cast(ctx, as)) == 0
}

func (n Null) IsRef() bool                 { return false }
func (n Null) Type() Type                  { return NullType }
func (n Null) AsInt(Context) Int           { return 0 }
func (n Null) AsFloat(Context) Float       { return 0 }
func (n Null) AsBool(Context) Bool         { return false }
func (n Null) AsString(Context) String     { return "" }
func (n Null) AsNull(Context) Null         { return n }
func (n Null) AsArray(Context) *Array      { return NewArray(nil) }
func (n Null) AsCallable(Context) Callable { panic("not callable") }
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
		panic(fmt.Sprintf("cannot cast %s to %s", n.Type().String(), t.String()))
	}
}
func (n Null) DebugInfo(Context) String { return "NULL" }

type Array struct {
	// Value type is Ref because assigning a value to map even in go stdlib is done through returning a pointer to new value in map.
	// We should do something similar and keep in mind the data evacuation in go maps
	hash HashTable
	next Int

	iterator struct {
		i    int
		iter Iterator
	}
}

func (a *Array) Add(ctx Context, v Value) Value {
	if v.Type() != ArrayType {
		// TODO: Fatal error
		return nil
	}

	result := a.hash.clone()
	op := v.AsArray(ctx)
	result.add(op.hash)
	return &Array{hash: result, next: max(a.next, op.next)}
}
func (a *Array) Identical(v Value) Bool {
	if a == v {
		return true
	}

	return Bool(v.Type() == ArrayType && a.hash.identical(v.(*Array).hash))
}
func (a *Array) Compare(ctx Context, v Value) Int { return compare(ctx, a, v) }
func (a *Array) Equal(ctx Context, v Value) Bool {
	if a.Identical(v) {
		return true
	}

	as := Juggle(ArrayType, v.Type())
	return a.Cast(ctx, as).(Comparable).Compare(ctx, v.Cast(ctx, as)) == 0
}

func (a *Array) GetIterator(ctx Context) Iterator {
	if a.iterator.iter == nil {
		keys := a.hash.getKeys(func(x, y Value) int { return int(x.(Comparable).Compare(ctx, y)) })

		a.iterator.iter = &InternalIterator[*Array]{
			this:      a,
			nextFn:    func(ctx Context, array *Array) { array.iterator.i++ },
			currentFn: func(ctx Context, array *Array) Value { return array.hash.values[array.iterator.i] },
			keyFn:     func(ctx Context, array *Array) Value { return keys[array.iterator.i] },
			validFn:   func(ctx Context, array *Array) Bool { return array.iterator.i < len(keys) },
			rewindFn:  func(ctx Context, array *Array) { array.iterator.i = 0 },
		}
	}

	return a.iterator.iter
}

func (a *Array) Copy() *Array {
	return &Array{hash: a.hash.clone(), next: a.next}
}
func (a *Array) access(key Value) (Ref, bool) {
	v, ok := a.hash.access(key)
	return Ref{v}, ok
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
		defer func() {
			key = key.AsInt(ctx)
			a.next = key.(Int) + 1
		}()
	}

	return Ref{a.hash.assign(key)}
}
func (a *Array) delete(key Value) { a.hash.delete(key) }

func NewArray(init map[Value]Value, next ...Int) *Array {
	if len(next) == 0 {
		next = []Int{math.MinInt}
	}
	return &Array{hash: NewHash(init), next: next[0]}
}

func (a *Array) IsRef() bool { return false }
func (a *Array) Type() Type  { return ArrayType }
func (a *Array) AsInt(Context) Int {
	if len(a.hash.values) > 0 {
		return 1
	}

	return 0
}
func (a *Array) AsFloat(Context) Float {
	if len(a.hash.values) > 0 {
		return 1
	}

	return 0
}
func (a *Array) AsBool(Context) Bool { return len(a.hash.values) > 0 }
func (a *Array) AsString(ctx Context) String {
	ctx.Throw(NewThrowable("array to string conversion", EWarning))
	return "Array"
}
func (a *Array) AsNull(Context) Null    { return Null{} }
func (a *Array) AsArray(Context) *Array { return a }
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
func (a *Array) DebugInfo(ctx Context) String {
	var str strings.Builder
	str.WriteString(fmt.Sprintf("array(%d) {", len(a.hash.values)))

	for key, i := range a.hash.keys {
		var dbg strings.Builder
		v := string(a.hash.values[i].DebugInfo(ctx))

		dbg.Grow(8 + int(Bool(a.hash.values[i].IsRef()).AsInt(ctx)) + len(v))
		dbg.WriteString("\n[%v]=>\n")

		if a.hash.values[i].IsRef() {
			dbg.WriteByte('&')
		}

		dbg.WriteString(v)
		str.WriteString(stringIndent(fmt.Sprintf(dbg.String(), key), 2))
	}

	str.WriteString("\n}")

	return String(str.String())
}

type Ref struct{ ref *Value }

func (r Ref) Compare(ctx Context, v Value) Int    { return (*r.ref).(Comparable).Compare(ctx, v) }
func (r Ref) Equal(ctx Context, v Value) Bool     { return (*r.ref).(Comparable).Equal(ctx, v) }
func (r Ref) Identical(v Value) Bool              { return (*r.ref).(Identifiable).Identical(v) }
func (r Ref) Add(ctx Context, v Value) Value      { return (*r.ref).(SupportsMath).Add(ctx, v) }
func (r Ref) Sub(ctx Context, v Value) Value      { return (*r.ref).(SupportsMath).Sub(ctx, v) }
func (r Ref) Mul(ctx Context, v Value) Value      { return (*r.ref).(SupportsMath).Mul(ctx, v) }
func (r Ref) Div(ctx Context, v Value) Value      { return (*r.ref).(SupportsMath).Div(ctx, v) }
func (r Ref) Mod(ctx Context, v Value) Value      { return (*r.ref).(SupportsMath).Mod(ctx, v) }
func (r Ref) Pow(ctx Context, v Value) Value      { return (*r.ref).(SupportsMath).Pow(ctx, v) }
func (r Ref) ShiftLeft(ctx Context, v Value) Int  { return (*r.ref).(SupportsBits).ShiftLeft(ctx, v) }
func (r Ref) ShiftRight(ctx Context, v Value) Int { return (*r.ref).(SupportsBits).ShiftRight(ctx, v) }
func (r Ref) BwAnd(ctx Context, v Value) Int      { return (*r.ref).(SupportsBits).BwAnd(ctx, v) }
func (r Ref) BwOr(ctx Context, v Value) Int       { return (*r.ref).(SupportsBits).BwOr(ctx, v) }
func (r Ref) BwXor(ctx Context, v Value) Int      { return (*r.ref).(SupportsBits).BwXor(ctx, v) }
func (r Ref) BwNot(ctx Context) Int               { return (*r.ref).(SupportsBits).BwNot(ctx) }

func NewRef(v *Value) Ref {
	if v == nil {
		n := Value(Null{})
		v = &n
	}

	return Ref{v}
}

func (r Ref) IsRef() bool                    { return true }
func (r Ref) Deref() *Value                  { return r.ref }
func (r Ref) Type() Type                     { return (*r.ref).Type() }
func (r Ref) AsInt(ctx Context) Int          { return (*r.ref).AsInt(ctx) }
func (r Ref) AsFloat(ctx Context) Float      { return (*r.ref).AsFloat(ctx) }
func (r Ref) AsBool(ctx Context) Bool        { return (*r.ref).AsBool(ctx) }
func (r Ref) AsString(ctx Context) String    { return (*r.ref).AsString(ctx) }
func (r Ref) AsNull(ctx Context) Null        { return (*r.ref).AsNull(ctx) }
func (r Ref) AsArray(ctx Context) *Array     { return (*r.ref).AsArray(ctx) }
func (r Ref) Cast(ctx Context, t Type) Value { return (*r.ref).Cast(ctx, t) }
func (r Ref) DebugInfo(ctx Context) String   { return (*r.ref).DebugInfo(ctx) }

func stringIndent(s string, n int) string {
	return strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", n))
}
