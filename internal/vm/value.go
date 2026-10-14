package vm

import (
	"math"
)

// Value — ABI-граница для встроенных функций и расширений.
//
// Внутри VM работает Slot (typ + num + ptr, см. slot.go). На границе вызова
// встроенной функции слот передаётся как Value (интерфейс), а результат
// приводится обратно к Slot.
//
// ВАЖНО про цену границы (измерено): Slot — 24 байта, он НЕ влезает в одно
// машинное слово, поэтому упаковка Slot КАК ЕСТЬ в Value аллоцирует (direct
// interface не срабатывает). Чтобы граница была бесплатной, слот конвертируют
// в конкретный 8-байтовый тип (Int64/Float64/Bool/…Ptr). Здесь Value реализует
// Slot напрямую — это удобно, но для горячих встроенных лучше конкретные типы.
type Value interface {
	Type() Type
	ToBool() bool
	ToInt() int64
	ToFloat() float64
	ToString() string
	Cast(t Type) Value
}

// Cast приводит слот к заданному типу PHP.
func (s Slot) Cast(t Type) Value {
	s = deref(s)
	if s.typ == t {
		return s
	}
	switch t {
	case NullType:
		return MakeNull()
	case BoolType:
		return MakeBool(s.ToBool())
	case IntType:
		return MakeInt(s.ToInt())
	case FloatType:
		return MakeFloat(s.ToFloat())
	case StringType:
		return MakeString(s.ToString())
	}
	return s
}

// SlotOf записывает Value (границу ABI) прямо в поля существующего слота dst
// (тип + значение), без создания временного Slot и копии.
func SlotOf(dst *Slot, v Value) {
	if v == nil {
		dst.typ, dst.num, dst.ptr = NullType, 0, nil
		return
	}
	if s, ok := v.(Slot); ok {
		dst.typ, dst.num, dst.ptr = s.typ, s.num, s.ptr
		return
	}
	switch v.Type() {
	case IntType:
		dst.typ, dst.num, dst.ptr = IntType, uint64(v.ToInt()), nil
	case FloatType:
		dst.typ, dst.num, dst.ptr = FloatType, math.Float64bits(v.ToFloat()), nil
	case BoolType:
		dst.typ, dst.num, dst.ptr = BoolType, b2i(v.ToBool()), nil
	case StringType:
		*dst = MakeString(v.ToString())
	default:
		dst.typ, dst.num, dst.ptr = NullType, 0, nil
	}
}

// Проверка на этапе компиляции, что Slot удовлетворяет Value.
var _ Value = Slot{}
