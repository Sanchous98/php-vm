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

func (i InternalIterator[T]) IsRef() bool                         { return i.this.IsRef() }
func (i InternalIterator[T]) AsInt(ctx Context) Int               { return i.this.AsInt(ctx) }
func (i InternalIterator[T]) AsFloat(ctx Context) Float           { return i.this.AsFloat(ctx) }
func (i InternalIterator[T]) AsBool(ctx Context) Bool             { return i.this.AsBool(ctx) }
func (i InternalIterator[T]) AsString(ctx Context) String         { return i.this.AsString(ctx) }
func (i InternalIterator[T]) AsNull(ctx Context) Null             { return i.this.AsNull(ctx) }
func (i InternalIterator[T]) AsArray(ctx Context) *Array          { return i.this.AsArray(ctx) }
func (i InternalIterator[T]) AsObject(ctx Context) *Object        { return i.this.AsObject(ctx) }
func (i InternalIterator[T]) Cast(ctx Context, t TypeShape) Value { return i.this.Cast(ctx, t) }
func (i InternalIterator[T]) Type() Type                          { return i.this.Type() }
func (i InternalIterator[T]) DebugInfo(ctx Context) String        { return i.this.DebugInfo(ctx) }

func (i InternalIterator[T]) Next(ctx Context)          { i.nextFn(ctx, i.this) }
func (i InternalIterator[T]) Current(ctx Context) Value { return i.currentFn(ctx, i.this) }
func (i InternalIterator[T]) Key(ctx Context) Value     { return i.keyFn(ctx, i.this) }
func (i InternalIterator[T]) Rewind(ctx Context)        { i.rewindFn(ctx, i.this) }
func (i InternalIterator[T]) Valid(ctx Context) Bool    { return i.validFn(ctx, i.this) }
