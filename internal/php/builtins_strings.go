package php

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"hash"
	"math"
	"os"
	"phpvm/internal/vm"
	"strconv"
	"strings"
	"time"
)

// Файл содержит встроенные функции обработки строк: sprintf/pack/unpack,
// trim/explode/implode, str_*, html/base64/url, soundex/metaphone/levenshtein.

func versionCompareFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("version_compare() expects at least 2 arguments, %d given", len(ctx.Args()))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(false)
	}
	cmp := phpVersionCompare(arg(ctx, 0).ToString(), arg(ctx, 1).ToString())
	if len(ctx.Args()) < 3 || arg(ctx, 2).IsNull() {
		return vm.Int(int64(cmp))
	}
	op := arg(ctx, 2).ToString()
	switch op {
	case "<", "lt":
		return vm.Bool(cmp == -1)
	case "<=", "le":
		return vm.Bool(cmp != 1)
	case ">", "gt":
		return vm.Bool(cmp == 1)
	case ">=", "ge":
		return vm.Bool(cmp != -1)
	case "==", "=", "eq":
		return vm.Bool(cmp == 0)
	case "!=", "<>", "ne":
		return vm.Bool(cmp != 0)
	}
	ctx.Throw(vm.NewThrowable("ValueError",
		"version_compare(): Argument #3 ($operator) must be a valid comparison operator"))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.Bool(false)
}

// normBool повторяет ZEND_NORMALIZE_BOOL.
func normBool(n int) int {
	if n > 0 {
		return 1
	}
	if n < 0 {
		return -1
	}
	return 0
}

func isVerDigit(c byte) bool  { return c >= '0' && c <= '9' && c != '.' }
func isVerNDigit(c byte) bool { return !(c >= '0' && c <= '9') && c != '.' }
func isVerAlnum(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// phpCanonicalizeVersion повторяет php_canonicalize_version (versioning.c).
func phpCanonicalizeVersion(version string) string {
	if version == "" {
		return ""
	}
	var buf []byte
	lp := version[0]
	buf = append(buf, lp)
	for p := 1; p < len(version); p++ {
		c := version[p]
		lq := buf[len(buf)-1]
		switch {
		case c == '-' || c == '_' || c == '+':
			if lq != '.' {
				buf = append(buf, '.')
			}
		case (isVerNDigit(lp) && isVerDigit(c)) || (isVerDigit(lp) && isVerNDigit(c)):
			if lq != '.' {
				buf = append(buf, '.')
			}
			buf = append(buf, c)
		case !isVerAlnum(c):
			if lq != '.' {
				buf = append(buf, '.')
			}
		default:
			buf = append(buf, c)
		}
		lp = c
	}
	return string(buf)
}

var specialVersionForms = []struct {
	name  string
	order int
}{
	{"dev", 0}, {"alpha", 1}, {"a", 1}, {"beta", 2}, {"b", 2},
	{"RC", 3}, {"rc", 3}, {"#", 4}, {"pl", 5}, {"p", 5},
}

// compareSpecialVersionForms повторяет compare_special_version_forms.
func compareSpecialVersionForms(f1, f2 string) int {
	lookup := func(s string) int {
		for _, f := range specialVersionForms {
			if strings.HasPrefix(s, f.name) {
				return f.order
			}
		}
		return -1
	}
	return normBool(lookup(f1) - lookup(f2))
}

// phpVersionCompare повторяет php_version_compare (versioning.c).
func phpVersionCompare(orig1, orig2 string) int {
	if orig1 == "" || orig2 == "" {
		if orig1 == "" && orig2 == "" {
			return 0
		}
		if orig1 != "" {
			return 1
		}
		return -1
	}
	var ver1, ver2 string
	if orig1[0] == '#' {
		ver1 = orig1
	} else {
		ver1 = phpCanonicalizeVersion(orig1)
	}
	if orig2[0] == '#' {
		ver2 = orig2
	} else {
		ver2 = phpCanonicalizeVersion(orig2)
	}
	compare := 0
	p1, n1 := 0, true
	p2, n2 := 0, true
	for p1 < len(ver1) && p2 < len(ver2) && n1 && n2 {
		var e1, e2 int
		if i := strings.IndexByte(ver1[p1:], '.'); i >= 0 {
			e1 = p1 + i
			n1 = true
		} else {
			e1 = len(ver1)
			n1 = false
		}
		if i := strings.IndexByte(ver2[p2:], '.'); i >= 0 {
			e2 = p2 + i
			n2 = true
		} else {
			e2 = len(ver2)
			n2 = false
		}
		s1 := ver1[p1:e1]
		s2 := ver2[p2:e2]
		d1 := len(s1) > 0 && s1[0] >= '0' && s1[0] <= '9'
		d2 := len(s2) > 0 && s2[0] >= '0' && s2[0] <= '9'
		if d1 && d2 {
			l1, _ := strtolPrefix(s1)
			l2, _ := strtolPrefix(s2)
			compare = normBool(int(l1 - l2))
		} else if !d1 && !d2 {
			compare = compareSpecialVersionForms(s1, s2)
		} else if d1 {
			compare = compareSpecialVersionForms("#N#", s2)
		} else {
			compare = compareSpecialVersionForms(s1, "#N#")
		}
		if compare != 0 {
			break
		}
		if n1 {
			p1 = e1 + 1
		}
		if n2 {
			p2 = e2 + 1
		}
	}
	if compare == 0 {
		if n1 {
			s1 := ver1[p1:]
			if len(s1) > 0 && s1[0] >= '0' && s1[0] <= '9' {
				compare = 1
			} else {
				compare = phpVersionCompare(s1, "#N#")
			}
		} else if n2 {
			s2 := ver2[p2:]
			if len(s2) > 0 && s2[0] >= '0' && s2[0] <= '9' {
				compare = -1
			} else {
				compare = phpVersionCompare("#N#", s2)
			}
		}
	}
	return compare
}

func numberFormatFn(ctx *vm.Context) vm.Value {
	a := derefSlot(arg0(ctx))
	dec := int64(0)
	if len(ctx.Args()) >= 2 {
		dec = intParam(ctx, 1, "number_format", "decimals")
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	decPoint := "."
	if len(ctx.Args()) >= 3 && arg(ctx, 2).Type() != vm.NullType {
		decPoint = arg(ctx, 2).ToString()
	}
	sep := ","
	if len(ctx.Args()) >= 4 && arg(ctx, 3).Type() != vm.NullType {
		sep = arg(ctx, 3).ToString()
	}
	if a.Type() == vm.IntType {
		return vm.Str(numberFormatLong(a.ToInt(), dec, decPoint, sep))
	}
	return vm.Str(numberFormatFloat(a.ToFloat(), dec, decPoint, sep))
}

// intParam приводит аргумент встроенной функции к int (Z_PARAM_LONG) по
// правилам слабой типизации: int/bool -> int, null -> 0 с Deprecated,
// числовая строка -> int, float -> int (с Deprecated о потере точности).
func intParam(ctx *vm.Context, i int, fn, param string) int64 {
	a := derefSlot(arg(ctx, i))
	switch a.Type() {
	case vm.IntType:
		return a.ToInt()
	case vm.BoolType:
		if a.ToBool() {
			return 1
		}
		return 0
	case vm.FloatType:
		f := a.ToFloat()
		if f != math.Trunc(f) {
			ctx.VM().Deprecated("Implicit conversion from float " + vm.FloatShort(f) + " to int loses precision")
		}
		return floatToInt64(f)
	case vm.NullType:
		ctx.VM().Deprecated(fn + "(): Passing null to parameter #" + strconv.Itoa(i+1) + " ($" + param + ") of type int is deprecated")
		return 0
	case vm.StringType:
		if v, ok := vm.NumericStringSlot(a.StringVal()); ok {
			if v.Type() == vm.FloatType {
				f := v.ToFloat()
				if f != math.Trunc(f) {
					ctx.VM().Deprecated("Implicit conversion from float-string \"" + a.StringVal() + "\" to int loses precision")
				}
				return floatToInt64(f)
			}
			return v.ToInt()
		}
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		vm.ArgTypeError(fn, i+1, param, "int", phpTypeName(ctx, i), "", 0)))
	return 0
}

// floatToInt64 — приведение float к int с насыщением (как Zend).
func floatToInt64(f float64) int64 {
	if math.IsNaN(f) {
		return 0
	}
	if f >= 9223372036854775808.0 {
		return math.MaxInt64
	}
	if f <= -9223372036854775808.0 {
		return math.MinInt64
	}
	return int64(f)
}

var numberFormatPowers = [...]uint64{
	1, 10, 100, 1000, 10000,
	100000, 1000000, 10000000, 100000000, 1000000000,
	10000000000, 100000000000, 1000000000000, 10000000000000, 100000000000000,
	1000000000000000, 10000000000000000, 100000000000000000, 1000000000000000000, 10000000000000000000,
}

// numberFormatLong — _php_math_number_format_long.
func numberFormatLong(num int64, dec int64, decPoint, sep string) string {
	neg := num < 0
	var tmp uint64
	if neg {
		tmp = uint64(-(num + 1)) + 1
	} else {
		tmp = uint64(num)
	}
	if dec < 0 {
		if dec < -int64(len(numberFormatPowers)-1) {
			tmp = 0
		} else {
			power := numberFormatPowers[-dec]
			powerHalf := power / 2
			rest := tmp % power
			tmp = tmp / power
			if rest >= powerHalf {
				tmp = tmp*power + power
			} else {
				tmp = tmp * power
			}
		}
		if tmp == 0 {
			neg = false
		}
	}
	buf := strconv.FormatUint(tmp, 10)
	return groupThousands(buf, dec, decPoint, sep, neg)
}

// numberFormatFloat — _php_math_number_format_ex.
func numberFormatFloat(d float64, dec int64, decPoint, sep string) string {
	if math.IsNaN(d) {
		return "nan"
	}
	if math.IsInf(d, 1) {
		return "inf"
	}
	if math.IsInf(d, -1) {
		return "-inf"
	}
	neg := d < 0
	if neg {
		d = -d
	}
	d = phpRound(d, int(clampInt64ToInt(dec)), 1)
	if dec < 0 {
		dec = 0
	}
	buf := strconv.FormatFloat(d, 'f', int(clampInt64ToInt(dec)), 64)
	if len(buf) > 0 && !(buf[0] >= '0' && buf[0] <= '9') {
		return buf
	}
	if neg && d == 0 {
		neg = false
	}
	return groupThousands(buf, dec, decPoint, sep, neg)
}

func clampInt64ToInt(v int64) int64 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	if v < math.MinInt32 {
		return math.MinInt32
	}
	return v
}

// groupThousands собирает результат из уже отформатированного числа:
// разделитель тысяч каждые 3 цифры, decPoint + dec знаков после точки.
func groupThousands(buf string, dec int64, decPoint, sep string, neg bool) string {
	intPart, frac := buf, ""
	if dec > 0 {
		if i := strings.IndexByte(buf, '.'); i >= 0 {
			intPart, frac = buf[:i], buf[i+1:]
		}
		for int64(len(frac)) < dec {
			frac += "0"
		}
		if int64(len(frac)) > dec {
			frac = frac[:dec]
		}
	}
	var b strings.Builder
	for i := 0; i < len(intPart); i++ {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteString(sep)
		}
		b.WriteByte(intPart[i])
	}
	out := b.String()
	if dec > 0 {
		out += decPoint + frac
	}
	if neg {
		out = "-" + out
	}
	return out
}

func strPadFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	n := int(arg(ctx, 1).ToInt())
	pad := " "
	if len(ctx.Args()) >= 3 {
		pad = arg(ctx, 2).ToString()
	}
	if len(s) >= n {
		return vm.Str(s)
	}
	if pad == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "str_pad(): Argument #3 ($pad_string) must be a non-empty string"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	padType := 1 // STR_PAD_RIGHT
	if len(ctx.Args()) >= 4 {
		padType = int(arg(ctx, 3).ToInt())
		if padType < 0 || padType > 2 {
			ctx.Throw(vm.NewThrowable("ValueError", "str_pad(): Argument #4 ($pad_type) must be STR_PAD_LEFT, STR_PAD_RIGHT, or STR_PAD_BOTH"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	need := n - len(s)
	mk := func(k int) string {
		if k <= 0 {
			return ""
		}
		return strings.Repeat(pad, k/len(pad)+1)[:k]
	}
	switch padType {
	case 0: // STR_PAD_LEFT
		return vm.Str(mk(need) + s)
	case 2: // STR_PAD_BOTH
		left := need / 2
		return vm.Str(mk(left) + s + mk(need-left))
	default:
		return vm.Str(s + mk(need))
	}
}

func strSplitFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	out := vm.NewArray()
	if len(ctx.Args()) >= 2 {
		n := int(arg(ctx, 1).ToInt())
		if n <= 0 {
			ctx.Throw(vm.NewThrowable("ValueError", "str_split(): Argument #2 ($length) must be greater than 0"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		for i := 0; i < len(s); i += n {
			end := i + n
			if end > len(s) {
				end = len(s)
			}
			out.Append(vm.MakeString(s[i:end]))
		}
		return vm.MakeArray(out)
	}
	for _, ch := range s {
		out.Append(vm.MakeString(string(ch)))
	}
	return vm.MakeArray(out)
}

// strtokState — состояние strtok() уровня запроса.
var (
	strtokState  string
	strtokPos    int
	strtokActive bool
)

func strtokFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) >= 2 {
		strtokState = arg(ctx, 0).ToString()
		strtokPos = 0
		strtokActive = true
		return strtokScan(arg(ctx, 1).ToString())
	}
	if !strtokActive {
		ctx.VM().Warning("strtok(): Both arguments must be provided when starting tokenization")
		return vm.Bool(false)
	}
	return strtokScan(arg0(ctx).ToString())
}

// strtokScan продолжает разбор strtokState с позиции strtokPos.
func strtokScan(token string) vm.Value {
	s := strtokState
	pe := len(s)
	p := strtokPos
	if p >= pe {
		return vm.Bool(false)
	}
	isDelim := func(c byte) bool { return strings.IndexByte(token, c) >= 0 }
	// пропускаем ведущие разделители
	for p < pe && isDelim(s[p]) {
		p++
		if p >= pe {
			// достигли конца при пропуске разделителей — состояние сбрасывается
			strtokActive = false
			return vm.Bool(false)
		}
	}
	start := p
	p++
	for p < pe {
		if isDelim(s[p]) {
			tok := s[start:p]
			strtokPos = p + 1
			return vm.Str(tok)
		}
		p++
	}
	// дошли до конца строки: отдаём остаток как токен
	if p-start != 0 {
		tok := s[start:p]
		strtokPos = p + 1
		return vm.Str(tok)
	}
	strtokActive = false
	return vm.Bool(false)
}

func trimChars(ctx *vm.Context, name string) string {
	if len(ctx.Args()) >= 2 {
		return expandCharList(ctx, name, arg(ctx, 1).ToString())
	}
	return " \t\n\r\x00\x0B"
}

// slotString приводит слот к строке, вызывая __toString() у объектов.
func slotString(ctx *vm.Context, s vm.Slot) string {
	if s.Type() == vm.ObjectType {
		if str, ok := objectToStringVia(ctx, s); ok {
			return str
		}
		return ""
	}
	return s.ToString()
}

// trimWith побайтово обрезает символы из cutset (в отличие от strings.Trim,
// который трактует cutset как UTF-8-руны — важно для байтовых наборов).
func trimWith(s, cut string, left, right bool) string {
	var set [256]bool
	for i := 0; i < len(cut); i++ {
		set[cut[i]] = true
	}
	st, en := 0, len(s)
	if left {
		for st < en && set[s[st]] {
			st++
		}
	}
	if right {
		for en > st && set[s[en-1]] {
			en--
		}
	}
	return s[st:en]
}

// expandCharList разворачивает диапазоны вида "A..Z"/"0..9" в набор символов
// ровно как php_charmask в trim/ltrim/rtrim, с диагностическими предупреждениями.
func expandCharList(ctx *vm.Context, name, s string) string {
	var mask [256]bool
	n := len(s)
	for input := 0; input < n; input++ {
		c := s[input]
		if input+3 < n && s[input+1] == '.' && s[input+2] == '.' && s[input+3] >= c {
			for x := c; x <= s[input+3]; x++ {
				mask[x] = true
			}
			input += 3
		} else if input+1 < n && s[input] == '.' && s[input+1] == '.' {
			switch {
			case input == 0:
				ctx.VM().Warning(name + "(): Invalid '..'-range, no character to the left of '..'")
			case input+2 >= n:
				ctx.VM().Warning(name + "(): Invalid '..'-range, no character to the right of '..'")
			case s[input-1] > s[input+2]:
				ctx.VM().Warning(name + "(): Invalid '..'-range, '..'-range needs to be incrementing")
			default:
				ctx.VM().Warning(name + "(): Invalid '..'-range")
			}
		} else {
			mask[c] = true
		}
	}
	var b []byte
	for i := 0; i < 256; i++ {
		if mask[i] {
			b = append(b, byte(i))
		}
	}
	return string(b)
}

// strstrFn — strstr()/stristr(): подстрока от первого вхождения needle.
func strstrFn(ctx *vm.Context, ci bool) vm.Value {
	name := "strstr"
	if ci {
		name = "stristr"
	}
	if arg(ctx, 0).Type() == vm.NullType {
		ctx.VM().Deprecated(name + "(): Passing null to parameter #1 ($haystack) of type string is deprecated")
	}
	if arg(ctx, 1).Type() == vm.NullType {
		ctx.VM().Deprecated(name + "(): Passing null to parameter #2 ($needle) of type string is deprecated")
	}
	if t := arg(ctx, 1).Type(); t == vm.ArrayType || t == vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 2, "needle", "string", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	h := stringifyValue(ctx, arg(ctx, 0))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	n := stringifyValue(ctx, arg(ctx, 1))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	before := len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool()
	idx := -1
	if ci {
		idx = strings.Index(asciiLower(h), asciiLower(n))
	} else {
		idx = strings.Index(h, n)
	}
	if n == "" {
		idx = 0
	}
	if idx < 0 {
		return vm.Bool(false)
	}
	if before {
		return vm.Str(h[:idx])
	}
	return vm.Str(h[idx:])
}

func substrFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	start := int(arg(ctx, 1).ToInt())
	if start < 0 {
		start = len(s) + start
		if start < 0 {
			start = 0
		}
	}
	if start > len(s) {
		return vm.Str("")
	}
	if len(ctx.Args()) >= 3 && !(arg(ctx, 2).Type() == vm.NullType) {
		length := int(arg(ctx, 2).ToInt())
		if length < 0 {
			end := len(s) + length
			if end < start {
				return vm.Str("")
			}
			return vm.Str(s[start:end])
		}
		if start+length > len(s) {
			return vm.Str(s[start:])
		}
		return vm.Str(s[start : start+length])
	}
	return vm.Str(s[start:])
}

// stringifyValue приводит значение к строке для implode/join (массивы ->
// "Array" с предупреждением, объекты без __toString -> Error).
func stringifyValue(ctx *vm.Context, v vm.Slot) string {
	s, err := ctx.VM().ToStringValue(v)
	if err != nil {
		ctx.Throw(vm.NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	return s
}

// warnObjConv предупреждает о приведении объекта к int/float (как PHP).
func warnObjConv(ctx *vm.Context, s vm.Slot, idx int, typ string) {
	if s.Type() == vm.ObjectType {
		ctx.VM().Warning("Object of class " + phpTypeName(ctx, idx) + " could not be converted to " + typ)
	}
}

func joinValues(a *vm.Array, sep string) string {
	parts := make([]string, 0, a.Len())
	for _, v := range a.All() {
		parts = append(parts, v.ToString())
	}
	return strings.Join(parts, sep)
}

// implodeNamed реализует implode()/join() с учётом различий имён параметров.
func implodeNamed(ctx *vm.Context, name string) string {
	a := ctx.Args()
	if len(a) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", name+"() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	if len(a) == 1 {
		if a[0].Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError(name, 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		parts := make([]string, 0, a[0].AsArray().Len())
		for _, v := range a[0].AsArray().All() {
			parts = append(parts, stringifyValue(ctx, v))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		return strings.Join(parts, "")
	}
	if a[1].Type() == vm.NullType {
		// PHP в этом случае разрешает перегрузку как одноместную форму.
		if a[0].Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError(name, 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		parts := make([]string, 0, a[0].AsArray().Len())
		for _, v := range a[0].AsArray().All() {
			parts = append(parts, stringifyValue(ctx, v))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		return strings.Join(parts, "")
	}
	if a[0].Type() == vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 1, "separator", "array|string", "resource", "", 0)))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	if a[0].Type() == vm.ArrayType || a[0].Type() == vm.ClosureType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 1, "separator", "string", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	if a[0].Type() == vm.NullType {
		ctx.VM().Deprecated(fmt.Sprintf("%s(): Passing null to parameter #1 ($separator) of type array|string is deprecated", name))
	}
	sep := slotString(ctx, a[0])
	if a[1].Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 2, "array", "?array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	parts := make([]string, 0, a[1].AsArray().Len())
	for _, v := range a[1].AsArray().All() {
		parts = append(parts, stringifyValue(ctx, v))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	return strings.Join(parts, sep)
}

func joinFn(ctx *vm.Context) string    { return implodeNamed(ctx, "join") }
func implodeFn(ctx *vm.Context) string { return implodeNamed(ctx, "implode") }

func explodeFn(ctx *vm.Context) vm.Value {
	sep := slotString(ctx, arg(ctx, 0))
	s := slotString(ctx, arg(ctx, 1))
	var parts []string
	if sep == "" {
		ctx.Throw(vm.NewThrowable("ValueError",
			"explode(): Argument #1 ($separator) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	parts = strings.Split(s, sep)
	if len(ctx.Args()) >= 3 {
		limit := arg(ctx, 2).ToInt()
		switch {
		case limit >= 0:
			if limit <= 1 {
				parts = []string{s}
			} else if int(limit) < len(parts) {
				rest := strings.Join(parts[limit-1:], sep)
				parts = append(append([]string{}, parts[:limit-1]...), rest)
			}
		case limit < 0:
			n := len(parts) + int(limit)
			if n < 0 {
				n = 0
			}
			if n < len(parts) {
				parts = parts[:n]
			}
		}
	}
	out := vm.NewArray()
	for _, p := range parts {
		out.Append(vm.MakeString(p))
	}
	return vm.MakeArray(out)
}

func strlenFn(ctx *vm.Context) vm.Value {
	a := arg(ctx, 0)
	t := a.Type()
	if t == vm.ArrayType || t == vm.ResourceType ||
		(t == vm.ObjectType && !ctx.VM().IsStringable(a)) ||
		(ctx.VM().StrictTypes() && t != vm.StringType) {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("strlen", 1, "string", "string", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Int(0)
	}
	if t == vm.NullType && !ctx.VM().StrictTypes() {
		ctx.VM().Deprecated("strlen(): Passing null to parameter #1 ($string) of type string is deprecated")
	}
	s, cerr := ctx.VM().ToStringValue(a)
	if cerr != nil {
		if th, ok := cerr.(*vm.Throwable); ok {
			ctx.Throw(th)
		} else {
			ctx.Throw(vm.NewThrowable("Error", cerr.Error()))
		}
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Int(0)
	}
	return vm.Int(int64(len(s)))
}

// --- sprintf ---

func sprintfFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "sprintf() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if t := arg(ctx, 0).Type(); t == vm.ArrayType || t == vm.ResourceType || (t == vm.ObjectType && !ctx.VM().IsStringable(arg(ctx, 0))) {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("sprintf", 1, "format", "string", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return vm.Str(sprintfValue(ctx, ctx.Args()))
}

// vsprintfFn — vsprintf($format, array $values).
func vsprintfFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "vsprintf() expects at least 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	format := arg(ctx, 0).ToString()
	all := []vm.Slot{vm.MakeString(format)}
	if a := arg(ctx, 1); a.Type() == vm.ArrayType {
		all = append(all, a.AsArray().Values()...)
	} else {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("vsprintf", 2, "values", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return vm.Str(sprintfValue(ctx, all))
}

// vprintfFn — vprintf($format, array $values).
func vprintfFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "vprintf() expects at least 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	format := arg(ctx, 0).ToString()
	all := []vm.Slot{vm.MakeString(format)}
	if a := arg(ctx, 1); a.Type() == vm.ArrayType {
		all = append(all, a.AsArray().Values()...)
	} else {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("vprintf", 2, "values", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	s := sprintfValue(ctx, all)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	ctx.Print(s)
	return vm.Int(int64(len(s)))
}

// sprintfCountArgs возвращает число аргументов (не считая format), требуемых
// строкой формата: максимум среди позиционных индексов и последовательных.
func sprintfCountArgs(format string) int {
	seq, maxPos := 0, 0
	i := 0
	for i < len(format) {
		if format[i] != '%' {
			i++
			continue
		}
		i++ // после '%'
		j := i
		num := 0
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			num = num*10 + int(format[j]-'0')
			j++
		}
		hasPos := false
		if j < len(format) && format[j] == '$' && num > 0 {
			hasPos = true
			i = j + 1
		}
		for i < len(format) {
			c := format[i]
			if c == '-' || c == '+' || c == '0' || c == ' ' {
				i++
			} else if c == '\'' && i+1 < len(format) {
				i += 2
			} else {
				break
			}
		}
		starArg := func() (int, bool) {
			// возвращает (posIndex, был ли позиционный) и продвигает i
			k := i
			n2 := 0
			for k < len(format) && format[k] >= '0' && format[k] <= '9' {
				n2 = n2*10 + int(format[k]-'0')
				k++
			}
			if k < len(format) && format[k] == '$' && n2 > 0 {
				i = k + 1
				return n2, true
			}
			return 0, false
		}
		if i < len(format) && format[i] == '*' {
			i++
			if p, ok := starArg(); ok {
				if p > maxPos {
					maxPos = p
				}
			} else {
				seq++
			}
		} else {
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				i++
			}
		}
		if i < len(format) && format[i] == '.' {
			i++
			if i < len(format) && format[i] == '*' {
				i++
				if p, ok := starArg(); ok {
					if p > maxPos {
						maxPos = p
					}
				} else {
					seq++
				}
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					i++
				}
			}
		}
		if i >= len(format) {
			break
		}
		if format[i] == '%' {
			i++
			continue
		}
		if format[i] == 'l' { // length modifier (игнорируется, как в php_sprintf)
			i++
			if i >= len(format) {
				break
			}
		}
		i++ // спецификатор
		if hasPos {
			if num > maxPos {
				maxPos = num
			}
		} else {
			seq++
		}
	}
	if seq > maxPos {
		return seq
	}
	return maxPos
}

// sprintfIntArg извлекает целочисленный аргумент динамической ширины/точности.
func sprintfIntArg(a vm.Slot) (int64, bool) {
	switch a.Type() {
	case vm.IntType:
		return a.ToInt(), true
	case vm.FloatType:
		return int64(a.ToFloat()), true
	case vm.StringType:
		s := strings.TrimSpace(a.ToString())
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return n, true
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return int64(f), true
		}
	}
	return 0, false
}

// sprintfArgNumsValid проверяет N$ спецификаторы: число должно быть в
// диапазоне (0, 2147483647).
func sprintfArgNumsValid(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		k := i
		num := 0
		for k < len(format) && format[k] >= '0' && format[k] <= '9' {
			num = num*10 + int(format[k]-'0')
			k++
		}
		if k < len(format) && format[k] == '$' {
			if num <= 0 || num >= 2147483647 {
				return false
			}
		}
	}
	return true
}

func sprintfValue(ctx *vm.Context, args []vm.Slot) string {
	if len(args) == 0 {
		return ""
	}
	format := stringifyValue(ctx, args[0])
	if ctx.HasThrown() {
		return *new(string)
	}
	if !sprintfArgNumsValid(format) {
		ctx.Throw(vm.NewThrowable("ValueError",
			"Argument number specifier must be greater than zero and less than 2147483647"))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	need := sprintfCountArgs(format)
	if len(args)-1 < need {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("%d arguments are required, %d given", need+1, len(args))))
		if ctx.HasThrown() {
			return *new(string)
		}
	}
	argi := 1
	var b strings.Builder
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			b.WriteByte(format[i])
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			b.WriteByte('%')
			continue
		}
		// позиционный спецификатор N$
		posArg := 0
		{
			k := i
			num := 0
			for k < len(format) && format[k] >= '0' && format[k] <= '9' {
				num = num*10 + int(format[k]-'0')
				k++
			}
			if k < len(format) && format[k] == '$' && num > 0 {
				posArg = num
				i = k + 1
			}
		}
		// флаги
		leftAlign, zeroPad, plusSign := false, false, false
		var padChar byte
		for i < len(format) {
			switch format[i] {
			case '-':
				leftAlign = true
			case '0':
				zeroPad = true
			case '+':
				plusSign = true
			case ' ':
				// PHP игнорирует флаг пробела
			case '\'':
				if i+1 >= len(format) {
					ctx.Throw(vm.NewThrowable("ValueError", "Missing padding character"))
					if ctx.HasThrown() {
						return *new(string)
					}
				}
				padChar = format[i+1]
				i++ // пользовательский символ заполнения
			default:
				goto width
			}
			i++
		}
	width:
		width := int64(0)
		if i < len(format) && format[i] == '*' {
			i++
			posW := 0
			{
				k := i
				n2 := 0
				for k < len(format) && format[k] >= '0' && format[k] <= '9' {
					n2 = n2*10 + int(format[k]-'0')
					k++
				}
				if k < len(format) && format[k] == '$' && n2 > 0 {
					posW = n2
					i = k + 1
				}
			}
			if posW == 0 {
				posW = argi
				argi++
			}
			if posW >= len(args) {
				ctx.Throw(vm.NewThrowable("ArgumentCountError",
					fmt.Sprintf("%d arguments are required, %d given", need+1, len(args))))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
			wv, ok := sprintfIntArg(args[posW])
			if !ok {
				ctx.Throw(vm.NewThrowable("ValueError", "Width must be an integer"))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
			if wv < 0 || wv > 2147483647 {
				ctx.Throw(vm.NewThrowable("ValueError", "Width must be between 0 and 2147483647"))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
			width = wv
		} else {
			for i < len(format) && format[i] >= '0' && format[i] <= '9' {
				if width <= 2147483647 {
					width = width*10 + int64(format[i]-'0')
					if width > 2147483647 {
						width = 2147483648
					}
				}
				i++
			}
			if width > 2147483647 {
				ctx.Throw(vm.NewThrowable("ValueError", "Width must be between 0 and 2147483647"))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
		}
		precision := int64(-1)
		havePrec := false
		if i < len(format) && format[i] == '.' {
			i++
			havePrec = true
			precision = 0
			if i < len(format) && format[i] == '*' {
				i++
				posP := 0
				{
					k := i
					n2 := 0
					for k < len(format) && format[k] >= '0' && format[k] <= '9' {
						n2 = n2*10 + int(format[k]-'0')
						k++
					}
					if k < len(format) && format[k] == '$' && n2 > 0 {
						posP = n2
						i = k + 1
					}
				}
				if posP == 0 {
					posP = argi
					argi++
				}
				if posP >= len(args) {
					ctx.Throw(vm.NewThrowable("ArgumentCountError",
						fmt.Sprintf("%d arguments are required, %d given", need+1, len(args))))
					if ctx.HasThrown() {
						return *new(string)
					}
				}
				pv, ok := sprintfIntArg(args[posP])
				if !ok {
					ctx.Throw(vm.NewThrowable("ValueError", "Precision must be an integer"))
					if ctx.HasThrown() {
						return *new(string)
					}
				}
				if pv < -1 || pv > 2147483647 {
					ctx.Throw(vm.NewThrowable("ValueError", "Precision must be between -1 and 2147483647"))
					if ctx.HasThrown() {
						return *new(string)
					}
				}
				precision = pv
			} else {
				for i < len(format) && format[i] >= '0' && format[i] <= '9' {
					if precision <= 2147483647 {
						precision = precision*10 + int64(format[i]-'0')
						if precision > 2147483647 {
							precision = 2147483648
						}
					}
					i++
				}
				if precision > 2147483647 {
					ctx.Throw(vm.NewThrowable("ValueError", "Precision must be between 0 and 2147483647"))
					if ctx.HasThrown() {
						return *new(string)
					}
				}
			}
		}
		if i >= len(format) {
			ctx.Throw(vm.NewThrowable("ValueError", "Missing format specifier at end of string"))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		if format[i] == 'l' { // length modifier (игнорируется)
			i++
			if i >= len(format) {
				ctx.Throw(vm.NewThrowable("ValueError", "Missing format specifier at end of string"))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
		}
		spec := format[i]
		if spec == '%' {
			// php все равно «потребляет» аргумент для спецификатора '%'.
			if posArg == 0 {
				if argi >= len(args) {
					ctx.Throw(vm.NewThrowable("ArgumentCountError",
						fmt.Sprintf("%d arguments are required, %d given", need+1, len(args))))
					if ctx.HasThrown() {
						return *new(string)
					}
				}
				argi++
			}
			b.WriteByte('%')
			continue
		}
		if havePrec && precision == -1 && spec != 'g' && spec != 'G' && spec != 'h' && spec != 'H' {
			ctx.Throw(vm.NewThrowable("ValueError", "Precision -1 is only supported for %g, %G, %h and %H"))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		var a vm.Slot
		aIdx := 0
		if posArg > 0 {
			if posArg >= len(args) {
				ctx.Throw(vm.NewThrowable("ArgumentCountError",
					fmt.Sprintf("%d arguments are required, %d given", need+1, len(args))))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
			a = args[posArg]
			aIdx = posArg
		} else {
			if argi >= len(args) {
				ctx.Throw(vm.NewThrowable("ArgumentCountError",
					fmt.Sprintf("%d arguments are required, %d given", need+1, len(args))))
				if ctx.HasThrown() {
					return *new(string)
				}
			}
			a = args[argi]
			aIdx = argi
			argi++
		}
		toInt := func() int64 {
			if a.Type() == vm.ObjectType {
				warnObjConv(ctx, a, aIdx, "int")
				return 1
			}
			if a.Type() == vm.ArrayType {
				if a.AsArray().Len() == 0 {
					return 0
				}
				return 1
			}
			return a.ToInt()
		}
		toFloat := func() float64 {
			if a.Type() == vm.ObjectType {
				warnObjConv(ctx, a, aIdx, "float")
				return 1
			}
			if a.Type() == vm.ArrayType {
				if a.AsArray().Len() == 0 {
					return 0
				}
				return 1
			}
			return a.ToFloat()
		}
		if (spec == 'f' || spec == 'F' || spec == 'e' || spec == 'E' ||
			spec == 'g' || spec == 'G' || spec == 'h' || spec == 'H') && havePrec && precision > 53 {
			ctx.VM().Notice(fmt.Sprintf("sprintf(): Requested precision of %d digits was truncated to PHP maximum of 53 digits", precision))
			precision = 53
		}
		var body string
		switch spec {
		case 'd', 'u':
			v := toInt()
			neg := v < 0
			digits := strconv.FormatInt(v, 10)
			if neg {
				digits = digits[1:]
			}
			// В PHP 8.3 точность для %d/%u игнорируется.
			switch {
			case neg:
				body = "-" + digits
			case plusSign:
				body = "+" + digits
			default:
				body = digits
			}
		case 'f', 'F':
			p := int(precision)
			if p < 0 {
				p = 6
			}
			body = addNumericSign(strconv.FormatFloat(toFloat(), 'f', p, 64), plusSign)
		case 'e', 'E':
			p := int(precision)
			if p < 0 {
				p = 6
			}
			body = addNumericSign(trimExp(strconv.FormatFloat(toFloat(), byte(spec), p, 64)), plusSign)
		case 'g', 'G', 'h', 'H':
			gs := byte(spec)
			if spec == 'h' {
				gs = 'g'
			} else if spec == 'H' {
				gs = 'G'
			}
			p := 6
			if havePrec {
				p = int(precision)
			}
			body = addNumericSign(strconv.FormatFloat(toFloat(), gs, p, 64), plusSign)
		case 'x':
			body = intPrecision(strconv.FormatInt(toInt(), 16), precision, havePrec)
		case 'X':
			body = strings.ToUpper(intPrecision(strconv.FormatInt(toInt(), 16), precision, havePrec))
		case 'o':
			body = intPrecision(strconv.FormatInt(toInt(), 8), precision, havePrec)
		case 'b':
			body = intPrecision(strconv.FormatInt(toInt(), 2), precision, havePrec)
		case 'c':
			// PHP игнорирует ширину, точность и флаги для %c.
			b.WriteByte(byte(toInt()))
			continue
		case 's':
			body = stringifyValue(ctx, a)
			if ctx.HasThrown() {
				return *new(string)
			}
			if precision >= 0 && precision < int64(len(body)) {
				body = body[:int(precision)]
			}
		default:
			ctx.Throw(vm.NewThrowable("ValueError", "Unknown format specifier \""+string(spec)+"\""))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		zeroPadEff := zeroPad
		if leftAlign && (spec == 'd' || spec == 'u') {
			zeroPadEff = false
		}
		b.WriteString(pad(body, width, leftAlign, zeroPadEff, padChar))
	}
	return b.String()
}

// intPrecision применяет точность для целочисленных спецификаторов: минимум
// цифр (нули слева); точность 0 для нуля даёт пустую строку.
func intPrecision(digits string, precision int64, have bool) string {
	if !have || precision < 0 {
		return digits
	}
	if precision == 0 && digits == "0" {
		return ""
	}
	for int64(len(digits)) < precision {
		digits = "0" + digits
	}
	return digits
}

func addNumericSign(s string, plus bool) string {
	if len(s) == 0 || s[0] == '-' || s[0] == '+' {
		return s
	}
	if plus {
		return "+" + s
	}
	return s
}

func pad(s string, width int64, leftAlign, zeroPad bool, padChar byte) string {
	if int64(len(s)) >= width {
		return s
	}
	fill := byte(' ')
	if padChar != 0 {
		fill = padChar
	} else if zeroPad {
		fill = '0'
	}
	padding := strings.Repeat(string(fill), int(width)-len(s))
	if leftAlign {
		return s + padding
	}
	if fill == '0' && len(s) > 0 && (s[0] == '-' || s[0] == '+') {
		return string(s[0]) + padding + s[1:]
	}
	return padding + s
}

// --- дополнительные строковые функции ---

func clipLen(s string, n int) string {
	if n < 0 || n >= len(s) {
		return s
	}
	return s[:n]
}

func phpStrCmp(a, b string) int64 {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

// phpStrCmpRaw — семантика zend_binary_strcmp: разность первых различающихся
// байт; если общий префикс совпал — знак разности длин (-1/0/1).
func phpStrCmpRaw(a, b string) int64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return int64(a[i]) - int64(b[i])
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}

func stripslashesFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			if i+1 < len(s) {
				i++
				if s[i] == '0' {
					b.WriteByte(0)
				} else {
					b.WriteByte(s[i])
				}
			}
			continue
		}
		b.WriteByte(s[i])
	}
	return vm.Str(b.String())
}

func strRot13(s string) string {
	b := []byte(s)
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z':
			b[i] = 'a' + (c-'a'+13)%26
		case c >= 'A' && c <= 'Z':
			b[i] = 'A' + (c-'A'+13)%26
		}
	}
	return string(b)
}

func wordwrapFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if s == "" {
		return vm.Str("")
	}
	width := int(arg(ctx, 1).ToInt())
	if len(ctx.Args()) < 2 {
		width = 75
	}
	breakStr := "\n"
	if len(ctx.Args()) >= 3 {
		breakStr = arg(ctx, 2).ToString()
	}
	if breakStr == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "wordwrap(): Argument #3 ($break) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	cut := false
	if len(ctx.Args()) >= 4 {
		cut = arg(ctx, 3).ToBool()
	}
	if width == 0 && cut {
		ctx.Throw(vm.NewThrowable("ValueError",
			"wordwrap(): Argument #4 ($cut_long_words) cannot be true when argument #2 ($width) is 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	bl := len(breakStr)
	n := len(s)
	var b strings.Builder
	laststart, lastspace := 0, 0
	i := 0
	for i = 0; i < n; i++ {
		if i+bl < n && s[i:i+bl] == breakStr {
			b.WriteString(s[laststart : i+bl])
			i += bl - 1
			laststart = i + 1
			lastspace = i + 1
		} else if s[i] == ' ' {
			if i-laststart >= width {
				b.WriteString(s[laststart:i])
				b.WriteString(breakStr)
				laststart = i + 1
			}
			lastspace = i
		} else if i-laststart >= width && cut && laststart >= lastspace {
			b.WriteString(s[laststart:i])
			b.WriteString(breakStr)
			laststart = i
			lastspace = i
		} else if i-laststart >= width && laststart < lastspace {
			b.WriteString(s[laststart:lastspace])
			b.WriteString(breakStr)
			laststart = lastspace + 1
			lastspace = lastspace + 1
		}
	}
	if laststart != i {
		b.WriteString(s[laststart:i])
	}
	return vm.Str(b.String())
}

func chunkSplitFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	n := 76
	if len(ctx.Args()) >= 2 {
		n = int(arg(ctx, 1).ToInt())
	}
	end := "\r\n"
	if len(ctx.Args()) >= 3 {
		end = arg(ctx, 2).ToString()
	}
	if n <= 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "chunk_split(): Argument #2 ($length) must be greater than 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var b strings.Builder
	if len(s) == 0 {
		b.WriteString(end)
		return vm.Str(b.String())
	}
	for i := 0; i < len(s); i += n {
		e := i + n
		if e > len(s) {
			e = len(s)
		}
		b.WriteString(s[i:e])
		b.WriteString(end)
	}
	return vm.Str(b.String())
}

func replaceFold(s, search, replace string) string {
	if search == "" {
		return s
	}
	ls := asciiLower(s)
	lsearch := asciiLower(search)
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(ls[i:], lsearch)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j])
		b.WriteString(replace)
		i += j + len(search)
	}
	return b.String()
}

func strIReplaceFn(ctx *vm.Context) vm.Value { return strReplaceImpl(ctx, true) }

func strReplaceFn(ctx *vm.Context) vm.Value { return strReplaceImpl(ctx, false) }

// strReplaceImpl реализует str_replace()/str_ireplace() с массивами и
// by-ref $count.
func strReplaceImpl(ctx *vm.Context, fold bool) vm.Value {
	name := "str_replace"
	if fold {
		name = "str_ireplace"
	}
	args := ctx.Args()
	if len(args) < 3 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("%s() expects at least 3 arguments, %d given", name, len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	search, replace, subject := arg(ctx, 0), arg(ctx, 1), arg(ctx, 2)
	if search.Type() == vm.NullType {
		ctx.VM().Deprecated(fmt.Sprintf("%s(): Passing null to parameter #1 ($search) of type array|string is deprecated", name))
	}
	if replace.Type() == vm.NullType {
		ctx.VM().Deprecated(fmt.Sprintf("%s(): Passing null to parameter #2 ($replace) of type array|string is deprecated", name))
	}
	if subject.Type() == vm.NullType {
		ctx.VM().Deprecated(fmt.Sprintf("%s(): Passing null to parameter #3 ($subject) of type array|string is deprecated", name))
	}
	if search.Type() == vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 1, "search", "array|string", "resource", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if replace.Type() == vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 2, "replace", "array|string", "resource", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if subject.Type() == vm.ResourceType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError(name, 3, "subject", "array|string", "resource", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if replace.Type() == vm.ArrayType && search.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			fmt.Sprintf("%s(): Argument #2 ($replace) must be of type string when argument #1 ($search) is a string", name)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var searches []string
	if search.Type() == vm.ArrayType {
		for _, v := range search.AsArray().All() {
			searches = append(searches, stringifyValue(ctx, v))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	} else {
		searches = []string{stringifyValue(ctx, search)}
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var replaces []string
	if replace.Type() == vm.ArrayType {
		for _, v := range replace.AsArray().All() {
			replaces = append(replaces, stringifyValue(ctx, v))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	total := int64(0)
	one := func(s string) string {
		for idx, se := range searches {
			rep := ""
			if replace.Type() != vm.ArrayType {
				rep = stringifyValue(ctx, replace)
				if ctx.HasThrown() {
					return *new(string)
				}
			} else if idx < len(replaces) {
				rep = replaces[idx]
			}
			var n int
			s, n = replaceCounted(s, se, rep, fold)
			total += int64(n)
		}
		return s
	}
	var result vm.Value
	if subject.Type() == vm.ArrayType {
		out := vm.NewArray()
		for k, v := range subject.AsArray().All() {
			ns := one(stringifyValue(ctx, v))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			if k.IsStr() {
				out.SetStr(k.StrVal(), vm.MakeString(ns))
			} else {
				out.SetInt(k.IntVal(), vm.MakeString(ns))
			}
		}
		result = vm.MakeArray(out)
	} else {
		result = vm.MakeString(one(stringifyValue(ctx, subject)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	pregWriteRef(ctx, 3, vm.MakeInt(total))
	return result
}

// replaceCounted заменяет search на replace и возвращает число замен.
func replaceCounted(s, search, replace string, fold bool) (string, int) {
	if search == "" {
		return s, 0
	}
	if !fold {
		n := strings.Count(s, search)
		if n == 0 {
			return s, 0
		}
		return strings.ReplaceAll(s, search, replace), n
	}
	ls := asciiLower(s)
	lsearch := asciiLower(search)
	var b strings.Builder
	n := 0
	i := 0
	for {
		j := strings.Index(ls[i:], lsearch)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		b.WriteString(s[i : i+j])
		b.WriteString(replace)
		i += j + len(search)
		n++
	}
	return b.String(), n
}

func substrReplaceFn(ctx *vm.Context) vm.Value {
	subject := arg(ctx, 0)
	replace := arg(ctx, 1)
	offset := arg(ctx, 2)
	hasLen := len(ctx.Args()) >= 4
	length := arg(ctx, 3)
	lenIsNull := !hasLen || length.Type() == vm.NullType
	if subject.Type() != vm.ArrayType {
		if offset.Type() == vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError", "substr_replace(): Argument #3 ($offset) cannot be an array when working on a single string"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if hasLen && length.Type() == vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError", "substr_replace(): Argument #4 ($length) cannot be an array when working on a single string"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		rep := nthStrCtx(ctx, replace, 0)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		l := 0
		if !lenIsNull {
			l = int(length.ToInt())
		}
		return vm.Str(substrReplaceOne(subject.ToString(), rep, int(offset.ToInt()), !lenIsNull, l))
	}
	out := vm.NewArray()
	i := 0
	for k, v := range subject.AsArray().All() {
		s := stringifyValue(ctx, v)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		off := nthInt(offset, i)
		sr := s
		if off < 0 {
			off = len(sr) + off
			if off < 0 {
				off = 0
			}
		} else if off > len(sr) {
			off = len(sr)
		}
		var l int
		if length.Type() == vm.ArrayType {
			if lv, ok := nthIntOK(length, i); ok {
				l = lv
			} else {
				l = len(sr)
			}
		} else if !lenIsNull {
			l = int(length.ToInt())
		} else {
			l = len(sr)
		}
		if l < 0 {
			l = (len(sr) - off) + l
			if l < 0 {
				l = 0
			}
		}
		if l > len(sr)-off {
			l = len(sr) - off
		}
		rep := nthStrCtx(ctx, replace, i)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		ns := sr[:off] + rep + sr[off+l:]
		if k.IsStr() {
			out.SetStr(k.StrVal(), vm.MakeString(ns))
		} else {
			out.SetInt(k.IntVal(), vm.MakeString(ns))
		}
		i++
	}
	return vm.MakeArray(out)
}

func nthInt(s vm.Slot, i int) int {
	if s.Type() == vm.ArrayType {
		idx := 0
		for _, v := range s.AsArray().All() {
			if idx == i {
				return slotToIntPHP(v)
			}
			idx++
		}
		return 0
	}
	return slotToIntPHP(s)
}

// slotToIntPHP повторяет zval_get_long: непустой массив -> 1.
func slotToIntPHP(v vm.Slot) int {
	if v.Type() == vm.ArrayType {
		if v.AsArray().Len() > 0 {
			return 1
		}
		return 0
	}
	return int(v.ToInt())
}

// nthIntOK — как nthInt, но сообщает, был ли индекс в пределах массива.
func nthIntOK(s vm.Slot, i int) (int, bool) {
	if s.Type() == vm.ArrayType {
		idx := 0
		for _, v := range s.AsArray().All() {
			if idx == i {
				return int(v.ToInt()), true
			}
			idx++
		}
		return 0, false
	}
	return int(s.ToInt()), true
}

// nthStrCtx — как nthStr, но приводит значение через stringifyValue,
// эмитя предупреждение «Array to string conversion».
func nthStrCtx(ctx *vm.Context, s vm.Slot, i int) string {
	if s.Type() == vm.ArrayType {
		idx := 0
		for _, v := range s.AsArray().All() {
			if idx == i {
				return stringifyValue(ctx, v)
			}
			idx++
		}
		return ""
	}
	return stringifyValue(ctx, s)
}

func nthStr(s vm.Slot, i int) string {
	if s.Type() == vm.ArrayType {
		idx := 0
		for _, v := range s.AsArray().All() {
			if idx == i {
				return v.ToString()
			}
			idx++
		}
		return ""
	}
	return s.ToString()
}

func substrReplaceOne(s, rep string, off int, useLen bool, l int) string {
	if off < 0 {
		off = len(s) + off
		if off < 0 {
			off = 0
		}
	}
	if off > len(s) {
		off = len(s)
	}
	end := len(s)
	if useLen {
		if l >= 0 {
			if l > len(s)-off {
				l = len(s) - off
			}
			end = off + l
		} else {
			end = len(s) + l
			if end < off {
				end = off
			}
		}
	}
	return s[:off] + rep + s[end:]
}

func substrCompareFn(ctx *vm.Context) vm.Value {
	h := arg(ctx, 0).ToString()
	needle := arg(ctx, 1).ToString()
	ci := false
	if len(ctx.Args()) >= 5 {
		ci = arg(ctx, 4).ToBool()
	}
	// offset: отрицательные значения при выходе за левую границу зажимаются в 0,
	// положительные за правой — ValueError (PHP 8.3).
	off := int(arg(ctx, 2).ToInt())
	if off < 0 {
		off += len(h)
		if off < 0 {
			off = 0
		}
	} else if off > len(h) {
		ctx.Throw(vm.NewThrowable("ValueError", "substr_compare(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	sub := h[off:]
	var n int = -1
	if len(ctx.Args()) >= 4 && arg(ctx, 3).Type() != vm.NullType {
		l := arg(ctx, 3).ToInt()
		if l < 0 {
			ctx.Throw(vm.NewThrowable("ValueError", "substr_compare(): Argument #4 ($length) must be greater than or equal to 0"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		n = int(l)
	}
	if n < 0 {
		a, b := sub, needle
		if ci {
			a, b = asciiLower(a), asciiLower(b)
		}
		return vm.Int(int64(phpStrCmp(a, b)))
	}
	// Ограниченное сравнение до n байт (как zend_binary_strncmp).
	l := n
	if len(sub) < l {
		l = len(sub)
	}
	if len(needle) < l {
		l = len(needle)
	}
	a, b := sub[:l], needle[:l]
	if ci {
		a, b = asciiLower(a), asciiLower(b)
	}
	c := phpStrCmp(a, b)
	if c != 0 {
		return vm.Int(c)
	}
	if l == n {
		return vm.Int(0)
	}
	if len(sub) > len(needle) {
		return vm.Int(1)
	}
	if len(sub) < len(needle) {
		return vm.Int(-1)
	}
	return vm.Int(0)
}

// checkOffset приводит offset к абсолютному и проверяет диапазон (PHP 8.3).
func checkOffset(ctx *vm.Context, off int64, n int, fn string) int {
	if off < -int64(n) || off > int64(n) {
		ctx.Throw(vm.NewThrowable("ValueError", fn+"(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"))
		if ctx.HasThrown() {
			return *new(int)
		}
	}
	o := int(off)
	if o < 0 {
		return n + o
	}
	return o
}

func strtrFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if len(ctx.Args()) >= 3 {
		switch arg(ctx, 1).Type() {
		case vm.ArrayType:
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("strtr", 2, "from", "string", "array", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		case vm.ResourceType:
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("strtr", 2, "from", "array|string", "resource", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		case vm.NullType:
			ctx.VM().Deprecated("strtr(): Passing null to parameter #2 ($from) of type array|string is deprecated")
		}
		switch arg(ctx, 2).Type() {
		case vm.ArrayType:
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("strtr", 3, "to", "?string", "array", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		case vm.ResourceType:
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("strtr", 3, "to", "?string", "resource", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		case vm.NullType:
			// null допустим
		}
		from := slotString(ctx, arg(ctx, 1))
		to := slotString(ctx, arg(ctx, 2))
		// Пары позиционные; при дублирующихся символах побеждает последняя (как
		// хеш-таблица zend_strtr).
		var tbl [256]byte
		var set [256]bool
		n := len(from)
		if len(to) < n {
			n = len(to)
		}
		for j := 0; j < n; j++ {
			tbl[from[j]] = to[j]
			set[from[j]] = true
		}
		b := []byte(s)
		for i, c := range b {
			if set[c] {
				b[i] = tbl[c]
			}
		}
		return vm.Str(string(b))
	}
	if t := arg(ctx, 1).Type(); t != vm.ArrayType {
		if t == vm.NullType {
			ctx.VM().Deprecated("strtr(): Passing null to parameter #2 ($from) of type array|string is deprecated")
		}
		if t == vm.ResourceType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("strtr", 2, "from", "array|string", "resource", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("strtr", 2, "from", "array", "string", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if s == "" {
		return vm.Str("")
	}
	repl := arg(ctx, 1).AsArray()
	// Предупреждение о пустой строке выводится один раз на вызов.
	for k := range repl.All() {
		key := k.StrVal()
		if !k.IsStr() {
			key = strconv.FormatInt(k.IntVal(), 10)
		}
		if len(key) == 0 {
			ctx.VM().Warning("strtr(): Ignoring replacement of empty string")
			break
		}
	}
	var b strings.Builder
	i := 0
	for i < len(s) {
		best, bestRep := 0, ""
		for k, v := range repl.All() {
			key := k.StrVal()
			if !k.IsStr() {
				key = strconv.FormatInt(k.IntVal(), 10)
			}
			if len(key) == 0 {
				continue
			}
			if len(key) > best && strings.HasPrefix(s[i:], key) {
				best = len(key)
				bestRep = v.ToString()
			}
		}
		if best > 0 {
			b.WriteString(bestRep)
			i += best
		} else {
			b.WriteByte(s[i])
			i++
		}
	}
	return vm.Str(b.String())
}

// strtrArgTypeName возвращает имя типа аргумента как его видит PHP: скаляры
// приводятся к string в нестрогом режиме.
func strtrArgTypeName(ctx *vm.Context) string {
	switch arg(ctx, 1).Type() {
	case vm.ArrayType:
		return "array"
	case vm.ObjectType:
		return "string"
	}
	return "string"
}

func isDigitByte(c byte) bool { return c >= '0' && c <= '9' }

// strNatCmp — точный порт strnatcmp_ex() из PHP (ext/standard/strnatcmp.c,
// алгоритм Martin Pool). Нюансы: ведущие нули срезаются ТОЛЬКО в самом начале
// строк, а не в каждом числовом фрагменте; числовой фрагмент, начинающийся с
// нуля, сравнивается «слева» (compare_left), иначе «справа» (compare_right).
func strNatCmp(a, b string, fold bool) int64 {
	al, bl := len(a), len(b)
	if al == 0 || bl == 0 {
		switch {
		case al == bl:
			return 0
		case al > bl:
			return 1
		default:
			return -1
		}
	}
	i, j := 0, 0
	ca, cb := a[i], b[j]
	for ca == '0' && i+1 < al && isDigitByte(a[i+1]) {
		i++
		ca = a[i]
	}
	for cb == '0' && j+1 < bl && isDigitByte(b[j+1]) {
		j++
		cb = b[j]
	}
	for {
		for natIsSpace(ca) {
			i++
			ca = natByteAt(a, i)
		}
		for natIsSpace(cb) {
			j++
			cb = natByteAt(b, j)
		}
		if isDigitByte(ca) && isDigitByte(cb) {
			var res int
			if ca == '0' || cb == '0' {
				res, i, j = natCmpLeft(a, i, b, j)
			} else {
				res, i, j = natCmpRight(a, i, b, j)
			}
			if res != 0 {
				return int64(res)
			}
			if i >= al && j >= bl {
				return 0
			}
			if i >= al {
				return -1
			}
			if j >= bl {
				return 1
			}
			ca, cb = a[i], b[j]
		}
		if fold {
			if ca >= 'a' && ca <= 'z' {
				ca -= 32
			}
			if cb >= 'a' && cb <= 'z' {
				cb -= 32
			}
		}
		if ca < cb {
			return -1
		}
		if ca > cb {
			return 1
		}
		i++
		j++
		if i >= al && j >= bl {
			return 0
		}
		if i >= al {
			return -1
		}
		if j >= bl {
			return 1
		}
		ca, cb = a[i], b[j]
	}
}

func natByteAt(s string, i int) byte {
	if i < 0 || i >= len(s) {
		return 0
	}
	return s[i]
}

func natIsSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// natCmpLeft — compare_left: побеждает первое различие; более короткий проигрыш.
func natCmpLeft(a string, ai int, b string, bi int) (int, int, int) {
	for {
		ad := ai < len(a) && isDigitByte(a[ai])
		bd := bi < len(b) && isDigitByte(b[bi])
		switch {
		case !ad && !bd:
			return 0, ai, bi
		case !ad:
			return -1, ai, bi
		case !bd:
			return 1, ai, bi
		case a[ai] < b[bi]:
			return -1, ai, bi
		case a[ai] > b[bi]:
			return 1, ai, bi
		}
		ai++
		bi++
	}
}

// natCmpRight — compare_right: побеждает более длинный ряд цифр, при равенстве
// длины — большее значение (запоминается в bias).
func natCmpRight(a string, ai int, b string, bi int) (int, int, int) {
	bias := 0
	for {
		ad := ai < len(a) && isDigitByte(a[ai])
		bd := bi < len(b) && isDigitByte(b[bi])
		switch {
		case !ad && !bd:
			return bias, ai, bi
		case !ad:
			return -1, ai, bi
		case !bd:
			return 1, ai, bi
		case a[ai] < b[bi]:
			if bias == 0 {
				bias = -1
			}
		case a[ai] > b[bi]:
			if bias == 0 {
				bias = 1
			}
		}
		ai++
		bi++
	}
}

func strcspnFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	mask := arg(ctx, 1).ToString()
	off := 0
	if len(ctx.Args()) >= 3 {
		off = int(arg(ctx, 2).ToInt())
		if off < 0 {
			off = len(s) + off
			if off < 0 {
				off = 0
			}
		}
		if off > len(s) {
			return vm.Int(0)
		}
	}
	sub := s[off:]
	if len(ctx.Args()) >= 4 {
		l := int(arg(ctx, 3).ToInt())
		if l < 0 {
			l = len(s) - off + l
			if l < 0 {
				l = 0
			}
		}
		if l < len(sub) {
			sub = sub[:l]
		}
	}
	if mask == "" {
		// C-строка: пустая маска использует strlen до первого NUL.
		if z := strings.IndexByte(sub, 0); z >= 0 {
			return vm.Int(int64(z))
		}
		return vm.Int(int64(len(sub)))
	}
	n := 0
	for n < len(sub) && strings.IndexByte(mask, sub[n]) < 0 {
		n++
	}
	return vm.Int(int64(n))
}

func similarCount(a, b string) int {
	maxLen, pos1, pos2 := 0, 0, 0
	for i := 0; i < len(a); i++ {
		for j := 0; j < len(b); j++ {
			l := 0
			for i+l < len(a) && j+l < len(b) && a[i+l] == b[j+l] {
				l++
			}
			if l > maxLen {
				maxLen, pos1, pos2 = l, i, j
			}
		}
	}
	if maxLen == 0 {
		return 0
	}
	sum := maxLen
	if pos1 > 0 && pos2 > 0 {
		sum += similarCount(a[:pos1], b[:pos2])
	}
	if pos1+maxLen < len(a) && pos2+maxLen < len(b) {
		sum += similarCount(a[pos1+maxLen:], b[pos2+maxLen:])
	}
	return sum
}

func similarTextFn(ctx *vm.Context) vm.Value {
	s1 := arg(ctx, 0).ToString()
	s2 := arg(ctx, 1).ToString()
	sim := similarCount(s1, s2)
	if len(ctx.Args()) >= 3 {
		denom := (float64(len(s1)) + float64(len(s2))) / 2.0
		pct := 0.0
		if denom != 0 {
			pct = float64(sim) * 100 / denom
		}
		pregWriteRef(ctx, 2, vm.MakeFloat(pct))
	}
	return vm.Int(int64(sim))
}

func levenshteinFn(ctx *vm.Context) vm.Value {
	s1 := arg(ctx, 0).ToString()
	s2 := arg(ctx, 1).ToString()
	ci, cr, cd := 1, 1, 1
	if len(ctx.Args()) >= 3 {
		ci = int(arg(ctx, 2).ToInt())
	}
	if len(ctx.Args()) >= 4 {
		cr = int(arg(ctx, 3).ToInt())
	}
	if len(ctx.Args()) >= 5 {
		cd = int(arg(ctx, 4).ToInt())
	}
	m, n := len(s1), len(s2)
	if m == 0 {
		return vm.Int(int64(n * ci))
	}
	if n == 0 {
		return vm.Int(int64(m * cd))
	}
	prev := make([]int, n+1)
	cur := make([]int, n+1)
	for j := 0; j <= n; j++ {
		prev[j] = j * ci
	}
	for i := 1; i <= m; i++ {
		cur[0] = i * cd
		for j := 1; j <= n; j++ {
			cost := cr
			if s1[i-1] == s2[j-1] {
				cost = 0
			}
			best := prev[j-1] + cost
			if v := prev[j] + cd; v < best {
				best = v
			}
			if v := cur[j-1] + ci; v < best {
				best = v
			}
			cur[j] = best
		}
		prev, cur = cur, prev
	}
	return vm.Int(int64(prev[n]))
}

func soundex(s string) string {
	if s == "" {
		return "0000"
	}
	up := strings.ToUpper(s)
	if up[0] < 'A' || up[0] > 'Z' {
		return "0000"
	}
	code := func(c byte) byte {
		switch c {
		case 'B', 'F', 'P', 'V':
			return '1'
		case 'C', 'G', 'J', 'K', 'Q', 'S', 'X', 'Z':
			return '2'
		case 'D', 'T':
			return '3'
		case 'L':
			return '4'
		case 'M', 'N':
			return '5'
		case 'R':
			return '6'
		}
		return 0
	}
	out := []byte{up[0]}
	last := code(up[0])
	for i := 1; i < len(up) && len(out) < 4; i++ {
		c := code(up[i])
		if c != 0 && c != last {
			out = append(out, c)
		}
		last = c
	}
	for len(out) < 4 {
		out = append(out, '0')
	}
	return string(out)
}

func quotemeta(s string) string {
	const special = ".\\+*?[^]$()"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if strings.IndexByte(special, s[i]) >= 0 {
			b.WriteByte('\\')
		}
		b.WriteByte(s[i])
	}
	return b.String()
}

func addcslashesFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) >= 2 && arg(ctx, 1).Type() == vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("addcslashes", 2, "characters", "string", "array", "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	s := argToStringVal(ctx, 0)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	what := argToStringVal(ctx, 1)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if len(s) == 0 {
		return vm.Str("")
	}
	if what == "" {
		return vm.Str(s)
	}
	var flags [256]bool
	for i := 0; i < len(what); i++ {
		if i+3 < len(what) && what[i+1] == '.' && what[i+2] == '.' {
			lo, hi := what[i], what[i+3]
			if lo <= hi {
				for c := int(lo); c <= int(hi); c++ {
					flags[c] = true
				}
				i += 3
				continue
			}
			ctx.VM().Warning("addcslashes(): Invalid '..'-range, '..'-range needs to be incrementing")
		}
		flags[what[i]] = true
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if flags[c] {
			if c < 32 || c > 126 {
				b.WriteByte('\\')
				switch c {
				case '\n':
					b.WriteByte('n')
				case '\t':
					b.WriteByte('t')
				case '\r':
					b.WriteByte('r')
				case '\a':
					b.WriteByte('a')
				case '\v':
					b.WriteByte('v')
				case '\b':
					b.WriteByte('b')
				case '\f':
					b.WriteByte('f')
				default:
					oct := strconv.FormatInt(int64(c), 8)
					for len(oct) < 3 {
						oct = "0" + oct
					}
					b.WriteString(oct)
				}
				continue
			}
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	return vm.Str(b.String())
}

func arg1Str(ctx *vm.Context) string {
	return arg(ctx, 1).ToString()
}

func tagName(tag string) string {
	i := 0
	for i < len(tag) && (tag[i] == '/' || tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r') {
		i++
	}
	start := i
	for i < len(tag) && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' {
		i++
	}
	return tag[start:i]
}

func stripTagsFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	allow := ""
	allowSet := false
	if len(ctx.Args()) >= 2 {
		a := arg(ctx, 1)
		switch a.Type() {
		case vm.NullType:
			// без разрешённых тегов
		case vm.ArrayType:
			for _, v := range a.AsArray().Values() {
				allow += "<" + v.ToString() + ">"
			}
			allowSet = allow != ""
		case vm.ResourceType:
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("strip_tags", 2, "allowed_tags", "array|string|null", "resource", "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		default:
			// int/float/bool/object в нестрогом режиме приводятся к строке.
			allow = a.ToString()
			allowSet = true
		}
	}
	return vm.Str(stripTagsCore(s, strings.ToLower(allow), allowSet))
}

func isHTMLSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\v' || c == '\f' || c == '\r'
}

// phpTagFind повторяет php_tag_find: нормализует тег до вида <name> и ищет
// его в строке разрешённых тегов.
func phpTagFind(tag []byte, set string) bool {
	var norm []byte
	state := 0
	done := false
	for i := 0; i < len(tag) && !done; i++ {
		c := tag[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		switch {
		case c == '<':
			norm = append(norm, c)
		case c == '>':
			done = true
		case isHTMLSpace(c):
			if state == 1 {
				done = true
			}
		default:
			if state == 0 {
				state = 1
			}
			if c != '/' || (i == 0 || tag[i-1] != '<') && (i+1 >= len(tag) || tag[i+1] != '>') {
				norm = append(norm, c)
			}
		}
	}
	norm = append(norm, '>')
	return strings.Contains(set, string(norm))
}

// stripTagsCore — порт php_strip_tags_ex из ext/standard/string.c.
func stripTagsCore(s, allow string, allowSet bool) string {
	buf := s
	end := len(buf)
	rp := make([]byte, 0, len(buf))
	var tbuf []byte
	var lc byte
	state := 0
	depth := 0
	var inQ byte
	br := 0
	isXML := false
	p := 0
	for {
		switch state {
		case 0:
			if p >= end {
				return string(rp)
			}
			c := buf[p]
			switch {
			case c == 0:
				p++
			case c == '<':
				if inQ != 0 {
					p++
					continue
				}
				if p+1 < end && isHTMLSpace(buf[p+1]) {
					rp = append(rp, c)
					p++
					continue
				}
				lc = '<'
				state = 1
				if allowSet {
					tbuf = append(tbuf, '<')
				}
				p++
			case c == '>':
				if depth != 0 {
					depth--
					p++
					continue
				}
				if inQ != 0 {
					p++
					continue
				}
				rp = append(rp, c)
				p++
			default:
				rp = append(rp, c)
				p++
			}
		case 1:
			if p >= end {
				return string(rp)
			}
			c := buf[p]
			switch {
			case c == 0:
				p++
			case c == '<':
				if inQ != 0 {
					p++
					continue
				}
				if p+1 < end && isHTMLSpace(buf[p+1]) {
					if allowSet {
						tbuf = append(tbuf, c)
					}
					p++
					continue
				}
				depth++
				p++
			case c == '>':
				if depth != 0 {
					depth--
					p++
					continue
				}
				if inQ != 0 {
					p++
					continue
				}
				lc = '>'
				if isXML && p >= 1 && buf[p-1] == '-' {
					p++
					continue
				}
				inQ = 0
				state = 0
				isXML = false
				if allowSet {
					tbuf = append(tbuf, '>')
					if phpTagFind(tbuf, allow) {
						rp = append(rp, tbuf...)
					}
					tbuf = tbuf[:0]
				}
				p++
			case c == '"' || c == '\'':
				if p != 0 && (inQ == 0 || c == inQ) {
					if inQ != 0 {
						inQ = 0
					} else {
						inQ = c
					}
				}
				if allowSet {
					tbuf = append(tbuf, c)
				}
				p++
			case c == '!':
				if p >= 1 && buf[p-1] == '<' {
					state = 3
					lc = c
					p++
				} else {
					if allowSet {
						tbuf = append(tbuf, c)
					}
					p++
				}
			case c == '?':
				if p >= 1 && buf[p-1] == '<' {
					br = 0
					state = 2
					p++
				} else {
					if allowSet {
						tbuf = append(tbuf, c)
					}
					p++
				}
			default:
				if allowSet {
					tbuf = append(tbuf, c)
				}
				p++
			}
		case 2:
			if p >= end {
				return string(rp)
			}
			c := buf[p]
			switch {
			case c == '(':
				if lc != '"' && lc != '\'' {
					lc = '('
					br++
				}
				p++
			case c == ')':
				if lc != '"' && lc != '\'' {
					lc = ')'
					br--
				}
				p++
			case c == '>':
				if depth != 0 {
					depth--
					p++
					continue
				}
				if inQ != 0 {
					p++
					continue
				}
				if br == 0 && p >= 1 && lc != '"' && buf[p-1] == '?' {
					inQ = 0
					state = 0
					tbuf = tbuf[:0]
				}
				p++
			case c == '"' || c == '\'':
				if p >= 1 && buf[p-1] != '\\' {
					if lc == c {
						lc = 0
					} else if lc != '\\' {
						lc = c
					}
					if p != 0 && (inQ == 0 || c == inQ) {
						if inQ != 0 {
							inQ = 0
						} else {
							inQ = c
						}
					}
				}
				p++
			case c == 'l' || c == 'L':
				if p > 4 &&
					(buf[p-1] == 'm' || buf[p-1] == 'M') &&
					(buf[p-2] == 'x' || buf[p-2] == 'X') &&
					buf[p-3] == '?' && buf[p-4] == '<' {
					state = 1
					isXML = true
					p++
				} else {
					p++
				}
			default:
				p++
			}
		case 3:
			if p >= end {
				return string(rp)
			}
			c := buf[p]
			switch {
			case c == '>':
				if depth != 0 {
					depth--
					p++
					continue
				}
				if inQ != 0 {
					p++
					continue
				}
				inQ = 0
				state = 0
				tbuf = tbuf[:0]
				p++
			case c == '"' || c == '\'':
				if p != 0 && buf[p-1] != '\\' && (inQ == 0 || c == inQ) {
					if inQ != 0 {
						inQ = 0
					} else {
						inQ = c
					}
				}
				p++
			case c == '-':
				if p >= 2 && buf[p-1] == '-' && buf[p-2] == '!' {
					state = 4
					p++
				} else {
					p++
				}
			case c == 'E' || c == 'e':
				if p > 6 &&
					(buf[p-1] == 'p' || buf[p-1] == 'P') &&
					(buf[p-2] == 'y' || buf[p-2] == 'Y') &&
					(buf[p-3] == 't' || buf[p-3] == 'T') &&
					(buf[p-4] == 'c' || buf[p-4] == 'C') &&
					(buf[p-5] == 'o' || buf[p-5] == 'O') &&
					(buf[p-6] == 'd' || buf[p-6] == 'D') {
					state = 1
					p++
				} else {
					p++
				}
			default:
				p++
			}
		case 4:
			closed := false
			for p < end {
				c := buf[p]
				if c == '>' && inQ == 0 && p >= 2 && buf[p-1] == '-' && buf[p-2] == '-' {
					inQ = 0
					state = 0
					tbuf = tbuf[:0]
					p++
					closed = true
					break
				}
				p++
			}
			if !closed {
				return string(rp)
			}
		}
	}
}

// splitAllowedTags разбирает строку allowed_tags на имена тегов.
func splitAllowedTags(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool {
		return r == '<' || r == '>' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func findTagEnd(s string, start int) int {
	// PHP-теги и ASP-теги закрываются последовательностями ?> и %>.
	if start+1 < len(s) {
		switch s[start+1] {
		case '%':
			if k := strings.Index(s[start+2:], "%>"); k >= 0 {
				return start + 2 + k + 1
			}
		case '?':
			if k := strings.Index(s[start+2:], "?>"); k >= 0 {
				return start + 2 + k + 1
			}
		}
	}
	quote := byte(0)
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == '>' {
			return i
		}
	}
	return -1
}

func argSliceStr(ctx *vm.Context, i int) (string, bool) {
	a := arg(ctx, i)
	if a.Type() == vm.NullType {
		return "", false
	}
	return a.ToString(), true
}

// isUTF8Encoding сообщает, что имя кодировки — UTF-8.
func isUTF8Encoding(enc string) bool {
	e := strings.ToUpper(strings.ReplaceAll(enc, "_", "-"))
	return e == "UTF-8" || e == "UTF8" || e == "UTF-8-" || strings.HasPrefix(e, "UTF-8 ")
}

// htmlEncode реализует htmlspecialchars/htmlentities: кодирует специальные
// символы, а при all=true — все символы, для которых есть именованная сущность
// в выбранном диалекте (HTML 4.01 / HTML 5).
func htmlEncode(ctx *vm.Context, s string, flags int64, doubleEncode, all bool, enc string) string {
	quote := flags & 3
	dt := flags & 48
	table := html4EntityByRune
	switch dt {
	case 48:
		table = html5EntityByRune
	case 16:
		table = nil // XML 1.0: только базовые сущности (&,<,>,")
	}
	conv := enc != "" && !isUTF8Encoding(enc)
	if conv {
		s = mbConvertString(s, enc, "UTF-8")
	}
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c < 0x80 {
			switch c {
			case '&':
				if !doubleEncode && isEntityAt(s, i) {
					b.WriteByte(c)
				} else {
					b.WriteString("&amp;")
				}
			case '<':
				b.WriteString("&lt;")
			case '>':
				b.WriteString("&gt;")
			case '"':
				if quote >= 2 {
					b.WriteString("&quot;")
				} else {
					b.WriteByte(c)
				}
			case '\'':
				if quote == 3 {
					if dt == 0 {
						b.WriteString("&#039;")
					} else {
						b.WriteString("&apos;")
					}
				} else {
					b.WriteByte(c)
				}
			default:
				if all {
					if e, ok := table[rune(c)]; ok {
						b.WriteString(e)
					} else {
						b.WriteByte(c)
					}
				} else {
					b.WriteByte(c)
				}
			}
			i++
			continue
		}
		r, size, valid := decodeUTF8Char(s, i)
		if !valid {
			if flags&8 != 0 { // ENT_SUBSTITUTE
				b.WriteString("\uFFFD")
				i += size
				continue
			}
			if flags&4 != 0 { // ENT_IGNORE
				i += size
				continue
			}
			// Без ENT_SUBSTITUTE/ENT_IGNORE невалидная последовательность
			// обнуляет весь результат.
			return ""
		}
		if all {
			if e, ok := table[r]; ok {
				b.WriteString(e)
			} else {
				b.WriteString(s[i : i+size])
			}
		} else {
			b.WriteString(s[i : i+size])
		}
		i += size
	}
	res := b.String()
	if conv {
		res = mbConvertString(res, "UTF-8", enc)
	}
	return res
}

func isEntityAt(s string, i int) bool {
	if i >= len(s) || s[i] != '&' {
		return false
	}
	j := i + 1
	if j < len(s) && s[j] == '#' {
		j++
		if j < len(s) && (s[j] == 'x' || s[j] == 'X') {
			j++
			start := j
			for j < len(s) && isHexDigit(s[j]) {
				j++
			}
			return j > start && j < len(s) && s[j] == ';'
		}
		start := j
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
		return j > start && j < len(s) && s[j] == ';'
	}
	if j >= len(s) || !((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z')) {
		return false
	}
	start := j
	for j < len(s) && ((s[j] >= 'a' && s[j] <= 'z') || (s[j] >= 'A' && s[j] <= 'Z') || (s[j] >= '0' && s[j] <= '9')) {
		j++
	}
	if j >= len(s) || s[j] != ';' {
		return false
	}
	name := s[start:j]
	if _, ok := html4EntityNameToRune[name]; ok {
		return true
	}
	if _, ok := html5EntityNameToRune[name]; ok {
		return true
	}
	_, ok := namedEntities[name]
	return ok
}

func htmlEncodingArg(ctx *vm.Context, fname string) string {
	// Явно не заданная, null или пустая кодировка -> get_default_charset().
	if len(ctx.Args()) < 3 || arg(ctx, 2).IsNull() {
		return htmlDefaultCharset()
	}
	enc := arg(ctx, 2).ToString()
	if enc == "" {
		return htmlDefaultCharset()
	}
	if c, ok := htmlCanonCharset(enc); ok {
		return c
	}
	ctx.VM().Warning(fname + "(): Charset \"" + enc + "\" is not supported, assuming UTF-8")
	return "UTF-8"
}

// hlKind — категория токена подсветки/стриппера, соответствующая ветвям
// switch в zend_highlight.c (zend_strip.c).
type hlKind uint8

const (
	hlInlineHTML hlKind = iota
	hlOpenTag
	hlOpenTagEcho
	hlCloseTag
	hlWhitespace
	hlComment
	hlDocComment
	hlString
	hlVariable
	hlNumber
	hlIdent
	hlKeyword
	hlHeredocStart
	hlHeredocBody
	hlHeredocEnd
)

// hlToken — лексема исходника с точным сырым текстом.
type hlToken struct {
	kind hlKind
	text string
}

// phpKeyword — зарезервированные слова PHP 8.3 (лексер возвращает их без
// семантического значения -> в zend_highlight они окрашиваются как keyword).
// true/false/null и «магические» константы — это T_STRING/T_* со значением,
// поэтому здесь их нет.
var phpKeyword = map[string]bool{
	"abstract": true, "and": true, "array": true, "as": true, "break": true,
	"case": true, "catch": true, "class": true, "clone": true, "const": true,
	"continue": true, "declare": true, "default": true, "die": true, "do": true,
	"echo": true, "else": true, "elseif": true, "empty": true, "enddeclare": true,
	"endfor": true, "endforeach": true, "endif": true, "endswitch": true,
	"endwhile": true, "enum": true, "eval": true, "exit": true, "extends": true,
	"final": true, "finally": true, "fn": true, "for": true, "foreach": true,
	"function": true, "global": true, "goto": true, "if": true,
	"implements": true, "include": true, "include_once": true, "instanceof": true,
	"insteadof": true, "interface": true, "isset": true, "list": true,
	"match": true, "namespace": true, "new": true, "or": true, "print": true,
	"private": true, "protected": true, "public": true, "readonly": true,
	"require": true, "require_once": true, "return": true, "static": true,
	"switch": true, "throw": true, "trait": true, "try": true, "unset": true,
	"use": true, "var": true, "while": true, "xor": true, "yield": true,
}

// scanPHP разбивает исходник смешанного PHP/HTML на лексемы, повторяя
// разбиение zend_language_scanner.l в объёме, нужном для подсветки и стриппера.
func scanPHP(src string) []hlToken {
	n := len(src)
	var toks []hlToken
	i := 0
	inPHP := false
	for i < n {
		if !inPHP {
			start := i
			for i < n {
				j := strings.Index(src[i:], "<?")
				if j < 0 {
					i = n
					break
				}
				i += j
				if isOpenTagAt(src, i) {
					break
				}
				i += 2
			}
			if i > start {
				toks = append(toks, hlToken{hlInlineHTML, src[start:i]})
			}
			if i >= n {
				break
			}
			rest := src[i:]
			switch {
			case len(rest) >= 5 && strings.EqualFold(rest[:5], "<?php"):
				end := i + 5
				if end < n && isSpaceByte(src[end]) {
					if src[end] == '\r' && end+1 < n && src[end+1] == '\n' {
						end += 2
					} else {
						end++
					}
				}
				toks = append(toks, hlToken{hlOpenTag, src[i:end]})
				i = end
			case strings.HasPrefix(rest, "<?="):
				toks = append(toks, hlToken{hlOpenTagEcho, src[i : i+3]})
				i += 3
			default:
				toks = append(toks, hlToken{hlOpenTag, src[i : i+2]})
				i += 2
			}
			inPHP = true
			continue
		}
		c := src[i]
		switch {
		case c == '?' && i+1 < n && src[i+1] == '>':
			end := i + 2
			if end < n && src[end] == '\n' {
				end++
			} else if end+1 < n && src[end] == '\r' && src[end+1] == '\n' {
				end += 2
			}
			toks = append(toks, hlToken{hlCloseTag, src[i:end]})
			i = end
			inPHP = false
		case isSpaceByte(c):
			start := i
			for i < n && isSpaceByte(src[i]) {
				i++
			}
			toks = append(toks, hlToken{hlWhitespace, src[start:i]})
		case c == '/' && i+1 < n && src[i+1] == '/':
			start := i
			i += 2
			for i < n && src[i] != '\n' {
				if src[i] == '?' && i+1 < n && src[i+1] == '>' {
					break
				}
				i++
			}
			toks = append(toks, hlToken{hlComment, src[start:i]})
		case c == '#':
			if i+1 < n && src[i+1] == '[' {
				// T_ATTRIBUTE — как keyword (содержимое подсвечивается обычно).
				toks = append(toks, hlToken{hlKeyword, src[i : i+2]})
				i += 2
				continue
			}
			start := i
			i++
			for i < n && src[i] != '\n' {
				if src[i] == '?' && i+1 < n && src[i+1] == '>' {
					break
				}
				i++
			}
			toks = append(toks, hlToken{hlComment, src[start:i]})
		case c == '/' && i+1 < n && src[i+1] == '*':
			start := i
			i += 2
			for i < n && !(src[i] == '*' && i+1 < n && src[i+1] == '/') {
				i++
			}
			if i < n {
				i += 2
			}
			kind := hlComment
			// /** ... */ -> T_DOC_COMMENT (но /**/ остаётся обычным комментарием).
			if i-start > 4 && src[start+2] == '*' && src[start+3] != '/' {
				kind = hlDocComment
			}
			toks = append(toks, hlToken{kind, src[start:i]})
		case c == '\'':
			start := i
			i++
			for i < n {
				if src[i] == '\\' && i+1 < n {
					i += 2
					continue
				}
				if src[i] == '\'' {
					i++
					break
				}
				i++
			}
			toks = append(toks, hlToken{hlString, src[start:i]})
		case c == '"':
			// Двойная кавычка и интерполяция: Zend разбивает строку на
			// T_ENCAPSED_AND_WHITESPACE (цвет строки), T_VARIABLE и т.п.
			toks = append(toks, hlToken{hlString, "\""})
			i++
			litStart := i
			for i < n {
				ch := src[i]
				if ch == '\\' && i+1 < n {
					i += 2
					continue
				}
				if ch == '"' {
					break
				}
				if ch == '$' && i+1 < n && isIdentStart(src[i+1]) {
					if i > litStart {
						toks = append(toks, hlToken{hlString, src[litStart:i]})
					}
					vs := i
					i += 2
					for i < n && isIdentPart(src[i]) {
						i++
					}
					if i < n && src[i] == '[' {
						depth := 1
						i++
						for i < n && depth > 0 {
							switch src[i] {
							case '[':
								depth++
							case ']':
								depth--
							}
							i++
						}
					} else {
						for i+1 < n && src[i] == '-' && src[i+1] == '>' {
							i += 2
							for i < n && isIdentPart(src[i]) {
								i++
							}
						}
					}
					toks = append(toks, hlToken{hlVariable, src[vs:i]})
					litStart = i
					continue
				}
				if ch == '$' && i+1 < n && src[i+1] == '{' {
					if i > litStart {
						toks = append(toks, hlToken{hlString, src[litStart:i]})
					}
					vs := i
					depth := 1
					i += 2
					for i < n && depth > 0 {
						switch src[i] {
						case '{':
							depth++
						case '}':
							depth--
						}
						i++
					}
					toks = append(toks, hlToken{hlVariable, src[vs:i]})
					litStart = i
					continue
				}
				i++
			}
			if i > litStart {
				toks = append(toks, hlToken{hlString, src[litStart:i]})
			}
			if i < n && src[i] == '"' {
				toks = append(toks, hlToken{hlString, "\""})
				i++
			}
		case c == '$':
			start := i
			i++
			if i < n && src[i] == '{' {
				depth := 1
				i++
				for i < n && depth > 0 {
					switch src[i] {
					case '{':
						depth++
					case '}':
						depth--
					}
					i++
				}
			} else if i < n && isIdentStart(src[i]) {
				for i < n && isIdentPart(src[i]) {
					i++
				}
			}
			toks = append(toks, hlToken{hlVariable, src[start:i]})
		case c >= '0' && c <= '9' || (c == '.' && i+1 < n && src[i+1] >= '0' && src[i+1] <= '9'):
			start := i
			i++
			for i < n && (isIdentPart(src[i]) || src[i] == '.') {
				i++
			}
			toks = append(toks, hlToken{hlNumber, src[start:i]})
		case c == '<' && i+2 < n && src[i+1] == '<' && src[i+2] == '<':
			i = scanHeredoc(src, i, &toks)
		case isIdentStart(c):
			start := i
			for i < n && isIdentPart(src[i]) {
				i++
			}
			w := src[start:i]
			if phpKeyword[strings.ToLower(w)] {
				toks = append(toks, hlToken{hlKeyword, w})
			} else {
				toks = append(toks, hlToken{hlIdent, w})
			}
		default:
			toks = append(toks, hlToken{hlKeyword, src[i : i+1]})
			i++
		}
	}
	return toks
}

// scanHeredoc сканирует heredoc/nowdoc начиная с `<<<`. Возвращает новую
// позицию; лексемы (start/body/end) добавляются в toks.
func scanHeredoc(src string, i int, toks *[]hlToken) int {
	n := len(src)
	start := i
	i += 3
	for i < n && (src[i] == ' ' || src[i] == '\t') {
		i++
	}
	var quote byte
	if i < n && (src[i] == '\'' || src[i] == '"') {
		quote = src[i]
		i++
	}
	lstart := i
	for i < n && isIdentPart(src[i]) {
		i++
	}
	label := src[lstart:i]
	if quote != 0 && i < n && src[i] == quote {
		i++
	}
	for i < n && src[i] != '\n' {
		i++
	}
	if i < n {
		i++
	}
	*toks = append(*toks, hlToken{hlHeredocStart, src[start:i]})
	if label == "" {
		return i
	}
	bodyStart := i
	pos := i
	endLabelStart := -1
	for pos <= n {
		q := pos
		for q < n && (src[q] == ' ' || src[q] == '\t') {
			q++
		}
		if q+len(label) <= n && src[q:q+len(label)] == label {
			after := q + len(label)
			if after >= n || !isIdentPart(src[after]) {
				endLabelStart = pos
				break
			}
		}
		nl := strings.IndexByte(src[pos:], '\n')
		if nl < 0 {
			break
		}
		pos += nl + 1
	}
	if endLabelStart < 0 {
		*toks = append(*toks, hlToken{hlHeredocBody, src[bodyStart:]})
		return n
	}
	*toks = append(*toks, hlToken{hlHeredocBody, src[bodyStart:endLabelStart]})
	indent := 0
	for endLabelStart+indent < n && (src[endLabelStart+indent] == ' ' || src[endLabelStart+indent] == '\t') {
		indent++
	}
	end := endLabelStart + indent + len(label)
	*toks = append(*toks, hlToken{hlHeredocEnd, src[endLabelStart:end]})
	return end
}

// highlightColor читает INI-цвет подсветки с запасным значением.
func highlightColor(name, def string) string {
	if v, ok := IniValue(name); ok && v != "" {
		return v
	}
	return def
}

// highlightHTMLPutc пишет символ с экранированием как zend_html_putc.
func highlightHTMLPutc(b *strings.Builder, c byte) {
	switch c {
	case '<':
		b.WriteString("&lt;")
	case '>':
		b.WriteString("&gt;")
	case '&':
		b.WriteString("&amp;")
	case '\t':
		b.WriteString("    ")
	default:
		b.WriteByte(c)
	}
}

// highlightHTMLPuts пишет строку с экранированием (zend_html_puts).
func highlightHTMLPuts(b *strings.Builder, s string) {
	for i := 0; i < len(s); i++ {
		highlightHTMLPutc(b, s[i])
	}
}

// highlightRender повторяет zend_highlight().
func highlightRender(src string) string {
	cStr := highlightColor("highlight.string", "#DD0000")
	cComment := highlightColor("highlight.comment", "#FF8000")
	cKeyword := highlightColor("highlight.keyword", "#007700")
	cDefault := highlightColor("highlight.default", "#0000BB")
	cHTML := highlightColor("highlight.html", "#000000")

	var b strings.Builder
	b.WriteString("<pre><code style=\"color: " + cHTML + "\">")
	last := cHTML
	for _, t := range scanPHP(src) {
		if t.kind == hlWhitespace {
			highlightHTMLPuts(&b, t.text)
			continue
		}
		next := cDefault
		switch t.kind {
		case hlInlineHTML:
			next = cHTML
		case hlComment, hlDocComment:
			next = cComment
		case hlString, hlHeredocBody:
			next = cStr
		case hlKeyword, hlHeredocStart, hlHeredocEnd:
			next = cKeyword
		}
		if last != next {
			if last != cHTML {
				b.WriteString("</span>")
			}
			last = next
			if last != cHTML {
				b.WriteString("<span style=\"color: " + last + "\">")
			}
		}
		highlightHTMLPuts(&b, t.text)
	}
	if last != cHTML {
		b.WriteString("</span>")
	}
	b.WriteString("</code></pre>")
	return b.String()
}

// phpStrip повторяет zend_strip().
func phpStrip(src string) string {
	toks := scanPHP(src)
	var b strings.Builder
	prevSpace := false
	for i := 0; i < len(toks); i++ {
		t := toks[i]
		switch t.kind {
		case hlWhitespace:
			if !prevSpace {
				b.WriteString(" ")
				prevSpace = true
			}
			continue
		case hlComment, hlDocComment:
			continue
		case hlHeredocEnd:
			b.WriteString(t.text)
			if i+1 < len(toks) && toks[i+1].kind != hlWhitespace {
				b.WriteString(toks[i+1].text)
				i++
			}
			b.WriteString("\n")
			prevSpace = true
			continue
		default:
			b.WriteString(t.text)
		}
		prevSpace = false
	}
	return b.String()
}

// highlightStringFn — highlight_string($string, $return = false).
func highlightStringFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		return vm.Bool(false)
	}
	out := highlightRender(arg0(ctx).ToString())
	if len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool() {
		return vm.Str(out)
	}
	ctx.Print(out)
	return vm.Bool(true)
}

// highlightFileFn — highlight_file($filename, $return = false) (и show_source).
func highlightFileFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		return vm.Bool(false)
	}
	name := arg0(ctx).ToString()
	data, ok := readPathBytes(name)
	if !ok {
		ctx.VM().Warning("highlight_file(" + name + "): Failed to open stream: " + openFailReason(name, "r"))
		ctx.VM().Warning("highlight_file(): Failed opening '" + name + "' for highlighting")
		return vm.Bool(false)
	}
	out := highlightRender(string(data))
	if len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool() {
		return vm.Str(out)
	}
	ctx.Print(out)
	return vm.Bool(true)
}

// phpStripWhitespaceFn — php_strip_whitespace($filename).
func phpStripWhitespaceFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) == 0 {
		return vm.Str("")
	}
	name := arg0(ctx).ToString()
	data, ok := readPathBytes(name)
	if !ok {
		ctx.VM().Warning("php_strip_whitespace(" + name + "): Failed to open stream: " + openFailReason(name, "r"))
		return vm.Str("")
	}
	return vm.Str(phpStrip(string(data)))
}

func htmlspecialcharsFn(ctx *vm.Context) vm.Value {
	flags := int64(11)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	doubleEncode := true
	if len(ctx.Args()) >= 4 {
		doubleEncode = arg(ctx, 3).ToBool()
	}
	return vm.Str(htmlEncodeCore(arg0(ctx).ToString(), flags, doubleEncode, false, htmlEncodingArg(ctx, "htmlspecialchars")))
}

func htmlentitiesFn(ctx *vm.Context) vm.Value {
	flags := int64(11)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	doubleEncode := true
	if len(ctx.Args()) >= 4 {
		doubleEncode = arg(ctx, 3).ToBool()
	}
	enc := htmlEncodingArg(ctx, "htmlentities")
	if htmlCharsetPartial(enc) {
		ctx.VM().Notice("htmlentities(): Only basic entities substitution is supported for multi-byte encodings other than UTF-8; functionality is equivalent to htmlspecialchars")
	}
	return vm.Str(htmlEncodeCore(arg0(ctx).ToString(), flags, doubleEncode, true, enc))
}

// isMultibyteEncoding сообщает, что кодировка — многобайтовая и не UTF-8.
func isMultibyteEncoding(enc string) bool {
	e := strings.ToUpper(strings.ReplaceAll(enc, "_", "-"))
	if isUTF8Encoding(enc) {
		return false
	}
	for _, p := range []string{"SJIS", "EUC-JP", "EUCJP", "CP932", "CP51932", "JIS", "2022", "EUC-KR", "BIG-5", "BIG5", "GB2312", "GBK", "CP936", "UTF-16", "UTF-32"} {
		if strings.Contains(e, p) {
			return true
		}
	}
	return false
}

var namedEntities = map[string]string{
	"amp": "&", "lt": "<", "gt": ">", "quot": "\"", "apos": "'",
	"nbsp": "\u00a0", "iexcl": "\u00a1", "cent": "\u00a2", "pound": "\u00a3",
	"curren": "\u00a4", "yen": "\u00a5", "brvbar": "\u00a6", "sect": "\u00a7",
	"uml": "\u00a8", "copy": "\u00a9", "ordf": "\u00aa", "laquo": "\u00ab",
	"not": "\u00ac", "shy": "\u00ad", "reg": "\u00ae", "macr": "\u00af",
	"deg": "\u00b0", "plusmn": "\u00b1", "sup2": "\u00b2", "sup3": "\u00b3",
	"acute": "\u00b4", "micro": "\u00b5", "para": "\u00b6", "middot": "\u00b7",
	"cedil": "\u00b8", "sup1": "\u00b9", "ordm": "\u00ba", "raquo": "\u00bb",
	"frac14": "\u00bc", "frac12": "\u00bd", "frac34": "\u00be", "iquest": "\u00bf",
	"Agrave": "\u00c0", "Aacute": "\u00c1", "Acirc": "\u00c2", "Atilde": "\u00c3",
	"Auml": "\u00c4", "Aring": "\u00c5", "AElig": "\u00c6", "Ccedil": "\u00c7",
	"Egrave": "\u00c8", "Eacute": "\u00c9", "Ecirc": "\u00ca", "Euml": "\u00cb",
	"Igrave": "\u00cc", "Iacute": "\u00cd", "Icirc": "\u00ce", "Iuml": "\u00cf",
	"ETH": "\u00d0", "Ntilde": "\u00d1", "Ograve": "\u00d2", "Oacute": "\u00d3",
	"Ocirc": "\u00d4", "Otilde": "\u00d5", "Ouml": "\u00d6", "times": "\u00d7",
	"Oslash": "\u00d8", "Ugrave": "\u00d9", "Uacute": "\u00da", "Ucirc": "\u00db",
	"Uuml": "\u00dc", "Yacute": "\u00dd", "THORN": "\u00de", "szlig": "\u00df",
	"agrave": "\u00e0", "aacute": "\u00e1", "acirc": "\u00e2", "atilde": "\u00e3",
	"auml": "\u00e4", "aring": "\u00e5", "aelig": "\u00e6", "ccedil": "\u00e7",
	"egrave": "\u00e8", "eacute": "\u00e9", "ecirc": "\u00ea", "euml": "\u00eb",
	"igrave": "\u00ec", "iacute": "\u00ed", "icirc": "\u00ee", "iuml": "\u00ef",
	"eth": "\u00f0", "ntilde": "\u00f1", "ograve": "\u00f2", "oacute": "\u00f3",
	"ocirc": "\u00f4", "otilde": "\u00f5", "ouml": "\u00f6", "divide": "\u00f7",
	"oslash": "\u00f8", "ugrave": "\u00f9", "uacute": "\u00fa", "ucirc": "\u00fb",
	"uuml": "\u00fc", "yacute": "\u00fd", "thorn": "\u00fe", "yuml": "\u00ff",
	"OElig": "\u0152", "oelig": "\u0153", "Scaron": "\u0160", "scaron": "\u0161",
	"Yuml": "\u0178", "fnof": "\u0192", "circ": "\u02c6", "tilde": "\u02dc",
	"Alpha": "\u0391", "Beta": "\u0392", "Gamma": "\u0393", "Delta": "\u0394",
	"Epsilon": "\u0395", "Zeta": "\u0396", "Eta": "\u0397", "Theta": "\u0398",
	"lambda": "\u03bb", "mu": "\u03bc", "pi": "\u03c0", "sigma": "\u03c3",
	"ndash": "\u2013", "mdash": "\u2014", "lsquo": "\u2018", "rsquo": "\u2019",
	"ldquo": "\u201c", "rdquo": "\u201d", "bull": "\u2022", "hellip": "\u2026",
	"trade": "\u2122", "euro": "\u20ac",
	"prime": "\u2032", "Prime": "\u2033",
}

// htmlSpecialcharsDecodeFn — htmlspecialchars_decode(): декодирует только
// «специальные» сущности (&amp; &lt; &gt; &quot; и их числовые эквиваленты,
// плюс &#039; при ENT_QUOTES), но не произвольные именованные сущности.
func htmlSpecialcharsDecodeFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	flags := int64(11)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	quote := flags & 3
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] != '&' {
			b.WriteByte(s[i])
			i++
			continue
		}
		semi := strings.IndexByte(s[i:], ';')
		if semi < 0 || semi > 32 {
			b.WriteByte(s[i])
			i++
			continue
		}
		ent := s[i+1 : i+semi]
		var r rune
		ok := false
		switch ent {
		case "amp":
			r, ok = '&', true
		case "lt":
			r, ok = '<', true
		case "gt":
			r, ok = '>', true
		case "quot":
			r, ok = '"', true
		case "apos":
			if flags&48 != 0 {
				r, ok = '\'', true
			}
		default:
			if len(ent) >= 2 && ent[0] == '#' {
				var code int64
				if ent[1] == 'x' || ent[1] == 'X' {
					if v, err := strconv.ParseInt(ent[2:], 16, 64); err == nil {
						code, ok = v, true
					}
				} else if v, err := strconv.ParseInt(ent[1:], 10, 64); err == nil {
					code, ok = v, true
				}
				if ok {
					r = rune(code)
					switch r {
					case '&', '<', '>', '"', '\'':
					default:
						ok = false
					}
				}
			}
		}
		if !ok {
			b.WriteByte(s[i])
			i++
			continue
		}
		switch r {
		case '"':
			if quote < 2 {
				b.WriteString(s[i : i+semi+1])
				i += semi + 1
				continue
			}
		case '\'':
			if quote != 3 {
				b.WriteString(s[i : i+semi+1])
				i += semi + 1
				continue
			}
		}
		b.WriteRune(r)
		i += semi + 1
	}
	return vm.Str(b.String())
}

func htmlEntityDecodeFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	flags := int64(11)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	enc := ""
	if len(ctx.Args()) >= 3 && !arg(ctx, 2).IsNull() {
		enc = arg(ctx, 2).ToString()
	}
	var cs string
	if enc == "" {
		cs = htmlDefaultCharset()
	} else if c, ok := htmlCanonCharset(enc); ok {
		cs = c
	} else {
		ctx.VM().Warning("html_entity_decode(): Charset \"" + enc + "\" is not supported, assuming UTF-8")
		cs = "UTF-8"
	}
	return vm.Str(htmlDecodeAll(s, flags, cs))
}

// isLatin1Encoding — ISO-8859-1 (или эквивалент), где всё > 0xFF не представимо.
func isLatin1Encoding(enc string) bool {
	e := strings.ToUpper(strings.ReplaceAll(enc, "_", "-"))
	return e == "ISO-8859-1" || e == "ISO8859-1" || e == "LATIN1" || e == "L1" || e == "CP819"
}

// latin1Encode переводит строку в ISO-8859-1: кодовые точки > 0xFF заменяются '?'.
func latin1Encode(s string) string {
	var b []byte
	for _, r := range s {
		if r <= 0xff {
			b = append(b, byte(r))
		} else {
			b = append(b, '?')
		}
	}
	return string(b)
}

func decodeEntity(ent string, quote, dt int64) (string, bool) {
	if ent == "" {
		return "", false
	}
	if ent[0] == '#' {
		var v int64
		var err error
		if len(ent) > 1 && (ent[1] == 'x' || ent[1] == 'X') {
			v, err = strconv.ParseInt(ent[2:], 16, 64)
		} else {
			v, err = strconv.ParseInt(ent[1:], 10, 64)
		}
		if err != nil || v < 0 {
			return "", false
		}
		if v == 34 && quote < 2 {
			return "", false
		}
		if v == 39 && quote != 3 {
			return "", false
		}
		return string(rune(v)), true
	}
	switch ent {
	case "quot":
		if quote < 2 {
			return "", false
		}
		return "\"", true
	case "apos":
		if dt == 0 || quote != 3 {
			return "", false
		}
		return "'", true
	}
	html5 := dt == 48
	if html5 {
		if v, ok := html5EntityNameToRune[ent]; ok {
			return v, true
		}
		return "", false
	}
	if v, ok := html4EntityNameToRune[ent]; ok {
		return v, true
	}
	return "", false
}

var html5Entities = map[string]string{
	"ensp":   "\u2002",
	"emsp":   "\u2003",
	"thinsp": "\u2009",
	"zwnj":   "\u200c",
	"zwj":    "\u200d",
	"lrm":    "\u200e",
	"rlm":    "\u200f",
	"sbquo":  "\u201a",
	"bdquo":  "\u201e",
	"dagger": "\u2020",
	"Dagger": "\u2021",
	"permil": "\u2030",
	"lsaquo": "\u2039",
	"rsaquo": "\u203a",
	"oline":  "\u203e",
	"frasl":  "\u2044",
	"hellip": "\u2026",
}

func countCharsFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	mode := 0
	if len(ctx.Args()) >= 2 {
		mode = int(arg(ctx, 1).ToInt())
	}
	if mode < 0 || mode > 4 {
		ctx.Throw(vm.NewThrowable("ValueError",
			"count_chars(): Argument #2 ($mode) must be between 0 and 4 (inclusive)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	switch mode {
	case 3, 4:
		var b strings.Builder
		for c := 0; c < 256; c++ {
			if (mode == 3 && counts[c] > 0) || (mode == 4 && counts[c] == 0) {
				b.WriteByte(byte(c))
			}
		}
		return vm.Str(b.String())
	}
	out := vm.NewArray()
	for c := 0; c < 256; c++ {
		if mode == 1 && counts[c] == 0 {
			continue
		}
		if mode == 2 && counts[c] != 0 {
			continue
		}
		out.SetInt(int64(c), vm.MakeInt(int64(counts[c])))
	}
	return vm.MakeArray(out)
}

func wordChars(s string, extra string) []struct{ Start, End int } {
	var words []struct{ Start, End int }
	var extraSet [256]bool
	for i := 0; i < len(extra); i++ {
		extraSet[extra[i]] = true
	}
	isW := func(c byte) bool {
		return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '\'' || c == '-' || c >= 0x80 || extraSet[c]
	}
	i := 0
	for i < len(s) {
		if isW(s[i]) {
			start := i
			hasLetter := false
			for i < len(s) && isW(s[i]) {
				if (s[i] >= 'a' && s[i] <= 'z') || (s[i] >= 'A' && s[i] <= 'Z') || s[i] >= 0x80 {
					hasLetter = true
				}
				i++
			}
			if hasLetter {
				words = append(words, struct{ Start, End int }{start, i})
			}
			continue
		}
		i++
	}
	return words
}

func isWordByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '\'' || c == '-' || c >= 0x80
}

func strWordCountFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	mode := 0
	if len(ctx.Args()) >= 2 {
		mode = int(arg(ctx, 1).ToInt())
	}
	if mode != 0 && mode != 1 && mode != 2 {
		ctx.Throw(vm.NewThrowable("ValueError", "str_word_count(): Argument #2 ($format) must be a valid format value"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	var ch [256]bool
	hasList := len(ctx.Args()) >= 3 && arg(ctx, 2).Type() != vm.NullType
	if hasList {
		extra := arg(ctx, 2).ToString()
		for i := 0; i < len(extra); i++ {
			ch[extra[i]] = true
		}
	}
	if len(s) == 0 {
		if mode == 0 {
			return vm.Int(0)
		}
		return vm.MakeArray(vm.NewArray())
	}
	// Как в php-src: крайние апостроф/дефис не являются частью слова,
	// если явно не разрешены char_list.
	p := 0
	e := len(s)
	if (s[p] == '\'' && !ch['\'']) || (s[p] == '-' && !ch['-']) {
		p++
	}
	if s[e-1] == '-' && !ch['-'] {
		e--
	}
	type span struct{ start, end int }
	var words []span
	for p < e {
		start := p
		for p < e {
			c := s[p]
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || ch[c] || c == '\'' || c == '-' {
				p++
				continue
			}
			break
		}
		if p > start {
			words = append(words, span{start, p})
		}
		p++
	}
	switch mode {
	case 1:
		out := vm.NewArray()
		for _, w := range words {
			out.Append(vm.MakeString(s[w.start:w.end]))
		}
		return vm.MakeArray(out)
	case 2:
		out := vm.NewArray()
		for _, w := range words {
			out.SetInt(int64(w.start), vm.MakeString(s[w.start:w.end]))
		}
		return vm.MakeArray(out)
	}
	return vm.Int(int64(len(words)))
}

func nl2brFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '\r':
			b.WriteString("<br />")
			b.WriteByte(c)
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
				b.WriteByte('\n')
			}
		case '\n':
			b.WriteString("<br />")
			b.WriteByte(c)
			if i+1 < len(s) && s[i+1] == '\r' {
				i++
				b.WriteByte('\r')
			}
		default:
			b.WriteByte(c)
		}
	}
	return vm.Str(b.String())
}

func hex2binFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if len(s)%2 != 0 {
		ctx.VM().Warning("hex2bin(): Hexadecimal input string must have an even length")
		return vm.Bool(false)
	}
	out, err := hex.DecodeString(s)
	if err != nil {
		ctx.VM().Warning("hex2bin(): Input string must be hexadecimal string")
		return vm.Bool(false)
	}
	return vm.Str(string(out))
}

func base64DecodeFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	strict := false
	if len(ctx.Args()) >= 2 {
		strict = arg(ctx, 1).ToBool()
	}
	out, ok := phpBase64Decode(s, strict)
	if !ok {
		return vm.Bool(false)
	}
	return vm.Str(out)
}

// phpBase64Decode повторяет php_base64_decode_impl: нестрогий режим
// пропускает '='/мусор и декодирует сплошным потоком, строгий — отвергает
// мусор, данные после '=' и неверный паддинг.
func phpBase64Decode(s string, strict bool) (string, bool) {
	rev := func(c byte) int {
		switch {
		case c >= 'A' && c <= 'Z':
			return int(c - 'A')
		case c >= 'a' && c <= 'z':
			return int(c-'a') + 26
		case c >= '0' && c <= '9':
			return int(c-'0') + 52
		case c == '+':
			return 62
		case c == '/':
			return 63
		case c == '\t' || c == '\n' || c == '\r' || c == ' ':
			return -1
		}
		return -2
	}
	var out []byte
	var pending byte
	i := 0
	padding := 0
	for k := 0; k < len(s); k++ {
		ch := s[k]
		if ch == '=' {
			padding++
			continue
		}
		v := rev(ch)
		if !strict {
			if v < 0 {
				continue
			}
		} else {
			if v == -1 {
				continue
			}
			if v == -2 || padding > 0 {
				return "", false
			}
		}
		switch i % 4 {
		case 0:
			pending = byte(v << 2)
		case 1:
			out = append(out, pending|byte(v>>4))
			pending = byte((v & 0x0f) << 4)
		case 2:
			out = append(out, pending|byte(v>>2))
			pending = byte((v & 0x03) << 6)
		case 3:
			out = append(out, pending|byte(v))
		}
		i++
	}
	if strict && i%4 == 1 {
		return "", false
	}
	if strict && padding > 0 && (padding > 2 || (i+padding)%4 != 0) {
		return "", false
	}
	return string(out), true
}

func phpURLEncode(s string, raw bool) string {
	const hexchars = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || (raw && c == '~') {
			b.WriteByte(c)
		} else if c == ' ' && !raw {
			b.WriteByte('+')
		} else {
			b.WriteByte('%')
			b.WriteByte(hexchars[c>>4])
			b.WriteByte(hexchars[c&15])
		}
	}
	return b.String()
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return -1
}

func phpURLDecode(s string, raw bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '%' && i+2 < len(s) {
			h1, h2 := hexVal(s[i+1]), hexVal(s[i+2])
			if h1 >= 0 && h2 >= 0 {
				b.WriteByte(byte(h1<<4 | h2))
				i += 2
				continue
			}
		}
		if c == '+' && !raw {
			b.WriteByte(' ')
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

func phpHash(h hash.Hash, s string, raw bool) string {
	h.Write([]byte(s))
	sum := h.Sum(nil)
	if raw {
		return string(sum)
	}
	return hex.EncodeToString(sum)
}

func hashFileFn(sha bool) func(ctx *vm.Context) vm.Value {
	return func(ctx *vm.Context) vm.Value {
		name := "md5_file"
		if sha {
			name = "sha1_file"
		}
		if arg0(ctx).Type() == vm.NullType {
			ctx.VM().Deprecated(name + "(): Passing null to parameter #1 ($filename) of type string is deprecated")
		}
		path := arg0(ctx).ToString()
		if path == "" {
			ctx.Throw(vm.NewThrowable("ValueError", "Path cannot be empty"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		data, err := os.ReadFile(path)
		if err != nil {
			ctx.VM().Warning(name + "(" + path + "): Failed to open stream: No such file or directory")
			return vm.Bool(false)
		}
		raw := false
		if len(ctx.Args()) >= 2 {
			raw = arg(ctx, 1).ToBool()
		}
		if sha {
			return vm.Str(phpHash(sha1.New(), string(data), raw))
		}
		return vm.Str(phpHash(md5.New(), string(data), raw))
	}
}

func basenameFn(ctx *vm.Context) vm.Value {
	p := arg0(ctx).ToString()
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		p = p[i+1:]
	}
	if len(ctx.Args()) >= 2 {
		suffix := arg(ctx, 1).ToString()
		if suffix != "" && p != suffix && strings.HasSuffix(p, suffix) {
			p = p[:len(p)-len(suffix)]
		}
	}
	return vm.Str(p)
}

func dirnameFn(ctx *vm.Context) vm.Value {
	p := arg0(ctx).ToString()
	levels := 1
	if len(ctx.Args()) >= 2 {
		levels = int(arg(ctx, 1).ToInt())
	}
	if levels < 1 {
		ctx.Throw(vm.NewThrowable("ValueError",
			"dirname(): Argument #2 ($levels) must be greater than or equal to 1"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	for i := 0; i < levels; i++ {
		p = dirnameOne(p)
	}
	return vm.Str(p)
}

func dirnameOne(p string) string {
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	i := strings.LastIndexByte(p, '/')
	if i < 0 {
		return "."
	}
	if i == 0 {
		return "/"
	}
	res := p[:i]
	for len(res) > 1 && strings.HasSuffix(res, "/") {
		res = res[:len(res)-1]
	}
	return res
}

func pathinfoFn(ctx *vm.Context) vm.Value {
	p := arg0(ctx).ToString()
	base := basenameOf(p)
	filename := base
	ext := ""
	hasExt := false
	if i := strings.LastIndexByte(base, '.'); i >= 0 {
		hasExt = true
		ext = base[i+1:]
		filename = base[:i]
	}
	dir := dirnameOne(p)
	if p == "" {
		dir = ""
	}
	mk := func() *vm.Array {
		out := vm.NewArray()
		if p != "" {
			out.SetStr("dirname", vm.MakeString(dir))
		}
		out.SetStr("basename", vm.MakeString(base))
		if hasExt {
			out.SetStr("extension", vm.MakeString(ext))
		}
		out.SetStr("filename", vm.MakeString(filename))
		return out
	}
	if len(ctx.Args()) >= 2 {
		// PHP 8.3: при ненулевом $flags возвращается СТРОКА — компонент с
		// наивысшим приоритетом: DIRNAME > BASENAME > EXTENSION > FILENAME.
		if flags := int(arg(ctx, 1).ToInt()); flags != 0 {
			switch {
			case flags&1 != 0:
				return vm.Str(dir)
			case flags&2 != 0:
				return vm.Str(base)
			case flags&4 != 0:
				return vm.Str(ext)
			case flags&8 != 0:
				return vm.Str(filename)
			}
			return vm.Str("")
		}
	}
	return vm.MakeArray(mk())
}

func basenameOf(p string) string {
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}
	return p
}

func ucwordsFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	sep := " \t\r\n\x0b\f"
	if len(ctx.Args()) >= 2 {
		sep = arg(ctx, 1).ToString()
	}
	b := []byte(s)
	state := 0
	for i, c := range b {
		if strings.IndexByte(sep, c) >= 0 {
			state = 0
		} else if state == 0 {
			if c >= 'a' && c <= 'z' {
				b[i] = c - 32
			}
			state = 1
		}
	}
	return vm.Str(string(b))
}

func trimExp(s string) string {
	i := strings.IndexAny(s, "eE")
	if i < 0 {
		return s
	}
	mant := s[:i+1]
	exp := s[i+1:]
	sign := ""
	if len(exp) > 0 && (exp[0] == '+' || exp[0] == '-') {
		sign = exp[:1]
		exp = exp[1:]
	}
	exp = strings.TrimLeft(exp, "0")
	if exp == "" {
		exp = "0"
	}
	return mant + sign + exp
}

func stripcslashesFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '\\' || i+1 >= len(s) {
			b.WriteByte(s[i])
			continue
		}
		i++
		switch s[i] {
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case 'a':
			b.WriteByte(7)
		case 't':
			b.WriteByte('\t')
		case 'v':
			b.WriteByte(11)
		case 'b':
			b.WriteByte('\b')
		case 'f':
			b.WriteByte('\f')
		case '\\':
			b.WriteByte('\\')
		case 'x':
			if i+1 < len(s) {
				h1 := hexVal(s[i+1])
				if h1 >= 0 {
					i++
					if i+1 < len(s) {
						h2 := hexVal(s[i+1])
						if h2 >= 0 {
							i++
							b.WriteByte(byte(h1<<4 | h2))
							continue
						}
					}
					b.WriteByte(byte(h1))
					continue
				}
			}
			b.WriteByte('x')
		default:
			if s[i] >= '0' && s[i] <= '7' {
				v := 0
				cnt := 0
				for cnt < 3 && i < len(s) && s[i] >= '0' && s[i] <= '7' {
					v = v*8 + int(s[i]-'0')
					i++
					cnt++
				}
				i--
				b.WriteByte(byte(v))
			} else {
				b.WriteByte(s[i])
			}
		}
	}
	return vm.Str(b.String())
}

func strspnFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	mask := arg(ctx, 1).ToString()
	off := 0
	if len(ctx.Args()) >= 3 {
		off = int(arg(ctx, 2).ToInt())
		if off < 0 {
			off = len(s) + off
			if off < 0 {
				off = 0
			}
		}
		if off > len(s) {
			return vm.Int(0)
		}
	}
	sub := s[off:]
	if len(ctx.Args()) >= 4 {
		l := int(arg(ctx, 3).ToInt())
		if l < 0 {
			l = len(s) - off + l
			if l < 0 {
				l = 0
			}
		}
		if l < len(sub) {
			sub = sub[:l]
		}
	}
	n := 0
	for n < len(sub) && strings.IndexByte(mask, sub[n]) >= 0 {
		n++
	}
	return vm.Int(int64(n))
}

// --- pack / unpack ---

func packNext(args []vm.Slot, argi *int) vm.Slot {
	if *argi < len(args) {
		v := args[*argi]
		*argi++
		return v
	}
	return vm.MakeNull()
}

func appendLE(b []byte, v uint64, n int) []byte {
	for i := 0; i < n; i++ {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

func appendBE(b []byte, v uint64, n int) []byte {
	for i := n - 1; i >= 0; i-- {
		b = append(b, byte(v>>(8*i)))
	}
	return b
}

func packFn(ctx *vm.Context) vm.Value {
	format := arg0(ctx).ToString()
	args := ctx.Args()
	argi := 1
	needArgs := func(kind byte, cnt int) {
		if argi+cnt > len(args) {
			ctx.Throw(vm.NewThrowable("ValueError", "Type "+string(kind)+": too few arguments"))
			if ctx.HasThrown() {
				return
			}
		}
	}
	var out []byte
	for i := 0; i < len(format); i++ {
		c := format[i]
		j := i + 1
		count, hasCount, star := 0, false, false
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			count = count*10 + int(format[j]-'0')
			hasCount = true
			j++
		}
		if j < len(format) && format[j] == '*' {
			star = true
			j++
		}
		i = j - 1
		reps := 1
		if star {
			reps = -1
		} else if hasCount {
			reps = count
		}
		switch c {
		case 'a', 'A', 'Z':
			n := count
			if !hasCount && !star {
				n = 1
			}
			var s string
			if star {
				for argi < len(args) {
					s += args[argi].ToString()
					argi++
				}
			} else {
				needArgs(c, 1)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				s = packNext(args, &argi).ToString()
			}
			if star {
				out = append(out, s...)
				if c == 'Z' {
					out = append(out, 0)
				}
				break
			}
			if c == 'Z' {
				take := n - 1
				if take < 0 {
					take = 0
				}
				if take > len(s) {
					take = len(s)
				}
				out = append(out, s[:take]...)
				for k := take; k < n; k++ {
					out = append(out, 0)
				}
			} else {
				pad := byte(0)
				if c == 'A' {
					pad = ' '
				}
				for k := 0; k < n; k++ {
					if k < len(s) {
						out = append(out, s[k])
					} else {
						out = append(out, pad)
					}
				}
			}
		case 'h', 'H':
			n := count
			if !hasCount && !star {
				n = 1
			}
			var s string
			if star {
				for argi < len(args) {
					s += args[argi].ToString()
					argi++
				}
			} else {
				needArgs(c, 1)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				s = packNext(args, &argi).ToString()
			}
			nib := len(s)
			if star || n > nib {
				n = nib
			}
			high := c == 'H'
			for k := 0; k+1 < n; k += 2 {
				h1, h2 := hexVal(s[k]), hexVal(s[k+1])
				if h1 < 0 {
					h1 = 0
				}
				if h2 < 0 {
					h2 = 0
				}
				if high {
					out = append(out, byte(h1<<4|h2))
				} else {
					out = append(out, byte(h2<<4|h1))
				}
			}
			if n%2 == 1 {
				h1 := hexVal(s[n-1])
				if h1 < 0 {
					h1 = 0
				}
				if high {
					out = append(out, byte(h1<<4))
				} else {
					out = append(out, byte(h1))
				}
			}
		case 'c', 'C', 's', 'S', 'v', 'n', 'i', 'I', 'l', 'L', 'V', 'N', 'q', 'Q', 'P', 'J':
			n := 1
			switch c {
			case 's', 'S', 'v', 'n':
				n = 2
			case 'i', 'I', 'l', 'L', 'V', 'N':
				n = 4
			case 'q', 'Q', 'P', 'J':
				n = 8
			}
			be := c == 'n' || c == 'N' || c == 'J'
			emit := func(v uint64) {
				if be {
					out = appendBE(out, v, n)
				} else {
					out = appendLE(out, v, n)
				}
			}
			if !star {
				needArgs(c, reps)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if star {
				for argi < len(args) {
					emit(uint64(packNext(args, &argi).ToInt()))
				}
			} else {
				for k := 0; k < reps; k++ {
					emit(uint64(packNext(args, &argi).ToInt()))
				}
			}
		case 'f':
			if !star {
				needArgs(c, reps)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if star {
				for argi < len(args) {
					bits := math.Float32bits(float32(packNext(args, &argi).ToFloat()))
					out = appendLE(out, uint64(bits), 4)
				}
			} else {
				for k := 0; k < reps; k++ {
					bits := math.Float32bits(float32(packNext(args, &argi).ToFloat()))
					out = appendLE(out, uint64(bits), 4)
				}
			}
		case 'd':
			if !star {
				needArgs(c, reps)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if star {
				for argi < len(args) {
					bits := math.Float64bits(packNext(args, &argi).ToFloat())
					out = appendLE(out, bits, 8)
				}
			} else {
				for k := 0; k < reps; k++ {
					bits := math.Float64bits(packNext(args, &argi).ToFloat())
					out = appendLE(out, bits, 8)
				}
			}
		case 'e', 'E':
			beE := c == 'E'
			emitE := func(v float64) {
				bits := math.Float64bits(v)
				if beE {
					out = appendBE(out, bits, 8)
				} else {
					out = appendLE(out, bits, 8)
				}
			}
			if !star {
				needArgs(c, reps)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if star {
				for argi < len(args) {
					emitE(packNext(args, &argi).ToFloat())
				}
			} else {
				for k := 0; k < reps; k++ {
					emitE(packNext(args, &argi).ToFloat())
				}
			}
		case 'g', 'G':
			beG := c == 'G'
			emitG := func(v float64) {
				bits := uint64(math.Float32bits(float32(v)))
				if beG {
					out = appendBE(out, bits, 4)
				} else {
					out = appendLE(out, bits, 4)
				}
			}
			if !star {
				needArgs(c, reps)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if star {
				for argi < len(args) {
					emitG(packNext(args, &argi).ToFloat())
				}
			} else {
				for k := 0; k < reps; k++ {
					emitG(packNext(args, &argi).ToFloat())
				}
			}
		case 'x':
			n := count
			if !star && !hasCount {
				n = 1
			}
			for k := 0; k < n; k++ {
				out = append(out, 0)
			}
		case 'X':
			n := count
			if !star && !hasCount {
				n = 1
			}
			if n > len(out) {
				n = len(out)
			}
			out = out[:len(out)-n]
		case '@':
			for len(out) < count {
				out = append(out, 0)
			}
			if count < len(out) {
				out = out[:count]
			}
		case '/':
			// разделитель полей: игнорируется
		}
	}
	return vm.Str(string(out))
}

func unpackFn(ctx *vm.Context) vm.Value {
	format := arg0(ctx).ToString()
	data := arg1Str(ctx)
	offset := 0
	if len(ctx.Args()) >= 3 {
		offset = int(arg(ctx, 2).ToInt())
		if offset < 0 || offset > len(data) {
			ctx.Throw(vm.NewThrowable("ValueError",
				"unpack(): Argument #3 ($offset) must be contained in argument #2 ($data)"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	pos := offset
	out := vm.NewArray()
	autoKey := 1
	read := func(n int) []byte {
		b := make([]byte, n)
		for k := 0; k < n; k++ {
			if pos+k < len(data) {
				b[k] = data[pos+k]
			}
		}
		pos += n
		return b
	}
	for i := 0; i < len(format); i++ {
		c := format[i]
		j := i + 1
		count, hasCount, star := 0, false, false
		for j < len(format) && format[j] >= '0' && format[j] <= '9' {
			count = count*10 + int(format[j]-'0')
			hasCount = true
			j++
		}
		if j < len(format) && format[j] == '*' {
			star = true
			j++
		}
		name := ""
		isVal := strings.IndexByte("aAZhHcCsSvniIlLVNqQPJfdeEgG", c) >= 0
		if isVal {
			ns := j
			for j < len(format) && ((format[j] >= 'a' && format[j] <= 'z') || (format[j] >= 'A' && format[j] <= 'Z') || format[j] == '_' || (format[j] >= '0' && format[j] <= '9')) {
				j++
			}
			if j > ns {
				name = format[ns:j]
			}
		}
		i = j - 1
		reps := 1
		if star {
			reps = -1
		} else if hasCount {
			reps = count
		}
		named := name != ""
		repName := named && (star || reps != 1)
		key := func(k int) vm.ArrayKey {
			if named {
				if repName {
					return vm.MakeStrKey(name + strconv.Itoa(k+1))
				}
				return vm.MakeStrKey(name)
			}
			kk := vm.MakeIntKey(int64(autoKey))
			autoKey++
			return kk
		}
		checkNeed := func(unit, reps int) bool {
			total := int64(unit) * int64(reps)
			if pos+int(total) > len(data) {
				verb := "were"
				if len(data)-pos == 1 {
					verb = "was"
				}
				ctx.VM().Warning(fmt.Sprintf("unpack(): Type %c: not enough input values, need %d values but only %d %s provided", c, total, len(data)-pos, verb))
				return false
			}
			return true
		}
		switch c {
		case 'a', 'A', 'Z':
			if !star && !checkNeed(1, reps) {
				return vm.Bool(false)
			}
			n := reps
			if n < 0 {
				n = len(data) - pos
			}
			b := read(n)
			s := string(b)
			switch c {
			case 'A':
				s = strings.TrimRight(s, " \t\n\r\v\f\x00")
			case 'Z':
				if idx := strings.IndexByte(s, 0); idx >= 0 {
					s = s[:idx]
				}
			}
			out.Set(key(0), vm.MakeString(s))
		case 'h', 'H':
			if reps > 2147483647 {
				ctx.VM().Warning("unpack(): Type " + string(c) + ": integer overflow")
				return vm.Bool(false)
			}
			if !star && !checkNeed(1, (reps+1)/2) {
				return vm.Bool(false)
			}
			n := count
			if !hasCount && !star {
				n = 1
			}
			nbytes := n/2 + n%2
			if star {
				nbytes = len(data) - pos
				n = nbytes * 2
			}
			b := read(nbytes)
			enc := make([]byte, 0, len(b)*2)
			const hexd = "0123456789abcdef"
			if c == 'H' {
				for _, by := range b {
					enc = append(enc, hexd[by>>4], hexd[by&15])
				}
			} else {
				for _, by := range b {
					enc = append(enc, hexd[by&15], hexd[by>>4])
				}
			}
			if n < len(enc) {
				enc = enc[:n]
			}
			out.Set(key(0), vm.MakeString(string(enc)))
		case 'c', 'C', 's', 'S', 'v', 'n', 'i', 'I', 'l', 'L', 'V', 'N', 'q', 'Q', 'P', 'J':
			n := 1
			signed := c == 'c' || c == 's' || c == 'i' || c == 'l' || c == 'q'
			switch c {
			case 's', 'S', 'v', 'n':
				n = 2
			case 'i', 'I', 'l', 'L', 'V', 'N':
				n = 4
			case 'q', 'Q', 'P', 'J':
				n = 8
			}
			if !star && !checkNeed(n, reps) {
				return vm.Bool(false)
			}
			be := c == 'n' || c == 'N' || c == 'J'
			decode := func(b []byte) int64 {
				var v uint64
				if be {
					for k := 0; k < n; k++ {
						v = v<<8 | uint64(b[k])
					}
				} else {
					for k := n - 1; k >= 0; k-- {
						v = v<<8 | uint64(b[k])
					}
				}
				if signed && n < 8 {
					shift := uint(64 - 8*n)
					return int64(v<<shift) >> shift
				}
				return int64(v)
			}
			if star {
				for k := 0; pos+n <= len(data); k++ {
					b := read(n)
					out.Set(key(k), vm.MakeInt(decode(b)))
				}
			} else {
				for k := 0; k < reps; k++ {
					b := read(n)
					out.Set(key(k), vm.MakeInt(decode(b)))
				}
			}
		case 'f':
			if !star && !checkNeed(4, reps) {
				return vm.Bool(false)
			}
			if star {
				for k := 0; pos+4 <= len(data); k++ {
					b := read(4)
					bits := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
					out.Set(key(k), vm.MakeFloat(float64(math.Float32frombits(uint32(bits)))))
				}
			} else {
				for k := 0; k < reps; k++ {
					b := read(4)
					bits := uint64(b[0]) | uint64(b[1])<<8 | uint64(b[2])<<16 | uint64(b[3])<<24
					out.Set(key(k), vm.MakeFloat(float64(math.Float32frombits(uint32(bits)))))
				}
			}
		case 'd':
			if !star && !checkNeed(8, reps) {
				return vm.Bool(false)
			}
			if star {
				for k := 0; pos+8 <= len(data); k++ {
					b := read(8)
					var bits uint64
					for kk := 7; kk >= 0; kk-- {
						bits = bits<<8 | uint64(b[kk])
					}
					out.Set(key(k), vm.MakeFloat(math.Float64frombits(bits)))
				}
			} else {
				for k := 0; k < reps; k++ {
					b := read(8)
					var bits uint64
					for kk := 7; kk >= 0; kk-- {
						bits = bits<<8 | uint64(b[kk])
					}
					out.Set(key(k), vm.MakeFloat(math.Float64frombits(bits)))
				}
			}
		case 'e', 'E':
			if !star && !checkNeed(8, reps) {
				return vm.Bool(false)
			}
			beE := c == 'E'
			decodeE := func(b []byte) float64 {
				var bits uint64
				if beE {
					for kk := 0; kk < 8; kk++ {
						bits = bits<<8 | uint64(b[kk])
					}
				} else {
					for kk := 7; kk >= 0; kk-- {
						bits = bits<<8 | uint64(b[kk])
					}
				}
				return math.Float64frombits(bits)
			}
			if star {
				for k := 0; pos+8 <= len(data); k++ {
					out.Set(key(k), vm.MakeFloat(decodeE(read(8))))
				}
			} else {
				for k := 0; k < reps; k++ {
					out.Set(key(k), vm.MakeFloat(decodeE(read(8))))
				}
			}
		case 'g', 'G':
			if !star && !checkNeed(4, reps) {
				return vm.Bool(false)
			}
			beG := c == 'G'
			decodeG := func(b []byte) float64 {
				var bits uint32
				if beG {
					for kk := 0; kk < 4; kk++ {
						bits = bits<<8 | uint32(b[kk])
					}
				} else {
					for kk := 3; kk >= 0; kk-- {
						bits = bits<<8 | uint32(b[kk])
					}
				}
				return float64(math.Float32frombits(bits))
			}
			if star {
				for k := 0; pos+4 <= len(data); k++ {
					out.Set(key(k), vm.MakeFloat(decodeG(read(4))))
				}
			} else {
				for k := 0; k < reps; k++ {
					out.Set(key(k), vm.MakeFloat(decodeG(read(4))))
				}
			}
		case 'x':
			n := count
			if !star && !hasCount {
				n = 1
			}
			pos += n
		case 'X':
			if star {
				ctx.VM().Warning("unpack(): Type X: '*' ignored")
				break
			}
			n := count
			if !star && !hasCount {
				n = 1
			}
			pos -= n
			if pos < offset {
				pos = offset
			}
		case '@':
			pos = offset + count
		case '/':
			// разделитель полей: игнорируется
		default:
			ctx.Throw(vm.NewThrowable("ValueError", "Invalid format type "+string(c)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	return vm.MakeArray(out)
}

// --- прочее ---

// phpCSVNoEscape — PHP_CSV_NO_ESCAPE (escape отключён).
const phpCSVNoEscape = -1

// isCSVSpace повторяет C isspace() для unsigned char.
func isCSVSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\v', '\f', '\r':
		return true
	}
	return false
}

// lookupTrailingSpaces повторяет php_fgetcsv_lookup_trailing_spaces: возвращает
// индекс первого хвостового пробельного символа (с учётом \n / \r\n).
func lookupTrailingSpaces(s string, limit int) int {
	ptr := 0
	var last0, last1 byte
	for ptr < limit {
		last0 = last1
		last1 = s[ptr]
		ptr++
	}
	switch last1 {
	case '\n':
		if last0 == '\r' {
			return ptr - 2
		}
		return ptr - 1
	case '\r':
		return ptr - 1
	}
	return ptr
}

// phpFgetcsvString — точный порт php_fgetcsv(stream==NULL, ...). Возвращает nil
// для пустой строки (в PHP превращается в array(1){NULL}).
func phpFgetcsvString(delim, enc byte, esc int, buf string) []string {
	n := len(buf)
	limit := lookupTrailingSpaces(buf, n)
	charAt := func(i int) byte {
		if i >= 0 && i < n {
			return buf[i]
		}
		return 0
	}
	mbLen := func(i int) int {
		if i >= limit {
			return 0
		}
		c := charAt(i)
		if c == 0 || c < 0x80 {
			return 1
		}
		var l int
		switch {
		case c&0xE0 == 0xC0:
			l = 2
		case c&0xF0 == 0xE0:
			l = 3
		case c&0xF8 == 0xF0:
			l = 4
		default:
			return 1
		}
		if i+l > n {
			return 1
		}
		for k := 1; k < l; k++ {
			if charAt(i+k)&0xC0 != 0x80 {
				return 1
			}
		}
		return l
	}

	lineEnd := limit
	lineEndLen := n - lineEnd

	var values []string
	firstField := true
	bptr := 0
	incLen := mbLen(bptr)
	for {
		var field []byte
		hunkBegin := 0
		appendRange := func(from, to int) {
			for k := from; k < to; k++ {
				field = append(field, charAt(k))
			}
		}
		if incLen == 1 {
			tmp := bptr
			for charAt(tmp) != delim && isCSVSpace(charAt(tmp)) {
				tmp++
			}
			if charAt(tmp) == enc && tmp < limit {
				bptr = tmp
			}
		}
		if firstField && bptr == lineEnd {
			return nil
		}
		firstField = false
		if incLen != 0 && charAt(bptr) == enc {
			state := 0
			bptr++
			hunkBegin = bptr
			for {
				switch {
				case incLen == 0:
					switch state {
					case 2:
						appendRange(hunkBegin, bptr-1)
						hunkBegin = bptr
					case 1:
						appendRange(hunkBegin, bptr)
						hunkBegin = bptr
						fallthrough
					case 0:
						if hunkBegin != lineEnd {
							appendRange(hunkBegin, bptr)
							hunkBegin = bptr
						}
						appendRange(lineEnd, lineEnd+lineEndLen)
						if bptr > limit {
							if hunkBegin == bptr {
								hunkBegin--
							}
							bptr--
						}
					}
					goto quit2
				case incLen == 1:
					switch state {
					case 1:
						bptr++
						state = 0
					case 2:
						if charAt(bptr) != enc {
							appendRange(hunkBegin, bptr-1)
							hunkBegin = bptr
							goto quit2
						}
						appendRange(hunkBegin, bptr)
						bptr++
						hunkBegin = bptr
						state = 0
					default:
						if charAt(bptr) == enc {
							state = 2
						} else if esc != phpCSVNoEscape && int(charAt(bptr)) == esc {
							state = 1
						}
						bptr++
					}
				default: // incLen > 1
					switch state {
					case 2:
						appendRange(hunkBegin, bptr-1)
						hunkBegin = bptr
						goto quit2
					case 1:
						bptr += incLen
						appendRange(hunkBegin, bptr)
						hunkBegin = bptr
						state = 0
					default:
						bptr += incLen
					}
				}
				incLen = mbLen(bptr)
			}
		quit2:
			for {
				if incLen <= 0 {
					break
				}
				if incLen == 1 && charAt(bptr) == delim {
					break
				}
				bptr += incLen
				incLen = mbLen(bptr)
			}
			appendRange(hunkBegin, bptr)
			bptr += incLen
		} else {
			hunkBegin = bptr
			for {
				if incLen <= 0 {
					break
				}
				if incLen == 1 && charAt(bptr) == delim {
					break
				}
				bptr += incLen
				incLen = mbLen(bptr)
			}
			appendRange(hunkBegin, bptr)
			compEnd := lookupTrailingSpaces(string(field), len(field))
			field = field[:compEnd]
			if charAt(bptr) == delim {
				bptr++
			}
			values = append(values, string(field))
			if incLen <= 0 {
				break
			}
			// next iteration: incLen пересчитывается сверху
			incLen = mbLen(bptr)
			continue
		}
		values = append(values, string(field))
		if incLen <= 0 {
			break
		}
		incLen = mbLen(bptr)
	}
	return values
}

func strGetcsvFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	delim := byte(',')
	if len(ctx.Args()) >= 2 {
		if v := arg(ctx, 1).ToString(); v != "" {
			delim = v[0]
		}
	}
	enc := byte('"')
	if len(ctx.Args()) >= 3 {
		if v := arg(ctx, 2).ToString(); v != "" {
			enc = v[0]
		}
	}
	esc := int('\\')
	if len(ctx.Args()) >= 4 {
		if v := arg(ctx, 3).ToString(); v != "" {
			esc = int(v[0])
		} else {
			esc = phpCSVNoEscape
		}
	}
	values := phpFgetcsvString(delim, enc, esc, s)
	out := vm.NewArray()
	if values == nil {
		out.Append(vm.MakeNull())
		return vm.MakeArray(out)
	}
	for _, f := range values {
		out.Append(vm.MakeString(f))
	}
	return vm.MakeArray(out)
}

func latin1ToUTF8(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x80 {
			b.WriteByte(c)
		} else {
			b.WriteByte(0xc0 | c>>6)
			b.WriteByte(0x80 | c&0x3f)
		}
	}
	return b.String()
}

// decodeUTF8Char декодирует один UTF-8 символ с валидацией (как
// php_next_utf8_char). size — число реально потреблённых байт даже при ошибке.
func decodeUTF8Char(s string, i int) (r rune, size int, valid bool) {
	c := s[i]
	if c < 0x80 {
		return rune(c), 1, true
	}
	var l int
	var rr rune
	switch {
	case c < 0xC2:
		return 0, 1, false
	case c < 0xE0:
		l, rr = 2, rune(c&0x1F)
	case c < 0xF0:
		l, rr = 3, rune(c&0x0F)
	case c < 0xF5:
		l, rr = 4, rune(c&0x07)
	default:
		return 0, 1, false
	}
	for k := 1; k < l; k++ {
		if i+k >= len(s) || s[i+k]&0xC0 != 0x80 {
			return 0, k, false
		}
		rr = rr<<6 | rune(s[i+k]&0x3F)
	}
	if rr > 0x10FFFF || (rr >= 0xD800 && rr <= 0xDFFF) ||
		(l == 2 && rr < 0x80) || (l == 3 && rr < 0x800) || (l == 4 && rr < 0x10000) {
		return 0, l, false
	}
	return rr, l, true
}

func utf8ToLatin1(s string) string {
	var b []byte
	n := len(s)
	for i := 0; i < n; {
		c := s[i]
		if c < 0x80 {
			b = append(b, c)
			i++
			continue
		}
		var l int
		var r rune
		switch {
		case c < 0xC2:
			b = append(b, '?')
			i++
			continue
		case c < 0xE0:
			l = 2
			r = rune(c & 0x1F)
		case c < 0xF0:
			l = 3
			r = rune(c & 0x0F)
		case c < 0xF5:
			l = 4
			r = rune(c & 0x07)
		default:
			b = append(b, '?')
			i++
			continue
		}
		k := 1
		ok := true
		for ; k < l; k++ {
			if i+k >= n || s[i+k]&0xC0 != 0x80 {
				ok = false
				break
			}
			r = r<<6 | rune(s[i+k]&0x3F)
		}
		if !ok {
			b = append(b, '?')
			i += k
			continue
		}
		if r > 0xFF || (r >= 0xD800 && r <= 0xDFFF) ||
			(l == 2 && r < 0x80) || (l == 3 && r < 0x800) || (l == 4 && r < 0x10000) {
			b = append(b, '?')
		} else {
			b = append(b, byte(r))
		}
		i += l
	}
	return string(b)
}

// --- metaphone (порт алгоритма PHP) ---

var metaCodes = [26]byte{
	1, 16, 4, 16, 9, 2, 4, 16, 9, 2, 0, 2, 2, 2, 1, 4, 0, 2, 4, 4, 1, 0, 0, 0, 8, 0,
}

func metaEncode(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return metaCodes[c-'A']
	}
	return 0
}

func metaVowel(c byte) bool    { return metaEncode(c)&1 != 0 }
func metaAffectH(c byte) bool  { return metaEncode(c)&4 != 0 }
func metaMakeSoft(c byte) bool { return metaEncode(c)&8 != 0 }
func metaNoGHToF(c byte) bool  { return metaEncode(c)&16 != 0 }

type metaphoneState struct {
	word  string
	wIdx  int
	out   []byte
	maxPh int
}

func (m *metaphoneState) rawCurr() byte {
	if m.wIdx < len(m.word) {
		return m.word[m.wIdx]
	}
	return 0
}

func (m *metaphoneState) rawNext() byte {
	if m.wIdx+1 < len(m.word) {
		return m.word[m.wIdx+1]
	}
	return 0
}

func (m *metaphoneState) next() byte { return metaUpper(m.rawNext()) }
func (m *metaphoneState) curr() byte { return metaUpper(m.rawCurr()) }
func (m *metaphoneState) afterNext() byte {
	if m.rawNext() != 0 && m.wIdx+2 < len(m.word) {
		return metaUpper(m.word[m.wIdx+2])
	}
	return 0
}

func (m *metaphoneState) lookBack(n int) byte {
	if m.wIdx >= n {
		return metaUpper(m.word[m.wIdx-n])
	}
	return 0
}

func (m *metaphoneState) lookAhead(n int) byte {
	if m.wIdx+n < len(m.word) {
		return metaUpper(m.word[m.wIdx+n])
	}
	return 0
}

func (m *metaphoneState) phonize(c byte) { m.out = append(m.out, c) }

func metaUpper(c byte) byte {
	if c >= 'a' && c <= 'z' {
		return c - 32
	}
	return c
}

func isAlphaByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func phpMetaphone(word string, maxPh int) string {
	m := &metaphoneState{word: word, maxPh: maxPh}
	var curr byte
	for {
		curr = m.rawCurr()
		if isAlphaByte(curr) {
			break
		}
		if curr == 0 {
			return string(m.out)
		}
		m.wIdx++
	}
	curr = metaUpper(curr)
	switch curr {
	case 'A':
		if m.next() == 'E' {
			m.phonize('E')
			m.wIdx += 2
		} else {
			m.phonize('A')
			m.wIdx++
		}
	case 'G', 'K', 'P':
		if m.next() == 'N' {
			m.phonize('N')
			m.wIdx += 2
		}
	case 'W':
		nl := m.next()
		if nl == 'R' {
			m.phonize('R')
			m.wIdx += 2
		} else if nl == 'H' || metaVowel(nl) {
			m.phonize('W')
			m.wIdx += 2
		}
	case 'X':
		m.phonize('S')
		m.wIdx++
	case 'E', 'I', 'O', 'U':
		m.phonize(curr)
		m.wIdx++
	}
	for {
		curr = m.rawCurr()
		if curr == 0 || (m.maxPh != 0 && len(m.out) >= m.maxPh) {
			break
		}
		if !isAlphaByte(curr) {
			m.wIdx++
			continue
		}
		curr = metaUpper(curr)
		prev := m.lookBack(1)
		if curr == prev && curr != 'C' {
			m.wIdx++
			continue
		}
		skip := 0
		switch curr {
		case 'B':
			if prev != 'M' {
				m.phonize('B')
			}
		case 'C':
			nl := m.next()
			if metaMakeSoft(nl) {
				if nl == 'I' && m.afterNext() == 'A' {
					m.phonize('X')
				} else if prev == 'S' {
					// dropped
				} else {
					m.phonize('S')
				}
			} else if nl == 'H' {
				m.phonize('X')
				skip++
			} else {
				m.phonize('K')
			}
		case 'D':
			if m.next() == 'G' && metaMakeSoft(m.afterNext()) {
				m.phonize('J')
				skip++
			} else {
				m.phonize('T')
			}
		case 'G':
			nl := m.next()
			if nl == 'H' {
				if !(metaNoGHToF(m.lookBack(3)) || m.lookBack(4) == 'H') {
					m.phonize('F')
					skip++
				}
			} else if nl == 'N' {
				an := m.afterNext()
				if !(isAlphaByte(an) || (an == 'E' && m.lookAhead(3) == 'D')) {
					m.phonize('K')
				}
			} else if metaMakeSoft(nl) && prev != 'G' {
				m.phonize('J')
			} else {
				m.phonize('K')
			}
		case 'H':
			if metaVowel(m.next()) && !metaAffectH(prev) {
				m.phonize('H')
			}
		case 'K':
			if prev != 'C' {
				m.phonize('K')
			}
		case 'P':
			if m.next() == 'H' {
				m.phonize('F')
			} else {
				m.phonize('P')
			}
		case 'Q':
			m.phonize('K')
		case 'S':
			nl := m.next()
			if nl == 'I' && (m.afterNext() == 'O' || m.afterNext() == 'A') {
				m.phonize('X')
			} else if nl == 'H' {
				m.phonize('X')
				skip++
			} else {
				m.phonize('S')
			}
		case 'T':
			nl := m.next()
			if nl == 'I' && (m.afterNext() == 'O' || m.afterNext() == 'A') {
				m.phonize('X')
			} else if nl == 'H' {
				m.phonize('0')
				skip++
			} else if !(nl == 'C' && m.afterNext() == 'H') {
				m.phonize('T')
			}
		case 'V':
			m.phonize('F')
		case 'W':
			if metaVowel(m.next()) {
				m.phonize('W')
			}
		case 'X':
			m.phonize('K')
			m.phonize('S')
		case 'Y':
			if metaVowel(m.next()) {
				m.phonize('Y')
			}
		case 'Z':
			m.phonize('S')
		case 'F', 'J', 'L', 'M', 'N', 'R':
			m.phonize(curr)
		}
		m.wIdx += skip
		m.wIdx++
	}
	return string(m.out)
}

func metaphoneFn(ctx *vm.Context) vm.Value {
	word := arg0(ctx).ToString()
	maxPh := 0
	if len(ctx.Args()) >= 2 {
		maxPh = int(arg(ctx, 1).ToInt())
	}
	if maxPh < 0 {
		ctx.Throw(vm.NewThrowable("ValueError",
			"metaphone(): Argument #2 ($max_phonemes) must be greater than or equal to 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return vm.Str(phpMetaphone(word, maxPh))
}

func strposFn(ctx *vm.Context, caseInsensitive, last bool) vm.Value {
	fn := chooseStr(caseInsensitive, last)
	if arg(ctx, 0).Type() == vm.NullType {
		ctx.VM().Deprecated(fn + "(): Passing null to parameter #1 ($haystack) of type string is deprecated")
	}
	if arg(ctx, 1).Type() == vm.NullType {
		ctx.VM().Deprecated(fn + "(): Passing null to parameter #2 ($needle) of type string is deprecated")
	}
	h := arg(ctx, 0).ToString()
	n := arg(ctx, 1).ToString()
	fold := func(s string) string {
		if caseInsensitive {
			return asciiLower(s)
		}
		return s
	}
	nn := fold(n)

	if len(ctx.Args()) < 3 {
		var idx int
		if last {
			idx = strings.LastIndex(fold(h), nn)
		} else {
			idx = strings.Index(fold(h), nn)
		}
		if idx < 0 {
			return vm.Bool(false)
		}
		return vm.Int(int64(idx))
	}

	off := offsetArg(ctx, 2, len(h), fn)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if last {
		if off < 0 {
			limit := len(h) + off
			sub := fold(h)
			best := -1
			from := 0
			for {
				j := strings.Index(sub[from:], nn)
				if j < 0 {
					break
				}
				j += from
				if j > limit {
					break
				}
				best = j
				from = j + 1
			}
			if best < 0 {
				return vm.Bool(false)
			}
			return vm.Int(int64(best))
		}
		idx := strings.LastIndex(fold(h[off:]), nn)
		if idx < 0 {
			return vm.Bool(false)
		}
		return vm.Int(int64(off + idx))
	}
	start := off
	if start < 0 {
		start = len(h) + start
	}
	idx := strings.Index(fold(h[start:]), nn)
	if idx < 0 {
		return vm.Bool(false)
	}
	return vm.Int(int64(start + idx))
}

// offsetArg разбирает $offset в strpos/strrpos-семействе: строка не-numeric
// или float вне диапазона int -> TypeError, выход за пределы строки -> ValueError.
func offsetArg(ctx *vm.Context, i int, n int, fn string) int {
	s := arg(ctx, i)
	var off int64
	switch s.Type() {
	case vm.FloatType:
		f := s.ToFloat()
		if f >= 9223372036854775808.0 || f < -9223372036854775808.0 {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError(fn, i+1, "offset", "int", "float", "", 0)))
			if ctx.HasThrown() {
				return *new(int)
			}
		}
		off = int64(f)
	case vm.StringType:
		if !isNumericString(s.ToString()) {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError(fn, i+1, "offset", "int", "string", "", 0)))
			if ctx.HasThrown() {
				return *new(int)
			}
		}
		off = s.ToInt()
	default:
		off = s.ToInt()
	}
	if off < -int64(n) || off > int64(n) {
		ctx.Throw(vm.NewThrowable("ValueError",
			fn+"(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"))
		if ctx.HasThrown() {
			return *new(int)
		}
	}
	return int(off)
}

func chooseStr(caseInsensitive, last bool) string {
	switch {
	case caseInsensitive && last:
		return "strripos"
	case caseInsensitive:
		return "stripos"
	case last:
		return "strrpos"
	}
	return "strpos"
}

func substrCountFn(ctx *vm.Context) vm.Value {
	h := arg(ctx, 0).ToString()
	n := arg(ctx, 1).ToString()
	if n == "" {
		ctx.Throw(vm.NewThrowable("ValueError",
			"substr_count(): Argument #2 ($needle) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	lenH := len(h)
	off := int64(0)
	if len(ctx.Args()) >= 3 {
		off = arg(ctx, 2).ToInt()
		if off < 0 {
			if -off > int64(lenH) {
				ctx.Throw(vm.NewThrowable("ValueError", "substr_count(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			off = int64(lenH) + off
		} else if off > int64(lenH) {
			ctx.Throw(vm.NewThrowable("ValueError", "substr_count(): Argument #3 ($offset) must be contained in argument #1 ($haystack)"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	sub := h[off:]
	if len(ctx.Args()) >= 4 && arg(ctx, 3).Type() != vm.NullType {
		l := arg(ctx, 3).ToInt()
		if l < 0 {
			if -l > int64(len(sub)) {
				ctx.Throw(vm.NewThrowable("ValueError", "substr_count(): Argument #4 ($length) must be contained in argument #1 ($haystack)"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			l = int64(len(sub)) + l
		} else if off+l > int64(lenH) {
			ctx.Throw(vm.NewThrowable("ValueError", "substr_count(): Argument #4 ($length) must be contained in argument #1 ($haystack)"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if l < int64(len(sub)) {
			sub = sub[:l]
		}
	}
	return vm.Int(int64(strings.Count(sub, n)))
}

// --- ещё строковые функции ---

func strpbrkFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	chars := arg(ctx, 1).ToString()
	if chars == "" {
		ctx.Throw(vm.NewThrowable("ValueError", "strpbrk(): Argument #2 ($characters) must be a non-empty string"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if i := strings.IndexAny(s, chars); i >= 0 {
		return vm.Str(s[i:])
	}
	return vm.Bool(false)
}

func getHTMLTranslationTableFn(ctx *vm.Context) vm.Value {
	table := int64(1) // HTML_ENTITIES
	if len(ctx.Args()) >= 1 {
		a := arg(ctx, 0)
		if a.Type() == vm.FloatType {
			f := a.ToFloat()
			if f != f || f > 9.3e18 || f < -9.3e18 {
				ctx.Throw(vm.NewThrowable("TypeError",
					vm.ArgTypeError("get_html_translation_table", 1, "table", "int", "float", "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		}
		table = a.ToInt()
	}
	flags := int64(11)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	quote := flags & 3
	dt := flags & 48
	enc := "UTF-8"
	if len(ctx.Args()) >= 3 {
		enc = arg(ctx, 2).ToString()
		if enc == "" {
			enc = "UTF-8"
		} else if c, ok := mbCanon(enc); ok {
			enc = c
		}
	}
	out := vm.NewArray()
	set := func(key, val string) {
		if key != "" {
			out.SetStr(key, vm.MakeString(val))
		}
	}
	aposRef := "&#039;"
	if dt != 0 {
		aposRef = "&apos;"
	}
	aposEntity := "&#039;"
	if dt == 48 || dt == 16 {
		aposEntity = "&apos;"
	}
	if table != 0 && isMultibyteEncoding(enc) {
		set("&", "&amp;")
		set("<", "&lt;")
		set(">", "&gt;")
		if quote >= 2 {
			set("\"", "&quot;")
		}
		if quote == 3 {
			set("'", aposRef)
		}
		return vm.MakeArray(out)
	}
	if table == 0 { // HTML_SPECIALCHARS
		set("&", "&amp;")
		set("<", "&lt;")
		set(">", "&gt;")
		if quote >= 2 {
			set("\"", "&quot;")
		}
		if quote == 3 {
			set("'", aposRef)
		}
		return vm.MakeArray(out)
	}
	if dt == 16 { // XML 1.0 — только базовые сущности
		set("&", "&amp;")
		set("<", "&lt;")
		set(">", "&gt;")
		if quote >= 2 {
			set("\"", "&quot;")
		}
		if quote == 3 {
			set("'", aposEntity)
		}
		return vm.MakeArray(out)
	}
	src := html4TableUTF8
	if dt == 48 {
		src = html5TableUTF8
	}
	for ch, ent := range src {
		if ch == "\"" {
			if quote < 2 {
				continue
			}
			set(encodeString(ch, enc), "&quot;")
			continue
		}
		if ch == "'" {
			continue
		}
		set(encodeString(ch, enc), ent)
	}
	if quote == 3 {
		set("'", aposEntity)
	}
	return vm.MakeArray(out)
}

// encodeString кодирует строку (в UTF-8) в целевую кодировку; "" — если
// преобразование необратимо (символ непредставим в целевой кодировке).
func encodeString(s, enc string) string {
	if isUTF8Encoding(enc) || enc == "" {
		return s
	}
	r := mbConvertString(s, "UTF-8", enc)
	if r == "" {
		return ""
	}
	if mbConvertString(r, enc, "UTF-8") != s {
		return ""
	}
	return r
}

// encodeRune кодирует руну в целевую кодировку; "" — если непредставима.
func encodeRune(r rune, enc string) string {
	if isUTF8Encoding(enc) || enc == "" {
		return string(r)
	}
	s := mbConvertString(string(r), "UTF-8", enc)
	if s == "" {
		return ""
	}
	if mbConvertString(s, enc, "UTF-8") != string(r) {
		return ""
	}
	return s
}

func quotedPrintableEncode(s string) string {
	// Точный порт php_quot_print_encode (ext/standard/quot_print.c).
	const maxl = 75
	const hexchars = "0123456789ABCDEF"
	var b strings.Builder
	lp := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\r' && i+1 < len(s) && s[i+1] == '\n' && i > 0 {
			b.WriteString("\r\n")
			i++
			lp = 0
			continue
		}
		trailingSpace := c == ' ' && i+1 < len(s) && s[i+1] == '\r'
		if c < 0x20 || c == 0x7f || c >= 0x80 || c == '=' || trailingSpace {
			lp += 3
			brk := false
			switch {
			case c <= 0x7f:
				brk = lp > maxl
			case c <= 0xdf:
				brk = lp+3 > maxl
			case c <= 0xef:
				brk = lp+6 > maxl
			default:
				brk = lp+9 > maxl
			}
			if brk {
				b.WriteString("=\r\n")
				lp = 3
			}
			b.WriteByte('=')
			b.WriteByte(hexchars[c>>4])
			b.WriteByte(hexchars[c&15])
			continue
		}
		lp++
		if lp > maxl {
			b.WriteString("=\r\n")
			lp = 1
		}
		b.WriteByte(c)
	}
	return b.String()
}

func quotedPrintableDecode(s string) string {
	var b []byte
	for i := 0; i < len(s); i++ {
		if s[i] == '=' {
			if i+1 < len(s) && s[i+1] == '\n' {
				i++
				continue
			}
			if i+2 < len(s) {
				if s[i+1] == '\r' && s[i+2] == '\n' {
					i += 2
					continue
				}
				h1, h2 := hexVal(s[i+1]), hexVal(s[i+2])
				if h1 >= 0 && h2 >= 0 {
					b = append(b, byte(h1<<4|h2))
					i += 2
					continue
				}
			}
		}
		b = append(b, s[i])
	}
	return string(b)
}

func convertUUEncode(s string) string {
	src := []byte(s)
	var p []byte
	uuEnc := func(c byte) byte {
		if c != 0 {
			return (c & 0x3f) + ' '
		}
		return '`'
	}
	lineLen := 45
	i, n := 0, len(src)
	for i+3 < n {
		ee := i + lineLen
		if ee > n {
			ee = n
			lineLen = ee - i
			if lineLen%3 != 0 {
				ee = i + (lineLen/3)*3
			}
		}
		p = append(p, uuEnc(byte(lineLen)))
		for i < ee {
			p = append(p, uuEnc(src[i]>>2))
			p = append(p, uuEnc(((src[i]<<4)&060)|((src[i+1]>>4)&017)))
			p = append(p, uuEnc(((src[i+1]<<2)&074)|((src[i+2]>>6)&03)))
			p = append(p, uuEnc(src[i+2]&077))
			i += 3
		}
		if lineLen == 45 {
			p = append(p, '\n')
		}
	}
	if i < n {
		if lineLen == 45 {
			p = append(p, uuEnc(byte(n-i)))
			lineLen = 0
		}
		var b1, b2 byte
		if i+1 < n {
			b1 = src[i+1]
		}
		if i+2 < n {
			b2 = src[i+2]
		}
		p = append(p, uuEnc(src[i]>>2))
		p = append(p, uuEnc(((src[i]<<4)&060)|((b1>>4)&017)))
		if n-i > 1 {
			p = append(p, uuEnc(((b1<<2)&074)|((b2>>6)&03)))
		} else {
			p = append(p, uuEnc(0))
		}
		if n-i > 2 {
			p = append(p, uuEnc(b2&077))
		} else {
			p = append(p, uuEnc(0))
		}
	}
	if lineLen < 45 {
		p = append(p, '\n')
	}
	p = append(p, uuEnc(0))
	p = append(p, '\n')
	return string(p)
}

func uuChar(v byte) byte {
	v &= 0x3f
	if v == 0 {
		return '`'
	}
	return v + 32
}

// convertUUDecode реализует php_uudecode; второй результат — успех.
func convertUUDecode(s string) (string, bool) {
	if len(s) == 0 {
		return "", false
	}
	src := []byte(s)
	n := len(src)
	dec := func(i int) byte { return (src[i] - ' ') & 077 }
	var out []byte
	i := 0
	totalLen := 0
	for i < n {
		ln := int((src[i] - ' ') & 077)
		i++
		if ln == 0 {
			break
		}
		if ln > n {
			return "", false
		}
		totalLen += ln
		ee := i + 60
		if ln != 45 {
			ee = i + int(float64(ln)*1.33)
		}
		if ee > n {
			return "", false
		}
		for i < ee {
			if i+4 > n {
				return "", false
			}
			out = append(out, dec(i)<<2|dec(i+1)>>4)
			out = append(out, dec(i+1)<<4|dec(i+2)>>2)
			out = append(out, dec(i+2)<<6|dec(i+3))
			i += 4
		}
		if ln < 45 {
			break
		}
		i++ // пропустить \n
	}
	if totalLen > len(out) {
		if i+4 > n {
			return "", false
		}
		out = append(out, dec(i)<<2|dec(i+1)>>4)
		if totalLen > 1 {
			out = append(out, dec(i+1)<<4|dec(i+2)>>2)
			if totalLen > 2 {
				out = append(out, dec(i+2)<<6|dec(i+3))
			}
		}
	}
	if totalLen > len(out) {
		return "", false
	}
	return string(out[:totalLen]), true
}

func strtotimeFn(ctx *vm.Context) vm.Value {
	s := strings.TrimSpace(arg0(ctx).ToString())
	base := time.Now()
	if len(ctx.Args()) >= 2 && !arg(ctx, 1).IsNull() {
		base = time.Unix(arg(ctx, 1).ToInt(), 0)
	}
	base = base.In(dateLoc)
	ts, ok := phpStrtotime(s, base, dateLoc)
	if !ok {
		return vm.Bool(false)
	}
	return vm.Int(ts)
}

func strtotimeRelative(s string, base time.Time, loc *time.Location) (int64, bool) {
	i := 0
	sign := 1
	if s[i] == '+' {
		i++
	} else if s[i] == '-' {
		sign = -1
		i++
	}
	j := i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == i {
		return 0, false
	}
	n, _ := strconv.Atoi(s[i:j])
	rest := strings.TrimSpace(strings.ToLower(s[j:]))
	rest = strings.TrimSuffix(rest, "s")
	switch rest {
	case "second", "sec":
		base = base.Add(time.Duration(sign*n) * time.Second)
	case "minute", "min":
		base = base.Add(time.Duration(sign*n) * time.Minute)
	case "hour":
		base = base.Add(time.Duration(sign*n) * time.Hour)
	case "day":
		base = base.AddDate(0, 0, sign*n)
	case "week":
		base = base.AddDate(0, 0, 7*sign*n)
	case "month":
		base = base.AddDate(0, sign*n, 0)
	case "year":
		base = base.AddDate(sign*n, 0, 0)
	default:
		return 0, false
	}
	return base.Unix(), true
}

func strtotimeAbsolute(s string, base time.Time, loc *time.Location) (int64, bool) {
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"2006/01/02 15:04:05",
		"2006/01/02",
		"02 Jan 2006",
		"2 Jan 2006",
		"02 January 2006",
		"2 January 2006",
		"Jan 2 2006",
		"January 2 2006",
	}
	for _, l := range layouts {
		if t, err := time.ParseInLocation(l, s, loc); err == nil {
			return t.Unix(), true
		}
	}
	// только время — берём дату base
	for _, l := range []string{"15:04:05", "15:04"} {
		if hms, err := time.ParseInLocation(l, s, loc); err == nil {
			t := time.Date(base.Year(), base.Month(), base.Day(), hms.Hour(), hms.Minute(), hms.Second(), 0, loc)
			return t.Unix(), true
		}
	}
	return 0, false
}

// --- str_increment / str_decrement (PHP 8.3) ---

// isAlphaNumASCII сообщает, что строка состоит только из ASCII [0-9A-Za-z].
func isAlphaNumASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

func checkStrIncrArg(ctx *vm.Context, name, s string) {
	if len(s) == 0 {
		ctx.Throw(vm.NewThrowable("ValueError", name+"(): Argument #1 ($string) cannot be empty"))
		if ctx.HasThrown() {
			return
		}
	}
	if !isAlphaNumASCII(s) {
		ctx.Throw(vm.NewThrowable("ValueError", name+"(): Argument #1 ($string) must be composed only of alphanumeric ASCII characters"))
		if ctx.HasThrown() {
			return
		}
	}
}

func strIncrementFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	checkStrIncrArg(ctx, "str_increment", s)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	b := []byte(s)
	carry := true
	for i := len(b) - 1; i >= 0 && carry; i-- {
		c := b[i]
		switch {
		case c >= '0' && c < '9':
			b[i] = c + 1
			carry = false
		case c == '9':
			b[i] = '0'
		case c >= 'A' && c < 'Z':
			b[i] = c + 1
			carry = false
		case c == 'Z':
			b[i] = 'A'
		case c >= 'a' && c < 'z':
			b[i] = c + 1
			carry = false
		case c == 'z':
			b[i] = 'a'
		}
	}
	if carry {
		var pre byte
		switch b[0] {
		case '0':
			pre = '1'
		case 'A':
			pre = 'A'
		default:
			pre = 'a'
		}
		b = append([]byte{pre}, b...)
	}
	return vm.Str(string(b))
}

func strDecrementFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	if len(s) == 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "str_decrement(): Argument #1 ($string) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if !isAlphaNumASCII(s) {
		ctx.Throw(vm.NewThrowable("ValueError", "str_decrement(): Argument #1 ($string) must be composed only of alphanumeric ASCII characters"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if s[0] == '0' {
		ctx.Throw(vm.NewThrowable("ValueError", "str_decrement(): Argument #1 ($string) \""+s+"\" is out of decrement range"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	b := []byte(s)
	carry := true
	for i := len(b) - 1; i >= 0 && carry; i-- {
		c := b[i]
		switch {
		case c > '0' && c <= '9':
			b[i] = c - 1
			carry = false
		case c == '0':
			b[i] = '9'
		case c > 'A' && c <= 'Z':
			b[i] = c - 1
			carry = false
		case c == 'A':
			b[i] = 'Z'
		case c > 'a' && c <= 'z':
			b[i] = c - 1
			carry = false
		case c == 'a':
			b[i] = 'z'
		}
	}
	if carry {
		b = b[1:]
	}
	if len(b) == 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "str_decrement(): Argument #1 ($string) \""+s+"\" is out of decrement range"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	for len(b) > 1 && b[0] == '0' {
		b = b[1:]
	}
	return vm.Str(string(b))
}

// --- sscanf ---

// sscanfCountConversions считает спецификаторы преобразования в формате.
func sscanfCountConversions(format string) int {
	n := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		for i < len(format) && (format[i] == '-' || format[i] == '+' || format[i] == ' ' ||
			format[i] == '#' || format[i] == '0' || (format[i] >= '0' && format[i] <= '9')) {
			i++
		}
		for i < len(format) && strings.IndexByte("lhLjztq", format[i]) >= 0 {
			i++
		}
		// скобочный набор %[...]
		if i < len(format) && format[i] == '[' {
			i++
			if i < len(format) && format[i] == '^' {
				i++
			}
			if i < len(format) && format[i] == ']' {
				i++
			}
			for i < len(format) && format[i] != ']' {
				i++
			}
			n++
			continue
		}
		if i < len(format) {
			n++
		}
	}
	return n
}

// sscanfResultLen вычисляет длину результирующего массива sscanf.
func sscanfResultLen(format string) int {
	maxLen := 0
	seq := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		argnum := 0
		{
			k := i
			num := 0
			for k < len(format) && format[k] >= '0' && format[k] <= '9' {
				num = num*10 + int(format[k]-'0')
				k++
			}
			if k < len(format) && format[k] == '$' && num > 0 {
				argnum = num
				i = k + 1
			}
		}
		for i < len(format) && (format[i] == '-' || format[i] == '+' || format[i] == ' ' ||
			format[i] == '#' || format[i] == '0' || (format[i] >= '0' && format[i] <= '9')) {
			i++
		}
		for i < len(format) && strings.IndexByte("lhLjztq", format[i]) >= 0 {
			i++
		}
		if i < len(format) && format[i] == '[' {
			i++
			if i < len(format) && format[i] == '^' {
				i++
			}
			if i < len(format) && format[i] == ']' {
				i++
			}
			for i < len(format) && format[i] != ']' {
				i++
			}
		}
		idx := seq
		if argnum > 0 {
			idx = argnum - 1
		} else {
			seq++
		}
		if idx+1 > maxLen {
			maxLen = idx + 1
		}
	}
	return maxLen
}

// sscanfArgNumOverflow — есть ли N$ с индексом, выходящим за INT_MAX.
func sscanfArgNumOverflow(format string) bool {
	for i := 0; i < len(format); i++ {
		if format[i] != '%' {
			continue
		}
		i++
		if i < len(format) && format[i] == '%' {
			continue
		}
		k := i
		num := 0
		overflow := false
		for k < len(format) && format[k] >= '0' && format[k] <= '9' {
			num = num*10 + int(format[k]-'0')
			if num > 2147483647 {
				overflow = true
				num = 2147483647
			}
			k++
		}
		if k < len(format) && format[k] == '$' && overflow {
			return true
		}
	}
	return false
}

func isSScanfSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

func isHexByte(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func sscanfWriteRef(ctx *vm.Context, i int, v vm.Slot) {
	args := ctx.Args()
	if i < 0 || i >= len(args) {
		return
	}
	s := args[i]
	if s.IsRef() && s.Ref() != nil {
		s.Ref().Set(v)
	}
}

func sscanfFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("sscanf() expects at least 2 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	str := args[0].ToString()
	format := args[1].ToString()
	if sscanfArgNumOverflow(format) {
		ctx.Throw(vm.NewThrowable("ValueError", `"%n$" argument index out of range`))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	convCount := sscanfCountConversions(format)
	refArgs := len(args) - 2
	if refArgs > convCount {
		ctx.Throw(vm.NewThrowable("ValueError", "Variable is not assigned by any conversion specifiers"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	vals, okCount := sscanfParse(str, format)

	if refArgs > 0 {
		for i := 0; i < refArgs; i++ {
			// PHP присваивает только успешно прочитанные значения; остальные
			// переменные сохраняют прежние значения.
			if i >= okCount {
				break
			}
			var v vm.Slot = vm.MakeNull()
			if i < len(vals) {
				v = vals[i]
			}
			sscanfWriteRef(ctx, 2+i, v)
		}
		return vm.Int(int64(okCount))
	}
	a := vm.NewArray()
	for _, v := range vals {
		a.Append(v)
	}
	return vm.MakeArray(a)
}

// sscanfParse выполняет разбор; возвращает список значений (предразмеренный)
// и число успешных преобразований.
func sscanfParse(str, format string) ([]vm.Slot, int) {
	// Предразмер результата: PHP возвращает массив длиной в число
	// спецификаторов (или максимальный N$), заполненный NULL при неудаче.
	resultLen := sscanfResultLen(format)
	vals := make([]vm.Slot, resultLen)
	for i := range vals {
		vals[i] = vm.MakeNull()
	}
	okCount := 0
	seq := 0
	setPos := func(idx int, v vm.Slot) {
		for len(vals) <= idx {
			vals = append(vals, vm.MakeNull())
		}
		vals[idx] = v
	}
	si, fi := 0, 0
	for fi < len(format) {
		fc := format[fi]
		if fc == '%' {
			fi++
			if fi < len(format) && format[fi] == '%' {
				if si < len(str) && str[si] == '%' {
					si++
					fi++
					continue
				}
				return vals, okCount
			}
			// позиционный спецификатор N$ — индекс назначения.
			argnum := 0
			{
				k := fi
				num := 0
				for k < len(format) && format[k] >= '0' && format[k] <= '9' {
					num = num*10 + int(format[k]-'0')
					k++
				}
				if k < len(format) && format[k] == '$' && num > 0 {
					argnum = num
					fi = k + 1
				}
			}
			width := 0
			for fi < len(format) && (format[fi] == '-' || format[fi] == '+' || format[fi] == ' ' ||
				format[fi] == '#' || format[fi] == '0' || (format[fi] >= '0' && format[fi] <= '9')) {
				if format[fi] >= '0' && format[fi] <= '9' {
					width = width*10 + int(format[fi]-'0')
				}
				fi++
			}
			for fi < len(format) && strings.IndexByte("lhLjztq", format[fi]) >= 0 {
				fi++
			}
			if fi >= len(format) {
				break
			}
			spec := format[fi]
			fi++
			if spec == 'n' {
				// %n — число уже прочитанных символов (вход не потребляется).
				okCount++
				if argnum > 0 {
					setPos(argnum-1, vm.MakeInt(int64(si)))
				} else {
					setPos(seq, vm.MakeInt(int64(si)))
					seq++
				}
				continue
			}
			if spec == '[' {
				// скобочный набор: %[...] с необязательным '^' и ведущим ']'.
				neg := false
				if fi < len(format) && format[fi] == '^' {
					neg = true
					fi++
				}
				if fi < len(format) && format[fi] == ']' {
					fi++
				}
				setStart := fi
				for fi < len(format) && format[fi] != ']' {
					fi++
				}
				set := format[setStart:fi]
				if fi < len(format) {
					fi++
				}
				start := si
				for si < len(str) {
					in := strings.IndexByte(set, str[si]) >= 0
					if in == neg {
						break
					}
					si++
					if width > 0 && si-start >= width {
						break
					}
				}
				if si == start {
					return vals, okCount
				}
				okCount++
				v := vm.MakeString(str[start:si])
				if argnum > 0 {
					setPos(argnum-1, v)
				} else {
					setPos(seq, v)
					seq++
				}
				continue
			}
			if spec != 'c' {
				for si < len(str) && isSScanfSpace(str[si]) {
					si++
				}
			}
			v, n, ok := sscanfConv(str[si:], spec, width)
			if !ok {
				return vals, okCount
			}
			si += n
			okCount++
			if argnum > 0 {
				setPos(argnum-1, v)
			} else {
				setPos(seq, v)
				seq++
			}
			continue
		}
		if isSScanfSpace(fc) {
			fi++
			for si < len(str) && isSScanfSpace(str[si]) {
				si++
			}
			continue
		}
		if si < len(str) && str[si] == fc {
			si++
			fi++
			continue
		}
		return vals, okCount
	}
	return vals, okCount
}

// sscanfConv разбирает одно преобразование; n — сколько байт входа съедено.
func sscanfConv(s string, spec byte, width int) (vm.Slot, int, bool) {
	limit := func(n int) int {
		if width > 0 && n > width {
			return width
		}
		return n
	}
	switch spec {
	case 's':
		i := 0
		for i < len(s) && !isSScanfSpace(s[i]) && i < limit(len(s)) {
			i++
		}
		if i == 0 {
			return vm.MakeNull(), 0, false
		}
		return vm.MakeString(s[:i]), i, true
	case 'c':
		if len(s) == 0 {
			return vm.MakeNull(), 0, false
		}
		n := limit(1)
		return vm.MakeString(s[:n]), n, true
	case 'd', 'u':
		i, neg := 0, false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			neg = s[i] == '-'
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == start {
			return vm.MakeNull(), 0, false
		}
		v, _ := strconv.ParseInt(s[start:i], 10, 64)
		if neg {
			v = -v
		}
		return vm.MakeInt(v), i, true
	case 'x', 'X':
		i, neg := 0, false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			neg = s[i] == '-'
			i++
		}
		// необязательный префикс 0x
		if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
			i += 2
		}
		start := i
		for i < len(s) && isHexByte(s[i]) {
			i++
		}
		if i == start {
			return vm.MakeNull(), 0, false
		}
		v, _ := strconv.ParseInt(s[start:i], 16, 64)
		if neg {
			v = -v
		}
		return vm.MakeInt(v), i, true
	case 'o':
		i, neg := 0, false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			neg = s[i] == '-'
			i++
		}
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '7' {
			i++
		}
		if i == start {
			return vm.MakeNull(), 0, false
		}
		v, _ := strconv.ParseInt(s[start:i], 8, 64)
		if neg {
			v = -v
		}
		return vm.MakeInt(v), i, true
	case 'f', 'e', 'E', 'g', 'G':
		i, end := parseFloatToken(s)
		if i == 0 {
			return vm.MakeNull(), 0, false
		}
		f, _ := strconv.ParseFloat(s[:end], 64)
		return vm.MakeFloat(f), end, true
	case 'i':
		// автоопределение основания
		i, neg := 0, false
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			neg = s[i] == '-'
			i++
		}
		base := 10
		if i+1 < len(s) && s[i] == '0' && (s[i+1] == 'x' || s[i+1] == 'X') {
			base = 16
			i += 2
		} else if i < len(s) && s[i] == '0' {
			base = 8
		}
		start := i
		for i < len(s) {
			c := s[i]
			if c >= '0' && c <= '9' || base == 16 && isHexByte(c) {
				i++
			} else {
				break
			}
		}
		if i == start {
			return vm.MakeNull(), 0, false
		}
		v, _ := strconv.ParseInt(s[start:i], base, 64)
		if neg {
			v = -v
		}
		return vm.MakeInt(v), i, true
	}
	return vm.MakeNull(), 0, false
}

// parseFloatToken разбирает числовой токен; возвращает (знаковый префикс+цифры, конец).
func parseFloatToken(s string) (int, int) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	hasDigits := false
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
		hasDigits = true
	}
	if i < len(s) && s[i] == '.' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
			hasDigits = true
		}
	}
	if !hasDigits {
		return 0, 0
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		j := i + 1
		if j < len(s) && (s[j] == '+' || s[j] == '-') {
			j++
		}
		k := j
		for k < len(s) && s[k] >= '0' && s[k] <= '9' {
			k++
		}
		if k > j {
			i = k
		}
	}
	return i, i
}

// argToStringVal приводит аргумент к строке с учётом __toString объектов и
// предупреждения о массиве.
func argToStringVal(ctx *vm.Context, i int) string {
	v := arg(ctx, i)
	if v.Type() == vm.ObjectType || v.Type() == vm.ArrayType {
		s, err := ctx.VM().ToStringValue(v)
		if err != nil {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(string)
			}
		}
		return s
	}
	return v.ToString()
}

// asciiLower/asciiUpper — регистронезависимость только для ASCII (как в PHP:
// strtolower/strtoupper не трогают байты >0x7F и не портят невалидный UTF-8).
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func asciiUpper(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'a' && c <= 'z' {
			b[i] = c - 32
		}
	}
	return string(b)
}

// --- parse_str ---

type parseStrSeg struct {
	appendMode bool
	key        string
}

// parseStrName повторяет манглинг имён PHP: недопустимые байты -> '_'.
func parseStrName(s string) string {
	b := []byte(s)
	for i := range b {
		c := b[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

// parseStrIndexKey превращает сегмент индекса в ключ (int для чисел).
func parseStrSegKey(seg string) (vm.ArrayKey, bool) {
	if seg == "" {
		return vm.ArrayKey{}, false
	}
	n, err := strconv.ParseInt(seg, 10, 64)
	if err == nil {
		return vm.MakeIntKey(n), true
	}
	return vm.MakeStrKey(seg), true
}

func parseStrNested(arr *vm.Array, segs []parseStrSeg, val string) {
	seg := segs[0]
	if len(segs) == 1 {
		if seg.appendMode {
			arr.Append(vm.MakeString(val))
		} else {
			k, _ := parseStrSegKey(seg.key)
			arr.Set(k, vm.MakeString(val))
		}
		return
	}
	if seg.appendMode {
		child := vm.NewArray()
		arr.SetInt(int64(arr.Len()), vm.MakeArray(child))
		parseStrNested(child, segs[1:], val)
		return
	}
	k, _ := parseStrSegKey(seg.key)
	child := vm.NewArray()
	if v, ok := arr.Get(k); ok && v.Type() == vm.ArrayType {
		child = v.AsArray()
	} else {
		arr.Set(k, vm.MakeArray(child))
	}
	parseStrNested(child, segs[1:], val)
}

func parseStrInsert(root *vm.Array, key, val string) {
	lb := strings.IndexByte(key, '[')
	if lb < 0 {
		root.SetStr(key, vm.MakeString(val))
		return
	}
	if !strings.Contains(key, "]") {
		root.SetStr(parseStrName(key), vm.MakeString(val))
		return
	}
	base := parseStrName(key[:lb])
	var segs []parseStrSeg
	rest := key[lb:]
	for len(rest) > 0 && rest[0] == '[' {
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			break
		}
		seg := rest[1:j]
		if seg == "" {
			segs = append(segs, parseStrSeg{appendMode: true})
		} else {
			segs = append(segs, parseStrSeg{key: seg})
		}
		rest = rest[j+1:]
	}
	if len(segs) == 0 {
		root.SetStr(parseStrName(key), vm.MakeString(val))
		return
	}
	// база может быть вложенным массивом
	child := vm.NewArray()
	if v, ok := root.Get(vm.MakeStrKey(base)); ok && v.Type() == vm.ArrayType {
		child = v.AsArray()
	} else {
		root.SetStr(base, vm.MakeArray(child))
	}
	parseStrNested(child, segs, val)
}

func parseStrFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("parse_str() expects exactly 2 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	s := args[0].ToString()
	root := vm.NewArray()
	sep := "&"
	if v, ok := IniValue("arg_separator.input"); ok && v != "" {
		sep = v
	}
	maxVars := 1000
	if v, ok := IniValue("max_input_vars"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			maxVars = n
		}
	}
	var pairs []string
	if strings.ContainsAny(s, sep) {
		cur := 0
		for i := 0; i < len(s); i++ {
			if strings.IndexByte(sep, s[i]) >= 0 {
				pairs = append(pairs, s[cur:i])
				cur = i + 1
			}
		}
		pairs = append(pairs, s[cur:])
	} else {
		pairs = []string{s}
	}
	n := 0
	for _, pair := range pairs {
		if pair == "" {
			continue
		}
		k, v := pair, ""
		if i := strings.IndexByte(pair, '='); i >= 0 {
			k, v = pair[:i], pair[i+1:]
		}
		if n >= maxVars {
			break
		}
		n++
		parseStrInsert(root, phpURLDecode(k, false), phpURLDecode(v, false))
	}
	pregWriteRef(ctx, 1, vm.MakeArray(root))
	return vm.MakeNull()
}

// mbParseStrFn — mb_parse_str(): то же, что parse_str, но возвращает true
// (mbstring сохранил bool-возврат; parse_str с PHP 8 возвращает null).
func mbParseStrFn(ctx *vm.Context) vm.Value {
	parseStrFn(ctx)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return vm.MakeBool(true)
}

// --- hebrev ---

func isHeb(c byte) bool        { return c >= 224 && c <= 250 }
func isBlankHeb(c byte) bool   { return c == ' ' || c == '\t' }
func isNewlineHeb(c byte) bool { return c == '\n' || c == '\r' }

func isPunctC(c byte) bool {
	return (c >= 0x21 && c <= 0x2f) || (c >= 0x3a && c <= 0x40) ||
		(c >= 0x5b && c <= 0x60) || (c >= 0x7b && c <= 0x7e)
}

func hebrevConvert(s string, maxChars int) string {
	n := len(s)
	if n == 0 {
		return ""
	}
	at := func(i int) byte {
		if i < 0 || i >= n {
			return 0
		}
		return s[i]
	}
	heb := make([]byte, n+1)
	target := n - 1
	blockStart, blockEnd := 0, 0
	blockType := 1 // ENG
	if isHeb(s[0]) {
		blockType = 2 // HEB
	}
	for {
		if blockType == 2 {
			for (isHeb(at(blockEnd+1)) || isBlankHeb(at(blockEnd+1)) || isPunctC(at(blockEnd+1)) || at(blockEnd+1) == '\n') && blockEnd < n-1 {
				blockEnd++
			}
			for i := blockStart + 1; i <= blockEnd+1; i++ {
				c := s[i-1]
				switch c {
				case '(':
					c = ')'
				case ')':
					c = '('
				case '[':
					c = ']'
				case ']':
					c = '['
				case '{':
					c = '}'
				case '}':
					c = '{'
				case '<':
					c = '>'
				case '>':
					c = '<'
				case '\\':
					c = '/'
				case '/':
					c = '\\'
				}
				heb[target] = c
				target--
			}
			blockType = 1
		} else {
			for !isHeb(at(blockEnd+1)) && at(blockEnd+1) != '\n' && blockEnd < n-1 {
				blockEnd++
			}
			for (isBlankHeb(s[blockEnd]) || isPunctC(s[blockEnd])) && s[blockEnd] != '/' && s[blockEnd] != '-' && blockEnd > blockStart {
				blockEnd--
			}
			for i := blockEnd + 1; i >= blockStart+1; i-- {
				heb[target] = s[i-1]
				target--
			}
			blockType = 2
		}
		blockStart = blockEnd + 1
		if !(blockEnd < n-1) {
			break
		}
	}

	var broken []byte
	begin, end := n-1, n-1
	for {
		charCount := 0
		for (maxChars == 0 || (maxChars > 0 && charCount < maxChars)) && begin > 0 {
			charCount++
			begin--
			if isNewlineHeb(heb[begin]) {
				for begin > 0 && isNewlineHeb(heb[begin-1]) {
					begin--
					charCount++
				}
				break
			}
		}
		if maxChars >= 0 && charCount == maxChars {
			newCharCount, newBegin := charCount, begin
			for newCharCount > 0 {
				if isBlankHeb(heb[newBegin]) || isNewlineHeb(heb[newBegin]) {
					break
				}
				newBegin++
				newCharCount--
			}
			if newCharCount > 0 {
				begin = newBegin
			}
		}
		origBegin := begin
		if isBlankHeb(heb[begin]) {
			heb[begin] = '\n'
		}
		for begin <= end && isNewlineHeb(heb[begin]) {
			begin++
		}
		for i := begin; i <= end; i++ {
			broken = append(broken, heb[i])
		}
		for i := origBegin; i <= end && isNewlineHeb(heb[i]); i++ {
			broken = append(broken, heb[i])
		}
		begin = origBegin
		if begin == 0 {
			break
		}
		begin--
		end = begin
	}
	return string(broken)
}

func hebrevFn(ctx *vm.Context) vm.Value {
	s := arg0(ctx).ToString()
	maxChars := 0
	if len(ctx.Args()) >= 2 {
		maxChars = int(arg(ctx, 1).ToInt())
	}
	return vm.Str(hebrevConvert(s, maxChars))
}

// --- get_meta_tags ---

func metaTagsKey(name string) string {
	b := []byte(strings.ToLower(name))
	for i := range b {
		c := b[i]
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_') {
			b[i] = '_'
		}
	}
	return string(b)
}

func metaAttr(tag, want string) (string, bool) {
	i := 0
	n := len(tag)
	for i < n {
		for i < n && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r' || tag[i] == '/') {
			i++
		}
		start := i
		for i < n && tag[i] != '=' && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' && tag[i] != '>' && tag[i] != '/' {
			i++
		}
		key := strings.ToLower(tag[start:i])
		for i < n && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r') {
			i++
		}
		if i >= n || tag[i] != '=' {
			continue
		}
		i++
		for i < n && (tag[i] == ' ' || tag[i] == '\t' || tag[i] == '\n' || tag[i] == '\r') {
			i++
		}
		val := ""
		if i < n && (tag[i] == '"' || tag[i] == '\'') {
			q := tag[i]
			i++
			vs := i
			for i < n && tag[i] != q {
				i++
			}
			val = tag[vs:i]
			if i < n {
				i++
			}
		} else {
			vs := i
			for i < n && tag[i] != ' ' && tag[i] != '\t' && tag[i] != '\n' && tag[i] != '\r' && tag[i] != '>' {
				i++
			}
			val = tag[vs:i]
		}
		if key == want {
			return val, true
		}
	}
	return "", false
}

func getMetaTagsFn(ctx *vm.Context) vm.Value {
	path := arg0(ctx).ToString()
	raw, err := os.ReadFile(path)
	data := string(raw)
	if err != nil {
		ctx.VM().Warning("get_meta_tags(" + path + "): Failed to open stream: No such file or directory")
		return vm.Bool(false)
	}
	out := vm.NewArray()
	i := 0
	n := len(data)
	for i < n {
		// остановка на </head
		if data[i] == '<' && i+6 <= n && strings.EqualFold(data[i:i+6], "</head") {
			break
		}
		if data[i] != '<' || i+5 > n || !strings.EqualFold(data[i:i+5], "<meta") {
			i++
			continue
		}
		// найти '>' до следующего <meta
		j := i + 5
		end := -1
		for j < n {
			if data[j] == '>' {
				end = j
				break
			}
			if data[j] == '<' && j+5 <= n && strings.EqualFold(data[j:j+5], "<meta") {
				break
			}
			j++
		}
		if end < 0 {
			i = j
			continue
		}
		tag := data[i+5 : end]
		name, ok1 := metaAttr(tag, "name")
		content, ok2 := metaAttr(tag, "content")
		if ok1 && ok2 {
			out.SetStr(metaTagsKey(name), vm.MakeString(content))
		}
		i = end + 1
	}
	return vm.MakeArray(out)
}
