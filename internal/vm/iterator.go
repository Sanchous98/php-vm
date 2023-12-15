package vm

type Iterator interface {
	Value

	Next(Context)
	Current(Context) Value
	Key(Context) Value
	Rewind(Context)
	Valid(Context) Bool
}

type IteratorAggregate interface {
	GetIterator(Context) Iterator
}

type InternalIterator[T Value] struct {
	this      T
	nextFn    func(Context, T)
	currentFn func(Context, T) Value
	keyFn     func(Context, T) Value
	rewindFn  func(Context, T)
	validFn   func(Context, T) Bool
}

func (i InternalIterator[T]) Compare(ctx Context, v Value) Int {
	return any(i.this).(Comparable).Compare(ctx, v)
}
func (i InternalIterator[T]) Equal(ctx Context, v Value) Bool {
	return any(i.this).(Comparable).Equal(ctx, v)
}
func (i InternalIterator[T]) Identical(v Value) Bool {
	return any(i.this).(Identifiable).Identical(v)
}
func (i InternalIterator[T]) Add(ctx Context, v Value) Value {
	return any(i.this).(SupportsMath).Add(ctx, v)
}
func (i InternalIterator[T]) Sub(ctx Context, v Value) Value {
	return any(i.this).(SupportsMath).Sub(ctx, v)
}
func (i InternalIterator[T]) Mul(ctx Context, v Value) Value {
	return any(i.this).(SupportsMath).Mul(ctx, v)
}
func (i InternalIterator[T]) Div(ctx Context, v Value) Value {
	return any(i.this).(SupportsMath).Div(ctx, v)
}
func (i InternalIterator[T]) Mod(ctx Context, v Value) Value {
	return any(i).(SupportsMath).Mod(ctx, v)
}
func (i InternalIterator[T]) Pow(ctx Context, v Value) Value {
	return any(i.this).(SupportsMath).Pow(ctx, v)
}
func (i InternalIterator[T]) ShiftLeft(ctx Context, v Value) Int {
	return any(i.this).(SupportsBits).ShiftLeft(ctx, v)
}
func (i InternalIterator[T]) ShiftRight(ctx Context, v Value) Int {
	return any(i.this).(SupportsBits).ShiftRight(ctx, v)
}
func (i InternalIterator[T]) BwAnd(ctx Context, v Value) Int {
	return any(i.this).(SupportsBits).BwAnd(ctx, v)
}
func (i InternalIterator[T]) BwOr(ctx Context, v Value) Int {
	return any(i.this).(SupportsBits).BwOr(ctx, v)
}
func (i InternalIterator[T]) BwXor(ctx Context, v Value) Int {
	return any(i.this).(SupportsBits).BwXor(ctx, v)
}
func (i InternalIterator[T]) BwNot(ctx Context) Int { return any(i.this).(SupportsBits).BwNot(ctx) }

func (i InternalIterator[T]) IsRef() bool                    { return i.this.IsRef() }
func (i InternalIterator[T]) AsInt(ctx Context) Int          { return i.this.AsInt(ctx) }
func (i InternalIterator[T]) AsFloat(ctx Context) Float      { return i.this.AsFloat(ctx) }
func (i InternalIterator[T]) AsBool(ctx Context) Bool        { return i.this.AsBool(ctx) }
func (i InternalIterator[T]) AsString(ctx Context) String    { return i.this.AsString(ctx) }
func (i InternalIterator[T]) AsNull(ctx Context) Null        { return i.this.AsNull(ctx) }
func (i InternalIterator[T]) AsArray(ctx Context) *Array     { return i.this.AsArray(ctx) }
func (i InternalIterator[T]) Cast(ctx Context, t Type) Value { return i.this.Cast(ctx, t) }
func (i InternalIterator[T]) Type() Type                     { return i.this.Type() }
func (i InternalIterator[T]) DebugInfo(ctx Context) String   { return i.this.DebugInfo(ctx) }

func (i InternalIterator[T]) Next(ctx Context)          { i.nextFn(ctx, i.this) }
func (i InternalIterator[T]) Current(ctx Context) Value { return i.currentFn(ctx, i.this) }
func (i InternalIterator[T]) Key(ctx Context) Value     { return i.keyFn(ctx, i.this) }
func (i InternalIterator[T]) Rewind(ctx Context)        { i.rewindFn(ctx, i.this) }
func (i InternalIterator[T]) Valid(ctx Context) Bool    { return i.validFn(ctx, i.this) }
