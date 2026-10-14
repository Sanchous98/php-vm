package vm

import "math"

// PHP-типы как Go-типы — ABI-значения. НЕ знают, как хранятся (нет Pack/Slot):
// знают только свой PHP-тип и умеют конвертироваться. В слот их упаковывает
// фреймворк (SlotOf), а не сами значения.
type Int int64
type Float float64
type Bool bool
type Str string

func (Int) Type() Type   { return IntType }
func (Float) Type() Type { return FloatType }
func (Bool) Type() Type  { return BoolType }
func (Str) Type() Type   { return StringType }

func (v Int) ToBool() bool      { return v != 0 }
func (v Int) ToInt() int64      { return int64(v) }
func (v Int) ToFloat() float64  { return float64(v) }
func (v Int) ToString() string  { return intToString(int64(v)) }
func (v Int) Cast(t Type) Value { return Slot{typ: IntType, num: uint64(v)}.Cast(t) }

func (v Float) ToBool() bool     { return float64(v) != 0 }
func (v Float) ToInt() int64     { return floatToInt(float64(v)) }
func (v Float) ToFloat() float64 { return float64(v) }
func (v Float) ToString() string { return formatFloat(float64(v)) }
func (v Float) Cast(t Type) Value {
	return Slot{typ: FloatType, num: math.Float64bits(float64(v))}.Cast(t)
}

func (v Bool) ToBool() bool      { return bool(v) }
func (v Bool) ToInt() int64      { return int64(b2i(bool(v))) }
func (v Bool) ToFloat() float64  { return float64(b2i(bool(v))) }
func (v Bool) ToString() string  { return b2s(bool(v)) }
func (v Bool) Cast(t Type) Value { return Slot{typ: BoolType, num: b2i(bool(v))}.Cast(t) }

func (v Str) ToBool() bool      { return len(v) > 0 && v != "0" }
func (v Str) ToInt() int64      { return stringToInt(string(v)) }
func (v Str) ToFloat() float64  { f, _, _ := parseLeadingNumeric(string(v)); return f }
func (v Str) ToString() string  { return string(v) }
func (v Str) Cast(t Type) Value { return MakeString(string(v)).Cast(t) }
