package vm

import "fmt"

// Битовые операции PHP 8.3 (сверено с живым php):
//
//   & | ^   — если оба операнда строки, работаем побайтово; иначе оба
//             приводятся к int (bool/null -> 0/1, float -> trunc по правилам
//             (int)-каста, numeric-строка -> int). Строка без числового
//             префикса и array/object дают TypeError.
//   ~       — int -> ^int64; float -> ^trunc; string -> побайтовое
//             дополнение; bool/null/array/object -> TypeError.
//   << >>   — только int-домен (bool/null/float/numeric-строка -> int,
//             остальные строки и array/object -> TypeError). Отрицательный
//             сдвиг -> ArithmeticError. Сдвиг >= 64: << даёт 0, >> насыщается
//             знаковым битом (-1 для отрицательных, 0 иначе).

const (
	bitAnd = byte('&')
	bitOr  = byte('|')
	bitXor = byte('^')
)

// BitwiseBinary выполняет &, |, ^ над двумя слотами.
func BitwiseBinary(a, b Slot, op byte) (Slot, error) {
	a, b = deref(a), deref(b)
	if a.typ == StringType && b.typ == StringType {
		return MakeString(bitwiseStrings(a.StringVal(), b.StringVal(), op)), nil
	}
	ai, aok := bitwiseToInt(a)
	bi, bok := bitwiseToInt(b)
	if !aok || !bok {
		return Slot{}, bitwiseTypeError(a, b, bitwiseOpName(op))
	}
	switch op {
	case bitAnd:
		return MakeInt(ai & bi), nil
	case bitOr:
		return MakeInt(ai | bi), nil
	case bitXor:
		return MakeInt(ai ^ bi), nil
	}
	return Slot{}, fmt.Errorf("phpvm: неизвестная битовая операция")
}

// BitNot выполняет унарное ~ над слотом.
func BitNot(a Slot) (Slot, error) {
	a = deref(a)
	switch a.typ {
	case IntType:
		return MakeInt(^a.Int()), nil
	case FloatType:
		return MakeInt(^floatToInt(a.Float())), nil
	case StringType:
		s := a.StringVal()
		buf := make([]byte, len(s))
		for i := 0; i < len(s); i++ {
			buf[i] = ^s[i]
		}
		return MakeString(string(buf)), nil
	case BoolType:
		return Slot{}, NewThrowable("TypeError", "Cannot perform bitwise not on "+boolWord(a))
	case NullType:
		return Slot{}, NewThrowable("TypeError", "Cannot perform bitwise not on null")
	}
	return Slot{}, NewThrowable("TypeError", "Cannot perform bitwise not on "+operandTypeName(a))
}

// ShiftLeft выполняет <<.
func ShiftLeft(a, b Slot) (Slot, error) { return shift(a, b, true) }

// ShiftRight выполняет >>.
func ShiftRight(a, b Slot) (Slot, error) { return shift(a, b, false) }

func shift(a, b Slot, left bool) (Slot, error) {
	a, b = deref(a), deref(b)
	av, aok := shiftToInt(a)
	bv, bok := shiftToInt(b)
	opName := ">>"
	if left {
		opName = "<<"
	}
	if !aok || !bok {
		return Slot{}, bitwiseTypeError(a, b, opName)
	}
	if bv < 0 {
		return Slot{}, NewThrowable("ArithmeticError", "Bit shift by negative number")
	}
	if bv >= 64 {
		if left || av >= 0 {
			return MakeInt(0), nil
		}
		return MakeInt(-1), nil
	}
	if left {
		return MakeInt(av << uint(bv)), nil
	}
	return MakeInt(av >> uint(bv)), nil
}

// bitwiseStrings выполняет побайтовую операцию над строками.
//
// Результат: для & и ^ — длина min(len(a), len(b)); для | — длина
// max(len(a), len(b)), недостающие байты считаются нулями.
func bitwiseStrings(a, b string, op byte) string {
	if op == bitOr {
		n := len(a)
		if len(b) > n {
			n = len(b)
		}
		buf := make([]byte, n)
		for i := 0; i < n; i++ {
			var x, y byte
			if i < len(a) {
				x = a[i]
			}
			if i < len(b) {
				y = b[i]
			}
			buf[i] = x | y
		}
		return string(buf)
	}
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	buf := make([]byte, n)
	for i := 0; i < n; i++ {
		if op == bitAnd {
			buf[i] = a[i] & b[i]
		} else {
			buf[i] = a[i] ^ b[i]
		}
	}
	return string(buf)
}

// bitwiseToInt приводит операнд &,|,^ к int. ok == false означает TypeError.
func bitwiseToInt(s Slot) (int64, bool) {
	switch s.typ {
	case IntType:
		return s.Int(), true
	case BoolType:
		return int64(s.num), true
	case FloatType:
		return floatToInt(s.Float()), true
	case NullType:
		return 0, true
	case StringType:
		if _, _, ok := numericPrefix(s.StringVal()); !ok {
			return 0, false
		}
		return stringToInt(s.StringVal()), true
	}
	return 0, false
}

// shiftToInt приводит операнд <<,>> к int. Строки без числового префикса и
// array/object недопустимы.
func shiftToInt(s Slot) (int64, bool) {
	switch s.typ {
	case IntType:
		return s.Int(), true
	case BoolType:
		return int64(s.num), true
	case FloatType:
		return floatToInt(s.Float()), true
	case NullType:
		return 0, true
	case StringType:
		if _, _, ok := numericPrefix(s.StringVal()); !ok {
			return 0, false
		}
		return stringToInt(s.StringVal()), true
	}
	return 0, false
}

// bitwiseTypeError строит PHP-сообщение "Unsupported operand types: X op Y".
func bitwiseTypeError(a, b Slot, op string) error {
	// Zend для `*`, `&`, `|`, `^` печатает операнды упорядоченными по «весу»:
	// объект/замыкание раньше ресурса, ресурс раньше остальных. `[] * new
	// stdClass` → "stdClass * array", `[] * STDOUT` → "resource * array",
	// `[] * "foo"` → "array * string".
	switch op {
	case "*", "&", "|", "^":
		if msgTypeRank(b) > msgTypeRank(a) {
			a, b = b, a
		}
	}
	return NewThrowable("TypeError", fmt.Sprintf(
		"Unsupported operand types: %s %s %s", operandTypeName(a), op, operandTypeName(b)))
}

// msgTypeRank — порядок операндов в сообщении "Unsupported operand types"
// для `*`/`&`/`|`/`^`: объект/замыкание (2) > ресурс (1) > прочие (0).
func msgTypeRank(s Slot) int {
	switch deref(s).typ {
	case ObjectType, ClosureType:
		return 2
	case ResourceType:
		return 1
	}
	return 0
}

// operandTypeName возвращает имя типа PHP (для объектов — имя класса).
func operandTypeName(s Slot) string {
	if s.typ == ObjectType {
		if o := (*Object)(s.ptr); o != nil && o.class != nil {
			return o.class.Name()
		}
		return "object"
	}
	return s.typ.String()
}

func boolWord(s Slot) string {
	if s.ToBool() {
		return "true"
	}
	return "false"
}

// bitwiseOpName возвращает текстовый символ бинарной операции для сообщения.
func bitwiseOpName(op byte) string {
	switch op {
	case bitAnd:
		return "&"
	case bitOr:
		return "|"
	case bitXor:
		return "^"
	}
	return "?"
}
