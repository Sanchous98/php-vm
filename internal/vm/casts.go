package vm

import (
	"math"
	"strconv"
	"strings"
)

// Семантика числовых конверсий PHP 8.3 (сверено с живым php, см. testdata).
//
// Два разных пути в int:
//
//   - из строки: leading-numeric -> double -> trunc -> clamp в [MinInt64, MaxInt64];
//     (int)"1e20" == MaxInt64.
//   - из float: trunc, а при выходе за диапазон — обёртка по модулю 2^64;
//     (int)1e20 == 7766279631452241920, (int)NAN == 0.
//
// Наивный Go-каст неверен в обоих случаях: int64(1e20) == MinInt64.

const (
	twoTo63 = 9223372036854775808.0  // 2^63
	twoTo64 = 18446744073709551616.0 // 2^64
)

// floatToInt повторяет (int)$float: NaN/Inf -> 0, иначе trunc, а вне
// диапазона int64 — обёртка по модулю 2^64.
func floatToInt(f float64) int64 {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0
	}
	t := math.Trunc(f)
	if t >= -twoTo63 && t < twoTo63 {
		return int64(t)
	}
	m := math.Mod(t, twoTo64) // лежит в (-2^64, 2^64)
	if m < 0 {
		m += twoTo64
	}
	if m >= twoTo63 {
		return int64(m - twoTo64)
	}
	return int64(m)
}

// stringToInt повторяет (int)$string: leading-numeric -> double -> trunc ->
// clamp. Для целых, помещающихся в int64, значение берётся точно.
func stringToInt(s string) int64 {
	i, isInt, ok := parseLeadingNumericInt(s)
	if !ok {
		return 0
	}
	if isInt {
		return i
	}
	f, _, _ := parseLeadingNumeric(s)
	t := math.Trunc(f)
	if t >= twoTo63 {
		return math.MaxInt64
	}
	if t < -twoTo63 {
		return math.MinInt64
	}
	return int64(t)
}

// parseLeadingNumeric разбирает ведущее число в строке s, допуская ведущие
// пробелы. Возвращает значение как float64, длину распознанного префикса и
// признак успеха. Соответствует zend_strtod-семантике: "12abc" -> 12,
// "0x1A" -> 0, ".5" -> 0.5, "1e2" -> 100.
func parseLeadingNumeric(s string) (val float64, n int, ok bool) {
	str, n, ok := numericPrefix(s)
	if !ok {
		return 0, 0, false
	}
	f, err := strconv.ParseFloat(str, 64)
	if err != nil {
		return 0, 0, false
	}
	return f, n, true
}

// parseLeadingNumericInt пытается получить точное целое из ведущего числа.
// isInt == true только если число целое, без точки и экспоненты, и помещается
// в int64 (тогда i точно и без потери точности).
func parseLeadingNumericInt(s string) (i int64, isInt bool, ok bool) {
	str, _, ok := numericPrefix(s)
	if !ok {
		return 0, false, false
	}
	if strings.IndexAny(str, ".eE") >= 0 {
		return 0, false, true
	}
	v, err := strconv.ParseInt(str, 10, 64)
	if err != nil {
		return 0, false, true // целое, но не влезло -> вызывающий clamp'ит
	}
	return v, true, true
}

// numericPrefix возвращает ведущий числовой префикс: подстроку, её длину в
// исходной строке и признак успеха. Реализует скан zend_strtod: опциональные
// пробелы, знак, мантисса (цифры, возможно с точкой), опциональная экспонента.
func numericPrefix(s string) (string, int, bool) {
	i := 0
	for i < len(s) && isPHPSpace(s[i]) {
		i++
	}
	start := i
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	digitsBefore := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		digitsBefore++
	}
	digitsAfter := 0
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			digitsAfter++
		}
	}
	if digitsBefore == 0 && digitsAfter == 0 {
		return "", 0, false
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		expDigits := 0
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
			expDigits++
		}
		if expDigits > 0 {
			i = j
		}
	}
	return s[start:i], i, true
}

// NumericStringSlot конвертирует строку-число (is_numeric) в int или float по
// правилам Zend: целое в диапазоне int64 -> int, иначе float. ok=false, если
// строка не является числовой целиком.
func NumericStringSlot(s string) (Slot, bool) {
	if !IsNumericString(s) {
		return Slot{}, false
	}
	if i, isInt, ok := parseLeadingNumericInt(s); ok && isInt {
		return MakeInt(i), true
	}
	f, _, _ := parseLeadingNumeric(s)
	return MakeFloat(f), true
}

// IsNumericString повторяет is_numeric для строк: строка целиком (с
// допустимыми обрамляющими пробелами) должна быть числом.
func IsNumericString(s string) bool {
	i := 0
	for i < len(s) && isPHPSpace(s[i]) {
		i++
	}
	_, n, ok := numericPrefix(s[i:])
	if !ok {
		return false
	}
	n += i
	for n < len(s) && isPHPSpace(s[n]) {
		n++
	}
	return n == len(s)
}

func isPHPSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\v', '\f':
		return true
	}
	return false
}

func intToString(i int64) string { return strconv.FormatInt(i, 10) }

func b2s(b bool) string {
	if b {
		return "1"
	}
	return ""
}

// floatPrecision — значение INI-опции precision для string-конверсии float.
var floatPrecision = 14

// SetFloatPrecision задаёт precision для formatFloat (из INI precision).
func SetFloatPrecision(p int) {
	if p < -1 {
		p = -1
	}
	floatPrecision = p
}

// formatFloat повторяет string-конверсию float в PHP (precision, zend_gcvt):
// экспонента в виде E+/E- без ведущих нулей, у мантиссы всегда есть точка,
// спецзначения INF/-INF/NAN.
func formatFloat(f float64) string {
	if math.IsInf(f, 0) {
		if f < 0 {
			return "-INF"
		}
		return "INF"
	}
	if math.IsNaN(f) {
		return "NAN"
	}
	prec := floatPrecision
	shortest := prec < 0
	if prec < 1 {
		// precision = 0 Zend трактует как одну значащую цифру.
		prec = 1
	}
	var mant string
	var decpt int
	if shortest {
		mant, decpt = shortestDigits(f)
	} else {
		mant, decpt = roundedDigits(f, prec)
		mant = trimFloatZeros(mant)
	}
	useExp := decpt < -3
	if shortest {
		useExp = useExp || decpt > 17
	} else {
		useExp = useExp || decpt > prec
	}
	neg := math.Signbit(f)
	body := ""
	if useExp {
		m := mant[:1]
		if len(mant) > 1 {
			m += "." + mant[1:]
		} else {
			m += ".0"
		}
		exp := decpt - 1
		sign := "+"
		if exp < 0 {
			sign = "-"
			exp = -exp
		}
		body = m + "E" + sign + strconv.Itoa(exp)
	} else {
		switch {
		case decpt <= 0:
			body = "0." + strings.Repeat("0", -decpt) + mant
		case decpt >= len(mant):
			body = mant + strings.Repeat("0", decpt-len(mant))
		default:
			body = mant[:decpt] + "." + mant[decpt:]
		}
	}
	if neg {
		return "-" + body
	}
	return body
}

// shortestDigits возвращает цифры кратчайшего round-trip представления |f| и
// позицию десятичной точки (decpt: значение = 0.<digits> * 10^decpt).
func shortestDigits(f float64) (string, int) {
	s := strconv.FormatFloat(math.Abs(f), 'e', -1, 64)
	return splitExpDigits(s)
}

// roundedDigits возвращает prec значащих цифр |f| (округление как в %.*e).
func roundedDigits(f float64, prec int) (string, int) {
	if prec <= 0 {
		prec = 1
	}
	s := strconv.FormatFloat(math.Abs(f), 'e', prec-1, 64)
	return splitExpDigits(s)
}

// splitExpDigits разбирает запись вида "1.234e+05" в цифры без точки и decpt.
func splitExpDigits(s string) (string, int) {
	i := strings.IndexAny(s, "eE")
	mant := s
	exp := 0
	if i >= 0 {
		mant = s[:i]
		exp, _ = strconv.Atoi(s[i+1:])
	}
	decpt := exp + 1
	if j := strings.IndexByte(mant, '.'); j >= 0 {
		decpt = j + exp
		mant = mant[:j] + mant[j+1:]
	}
	// Убираем ведущие нули (mant всегда начинается с ненулевой цифры для
	// ненулевого f; для нуля mant == "0").
	for len(mant) > 1 && mant[0] == '0' {
		mant = mant[1:]
	}
	return mant, decpt
}

// trimFloatZeros убирает незначащие нули в дробной части (Zend: dtoa mode 0
// печатает без хвостовых нулей).
func trimFloatZeros(digits string) string {
	if len(digits) <= 1 {
		return digits
	}
	i := len(digits)
	for i > 1 && digits[i-1] == '0' {
		i--
	}
	return digits[:i]
}

// FormatFloat форматирует float как echo/конкатенация (precision из INI).
func FormatFloat(f float64) string { return formatFloat(f) }

// CastKind — вид приведения типа для опкода OpCast.
type CastKind uint8

const (
	CastInt CastKind = iota
	CastFloat
	CastString
	CastBool
	CastArray
	CastObject
	CastUnset
)

// Cast — экспорт приведения типа по правилам PHP (для settype и т.п.).
func (vm *VM) Cast(s Slot, kind CastKind) Slot { return castSlot(vm, s, int(kind)) }

// CastToObject — экспорт (object)-приведения (stdClass).
func (vm *VM) CastToObject(s Slot) Slot { return castToObject(vm, s) }

// slotClassName — имя класса значения для диагностик конверсии объекта
// (Zend печатает имя класса, для анонимных — Parent@anonymous/class@anonymous).
func (vm *VM) slotClassName(s Slot) string {
	if s.typ == ClosureType {
		return "Closure"
	}
	if ci := vm.classInfo(s); ci != nil {
		return classDisplayName(ci)
	}
	return "object"
}

// castSlot выполняет приведение типа по правилам PHP. vm нужен для
// (object)-приведения (поиск stdClass) и объектных диагностик; может быть nil
// (тогда диагностики не печатаются).
func castSlot(vm *VM, s Slot, kind int) Slot {
	s = deref(s)
	switch CastKind(kind) {
	case CastInt:
		if s.typ == ArrayType {
			if s.AsArray().Len() > 0 {
				return MakeInt(1)
			}
			return MakeInt(0)
		}
		if s.typ == ObjectType || s.typ == ClosureType {
			// Zend: объект -> int даёт 1 с Warning (zval_get_long).
			if vm != nil {
				vm.Warn("Object of class " + vm.slotClassName(s) + " could not be converted to int")
			}
			return MakeInt(1)
		}
		return MakeInt(s.ToInt())
	case CastFloat:
		if s.typ == ArrayType {
			// Zend: непустой массив -> float даёт 1.0.
			if s.AsArray().Len() > 0 {
				return MakeFloat(1)
			}
			return MakeFloat(0)
		}
		if s.typ == ObjectType || s.typ == ClosureType {
			// Zend: объект -> float даёт 1.0 с Warning (zval_get_double).
			if vm != nil {
				vm.Warn("Object of class " + vm.slotClassName(s) + " could not be converted to float")
			}
			return MakeFloat(1)
		}
		return MakeFloat(s.ToFloat())
	case CastString:
		if s.typ == ArrayType {
			// Zend: Warning + строка "Array" (как echo конкатенация).
			if vm != nil {
				vm.Warn("Array to string conversion")
			}
			return MakeString("Array")
		}
		if s.typ == ObjectType && vm != nil {
			if str, ok := vm.tryToString(s); ok {
				return MakeString(str)
			}
		}
		if s.typ == ArrayType {
			// Zend: массив -> string даёт "Array" с Warning.
			if vm != nil {
				vm.Warn("Array to string conversion")
			}
			return MakeString("Array")
		}
		return MakeString(s.ToString())
	case CastBool:
		return MakeBool(s.ToBool())
	case CastUnset:
		return MakeNull()
	case CastArray:
		return castToArray(vm, s)
	case CastObject:
		return castToObject(vm, s)
	}
	return s
}

// castToArray — (array)$x.
func castToArray(vm *VM, s Slot) Slot {
	switch s.typ {
	case ArrayType:
		return s
	case NullType:
		return MakeArray(NewArray())
	case ObjectType:
		a := NewArray()
		o := vm.objectOf(s)
		if o == nil {
			return MakeArray(a)
		}
		if arr, ok := vm.SplArrayContents(o); ok {
			return MakeArray(arr)
		}
		if vm.IsPlainObject(o) {
			// (array)$obj манглирует имена private/protected свойств
			// (\0Class\0name и \0*\0name), как Zend.
			for _, de := range vm.ObjectDumpEntries(o) {
				if de.Uninit {
					continue
				}
				key := de.Key
				switch {
				case strings.HasPrefix(de.Suffix, `:"`):
					cls := strings.TrimSuffix(strings.TrimPrefix(de.Suffix, `:"`), `":private`)
					key = "\x00" + cls + "\x00" + de.Key
				case de.Suffix == ":protected":
					key = "\x00*\x00" + de.Key
				}
				if n, ok := canonicalIntKey(key); ok {
					_ = a.SetInt(n, de.Val)
				} else {
					_ = a.SetStr(key, de.Val)
				}
			}
			return MakeArray(a)
		}
		for _, pp := range vm.ObjectPropPairs(o) {
			if n, ok := canonicalIntKey(pp.Key); ok {
				_ = a.SetInt(n, pp.Val)
			} else {
				_ = a.SetStr(pp.Key, pp.Val)
			}
		}
		return MakeArray(a)
	default:
		a := NewArray()
		_, _ = a.Append(s)
		return MakeArray(a)
	}
}

// castToObject — (object)$x (stdClass).
func castToObject(vm *VM, s Slot) Slot {
	if s.typ == ObjectType {
		return s
	}
	if vm == nil {
		return s
	}
	var std ClassInfo
	stdIdx := -1
	for i, c := range vm.classes {
		if c.Name() == "stdClass" {
			std = c
			stdIdx = i
			break
		}
	}
	if std == nil {
		return s
	}
	o := std.New(&vm.ctx, nil)
	if o == nil {
		return s
	}
	if s.typ != NullType {
		if s.typ == ArrayType {
			for k, v := range s.AsArray().All() {
				o.SetProp(k.phpString(), v)
			}
		} else {
			o.SetProp("scalar", s)
		}
	}
	return std.objectSlot(o, stdIdx)
}
