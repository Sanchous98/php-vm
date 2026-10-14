package php

import (
	"fmt"
	"math"
	"math/big"
	"phpvm/internal/vm"
	"strconv"
	"strings"
)

// Файл содержит математические функции.

// numArg приводит аргумент встроенной функции к типу int|float по правилам
// слабой типизации Zend: int/bool -> int, null -> int(0) с Deprecated,
// числовая строка -> int/float, иначе TypeError. ok=false — уже брошено
// исключение (вызывающий обязан немедленно вернуться).
func numArg(ctx *vm.Context, i int, fn, param string) (vm.Slot, bool) {
	a := derefSlot(arg(ctx, i))
	switch a.Type() {
	case vm.IntType, vm.FloatType:
		return a, true
	case vm.BoolType:
		if a.ToBool() {
			return vm.MakeInt(1), true
		}
		return vm.MakeInt(0), true
	case vm.NullType:
		ctx.VM().Deprecated(fn + "(): Passing null to parameter #" + strconv.Itoa(i+1) +
			" ($" + param + ") of type int|float is deprecated")
		return vm.MakeInt(0), true
	case vm.StringType:
		if v, ok := vm.NumericStringSlot(a.StringVal()); ok {
			return v, true
		}
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		vm.ArgTypeError(fn, i+1, param, "int|float", phpTypeName(ctx, i), "", 0)))
	return vm.Slot{}, false
}

// absFn — abs(int|float).
func absFn(ctx *vm.Context) vm.Value {
	v, ok := numArg(ctx, 0, "abs", "num")
	if !ok {
		return *new(vm.Value)
	}
	if v.Type() == vm.IntType {
		n := v.ToInt()
		if n == math.MinInt64 {
			return vm.Float(-float64(n))
		}
		if n < 0 {
			return vm.Int(-n)
		}
		return vm.Int(n)
	}
	return vm.Float(math.Abs(v.ToFloat()))
}

// floatUnary — функции вида ceil/floor/sqrt(int|float): float.
func floatUnary(ctx *vm.Context, name, param string, f func(float64) float64) vm.Value {
	v, ok := numArg(ctx, 0, name, param)
	if !ok {
		return *new(vm.Value)
	}
	return vm.Float(f(v.ToFloat()))
}

func roundFn(ctx *vm.Context) vm.Value {
	v, ok := numArg(ctx, 0, "round", "num")
	if !ok {
		return *new(vm.Value)
	}
	p := int64(0)
	if len(ctx.Args()) >= 2 {
		pv, ok := numArg(ctx, 1, "round", "precision")
		if !ok {
			return *new(vm.Value)
		}
		p = pv.ToInt()
	}
	mode := int64(1)
	if len(ctx.Args()) >= 3 {
		mv, ok := numArg(ctx, 2, "round", "mode")
		if !ok {
			return *new(vm.Value)
		}
		mode = mv.ToInt()
	}
	// Zend: целое при неотрицательной точности не округляется вовсе.
	if v.Type() == vm.IntType && p >= 0 {
		return vm.Float(float64(v.ToInt()))
	}
	return vm.Float(phpRound(v.ToFloat(), int(p), int(mode)))
}

// phpRound повторяет _php_math_round из Zend (включая предраундинг), чтобы
// round() совпадал с C-реализацией на граничных значениях.
func phpRound(value float64, places int, mode int) float64 {
	if math.IsNaN(value) || math.IsInf(value, 0) || value == 0 {
		return value
	}
	if places < math.MinInt32+1 {
		places = math.MinInt32 + 1
	}
	precisionPlaces := 14 - phpIntLog10Abs(value)
	f1 := phpIntPow10(absInt(places))
	var tmp float64
	if precisionPlaces > places && precisionPlaces-15 < places {
		usePrecision := precisionPlaces
		if usePrecision < math.MinInt32+1 {
			usePrecision = math.MinInt32 + 1
		}
		f2 := phpIntPow10(absInt(usePrecision))
		if usePrecision >= 0 {
			tmp = value * f2
		} else {
			tmp = value / f2
		}
		tmp = phpRoundHelper(tmp, mode)
		usePrecision = places - precisionPlaces
		if usePrecision < math.MinInt32+1 {
			usePrecision = math.MinInt32 + 1
		}
		f2 = phpIntPow10(absInt(usePrecision))
		tmp = tmp / f2
	} else {
		if places >= 0 {
			tmp = value * f1
		} else {
			tmp = value / f1
		}
		if math.Abs(tmp) >= 1e15 {
			return value
		}
	}
	tmp = phpRoundHelper(tmp, mode)
	if absInt(places) < 23 {
		if places > 0 {
			tmp = tmp / f1
		} else {
			tmp = tmp * f1
		}
	} else {
		buf := fmt.Sprintf("%15fe%d", tmp, -places)
		if tmp == 0 {
			// zend_strtod("0.000e<huge>") == 0 (Go на таком возвращает ErrRange).
			tmp = 0
		} else if v, err := strconv.ParseFloat(buf, 64); err == nil && !math.IsNaN(v) && !math.IsInf(v, 0) {
			tmp = v
		} else {
			tmp = value
		}
	}
	return tmp
}

// phpRoundHelper — php_round_helper: округление по режиму PHP_ROUND_HALF_*.
func phpRoundHelper(value float64, mode int) float64 {
	if value >= 0.0 {
		tmp := math.Floor(value + 0.5)
		if (mode == 2 && value == -0.5+tmp) || // HALF_DOWN
			(mode == 3 && value == 0.5+2*math.Floor(tmp/2.0)) || // HALF_EVEN
			(mode == 4 && value == 0.5+2*math.Floor(tmp/2.0)-1.0) { // HALF_ODD
			tmp = tmp - 1.0
		}
		return tmp
	}
	tmp := math.Ceil(value - 0.5)
	if (mode == 2 && value == 0.5+tmp) ||
		(mode == 3 && value == -0.5+2*math.Ceil(tmp/2.0)) ||
		(mode == 4 && value == -0.5+2*math.Ceil(tmp/2.0)+1.0) {
		tmp = tmp + 1.0
	}
	return tmp
}

// phpIntLog10Abs — floor(log10(|value|)) двоичным поиском, как в math.c.
func phpIntLog10Abs(value float64) int {
	value = math.Abs(value)
	if value < 1e-8 || value > 1e22 {
		return int(math.Floor(math.Log10(value)))
	}
	values := [...]float64{
		1e-8, 1e-7, 1e-6, 1e-5, 1e-4, 1e-3, 1e-2, 1e-1, 1e0, 1e1, 1e2,
		1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11, 1e12, 1e13,
		1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}
	result := 15
	if value < values[result] {
		result -= 8
	} else {
		result += 8
	}
	if value < values[result] {
		result -= 4
	} else {
		result += 4
	}
	if value < values[result] {
		result -= 2
	} else {
		result += 2
	}
	if value < values[result] {
		result -= 1
	} else {
		result += 1
	}
	if value < values[result] {
		result -= 1
	}
	return result - 8
}

// phpIntPow10 — 10^power (таблица Zend: 0..22, иначе pow).
func phpIntPow10(power int) float64 {
	if power < 0 || power > 22 {
		return math.Pow(10.0, float64(power))
	}
	powers := [...]float64{
		1e0, 1e1, 1e2, 1e3, 1e4, 1e5, 1e6, 1e7, 1e8, 1e9, 1e10, 1e11,
		1e12, 1e13, 1e14, 1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22}
	return powers[power]
}

// powFn — pow() с семантикой оператора ** (включая TypeError для
// нечисловых операндов).
func powFn(ctx *vm.Context) vm.Value {
	if n := len(ctx.Args()); n != 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("pow() expects exactly 2 arguments, %d given", n)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	r, err := ctx.VM().PowOp(arg(ctx, 0), arg(ctx, 1))
	if err != nil {
		if th, ok := err.(*vm.Throwable); ok {
			ctx.Throw(th)
		} else {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
		}
		return *new(vm.Value)
	}
	return r
}

func minMax(ctx *vm.Context, min bool) vm.Value {
	name := "max"
	if min {
		name = "min"
	}
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", name+"() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if len(args) == 1 {
		a := args[0]
		if a.Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError(name, 1, "value", "array", phpTypeName(ctx, 0), "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		arr := a.AsArray()
		if arr.Len() == 0 {
			ctx.Throw(vm.NewThrowable("ValueError", name+"(): Argument #1 ($value) must contain at least one element"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		var best vm.Slot
		first := true
		for _, v := range arr.Values() {
			if first {
				best, first = v, false
				continue
			}
			c := vm.CompareSlots(v, best)
			if (min && c < 0) || (!min && c > 0) {
				best = v
			}
		}
		return derefSlot(best)
	}
	best := args[0]
	for i := 1; i < len(args); i++ {
		c := vm.CompareSlots(args[i], best)
		if (min && c < 0) || (!min && c > 0) {
			best = args[i]
		}
	}
	return derefSlot(best)
}

func digitVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'z':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'Z':
		return int(c-'A') + 10
	}
	return -1
}

func baseConvertFn(ctx *vm.Context) vm.Value {
	str, ok := strParam(ctx, 0, "base_convert", "num")
	if !ok {
		return *new(vm.Value)
	}
	from := int(intParam(ctx, 1, "base_convert", "from_base"))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if from < 2 || from > 36 {
		ctx.Throw(vm.NewThrowable("ValueError", "base_convert(): Argument #2 ($from_base) must be between 2 and 36 (inclusive)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	to := int(intParam(ctx, 2, "base_convert", "to_base"))
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if to < 2 || to > 36 {
		ctx.Throw(vm.NewThrowable("ValueError", "base_convert(): Argument #3 ($to_base) must be between 2 and 36 (inclusive)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	s := strings.TrimSpace(str)
	// префиксы 0x/0o/0b (improvements RFC, PHP 8.3)
	if len(s) >= 2 && s[0] == '0' {
		switch s[1] {
		case 'x', 'X':
			if from == 16 {
				s = s[2:]
			}
		case 'o', 'O':
			if from == 8 {
				s = s[2:]
			}
		case 'b', 'B':
			if from == 2 {
				s = s[2:]
			}
		}
	}
	var n big.Int
	base := big.NewInt(int64(from))
	invalid := 0
	for i := 0; i < len(s); i++ {
		d := digitVal(s[i])
		if d < 0 || d >= from {
			invalid++
			continue
		}
		n.Mul(&n, base)
		n.Add(&n, big.NewInt(int64(d)))
	}
	if invalid > 0 {
		ctx.VM().Deprecated("Invalid characters passed for attempted conversion, these have been ignored")
	}
	if to == 10 {
		return vm.Str(n.String())
	}
	if n.Sign() == 0 {
		return vm.Str("0")
	}
	const glyphs = "0123456789abcdefghijklmnopqrstuvwxyz"
	baseTo := big.NewInt(int64(to))
	var out []byte
	q := new(big.Int)
	r := new(big.Int)
	for n.Sign() > 0 {
		q.QuoRem(&n, baseTo, r)
		out = append(out, glyphs[int(r.Int64())])
		n.Set(q)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return vm.Str(string(out))
}

// strParam приводит аргумент встроенной функции к string (Z_PARAM_STR).
func strParam(ctx *vm.Context, i int, fn, param string) (string, bool) {
	a := derefSlot(arg(ctx, i))
	switch a.Type() {
	case vm.StringType:
		return a.StringVal(), true
	case vm.IntType, vm.FloatType, vm.BoolType:
		return a.ToString(), true
	case vm.NullType:
		ctx.VM().Deprecated(fn + "(): Passing null to parameter #" + strconv.Itoa(i+1) + " ($" + param + ") of type string is deprecated")
		return "", true
	case vm.ObjectType:
		if ctx.VM().IsStringable(a) {
			return ctx.VM().Cast(a, vm.CastString).StringVal(), true
		}
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		vm.ArgTypeError(fn, i+1, param, "string", phpTypeName(ctx, i), "", 0)))
	return "", false
}

// baseToZval — _php_math_basetozval: разбор строки в базе base с подсчётом
// недопустимых символов (Deprecated) и переходом в float при переполнении.
func baseToZval(ctx *vm.Context, str string, base int64) vm.Value {
	// Пробелы по краям отбрасываются.
	s := 0
	e := len(str)
	for s < e && (str[s] == ' ' || str[s] == '\t' || str[s] == '\n' || str[s] == '\r' || str[s] == '\v' || str[s] == '\f') {
		s++
	}
	for e > s && (str[e-1] == ' ' || str[e-1] == '\t' || str[e-1] == '\n' || str[e-1] == '\r' || str[e-1] == '\v' || str[e-1] == '\f') {
		e--
	}
	body := str[s:e]
	if len(body) >= 2 {
		if base == 16 && body[0] == '0' && (body[1] == 'x' || body[1] == 'X') {
			body = body[2:]
		}
		if base == 8 && body[0] == '0' && (body[1] == 'o' || body[1] == 'O') {
			body = body[2:]
		}
		if base == 2 && body[0] == '0' && (body[1] == 'b' || body[1] == 'B') {
			body = body[2:]
		}
	}
	cutoff := int64(math.MaxInt64) / base
	cutlim := int64(math.MaxInt64) % base
	var num int64
	var fnum float64
	mode := 0
	invalid := 0
	for i := 0; i < len(body); i++ {
		c := body[i]
		var d int64
		switch {
		case c >= '0' && c <= '9':
			d = int64(c - '0')
		case c >= 'A' && c <= 'Z':
			d = int64(c-'A') + 10
		case c >= 'a' && c <= 'z':
			d = int64(c-'a') + 10
		default:
			invalid++
			continue
		}
		if d >= base {
			invalid++
			continue
		}
		if mode == 0 {
			if num < cutoff || (num == cutoff && d <= cutlim) {
				num = num*base + d
				continue
			}
			fnum = float64(num)
			mode = 1
		}
		fnum = fnum*float64(base) + float64(d)
	}
	if invalid > 0 {
		ctx.VM().Deprecated("Invalid characters passed for attempted conversion, these have been ignored")
	}
	if mode == 1 {
		return vm.Float(fnum)
	}
	return vm.Int(num)
}

// logFn — log(num[, base]) с проверками base (RFC math.c).
func logFn(ctx *vm.Context) vm.Value {
	num, ok := numArg(ctx, 0, "log", "num")
	if !ok {
		return *new(vm.Value)
	}
	if len(ctx.Args()) < 2 {
		return vm.Float(math.Log(num.ToFloat()))
	}
	base, ok := numArg(ctx, 1, "log", "base")
	if !ok {
		return *new(vm.Value)
	}
	b := base.ToFloat()
	n := num.ToFloat()
	if b == 2.0 {
		return vm.Float(math.Log2(n))
	}
	if b == 10.0 {
		return vm.Float(math.Log10(n))
	}
	if b == 1.0 {
		return vm.Float(math.NaN())
	}
	if b <= 0.0 {
		ctx.Throw(vm.NewThrowable("ValueError", "log(): Argument #2 ($base) must be greater than 0"))
		return *new(vm.Value)
	}
	return vm.Float(math.Log(n) / math.Log(b))
}

func isNumericFn(ctx *vm.Context) vm.Value {
	if n := len(ctx.Args()); n != 1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fmt.Sprintf(
			"is_numeric() expects exactly 1 argument, %d given", n)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	s := arg0(ctx)
	if s.IsNumeric() {
		return vm.Bool(true)
	}
	if s.Type() == vm.StringType {
		_, err := strconv.ParseFloat(strings.TrimSpace(s.StringVal()), 64)
		return vm.Bool(err == nil)
	}
	return vm.Bool(false)
}

func finiteF(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }

func isNumericString(s string) bool {
	_, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return err == nil
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}
