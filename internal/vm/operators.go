package vm

type Identifiable interface {
	Identical(Value) Bool
}

type Comparable interface {
	Compare(Context, Value) Int
	Equal(Context, Value) Bool
}

type Addable interface {
	Add(Context, Value) Value
}

type SupportsMath interface {
	Addable

	Sub(Context, Value) Value
	Mul(Context, Value) Value
	Div(Context, Value) Value
	Mod(Context, Value) Value
	Pow(Context, Value) Value
}

type SupportsBits interface {
	ShiftLeft(Context, Value) Int
	ShiftRight(Context, Value) Int
	BwAnd(Context, Value) Int
	BwOr(Context, Value) Int
	BwXor(Context, Value) Int
	BwNot(Context) Int
}
