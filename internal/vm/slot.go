package vm

import (
	"math"
	"unsafe"
)

// Slot — слот стека VM: 24 байта (typ + num + ptr).
//
//   - typ — тег типа;
//   - num — скаляры: int64 / float64 (биты) / bool; для строк — длина
//     (младшие 62 бита), бит interned (старший) и бит buf (бит 62 — данные во
//     внутреннем буфере, см. strbuf.go). Без боксинга и аллокаций;
//   - ptr — heap-значение (Array / Object[T]) либо данные строки, стёртые до
//     unsafe.Pointer. nil для скаляров и пустой строки.
//
// ptr — настоящий указатель, поэтому GC видит heap-значения и владеет их
// временем жизни: удерживает, пока слот жив, и собирает (включая циклы), когда
// ссылок не осталось. Числа лежат в num и GC не сканируются — их нельзя
// принять за адрес. Свой heap/handle/free-list не нужен.
//
// Строка хранится прямо в слоте: пара (ptr, len) — ровно то, как Go хранит
// string. ptr — интерьерный указатель на данные строки, поэтому GC держит
// массив байт живым, пока жив слот (литералы в .rodata GC не сканирует).
type Slot struct {
	typ Type
	num uint64
	ptr unsafe.Pointer
}

// lenMask/internedBit/bufBit — разбор num у строкового слота: младшие 62 бита —
// длина, бит 63 — признак «interned» (литерал/константа), бит 62 — признак
// «буфер» (данные лежат в heap-буфере с заголовком ПЕРЕД ними; см. strbuf.go).
// Длина строки не превышает 2^62-1, поэтому оба бита свободны.
const (
	lenMask     = (uint64(1) << 62) - 1
	internedBit = uint64(1) << 63
	bufBit      = uint64(1) << 62
)

// --- Конструкторы ---

func MakeNull() Slot           { return Slot{typ: NullType} }
func MakeBool(b bool) Slot     { return Slot{typ: BoolType, num: b2i(b)} }
func MakeInt(i int64) Slot     { return Slot{typ: IntType, num: uint64(i)} }
func MakeFloat(f float64) Slot { return Slot{typ: FloatType, num: math.Float64bits(f)} }

// stringData — данные строки как unsafe.Pointer. Для пустой строки
// unsafe.StringData может вернуть nil — нормализуем в nil (GC-безопасно).
func stringData(s string) unsafe.Pointer {
	if len(s) == 0 {
		return nil
	}
	return unsafe.Pointer(unsafe.StringData(s))
}

// MakeString кладёт строку прямо в слот (без heap-бокса *String): num —
// длина, ptr — данные строки.
func MakeString(s string) Slot {
	return Slot{typ: StringType, num: uint64(len(s)), ptr: stringData(s)}
}

// MakeInternedString создаёт «interned» строку (литерал/константа): debug_zval_dump
// помечает такие строки как interned.
func MakeInternedString(s string) Slot {
	return Slot{typ: StringType, num: uint64(len(s)) | internedBit, ptr: stringData(s)}
}

// makeBufString собирает строковый слот-буфер: ptr — интерьерный указатель на
// данные внутри heap-буфера (заголовок лежит непосредственно перед данными),
// num = len | bufBit. Конструктор внутренний — создаётся только strbuf.go.
func makeBufString(ptr unsafe.Pointer, length int) Slot {
	return Slot{typ: StringType, num: uint64(length) | bufBit, ptr: ptr}
}

// StringSlotIsBuf сообщает, что строковый слот хранит данные в буфере,
// допускающем дописывание на месте (путь OpConcatApp/OpConcat).
func StringSlotIsBuf(s Slot) bool {
	s = deref(s)
	return s.typ == StringType && s.num&bufBit != 0
}

// StringSlotInterned сообщает, что слот — interned-строка.
func StringSlotInterned(s Slot) bool {
	s = deref(s)
	return s.typ == StringType && s.num&internedBit != 0
}

// MakeObject кладёт объект в слот. nil превращается в null. Индекс класса (num)
// выставляет вызывающий (см. vm.slotOf / objectSlot), т.к. Slot не знает VM.
func MakeObject(o *Object) Slot {
	if o == nil {
		return MakeNull()
	}
	return Slot{typ: ObjectType, ptr: unsafe.Pointer(o)}
}

// MakeObjectOf кладёт *Object в слот через дескриптор класса.
func MakeObjectOf(vm *VM, o *Object) Slot {
	if vm == nil {
		return MakeNull()
	}
	return vm.slotOf(o)
}

// MakeArray кладёт массив в слот. nil превращается в null.
func MakeArray(a *Array) Slot {
	if a == nil {
		return MakeNull()
	}
	return Slot{typ: ArrayType, ptr: unsafe.Pointer(a)}
}

// AsArray возвращает *Array из слота. Только для typ == ArrayType.
func (s Slot) AsArray() *Array { return (*Array)(deref(s).ptr) }

// --- Аксессоры ---

// Type возвращает тег типа слота. Для ссылок — тип значения внутри.
func (s Slot) Type() Type { return deref(s).typ }

// IsNull сообщает, что слот содержит null.
func (s Slot) IsNull() bool { return deref(s).typ == NullType }

// Raw возвращает сырые биты поля num. Для отладки и round-trip тестов.
func (s Slot) Raw() uint64 { return s.num }

// Int возвращает int64 без проверки типа. Только для typ == IntType.
func (s Slot) Int() int64 { return int64(deref(s).num) }

// Float возвращает float64 без проверки типа. Только для typ == FloatType.
func (s Slot) Float() float64 { return math.Float64frombits(deref(s).num) }

// BoolVal возвращает bool без проверки типа. Только для typ == BoolType.
func (s Slot) BoolVal() bool { return deref(s).num != 0 }

// StringVal возвращает строку. Только для typ == StringType.
func (s Slot) StringVal() string {
	s = deref(s)
	if s.ptr == nil {
		return ""
	}
	return unsafe.String((*byte)(s.ptr), int(s.num&lenMask))
}

// ReadOnlyBytes возвращает представление байтов строкового слота без копии
// (интерьерный указатель; валидно, пока жив слот). ТОЛЬКО ДЛЯ ЧТЕНИЯ: писать
// в общий буфер строки нельзя. Для не-строк ok=false.
func (s Slot) ReadOnlyBytes() ([]byte, bool) {
	s = deref(s)
	if s.typ != StringType || s.ptr == nil {
		return nil, false
	}
	return unsafe.Slice((*byte)(s.ptr), int(s.num&lenMask)), true
}

// IsNumeric сообщает, что слот — int или float.
func (s Slot) IsNumeric() bool {
	t := deref(s).typ
	return t == IntType || t == FloatType
}

// ObjectClassName возвращает имя класса Object-слота (для диагностик, как
// zval_value_name в Zend). Для не-объектов возвращает "".
func (s Slot) ObjectClassName() string {
	s = deref(s)
	if s.typ != ObjectType || s.ptr == nil {
		return ""
	}
	return (*Object)(s.ptr).ClassName()
}

// --- Конверсии (семантика PHP 8.3) ---

// ToBool — истинность по правилам PHP.
func (s Slot) ToBool() bool {
	s = deref(s)
	switch s.typ {
	case NullType:
		return false
	case BoolType:
		return s.num != 0
	case IntType:
		return int64(s.num) != 0
	case FloatType:
		return math.Float64frombits(s.num) != 0 // -0.0 -> false
	case StringType:
		str := s.StringVal()
		return len(str) > 0 && str != "0"
	case ArrayType:
		return s.AsArray().Len() > 0
	case ObjectType, ClosureType, ResourceType:
		return true
	}
	return false
}

// ToFloat — числовое значение по правилам PHP.
func (s Slot) ToFloat() float64 {
	s = deref(s)
	switch s.typ {
	case BoolType:
		return float64(s.num)
	case IntType:
		return float64(int64(s.num))
	case FloatType:
		return math.Float64frombits(s.num)
	case StringType:
		f, _, _ := parseLeadingNumeric(s.StringVal())
		return f
	case ResourceType:
		return float64(int64(s.num))
	}
	return 0
}

// ToInt — целое значение по правилам PHP.
func (s Slot) ToInt() int64 {
	s = deref(s)
	switch s.typ {
	case BoolType:
		return int64(s.num)
	case IntType:
		return int64(s.num)
	case FloatType:
		return floatToInt(math.Float64frombits(s.num))
	case StringType:
		return stringToInt(s.StringVal())
	case ResourceType:
		return int64(s.num)
	}
	return 0
}

// ToString — строковое представление по правилам PHP.
func (s Slot) ToString() string {
	s = deref(s)
	switch s.typ {
	case BoolType:
		return b2s(s.num != 0)
	case IntType:
		return intToString(int64(s.num))
	case FloatType:
		return formatFloat(math.Float64frombits(s.num))
	case StringType:
		return s.StringVal()
	case ClosureType:
		return "Closure"
	case ResourceType:
		return "Resource id #" + intToString(int64(s.num))
	}
	return ""
}

func b2i(b bool) uint64 {
	if b {
		return 1
	}
	return 0
}
