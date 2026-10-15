package php

import (
	"errors"
	"fmt"
	"math"
	"phpvm/internal/vm"
	"sort"
	"strconv"
	"strings"
)

// Файл содержит встроенные функции для работы с массивами.

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// --- массивы ---

func arrayKeysFn(ctx *vm.Context) vm.Value {
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_keys", 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	hasSearch := len(ctx.Args()) >= 2
	needle := arg(ctx, 1)
	strict := len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool()
	arr := a.AsArray()
	if !hasSearch && arr.Packed() {
		// packed: ключи ровно 0..n-1 — типизированный быстрый путь.
		n := arr.Len()
		out := vm.NewArray()
		out.ReserveInts(n)
		for i := 0; i < n; i++ {
			if err := out.AppendInt(int64(i)); err != nil {
				ctx.Throw(vm.NewThrowable("Error", err.Error()))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		}
		return vm.MakeArray(out)
	}
	out := vm.NewArray()
	for k, v := range a.AsArray().All() {
		if hasSearch && !slotMatch(needle, v, strict) {
			continue
		}
		if k.IsStr() {
			out.Append(vm.MakeString(k.StrVal()))
		} else {
			out.Append(vm.MakeInt(k.IntVal()))
		}
	}
	return vm.MakeArray(out)
}

func arrayValuesFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	if a := arg0(ctx); a.Type() == vm.ArrayType {
		arr := a.AsArray()
		if ints := arr.Ints(); ints != nil {
			out.ReserveInts(len(ints))
			for _, x := range ints {
				_ = out.AppendInt(x)
			}
			return vm.MakeArray(out)
		}
		for _, v := range arr.All() {
			out.Append(derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

func arraySumFn(ctx *vm.Context) vm.Value {
	return arrayBinop(ctx, "Addition", 0, "array_sum")
}

func arrayProductFn(ctx *vm.Context) vm.Value {
	return arrayBinop(ctx, "Multiplication", 1, "array_product")
}

// arrayBinop повторяет php_array_binop: начальное целое, поэлементная операция
// с повышением int->float при переполнении/вещественном операнде; для
// неподдерживаемых типов — предупреждение и BC-поведение (resource -> id,
// нечисловая строка -> 0).
func arrayBinop(ctx *vm.Context, op string, initial int64, fname string) vm.Value {
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError(fname, 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	arr := a.AsArray()
	if arr.Len() == 0 {
		return vm.Int(initial)
	}
	mul := fname == "array_product"
	// Типизированные packed-массивы: поэлементная арифметика без матерализации
	// Values() и без switch на каждый элемент.
	if ints := arr.Ints(); ints != nil {
		ri, rf, isFloat := binopInts(ints, initial, mul)
		if isFloat {
			return vm.Float(rf)
		}
		return vm.Int(ri)
	}
	if floats := arr.Floats(); floats != nil {
		rf := float64(initial)
		for _, f := range floats {
			if mul {
				rf *= f
			} else {
				rf += f
			}
		}
		return vm.Float(rf)
	}
	isFloat := false
	ri := initial
	rf := float64(initial)
	warnUnsupported := func(s vm.Slot) {
		name := ""
		if s.Type() == vm.ObjectType {
			if o := ctx.VMInstance().SlotObject(s); o != nil {
				name = vm.ClassDisplayName(o.ClassName())
			}
		}
		if name == "" {
			name = binopTypeName(s)
		}
		ctx.VM().Warn(fname + "(): " + op + " is not supported on type " + name)
	}
	applyInt := func(x int64) {
		if isFloat {
			if mul {
				rf *= float64(x)
			} else {
				rf += float64(x)
			}
			return
		}
		if mul {
			if ri == 0 || x == 0 {
				ri = 0
				return
			}
			r := ri * x
			if r/x != ri {
				isFloat = true
				rf = float64(ri) * float64(x)
				return
			}
			ri = r
		} else {
			r := ri + x
			if (x > 0 && r < ri) || (x < 0 && r > ri) {
				isFloat = true
				rf = float64(ri) + float64(x)
				return
			}
			ri = r
		}
	}
	applyFloat := func(f float64) {
		if !isFloat {
			isFloat = true
			rf = float64(ri)
		}
		if mul {
			rf *= f
		} else {
			rf += f
		}
	}
	for _, v := range arr.Values() {
		v = derefSlot(v)
		switch v.Type() {
		case vm.IntType:
			applyInt(v.ToInt())
		case vm.FloatType:
			applyFloat(v.ToFloat())
		case vm.BoolType:
			applyInt(v.ToInt())
		case vm.NullType:
			applyInt(0)
		case vm.StringType:
			lv, dv, kind := numericStringKind(v.StringVal())
			if kind == 1 {
				applyInt(lv)
			} else if kind == 2 {
				applyFloat(dv)
			} else {
				applyInt(0)
				warnUnsupported(v)
			}
		case vm.ResourceType:
			if r := vm.AsResource(v); r != nil {
				applyInt(int64(r.ID))
			}
			warnUnsupported(v)
		case vm.ObjectType:
			warnUnsupported(v)
		default:
			warnUnsupported(v)
		}
	}
	if isFloat {
		return vm.Float(rf)
	}
	return vm.Int(ri)
}

// binopInts повторяет поэлементную int-арифметику array_sum/array_product
// (с повышением int->float при переполнении) для типизированного []int64.
func binopInts(ints []int64, initial int64, mul bool) (int64, float64, bool) {
	ri := initial
	rf := float64(initial)
	isFloat := false
	if mul {
		for _, x := range ints {
			if isFloat {
				rf *= float64(x)
				continue
			}
			if ri == 0 || x == 0 {
				ri = 0
				continue
			}
			r := ri * x
			if r/x != ri {
				isFloat = true
				rf = float64(ri) * float64(x)
				continue
			}
			ri = r
		}
		return ri, rf, isFloat
	}
	for _, x := range ints {
		if isFloat {
			rf += float64(x)
			continue
		}
		r := ri + x
		if (x > 0 && r < ri) || (x < 0 && r > ri) {
			isFloat = true
			rf = float64(ri) + float64(x)
			continue
		}
		ri = r
	}
	return ri, rf, isFloat
}

// binopTypeName возвращает имя типа для предупреждений array_sum/array_product.
func binopTypeName(s vm.Slot) string {
	switch s.Type() {
	case vm.ObjectType:
		return "object"
	case vm.ArrayType:
		return "array"
	case vm.ResourceType:
		return "resource"
	case vm.StringType:
		return "string"
	}
	return s.Type().String()
}

func arrayReverseFn(ctx *vm.Context) vm.Value {
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_reverse", 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	preserve := len(ctx.Args()) >= 2 && arg(ctx, 1).ToBool()
	type kv struct {
		k vm.ArrayKey
		v vm.Slot
	}
	var vals []kv
	for k, v := range a.AsArray().All() {
		vals = append(vals, kv{k, derefLoneRef(ctx, v)})
	}
	out := vm.NewArray()
	for i := len(vals) - 1; i >= 0; i-- {
		if preserve || vals[i].k.IsStr() {
			out.Set(vals[i].k, vals[i].v)
		} else {
			out.Append(vals[i].v)
		}
	}
	return vm.MakeArray(out)
}

func arrayFlipFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_flip", 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	for k, v := range a.AsArray().All() {
		var key vm.Slot
		if k.IsStr() {
			key = vm.MakeString(k.StrVal())
		} else {
			key = vm.MakeInt(k.IntVal())
		}
		switch v.Type() {
		case vm.IntType:
			out.Set(vm.MakeIntKey(v.ToInt()), key)
		case vm.StringType:
			out.Set(symKeyOf(ctx, v), key)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		default:
			ctx.VM().Warn("array_flip(): Can only flip string and integer values, entry skipped")
		}
	}
	return vm.MakeArray(out)
}

func arrayKeyExistsFn(ctx *vm.Context) vm.Value {
	// array_key_exists(key, array)
	key := arg(ctx, 0)
	arr := arg(ctx, 1)
	if arr.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_key_exists", 2, "array", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	switch key.Type() {
	case vm.ArrayType, vm.ObjectType, vm.ClosureType:
		ctx.Throw(vm.NewThrowable("TypeError", "Cannot access offset of type "+phpTypeName(ctx, 0)+" on array"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	case vm.ResourceType:
		if r := vm.AsResource(key); r != nil {
			ctx.VM().Warn("Resource ID#" + strconv.FormatInt(r.ID, 10) + " used as offset, casting to integer (" + strconv.FormatInt(r.ID, 10) + ")")
			key = vm.MakeInt(r.ID)
		}
	case vm.FloatType:
		f := key.Float()
		if f != math.Trunc(f) {
			ctx.VM().Deprecated("Implicit conversion from float " + phpFloat(f) + " to int loses precision")
		}
		key = vm.MakeInt(key.ToInt())
	case vm.BoolType:
		key = vm.MakeInt(key.ToInt())
	}
	_, ok := arr.AsArray().Get(keyOf(key))
	return vm.Bool(ok)
}

func arraySearchFn(ctx *vm.Context) vm.Value {
	needle := arg(ctx, 0)
	arr := arg(ctx, 1)
	if arr.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_search", 2, "haystack", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	strict := len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool()
	for k, v := range arr.AsArray().All() {
		if slotMatch(needle, v, strict) {
			if k.IsStr() {
				return vm.Str(k.StrVal())
			}
			return vm.Int(k.IntVal())
		}
	}
	return vm.Bool(false)
}

func inArrayFn(ctx *vm.Context) vm.Value {
	needle := arg(ctx, 0)
	arr := arg(ctx, 1)
	if arr.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("in_array", 2, "haystack", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	strict := len(ctx.Args()) >= 3 && arg(ctx, 2).ToBool()
	for _, v := range arr.AsArray().All() {
		if slotMatch(needle, v, strict) {
			return vm.Bool(true)
		}
	}
	return vm.Bool(false)
}

// slotMatch — равенство значения needle элементу haystack: строгое (===) или
// нестрогое (==) по правилам PHP.
func slotMatch(needle, v vm.Slot, strict bool) bool {
	if strict {
		return vm.Identical(needle, v).ToBool()
	}
	return vm.Equal(needle, v).ToBool()
}

func arrayFillFn(ctx *vm.Context) vm.Value {
	start := arg(ctx, 0).ToInt()
	num := arg(ctx, 1).ToInt()
	val := arg(ctx, 2)
	if num < 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "array_fill(): Argument #2 ($count) must be greater than or equal to 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if num > 1073741823 {
		ctx.Throw(vm.NewThrowable("ValueError", "array_fill(): Argument #2 ($count) must not exceed the maximum allowed array size"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if num > 0 && start > math.MaxInt64-(num-1) {
		ctx.Throw(vm.NewThrowable("Error", "Cannot add element to the array as the next element is already occupied"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	out := vm.NewArray()
	for i := int64(0); i < num; i++ {
		out.SetInt(start+i, val)
	}
	return vm.MakeArray(out)
}

// rangeNumStr форматирует значение start/end/step для сообщения об ошибке.
func rangeNumStr(s vm.Slot) string {
	switch s.Type() {
	case vm.IntType:
		return strconv.FormatInt(s.ToInt(), 10)
	case vm.FloatType:
		return phpExportFloat(s.ToFloat())
	default:
		return s.ToString()
	}
}

// numericStringKind разбирает числовую строку: kind=0 — не число, 1 — целое
// (точное, помещается в int64), 2 — float.
func numericStringKind(s string) (lval int64, dval float64, kind int) {
	if !isNumericString(s) {
		return 0, 0, 0
	}
	t := strings.TrimSpace(s)
	if !strings.ContainsAny(t, ".eE") {
		if i, err := strconv.ParseInt(t, 10, 64); err == nil {
			return i, float64(i), 1
		}
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, 0, 0
		}
		return 0, f, 2
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		return 0, 0, 0
	}
	return 0, f, 2
}

func rangeFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()

	// --- обработка $step (Z_PARAM_NUMBER) ---
	haveStep := len(ctx.Args()) >= 3
	step := int64(1)
	stepDouble := 1.0
	isStepDouble := false
	isStepNegative := false
	stepSlot := vm.MakeInt(1)
	if haveStep {
		stepSlot = arg(ctx, 2)
		switch stepSlot.Type() {
		case vm.FloatType:
			stepDouble = stepSlot.Float()
		case vm.StringType:
			lv, dv, kind := numericStringKind(stepSlot.StringVal())
			if kind == 2 {
				stepDouble = dv
			} else {
				step = lv
				if step < 0 {
					isStepNegative = true
					step = -step
				}
				stepDouble = float64(step)
				goto stepDone
			}
		default:
			step = stepSlot.ToInt()
			if step < 0 {
				isStepNegative = true
				step = -step
			}
			stepDouble = float64(step)
			goto stepDone
		}
		if math.IsInf(stepDouble, 0) {
			ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be a finite number, INF provided"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if math.IsNaN(stepDouble) {
			ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be a finite number, NAN provided"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if stepDouble < 0 {
			isStepNegative = true
			stepDouble = -stepDouble
		}
		step = vm.MakeFloat(stepDouble).ToInt()
		if float64(step) != stepDouble {
			isStepDouble = true
		}
	stepDone:
		if stepDouble == 0 {
			ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) cannot be 0"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}

	// --- обработка границ (Z_PARAM_NUMBER_OR_STR) ---
	// kind: 1 long, 2 double, 3 string, 4 single-byte numeric string
	var rangeL [2]int64
	var rangeD [2]float64
	process := func(s vm.Slot, argNum int) int {
		name := "start"
		if argNum == 2 {
			name = "end"
		}
		switch s.Type() {
		case vm.IntType:
			rangeL[argNum-1] = s.Int()
			rangeD[argNum-1] = float64(rangeL[argNum-1])
			return 1
		case vm.FloatType:
			rangeD[argNum-1] = s.Float()
			if math.IsInf(rangeD[argNum-1], 0) {
				ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #"+strconv.Itoa(argNum)+" ($"+name+") must be a finite number, INF provided"))
				if ctx.HasThrown() {
					return *new(int)
				}
			}
			if math.IsNaN(rangeD[argNum-1]) {
				ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #"+strconv.Itoa(argNum)+" ($"+name+") must be a finite number, NAN provided"))
				if ctx.HasThrown() {
					return *new(int)
				}
			}
			return 2
		case vm.StringType:
			str := s.StringVal()
			if len(str) == 0 {
				ctx.VM().Warn("range(): Argument #" + strconv.Itoa(argNum) + " ($" + name + ") must not be empty, casted to 0")
				rangeL[argNum-1], rangeD[argNum-1] = 0, 0
				return 1
			}
			lv, dv, kind := numericStringKind(str)
			if kind == 1 {
				rangeL[argNum-1] = lv
				rangeD[argNum-1] = float64(lv)
				if len(str) == 1 {
					return 4
				}
				return 1
			}
			if kind == 2 {
				rangeD[argNum-1] = dv
				if math.IsInf(dv, 0) {
					ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #"+strconv.Itoa(argNum)+" ($"+name+") must be a finite number, INF provided"))
					if ctx.HasThrown() {
						return *new(int)
					}
				}
				if math.IsNaN(dv) {
					ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #"+strconv.Itoa(argNum)+" ($"+name+") must be a finite number, NAN provided"))
					if ctx.HasThrown() {
						return *new(int)
					}
				}
				return 2
			}
			if len(str) != 1 {
				ctx.VM().Warn("range(): Argument #" + strconv.Itoa(argNum) + " ($" + name + ") must be a single byte, subsequent bytes are ignored")
			}
			rangeL[argNum-1], rangeD[argNum-1] = 0, 0
			return 3
		case vm.NullType:
			ctx.VM().Deprecated("range(): Passing null to parameter #" + strconv.Itoa(argNum) + " ($" + name + ") of type string|int|float is deprecated")
			rangeL[argNum-1], rangeD[argNum-1] = 0, 0
			return 1
		default:
			rangeL[argNum-1] = s.ToInt()
			rangeD[argNum-1] = float64(rangeL[argNum-1])
			return 1
		}
	}
	startType := process(arg(ctx, 0), 1)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	endType := process(arg(ctx, 1), 2)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}

	if startType >= 3 || endType >= 3 {
		doNumeric := false
		if startType < 3 || endType < 3 {
			if startType < 3 {
				if endType != 4 {
					ctx.VM().Warn("range(): Argument #1 ($start) must be a single byte string if argument #2 ($end) is a single byte string, argument #2 ($end) converted to 0")
				}
				endType = 1
			} else if endType < 3 {
				if startType != 4 {
					ctx.VM().Warn("range(): Argument #2 ($end) must be a single byte string if argument #1 ($start) is a single byte string, argument #1 ($start) converted to 0")
				}
				startType = 1
			}
			doNumeric = true
		} else if isStepDouble {
			if startType == 3 || endType == 3 {
				ctx.VM().Warn("range(): Argument #3 ($step) must be of type int when generating an array of characters, inputs converted to 0")
			}
			startType, endType = 1, 1
			doNumeric = true
		}
		if !doNumeric {
			slow := arg(ctx, 0).StringVal()
			shigh := arg(ctx, 1).StringVal()
			low := int(slow[0])
			high := int(shigh[0])
			if low > high {
				if low-high < int(step) {
					ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
				for c := low; c >= high; c -= int(step) {
					out.Append(vm.MakeString(string(byte(c))))
					if c-int(step) < 0 {
						break
					}
				}
			} else if high > low {
				if isStepNegative {
					ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be greater than 0 for increasing ranges"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
				if high-low < int(step) {
					ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
				for c := low; c <= high; c += int(step) {
					out.Append(vm.MakeString(string(byte(c))))
					if c+int(step) > 255 {
						break
					}
				}
			} else {
				out.Append(vm.MakeString(string(byte(low))))
			}
			return vm.MakeArray(out)
		}
	}

	// --- числовой диапазон ---
	if startType == 2 || endType == 2 || isStepDouble {
		sf, ef := rangeD[0], rangeD[1]
		if sf > ef {
			if sf-ef < stepDouble {
				ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			calc := (sf-ef)/stepDouble + 1
			if calc >= float64(maxRangeSize) {
				ctx.Throw(vm.NewThrowable("ValueError", rangeSizeMsg(vm.MakeFloat(ef), vm.MakeFloat(sf), vm.MakeFloat(stepDouble), true)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			size := int(math.Floor(calc + 0.5))
			for i := 0; i < size; i++ {
				e := sf - float64(i)*stepDouble
				if e < ef {
					break
				}
				out.Append(vm.MakeFloat(e))
			}
		} else if ef > sf {
			if isStepNegative {
				ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be greater than 0 for increasing ranges"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			if ef-sf < stepDouble {
				ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)"))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			calc := (ef-sf)/stepDouble + 1
			if calc >= float64(maxRangeSize) {
				ctx.Throw(vm.NewThrowable("ValueError", rangeSizeMsg(vm.MakeFloat(sf), vm.MakeFloat(ef), vm.MakeFloat(stepDouble), true)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			size := int(math.Floor(calc + 0.5))
			for i := 0; i < size; i++ {
				e := sf + float64(i)*stepDouble
				if e > ef {
					break
				}
				out.Append(vm.MakeFloat(e))
			}
		} else {
			out.Append(vm.MakeFloat(sf))
		}
		return vm.MakeArray(out)
	}

	sl, el := rangeL[0], rangeL[1]
	if sl > el {
		if uint64(sl)-uint64(el) < uint64(step) {
			ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		calc := (uint64(sl) - uint64(el)) / uint64(step)
		if calc >= uint64(maxRangeSize)-1 {
			ctx.Throw(vm.NewThrowable("ValueError", rangeSizeMsg(vm.MakeInt(el), vm.MakeInt(sl), vm.MakeInt(step), false)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		size := int(calc) + 1
		for i := 0; i < size; i++ {
			out.Append(vm.MakeInt(sl - int64(i)*step))
		}
	} else if el > sl {
		if isStepNegative {
			ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be greater than 0 for increasing ranges"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		if uint64(el)-uint64(sl) < uint64(step) {
			ctx.Throw(vm.NewThrowable("ValueError", "range(): Argument #3 ($step) must be less than the range spanned by argument #1 ($start) and argument #2 ($end)"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		calc := (uint64(el) - uint64(sl)) / uint64(step)
		if calc >= uint64(maxRangeSize)-1 {
			ctx.Throw(vm.NewThrowable("ValueError", rangeSizeMsg(vm.MakeInt(sl), vm.MakeInt(el), vm.MakeInt(step), false)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		size := int(calc) + 1
		for i := 0; i < size; i++ {
			out.Append(vm.MakeInt(sl + int64(i)*step))
		}
	} else {
		out.Append(vm.MakeInt(sl))
	}
	return vm.MakeArray(out)
}

func rangeSizeMsg(a, b, step vm.Slot, asFloat bool) string {
	num := func(s vm.Slot) string {
		if asFloat {
			return phpExportFloat(s.ToFloat())
		}
		return rangeNumStr(s)
	}
	return "The supplied range exceeds the maximum array size: start=" + num(a) +
		" end=" + num(b) + " step=" + num(step)
}

// derefLoneRef разыменовывает ссылку с refcount==1 (Zend unwrap reference при
// копировании значения); ссылки с несколькими владельцами сохраняются.
func derefLoneRef(ctx *vm.Context, v vm.Slot) vm.Slot {
	return derefLoneRefVM(ctx.VM(), v)
}

func derefLoneRefVM(m *vm.VM, v vm.Slot) vm.Slot {
	if v.IsRef() {
		if r := v.Ref(); r != nil && m.CountRefHolders(r) <= 1 {
			return vm.Deref(v)
		}
	}
	return v
}

func arrayMergeFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	total := 0
	for i, a := range args {
		if a.Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("array_merge", i+1, "", "array", phpTypeName(ctx, i), "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		total += a.AsArray().Len()
		if total > 1073741823 {
			ctx.Throw(vm.NewThrowable("Error", "The total number of elements must be lower than 1073741824"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	out := vm.NewArray()
	for _, a := range args {
		for k, v := range a.AsArray().All() {
			v = derefLoneRef(ctx, v)
			if v.Type() == vm.ArrayType {
				v.AsArray().MarkShared()
			}
			if k.IsStr() {
				out.SetStr(k.StrVal(), v)
			} else {
				out.Append(v)
			}
		}
	}
	return vm.MakeArray(out)
}

// mergeRecInto рекурсивно сливает arr в out по правилам array_merge_recursive:
// строковые ключи объединяются (при совпадении — в массив), числовые добавляются.
// path — массивы в текущей ветке рекурсии (детект циклов, как в PHP):
// errRecursion — цикл, errNextOccupied — переполнение авто-ключа.
func mergeRecInto(m *vm.VM, out *vm.Array, arr *vm.Array, path map[*vm.Array]bool) error {
	if path[arr] {
		return errRecursion
	}
	path[arr] = true
	defer delete(path, arr)
	for k, v := range arr.All() {
		v = derefLoneRefVM(m, v)
		if !k.IsStr() {
			if _, err := out.Append(v); err != nil {
				return err
			}
			continue
		}
		existing, ok := out.Get(k)
		if !ok {
			out.SetStr(k.StrVal(), v)
			continue
		}
		merged := vm.NewArray()
		if existing.Type() == vm.ArrayType {
			// Копируем существующие элементы, СОХРАНЯЯ ключи (в т.ч. числовые)
			// и nextFree — как Zend при рекурсивном слиянии вложенных массивов.
			copyEntriesPreserve(merged, existing.AsArray())
		} else if !isMergeSkipType(existing.Type()) {
			if _, err := merged.Append(vm.Deref(existing)); err != nil {
				return err
			}
		}
		if v.Type() == vm.ArrayType {
			if err := mergeRecInto(m, merged, v.AsArray(), path); err != nil {
				return err
			}
		} else if !isMergeSkipType(v.Type()) {
			if _, err := merged.Append(vm.Deref(v)); err != nil {
				return err
			}
		}
		out.SetStr(k.StrVal(), vm.MakeArray(merged))
	}
	return nil
}

// copyEntriesPreserve копирует элементы src в dst, сохраняя все ключи (в т.ч.
// числовые) и, соответственно, nextFree целевого массива.
func copyEntriesPreserve(dst, src *vm.Array) {
	for k, v := range src.All() {
		if k.IsStr() {
			dst.SetStr(k.StrVal(), v)
		} else {
			dst.Set(k, v)
		}
	}
	// nextFree «липкий» (не уменьшается при unset) — переносим его явно.
	if nf := src.NextFree(); nf > dst.NextFree() {
		dst.SetNextFree(nf)
	}
}

// isMergeSkipType сообщает, что значение-операнд рекурсивного слияния не
// участвует в результате: объекты/замыкания Zend не добавляет при совпадении
// строковых ключей (GH-16053: assertion failure / объект отбрасывается).
func isMergeSkipType(t vm.Type) bool {
	return t == vm.ObjectType || t == vm.ClosureType
}

// errRecursion — детект цикла в array_merge_recursive.
var errRecursion = errors.New("Recursion detected")

func arrayMergeRecursiveFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	total := 0
	for i, a := range args {
		if a.Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("array_merge_recursive", i+1, "", "array", phpTypeName(ctx, i), "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		total += a.AsArray().Len()
		if total > 1073741823 {
			ctx.Throw(vm.NewThrowable("Error", "The total number of elements must be lower than 1073741824"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	out := vm.NewArray()
	for _, a := range args {
		if err := mergeRecInto(ctx.VM(), out, a.AsArray(), map[*vm.Array]bool{}); err != nil {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
	}
	return vm.MakeArray(out)
}

// shuffleFn — shuffle(): перемешивает массив, переиндексируя с 0.
func shuffleFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "shuffle() expects exactly 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	a, set := refArrayArg(ctx, 0, "shuffle")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	vals := a.Values()
	phpShuffle(len(vals), func(i, j int) { vals[i], vals[j] = vals[j], vals[i] })
	out := vm.NewArray()
	for _, v := range vals {
		out.Append(v)
	}
	set(out)
	return vm.Bool(true)
}

// phpShuffle повторяет алгоритм php_array_data_shuffle: n_left идёт от n-1
// до 1, обмен элементов n_left и rnd_idx.
func phpShuffle(n int, swap func(i, j int)) {
	if n <= 1 {
		return
	}
	for nLeft := n - 1; nLeft > 0; nLeft-- {
		rnd := int(mtRangeLocked(0, int64(nLeft)))
		if rnd != nLeft {
			swap(nLeft, rnd)
		}
	}
}

// arrayRandFn — array_rand(): возвращает случайный ключ или массив ключей.
func arrayRandFn(ctx *vm.Context) vm.Value {
	if len(ctx.Args()) < 1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_rand() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	a := requireArrayArg(ctx, 0, "array_rand")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	keys := make([]vm.ArrayKey, 0, a.Len())
	for k := range a.All() {
		keys = append(keys, k)
	}
	numAvail := len(keys)
	if numAvail == 0 {
		ctx.Throw(vm.NewThrowable("ValueError", "array_rand(): Argument #1 ($array) cannot be empty"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	num := int64(1)
	if len(ctx.Args()) >= 2 {
		num = arg(ctx, 1).ToInt()
	}
	if num == 1 {
		idx := int(mtRangeLocked(0, int64(numAvail-1)))
		return arrayKeySlot(keys[idx])
	}
	if num <= 0 || num > int64(numAvail) {
		ctx.Throw(vm.NewThrowable("ValueError",
			"array_rand(): Argument #2 ($num) must be between 1 and the number of elements in argument #1 ($array)"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	negative := false
	if num > int64(numAvail>>1) {
		negative = true
		num = int64(numAvail) - num
	}
	selected := make([]bool, numAvail)
	for i := num; i > 0; {
		rv := int(mtRangeLocked(0, int64(numAvail-1)))
		if !selected[rv] {
			selected[rv] = true
			i--
		}
	}
	out := vm.NewArray()
	for idx, k := range keys {
		if selected[idx] != negative {
			out.Append(arrayKeySlot(k))
		}
	}
	return vm.MakeArray(out)
}

func arrayCombineFn(ctx *vm.Context) vm.Value {
	if arg(ctx, 0).Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("array_combine", 1, "keys", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if arg(ctx, 1).Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("array_combine", 2, "values", "array", phpTypeName(ctx, 1), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	kv := collect(arg(ctx, 0).AsArray())
	vv := collect(arg(ctx, 1).AsArray())
	if len(kv) != len(vv) {
		ctx.Throw(vm.NewThrowable("ValueError",
			"array_combine(): Argument #1 ($keys) and argument #2 ($values) must have the same number of elements"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	out := vm.NewArray()
	for i := range kv {
		out.Set(symKeyOf(ctx, kv[i]), vv[i])
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return vm.MakeArray(out)
}

func arraySliceFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	a := arg(ctx, 0)
	if a.Type() != vm.ArrayType {
		return vm.MakeArray(out)
	}
	arr := a.AsArray()
	n := arr.Len()
	preserve := len(ctx.Args()) >= 4 && arg(ctx, 3).ToBool()
	off := int(arg(ctx, 1).ToInt())
	if off < 0 {
		off = n + off
	}
	if off < 0 {
		off = 0
	}
	if off > n {
		return vm.MakeArray(out)
	}
	end := n
	if len(ctx.Args()) >= 3 {
		lslot := arg(ctx, 2)
		if lslot.Type() == vm.NullType {
			end = n
		} else {
			switch lslot.Type() {
			case vm.IntType, vm.BoolType, vm.FloatType:
			case vm.StringType:
				if !isNumericString(lslot.StringVal()) {
					ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_slice", 3, "length", "?int", "string", "", 0)))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
				}
			default:
				ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_slice", 3, "length", "?int", phpTypeName(ctx, 2), "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			l := int(lslot.ToInt())
			if l >= 0 {
				if l > n-off {
					end = n
				} else {
					end = off + l
				}
			} else {
				end = n + l
				if end < 0 {
					end = 0
				}
			}
		}
	}
	if end > n {
		end = n
	}
	if end < off {
		return vm.MakeArray(out)
	}
	// Собираем только запрошенный диапазон: хвост массива не копируется.
	type kv struct {
		k vm.ArrayKey
		v vm.Slot
	}
	vals := make([]kv, 0, end-off)
	idx := 0
	for k, v := range arr.All() {
		if idx >= end {
			break
		}
		if idx >= off {
			vals = append(vals, kv{k, v})
		}
		idx++
	}
	for _, e := range vals {
		if preserve || e.k.IsStr() {
			out.Set(e.k, e.v)
		} else {
			out.Append(e.v)
		}
	}
	return vm.MakeArray(out)
}

func arrayUniqueFn(ctx *vm.Context) vm.Value {
	a := arg(ctx, 0)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_unique", 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	flags := int64(2) // SORT_STRING по умолчанию
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	out := vm.NewArray()
	seen := map[string]bool{}
	for k, v := range a.AsArray().All() {
		var key string
		switch flags &^ int64(8) {
		case 1: // SORT_NUMERIC
			key = "#n" + strconv.FormatFloat(v.ToFloat(), 'g', -1, 64)
		case 2, 6: // SORT_STRING / SORT_NATURAL
			if v.Type() == vm.ObjectType {
				if _, ok := objectToStringVia(ctx, v); !ok {
					name := ""
					if o := ctx.VMInstance().SlotObject(v); o != nil {
						name = vm.ClassDisplayName(o.ClassName())
					}
					ctx.Throw(vm.NewThrowable("Error", "Object of class "+name+" could not be converted to string"))
					if ctx.HasThrown() {
						return *new(vm.Value)
					}
					return vm.MakeNull()
				}
			}
			key = "#s" + sortStringOf(ctx, v)
		default: // SORT_REGULAR
			key = "#r" + v.ToString()
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		out.Set(k, v)
	}
	return vm.MakeArray(out)
}

func arrayPadFn(ctx *vm.Context) vm.Value {
	a := arg(ctx, 0)
	size64 := arg(ctx, 1).ToInt()
	if size64 > 1073741823 || size64 < -1073741823 {
		ctx.Throw(vm.NewThrowable("ValueError", "array_pad(): Argument #2 ($length) must not exceed the maximum allowed array size"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	size := int(size64)
	pad := arg(ctx, 2)
	out := vm.NewArray()
	n := 0
	if a.Type() == vm.ArrayType {
		n = a.AsArray().Len()
	}
	// array_pad сохраняет строковые ключи и перенумеровывает числовые (как
	// Append), причём при паддинге слева счётчик учитывает добавленные элементы.
	if n >= absInt(size) {
		// Паддинг не нужен — исходный массив возвращается без изменений
		// (числовые ключи сохраняются).
		return a
	}
	if size > 0 {
		if a.Type() == vm.ArrayType {
			copyPadEntries(out, a.AsArray())
		}
		for i := n; i < size; i++ {
			out.Append(pad)
		}
	} else {
		for i := n; i < -size; i++ {
			out.Append(pad)
		}
		if a.Type() == vm.ArrayType {
			copyPadEntries(out, a.AsArray())
		}
	}
	return vm.MakeArray(out)
}

// copyPadEntries копирует элементы массива в out: строковые ключи сохраняются,
// числовые получают следующую авто-нумерацию (семантика array_pad).
func copyPadEntries(out, src *vm.Array) {
	for k, v := range src.All() {
		if k.IsStr() {
			out.SetStr(k.StrVal(), v)
		} else {
			out.Append(v)
		}
	}
}

func collect(a *vm.Array) []vm.Slot {
	var out []vm.Slot
	for _, v := range a.All() {
		out = append(out, v)
	}
	return out
}

func keyOf(s vm.Slot) vm.ArrayKey {
	switch s.Type() {
	case vm.IntType:
		return vm.MakeIntKey(s.ToInt())
	case vm.BoolType:
		if s.ToBool() {
			return vm.MakeIntKey(1)
		}
		return vm.MakeIntKey(0)
	case vm.NullType:
		return vm.MakeStrKey("")
	case vm.FloatType:
		return vm.MakeIntKey(int64(s.ToFloat()))
	}
	str := s.ToString()
	if i, ok := canonicalKeyString(str); ok {
		return vm.MakeIntKey(i)
	}
	return vm.MakeStrKey(str)
}

// symKeyOf — приведение как в array_combine()/array_fill_keys(): int-значение
// остаётся индексом, прочие сперва приводятся к строке (с вызовом __toString
// у объектов), затем канонизируются (false -> "", 1.9 -> "1.9", 2.0 -> int 2).
func symKeyOf(ctx *vm.Context, s vm.Slot) vm.ArrayKey {
	if s.Type() == vm.IntType {
		return vm.MakeIntKey(s.ToInt())
	}
	str, err := ctx.VMInstance().ToStringValue(s)
	if err != nil {
		ctx.Throw(vm.NewThrowable("Error", err.Error()))
		if ctx.HasThrown() {
			return *new(vm.ArrayKey)
		}
		return vm.MakeStrKey("")
	}
	if i, ok := canonicalKeyString(str); ok {
		return vm.MakeIntKey(i)
	}
	return vm.MakeStrKey(str)
}

// canonicalKeyString повторяет правило PHP о приведении канонической
// десятичной строки-целого к int-ключу. "01", "+5", "1e2", " 10", "-0" — не
// канонические и остаются строковыми.
func canonicalKeyString(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	neg := false
	body := s
	if s[0] == '-' {
		neg = true
		body = s[1:]
	}
	if body == "" {
		return 0, false
	}
	if body[0] == '0' && (body != "0" || neg) {
		return 0, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] < '0' || body[i] > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// --- операции над массивами ---

func arrKeyString(k vm.ArrayKey) string {
	if k.IsStr() {
		return "s:" + k.StrVal()
	}
	return "i:" + strconv.FormatInt(k.IntVal(), 10)
}

func restArrays(ctx *vm.Context) []*vm.Array {
	var out []*vm.Array
	for i := 1; i < len(ctx.Args()); i++ {
		a := arg(ctx, i)
		if a.Type() == vm.ArrayType {
			out = append(out, a.AsArray())
		}
	}
	return out
}

// diffIntersectStringSets строит множество строковых представлений элементов
// каждого массива others (как Zend при построении таблицы поиска). Конверсия
// выполняется один раз на элемент — предупреждения «Array to string
// conversion» выдаются именно здесь, до обхода первого массива.
func diffIntersectStringSets(ctx *vm.Context, others []*vm.Array) []map[string]bool {
	sets := make([]map[string]bool, len(others))
	for i, o := range others {
		s := make(map[string]bool, o.Len())
		for _, ov := range o.Values() {
			s[sortStringOf(ctx, ov)] = true
		}
		sets[i] = s
	}
	return sets
}

func arrayDiffFn(ctx *vm.Context) vm.Value {
	requireAllArrays(ctx, "array_diff")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	first := arg0(ctx)
	sets := diffIntersectStringSets(ctx, restArrays(ctx))
	for k, v := range first.AsArray().All() {
		sv := sortStringOf(ctx, v)
		found := false
		for _, s := range sets {
			if s[sv] {
				found = true
				break
			}
		}
		if !found {
			out.Set(k, derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

func arrayIntersectFn(ctx *vm.Context) vm.Value {
	requireAllArrays(ctx, "array_intersect")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	first := arg0(ctx)
	sets := diffIntersectStringSets(ctx, restArrays(ctx))
	for k, v := range first.AsArray().All() {
		sv := sortStringOf(ctx, v)
		inAll := true
		for _, s := range sets {
			if !s[sv] {
				inAll = false
				break
			}
		}
		if inAll {
			out.Set(k, derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

func arrayDiffKeyFn(ctx *vm.Context) vm.Value {
	requireAllArrays(ctx, "array_diff_key")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	first := arg0(ctx)
	others := map[string]bool{}
	for _, o := range restArrays(ctx) {
		for k := range o.All() {
			others[arrKeyString(k)] = true
		}
	}
	for k, v := range first.AsArray().All() {
		if !others[arrKeyString(k)] {
			out.Set(k, derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

// requireAllArrays проверяет, что все аргументы — массивы (формат ошибок PHP).
func requireAllArrays(ctx *vm.Context, fn string) {
	for i := range ctx.Args() {
		if arg(ctx, i).Type() != vm.ArrayType {
			arrayArgError(ctx, i, fn)
			if ctx.HasThrown() {
				return
			}
		}
	}
}

func arrayIntersectKeyFn(ctx *vm.Context) vm.Value {
	requireAllArrays(ctx, "array_intersect_key")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	first := arg0(ctx)
	rest := restArrays(ctx)
	for k, v := range first.AsArray().All() {
		inAll := true
		for _, o := range rest {
			if _, ok := o.Get(k); !ok {
				inAll = false
				break
			}
		}
		if inAll {
			out.Set(k, derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

func arrayDiffAssocFn(ctx *vm.Context) vm.Value {
	requireAllArrays(ctx, "array_diff_assoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	first := arg0(ctx)
	others := restArrays(ctx)
	for k, v := range first.AsArray().All() {
		found := false
		for _, o := range others {
			if ov, ok := o.Get(k); ok && sortStringOf(ctx, ov) == sortStringOf(ctx, v) {
				found = true
				break
			}
		}
		if !found {
			out.Set(k, derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

func arrayIntersectAssocFn(ctx *vm.Context) vm.Value {
	requireAllArrays(ctx, "array_intersect_assoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	first := arg0(ctx)
	rest := restArrays(ctx)
	for k, v := range first.AsArray().All() {
		inAll := true
		for _, o := range rest {
			ov, ok := o.Get(k)
			if !ok || sortStringOf(ctx, ov) != sortStringOf(ctx, v) {
				inAll = false
				break
			}
		}
		if inAll {
			out.Set(k, v)
		}
	}
	return vm.MakeArray(out)
}

func arrayKeyFirstFn(ctx *vm.Context) vm.Value {
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		return vm.MakeNull()
	}
	for k := range a.AsArray().All() {
		if k.IsStr() {
			return vm.Str(k.StrVal())
		}
		return vm.Int(k.IntVal())
	}
	return vm.MakeNull()
}

func arrayKeyLastFn(ctx *vm.Context) vm.Value {
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		return vm.MakeNull()
	}
	var last vm.ArrayKey
	found := false
	for k := range a.AsArray().All() {
		last, found = k, true
	}
	if !found {
		return vm.MakeNull()
	}
	if last.IsStr() {
		return vm.Str(last.StrVal())
	}
	return vm.Int(last.IntVal())
}

func arrayIsListFn(ctx *vm.Context) vm.Value {
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("array_is_list", 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	i := int64(0)
	for k := range a.AsArray().All() {
		if k.IsStr() || k.IntVal() != i {
			return vm.Bool(false)
		}
		i++
	}
	return vm.Bool(true)
}

func arrayCountValuesFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	a := arg0(ctx)
	if a.Type() != vm.ArrayType {
		return vm.MakeArray(out)
	}
	counts := map[string]int64{}
	var keys []vm.ArrayKey
	for _, v := range a.AsArray().All() {
		if v.Type() != vm.IntType && v.Type() != vm.StringType {
			ctx.VM().Warn("array_count_values(): Can only count string and integer values, entry skipped")
			continue
		}
		k := keyOf(v)
		s := arrKeyString(k)
		if _, ok := counts[s]; !ok {
			keys = append(keys, k)
		}
		counts[s]++
	}
	for _, k := range keys {
		out.Set(k, vm.MakeInt(counts[arrKeyString(k)]))
	}
	return vm.MakeArray(out)
}

func arrayChunkFn(ctx *vm.Context) vm.Value {
	a := arg0(ctx)
	size := int(arg(ctx, 1).ToInt())
	preserve := false
	if len(ctx.Args()) >= 3 {
		preserve = arg(ctx, 2).ToBool()
	}
	out := vm.NewArray()
	if size < 1 {
		ctx.Throw(vm.NewThrowable("ValueError", "array_chunk(): Argument #2 ($length) must be greater than 0"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if a.Type() != vm.ArrayType {
		return vm.MakeArray(out)
	}
	chunk := vm.NewArray()
	i := 0
	for k, v := range a.AsArray().All() {
		if i > 0 && i%size == 0 {
			out.Append(vm.MakeArray(chunk))
			chunk = vm.NewArray()
		}
		if preserve {
			chunk.Set(k, v)
		} else {
			chunk.Append(v)
		}
		i++
	}
	if i > 0 {
		out.Append(vm.MakeArray(chunk))
	}
	return vm.MakeArray(out)
}

func arrayFillKeysFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	a := arg0(ctx)
	val := arg(ctx, 1)
	if a.Type() == vm.ArrayType {
		for _, v := range a.AsArray().All() {
			out.Set(symKeyOf(ctx, v), val)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	return vm.MakeArray(out)
}

// columnIndexKeyOf приводит значение index_key строки к ключу результата
// array_column: ресурс — warning + id, float с дробью — Deprecated, объект/
// массив — TypeError, как при доступе к элементу массива.
func columnIndexKeyOf(ctx *vm.Context, s vm.Slot) vm.ArrayKey {
	switch s.Type() {
	case vm.IntType:
		return vm.MakeIntKey(s.ToInt())
	case vm.BoolType:
		if s.ToBool() {
			return vm.MakeIntKey(1)
		}
		return vm.MakeIntKey(0)
	case vm.NullType:
		return vm.MakeStrKey("")
	case vm.FloatType:
		f := s.Float()
		if f != math.Trunc(f) {
			ctx.VM().Deprecated("Implicit conversion from float " + phpFloat(f) + " to int loses precision")
		}
		return vm.MakeIntKey(int64(f))
	case vm.ResourceType:
		id := s.ToInt()
		ctx.VM().Warn(fmt.Sprintf("Resource ID#%d used as offset, casting to integer (%d)", id, id))
		return vm.MakeIntKey(id)
	case vm.StringType:
		if i, ok := canonicalKeyString(s.StringVal()); ok {
			return vm.MakeIntKey(i)
		}
		return vm.MakeStrKey(s.StringVal())
	}
	name := s.Type().String()
	if s.Type() == vm.ObjectType {
		name = "object"
		if o := ctx.VMInstance().SlotObject(s); o != nil {
			name = vm.ClassDisplayName(o.ClassName())
		}
	}
	ctx.Throw(vm.NewThrowable("TypeError", "Cannot access offset of type "+name+" on array"))
	if ctx.HasThrown() {
		return *new(vm.ArrayKey)
	}
	return vm.MakeStrKey("")
}

// objectToStringVia вызывает __toString() объекта, если метод есть.
func objectToStringVia(ctx *vm.Context, s vm.Slot) (string, bool) {
	o := ctx.VMInstance().SlotObject(s)
	if o == nil {
		return "", false
	}
	ci := ctx.VMInstance().FindClass(o.ClassName())
	if ci == nil {
		return "", false
	}
	res, err := ci.Call(o, ctx, "__toString", nil)
	if err != nil {
		// Имена методов в PHP регистронезависимы: пробуем иную раскладку.
		res, err = ci.Call(o, ctx, "__tostring", nil)
		if err != nil {
			return "", false
		}
	}
	return res.ToString(), true
}

// columnKeyCheck проверяет, что column_key/index_key — string|int|null
// (bool/float допустимы — приводятся к int). Иначе TypeError как в PHP.
func columnKeyCheck(ctx *vm.Context, i int, s vm.Slot) {
	name := "column_key"
	if i == 2 {
		name = "index_key"
	}
	bad := func() {
		ctx.Throw(vm.NewThrowable("TypeError",
			vm.ArgTypeError("array_column", i+1, name, "string|int|null", phpTypeName(ctx, i), "", 0)))
		if ctx.HasThrown() {
			return
		}
	}
	switch s.Type() {
	case vm.ArrayType, vm.ObjectType, vm.ClosureType:
		bad()
		if ctx.HasThrown() {
			return
		}
	case vm.BoolType:
		if ctx.VMInstance().StrictTypes() {
			bad()
			if ctx.HasThrown() {
				return
			}
		}
	case vm.FloatType:
		if ctx.VMInstance().StrictTypes() {
			bad()
			if ctx.HasThrown() {
				return
			}
		}
		f := s.Float()
		if f != math.Trunc(f) {
			ctx.VM().Deprecated("Implicit conversion from float " + phpFloat(f) + " to int loses precision")
		}
	}
}

// arrayColumnFn — array_column(): выбирает столбец из массива строк-массивов.
// column_key == null возвращает строки целиком; index_key задаёт ключи
// результата (если у строки нет такого поля — элемент добавляется с авто-ключом).
// Строки, не являющиеся массивами, пропускаются (объекты не поддержаны).
func arrayColumnFn(ctx *vm.Context) vm.Value {
	if arg(ctx, 0).Type() != vm.ArrayType {
		arrayArgError(ctx, 0, "array_column")
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	a := arg(ctx, 0).AsArray()
	col := arg(ctx, 1)
	convertColKey := func(i int, s vm.Slot) vm.Slot {
		if s.Type() != vm.ObjectType {
			columnKeyCheck(ctx, i, s)
			return s
		}
		if str, ok := objectToStringVia(ctx, s); ok {
			return vm.MakeString(str)
		}
		columnKeyCheck(ctx, i, s)
		return s
	}
	col = convertColKey(1, col)
	if col.Type() == vm.FloatType || col.Type() == vm.BoolType {
		col = vm.MakeInt(col.ToInt())
	}
	hasCol := col.Type() != vm.NullType
	hasIndex := len(ctx.Args()) >= 3 && arg(ctx, 2).Type() != vm.NullType
	index := arg(ctx, 2)
	if hasIndex {
		index = convertColKey(2, index)
		if index.Type() == vm.FloatType || index.Type() == vm.BoolType {
			index = vm.MakeInt(index.ToInt())
		}
	}
	vmI := ctx.VMInstance()
	fetch := func(data vm.Slot, key vm.Slot) (vm.Slot, bool) {
		data = derefSlot(data)
		if data.Type() == vm.ObjectType {
			name := key.ToString()
			if vmI.ObjectPropExists(data, name, false) || vmI.ObjectPropExists(data, name, true) {
				v, found, err := vmI.ReadProperty(data, name)
				if err != nil {
					throwCallable(ctx, err)
					if ctx.HasThrown() {
						return *new(vm.Slot), *new(bool)
					}
				}
				return v, found
			}
			return vm.Slot{}, false
		}
		if data.Type() == vm.ArrayType {
			v, ok := data.AsArray().Get(keyOf(key))
			return v, ok
		}
		return vm.Slot{}, false
	}
	out := vm.NewArray()
	for _, row := range a.All() {
		var val vm.Slot
		if hasCol {
			v, ok := fetch(row, col)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			if !ok {
				continue
			}
			val = derefSlot(v)
		} else {
			val = derefSlot(row)
		}
		if hasIndex {
			if ik, ok := fetch(row, index); ok {
				out.Set(columnIndexKeyOf(ctx, ik), val)
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
				continue
			}
		}
		out.Append(val)
	}
	return vm.MakeArray(out)
}

func arrayReplaceFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	first := arg0(ctx)
	if first.Type() == vm.ArrayType {
		for k, v := range first.AsArray().All() {
			out.Set(k, v)
		}
	}
	for i := 1; i < len(ctx.Args()); i++ {
		a := arg(ctx, i)
		if a.Type() != vm.ArrayType {
			continue
		}
		for k, v := range a.AsArray().All() {
			out.Set(k, v)
		}
	}
	return vm.MakeArray(out)
}

// arrayReplaceRecursiveFn — array_replace_recursive(): рекурсивно заменяет
// значения первого массива значениями последующих; вложенные массивы
// объединяются, а не перезаписываются целиком.
func arrayReplaceRecursiveFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	first := true
	for i := 0; i < len(ctx.Args()); i++ {
		a := arg(ctx, i)
		if a.Type() != vm.ArrayType {
			continue
		}
		if first {
			for k, v := range a.AsArray().All() {
				out.Set(k, derefLoneRef(ctx, v))
			}
			first = false
			continue
		}
		if !replaceRecursive(ctx.VM(), out, a.AsArray(), map[*vm.Array]bool{}) {
			ctx.Throw(vm.NewThrowable("Error", "Recursion detected"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.MakeNull()
		}
	}
	return vm.MakeArray(out)
}

// replaceRecursive рекурсивно сливает src в dst (dst изменяется на месте и
// является свежесозданным массивом, поэтому разделяемые вложенные массивы
// не мутируются — для конфликтов создаётся новый массив). path отслеживает
// текущую цепочку массивов для обнаружения рекурсии.
func replaceRecursive(m *vm.VM, dst, src *vm.Array, path map[*vm.Array]bool) bool {
	if path[src] {
		return false
	}
	path[src] = true
	defer delete(path, src)
	for k, v := range src.All() {
		v = derefLoneRefVM(m, v)
		old, ok := dst.Get(k)
		if ok && old.Type() == vm.ArrayType && v.Type() == vm.ArrayType {
			merged := vm.NewArray()
			for ok2, v2 := range old.AsArray().All() {
				merged.Set(ok2, v2)
			}
			if !replaceRecursive(m, merged, v.AsArray(), path) {
				return false
			}
			dst.Set(k, vm.MakeArray(merged))
			continue
		}
		dst.Set(k, v)
	}
	return true
}

// arrayChangeKeyCaseFn — array_change_key_case(): приводит строковые ключи к
// нижнему (CASE_LOWER, по умолчанию) или верхнему (CASE_UPPER) регистру.
// Числовые ключи не меняются.
func arrayChangeKeyCaseFn(ctx *vm.Context) vm.Value {
	out := vm.NewArray()
	a := arg(ctx, 0)
	if a.Type() != vm.ArrayType {
		ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_change_key_case", 1, "array", "array", phpTypeName(ctx, 0), "", 0)))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	upper := false
	if len(ctx.Args()) >= 2 {
		cs := arg(ctx, 1)
		switch cs.Type() {
		case vm.IntType, vm.BoolType, vm.FloatType:
		case vm.StringType:
			if !isNumericString(cs.StringVal()) {
				ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_change_key_case", 2, "case", "int", "string", "", 0)))
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
		default:
			ctx.Throw(vm.NewThrowable("TypeError", vm.ArgTypeError("array_change_key_case", 2, "case", "int", phpTypeName(ctx, 1), "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		upper = cs.ToInt() != 0
	}
	if a.Type() == vm.ArrayType {
		for k, v := range a.AsArray().All() {
			if k.IsStr() {
				s := k.StrVal()
				if upper {
					s = strings.ToUpper(s)
				} else {
					s = strings.ToLower(s)
				}
				out.SetStr(s, v)
			} else {
				out.SetInt(k.IntVal(), v)
			}
		}
	}
	return vm.MakeArray(out)
}

// arrayArgError бросает TypeError в формате PHP: у первого аргумента имя
// параметра ($array), у последующих (variadic) — только номер.
func arrayArgError(ctx *vm.Context, i int, fn string) {
	name := ""
	if i == 0 {
		name = "array"
	}
	ctx.Throw(vm.NewThrowable("TypeError",
		vm.ArgTypeError(fn, i+1, name, "array", phpTypeName(ctx, i), "", 0)))
	if ctx.HasThrown() {
		return
	}
}

// requireArrayArg проверяет, что аргумент #i (нумерация с 0) — массив.
func requireArrayArg(ctx *vm.Context, i int, fn string) *vm.Array {
	if arg(ctx, i).Type() != vm.ArrayType {
		arrayArgError(ctx, i, fn)
		if ctx.HasThrown() {
			return *new(*vm.Array)
		}
	}
	return arg(ctx, i).AsArray()
}

func arrayKeySlot(k vm.ArrayKey) vm.Slot {
	if k.IsStr() {
		return vm.MakeString(k.StrVal())
	}
	return vm.MakeInt(k.IntVal())
}

func arrayMapFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_map() expects at least 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	cb := args[0]
	requireCallableNamed(ctx, 0, "array_map", "callback", true)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	arrays := args[1:]
	// Один массив (горячий случай): без снимков значений/ключей — обход
	// исходного массива напрямую, буфер аргументов переиспользуется.
	if len(arrays) == 1 {
		if arrays[0].Type() != vm.ArrayType {
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("array_map", 2, "array", "array", phpTypeName(ctx, 1), "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		identity := cb.IsNull()
		if identity {
			src := derefSlot(arrays[0])
			if src.Type() == vm.ArrayType {
				src.AsArray().MarkShared()
				return src
			}
		}
		arr := arrays[0].AsArray()
		out := vm.NewArray()
		var ab [1]vm.Slot
		for k, v := range arr.All() {
			ab[0] = v
			res, err := ctx.VMInstance().CallCallable(ctx, cb, ab[:])
			throwCallable(ctx, err)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			out.Set(k, res)
		}
		return vm.MakeArray(out)
	}
	values := make([][]vm.Slot, len(arrays))
	keys := make([][]vm.ArrayKey, len(arrays))
	maxLen := 0
	for i := range arrays {
		if arrays[i].Type() != vm.ArrayType {
			nm := ""
			if i == 0 {
				nm = "array"
			}
			ctx.Throw(vm.NewThrowable("TypeError",
				vm.ArgTypeError("array_map", i+2, nm, "array", phpTypeName(ctx, i+1), "", 0)))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		arr := arrays[i].AsArray()
		values[i] = make([]vm.Slot, 0, arr.Len())
		keys[i] = make([]vm.ArrayKey, 0, arr.Len())
		for k, v := range arr.All() {
			keys[i] = append(keys[i], k)
			values[i] = append(values[i], v)
		}
		if len(values[i]) > maxLen {
			maxLen = len(values[i])
		}
	}
	// Один массив — ключи результата сохраняются; несколько — ключи
	// последовательные (как в PHP).
	single := len(arrays) == 1
	out := vm.NewArray()
	identity := cb.IsNull()
	if identity && single {
		// array_map(null, $arr): возвращает массив как есть (ключи сохранены).
		// В Zend это тот же zval/HashTable (COW), поэтому самоссылки внутри
		// корректно распознаются как *RECURSION* при var_dump.
		src := derefSlot(arrays[0])
		if src.Type() == vm.ArrayType {
			src.AsArray().MarkShared()
			return src
		}
	}
	for idx := 0; idx < maxLen; idx++ {
		if identity {
			row := vm.NewArray()
			for j := range arrays {
				if idx < len(values[j]) {
					row.Append(values[j][idx])
				} else {
					row.Append(vm.MakeNull())
				}
			}
			if single {
				out.Set(keys[0][idx], vm.MakeArray(row))
			} else {
				out.Append(vm.MakeArray(row))
			}
			continue
		}
		callArgs := make([]vm.Slot, len(arrays))
		for j := range arrays {
			if idx < len(values[j]) {
				callArgs[j] = values[j][idx]
			} else {
				callArgs[j] = vm.MakeNull()
			}
		}
		res, err := ctx.VMInstance().CallCallable(ctx, cb, callArgs)
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		if single {
			out.Set(keys[0][idx], res)
		} else {
			out.Append(res)
		}
	}
	return vm.MakeArray(out)
}

func arrayFilterFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_filter() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	arr := requireArrayArg(ctx, 0, "array_filter")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	if len(args) >= 2 {
		requireCallableNamed(ctx, 1, "array_filter", "callback", true)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	cb := vm.MakeNull()
	if len(args) >= 2 {
		cb = args[1]
	}
	mode := int64(0)
	if len(args) >= 3 {
		mode = args[2].ToInt()
	}
	out := vm.NewArray()
	useCb := !cb.IsNull()
	var ab [2]vm.Slot
	for k, v := range arr.All() {
		keep := v.ToBool()
		if useCb {
			n := 1
			switch mode {
			case 2:
				ab[0] = arrayKeySlot(k)
			case 1:
				ab[0] = v
				ab[1] = arrayKeySlot(k)
				n = 2
			default:
				ab[0] = v
			}
			res, err := ctx.VMInstance().CallCallable(ctx, cb, ab[:n])
			throwCallable(ctx, err)
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			keep = res.ToBool()
		}
		if keep {
			out.Set(k, v)
		}
	}
	return vm.MakeArray(out)
}

func arrayReduceFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_reduce() expects at least 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	arr := requireArrayArg(ctx, 0, "array_reduce")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := args[1]
	carry := vm.MakeNull()
	if len(args) >= 3 {
		carry = args[2]
	}
	var ab [2]vm.Slot
	for _, v := range arr.All() {
		ab[0], ab[1] = carry, v
		res, err := ctx.VMInstance().CallCallable(ctx, cb, ab[:])
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		carry = res
	}
	return carry
}

// arrayElem — пара ключ/значение (снимок массива для сортировки).
type arrayElem struct {
	k vm.ArrayKey
	v vm.Slot
}

// elemSorter — стабильная сортировка снимка массива пользовательским
// компаратором: sort.Stable по конкретному типу (без reflect-swapper, который
// использует sort.SliceStable).
type elemSorter struct {
	elems    []arrayElem
	byKey    bool
	callComp func(lhs, rhs vm.Slot) bool
}

func (s elemSorter) Len() int { return len(s.elems) }

func (s elemSorter) Less(i, j int) bool {
	if s.byKey {
		return s.callComp(arrayKeySlot(s.elems[i].k), arrayKeySlot(s.elems[j].k))
	}
	return s.callComp(s.elems[i].v, s.elems[j].v)
}

func (s elemSorter) Swap(i, j int) { s.elems[i], s.elems[j] = s.elems[j], s.elems[i] }

func arrayElems(a *vm.Array) []arrayElem {
	out := make([]arrayElem, 0, a.Len())
	for k, v := range a.All() {
		out = append(out, arrayElem{k, v})
	}
	return out
}

// refArrayArg возвращает массив-аргумент и функцию записи изменённого массива
// обратно в Ref-ячейку (если аргумент пришёл по ссылке).
func refArrayArg(ctx *vm.Context, i int, fn string) (*vm.Array, func(*vm.Array)) {
	s := arg(ctx, i)
	if s.Type() != vm.ArrayType {
		arrayArgError(ctx, i, fn)
		if ctx.HasThrown() {
			return *new(*vm.Array), *new(func(*vm.Array))
		}
	}
	if s.IsRef() && s.Ref() != nil {
		r := s.Ref()
		return r.Value().AsArray(), func(na *vm.Array) {
			// Перестройка не меняет объект массива для активных foreach by
			// ref: обновлённый массив наследует идентификатор старого
			// (позиции курсоров пересчитаны вызывающим до set).
			if old := derefSlot(r.Value()); old.Type() == vm.ArrayType {
				na.AdoptIterID(old.AsArray())
			}
			r.Set(vm.MakeArray(na))
		}
	}
	return s.AsArray(), func(*vm.Array) {}
}

func sortSimple(ctx *vm.Context, fn string, byKey, preserve, desc bool) vm.Value {
	if len(ctx.Args()) < 1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fn+"() expects exactly 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	flags := int64(0)
	if len(ctx.Args()) >= 2 {
		flags = arg(ctx, 1).ToInt()
	}
	a, set := refArrayArg(ctx, 0, fn)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	// Быстрый путь: сортировка значений packed-массива без построения
	// промежуточного []arrayElem и без сравнения слотов на каждый элемент.
	// Разрешён только там, где прямое сравнение Go совпадает с PHP
	// (int/float: SORT_REGULAR и SORT_NUMERIC; string: SORT_STRING).
	// Сортировка идёт на месте, поэтому только когда аргумент — настоящая
	// Ref-ячейка и массив не разделён (иначе копия + set(b), как у array_push).
	if !byKey && !preserve && a.Packed() && arg(ctx, 0).IsRef() && !a.Shared() {
		if ints := a.Ints(); ints != nil && (flags == 0 || flags == 1) {
			b := a.Unshare()
			view := b.Ints()
			if desc {
				sort.Slice(view, func(i, j int) bool { return view[i] > view[j] })
			} else {
				sort.Slice(view, func(i, j int) bool { return view[i] < view[j] })
			}
			b.SetNextFree(int64(len(view)))
			b.Reset()
			set(b)
			return vm.Bool(true)
		}
		if floats := a.Floats(); floats != nil && (flags == 0 || flags == 1) {
			b := a.Unshare()
			view := b.Floats()
			if desc {
				sort.SliceStable(view, func(i, j int) bool { return view[i] > view[j] })
			} else {
				sort.SliceStable(view, func(i, j int) bool { return view[i] < view[j] })
			}
			b.SetNextFree(int64(len(view)))
			b.Reset()
			set(b)
			return vm.Bool(true)
		}
		// SORT_STRING без SORT_FLAG_CASE: байтовое сравнение строк (как
		// zend_binary_strcmp); для packedStr сортируем сами элементы.
		if flags == 2 && a.PackedStrs() {
			b := a.Unshare()
			b.SortStrs(desc)
			b.SetNextFree(int64(b.Len()))
			b.Reset()
			set(b)
			return vm.Bool(true)
		}
	}
	elems := arrayElems(a)
	sort.SliceStable(elems, func(i, j int) bool {
		var c int
		if byKey {
			c = compareWithFlags(ctx, arrayKeySlot(elems[i].k), arrayKeySlot(elems[j].k), flags)
		} else {
			c = compareWithFlags(ctx, elems[i].v, elems[j].v, flags)
		}
		if desc {
			c = -c
		}
		return c < 0
	})
	out := vm.NewArray()
	for _, e := range elems {
		if preserve {
			out.Set(e.k, e.v)
		} else {
			out.Append(e.v)
		}
	}
	set(out)
	return vm.Bool(true)
}

// compareWithFlags сравнивает слоты с учётом флагов сортировки PHP
// (SORT_REGULAR/SORT_NUMERIC/SORT_STRING/SORT_NATURAL, SORT_FLAG_CASE).
func compareWithFlags(ctx *vm.Context, a, b vm.Slot, flags int64) int {
	const flagCase int64 = 8
	switch flags &^ flagCase {
	case 1: // SORT_NUMERIC
		af, bf := a.ToFloat(), b.ToFloat()
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	case 2: // SORT_STRING
		as, bs := sortStringOf(ctx, a), sortStringOf(ctx, b)
		if flags&flagCase != 0 {
			as, bs = strings.ToLower(as), strings.ToLower(bs)
		}
		return strings.Compare(as, bs)
	case 6: // SORT_NATURAL
		return int(strNatCmp(sortStringOf(ctx, a), sortStringOf(ctx, b), flags&flagCase != 0))
	}
	// SORT_REGULAR (0): PHP сравнивает два числовых значения/строки численно,
	// иначе — как строки (стандартное сравнение).
	if numSlot(a) && numSlot(b) {
		af, bf := a.ToFloat(), b.ToFloat()
		switch {
		case af < bf:
			return -1
		case af > bf:
			return 1
		default:
			return 0
		}
	}
	// Объект с __toString против строки сравнивается по строковому
	// представлению (zval_get_string зовёт __toString).
	if a.Type() == vm.ObjectType && b.Type() == vm.StringType {
		if s, ok := objectToStringVia(ctx, a); ok {
			return strings.Compare(s, b.ToString())
		}
	}
	if b.Type() == vm.ObjectType && a.Type() == vm.StringType {
		if s, ok := objectToStringVia(ctx, b); ok {
			return strings.Compare(a.ToString(), s)
		}
	}
	return vm.CompareSlots(a, b)
}

// sortStringOf приводит слот к строке для SORT_STRING/SORT_NATURAL: объекты —
// через __toString (как zval_get_tmp_string в PHP).
func sortStringOf(ctx *vm.Context, s vm.Slot) string {
	if s.Type() == vm.ObjectType {
		if str, ok := objectToStringVia(ctx, s); ok {
			return str
		}
	}
	if s.Type() == vm.ArrayType {
		ctx.VM().Warn("Array to string conversion")
		return "Array"
	}
	return s.ToString()
}

// numSlot сообщает, что слот участвует в числовом сравнении SORT_REGULAR:
// int/float, либо строка, целиком являющаяся числовой.
func numSlot(s vm.Slot) bool {
	t := s.Type()
	if t == vm.IntType || t == vm.FloatType {
		return true
	}
	if t != vm.StringType {
		return false
	}
	str := strings.TrimSpace(s.ToString())
	if str == "" {
		return false
	}
	_, err := strconv.ParseFloat(str, 64)
	return err == nil
}

func sortFn(ctx *vm.Context) vm.Value   { return sortSimple(ctx, "sort", false, false, false) }
func rsortFn(ctx *vm.Context) vm.Value  { return sortSimple(ctx, "rsort", false, false, true) }
func asortFn(ctx *vm.Context) vm.Value  { return sortSimple(ctx, "asort", false, true, false) }
func arsortFn(ctx *vm.Context) vm.Value { return sortSimple(ctx, "arsort", false, true, true) }
func ksortFn(ctx *vm.Context) vm.Value  { return sortSimple(ctx, "ksort", true, true, false) }
func krsortFn(ctx *vm.Context) vm.Value { return sortSimple(ctx, "krsort", true, true, true) }

func sortWithCallback(ctx *vm.Context, fn string, byKey, preserve bool) vm.Value {
	if len(ctx.Args()) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fn+"() expects exactly 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	a, set := refArrayArg(ctx, 0, fn)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := arg(ctx, 1)
	var callErr error
	boolRet := false
	var ab [2]vm.Slot
	sign := func(x int64) int64 {
		if x > 0 {
			return 1
		}
		if x < 0 {
			return -1
		}
		return 0
	}
	// callComp повторяет php_array_user_compare_unstable: bool-результат
	// вызывает Deprecated и (для false) повторный вызов с перестановкой
	// операндов, как в Zend.
	callComp := func(lhs, rhs vm.Slot) bool {
		ab[0], ab[1] = lhs, rhs
		res, err := ctx.VMInstance().CallCallable(ctx, cb, ab[:])
		if err != nil {
			if callErr == nil {
				callErr = err
			}
			return false
		}
		if res.Type() == vm.BoolType {
			boolRet = true
			if !res.ToBool() {
				ab[0], ab[1] = rhs, lhs
				res2, err2 := ctx.VMInstance().CallCallable(ctx, cb, ab[:])
				if err2 != nil {
					if callErr == nil {
						callErr = err2
					}
					return false
				}
				return -sign(res2.ToInt()) < 0
			}
			return sign(1) < 0
		}
		return sign(res.ToInt()) < 0
	}
	// usort (ключи не сохраняются): сортируем только значения — Swap
	// переставляет Slot (24 байта), а не пару ключ/значение.
	var elems []arrayElem
	var vals []vm.Slot
	if !preserve && !byKey {
		vals = a.Values()
		sort.Stable(slotSorter{vals: vals, callComp: callComp})
	} else {
		elems = arrayElems(a)
		sort.Stable(elemSorter{elems: elems, byKey: byKey, callComp: callComp})
	}
	if boolRet {
		ctx.VM().Deprecated(fn + "(): Returning bool from comparison function is deprecated, return an integer less than, equal to, or greater than zero")
	}
	throwCallable(ctx, callErr)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	if vals != nil {
		for _, v := range vals {
			out.Append(v)
		}
	} else {
		for _, e := range elems {
			if preserve {
				out.Set(e.k, e.v)
			} else {
				out.Append(e.v)
			}
		}
	}
	set(out)
	return vm.Bool(true)
}

// slotSorter — стабильная сортировка снимка значений (usort): ключи не
// сохраняются, поэтому Swap перемещает только сами значения.
type slotSorter struct {
	vals     []vm.Slot
	callComp func(lhs, rhs vm.Slot) bool
}

func (s slotSorter) Len() int           { return len(s.vals) }
func (s slotSorter) Less(i, j int) bool { return s.callComp(s.vals[i], s.vals[j]) }
func (s slotSorter) Swap(i, j int)      { s.vals[i], s.vals[j] = s.vals[j], s.vals[i] }

func usortFn(ctx *vm.Context) vm.Value  { return sortWithCallback(ctx, "usort", false, false) }
func uasortFn(ctx *vm.Context) vm.Value { return sortWithCallback(ctx, "uasort", false, true) }
func uksortFn(ctx *vm.Context) vm.Value { return sortWithCallback(ctx, "uksort", true, true) }

func arrayWalkFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_walk() expects at least 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if len(args) > 3 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fmt.Sprintf("array_walk() expects at most 3 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 1, "array_walk", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := args[1]
	ext := vm.MakeNull()
	hasExt := len(args) >= 3
	if hasExt {
		ext = args[2]
	}
	if arg(ctx, 0).Type() == vm.ObjectType {
		walkObjectProps(ctx, arg(ctx, 0), cb, ext, hasExt, "array_walk")
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(true)
	}
	a, set := refArrayArg(ctx, 0, "array_walk")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	a = a.Unshare()
	for k, v := range a.All() {
		cell := vm.NewRef(v)
		callArgs := []vm.Slot{cell, arrayKeySlot(k)}
		if hasExt {
			callArgs = append(callArgs, ext)
		}
		_, err := ctx.VMInstance().CallCallable(ctx, cb, callArgs)
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		a.Set(k, cell.Ref().Value())
	}
	set(a)
	return vm.Bool(true)
}

func arrayWalkRecursiveFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) < 2 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_walk_recursive() expects at least 2 arguments, 1 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if len(args) > 3 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", fmt.Sprintf("array_walk_recursive() expects at most 3 arguments, %d given", len(args))))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	requireCallableNamed(ctx, 1, "array_walk_recursive", "callback", false)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	cb := args[1]
	ext := vm.MakeNull()
	hasExt := len(args) >= 3
	if hasExt {
		ext = args[2]
	}
	if arg(ctx, 0).Type() == vm.ObjectType {
		walkObjectProps(ctx, arg(ctx, 0), cb, ext, hasExt, "array_walk_recursive")
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
		return vm.Bool(true)
	}
	a, set := refArrayArg(ctx, 0, "array_walk_recursive")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	a = a.Unshare()
	walkRecursive(ctx, a, cb, ext, hasExt)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	set(a)
	return vm.Bool(true)
}

// walkObjectProps обходит свойства объекта как array_walk: ключ — манглированное
// имя (private/protected), значение передаётся by-ref и записывается обратно.
func walkObjectProps(ctx *vm.Context, s vm.Slot, cb, ext vm.Slot, hasExt bool, fn string) {
	m := ctx.VMInstance()
	o := m.SlotObject(s)
	if o == nil {
		arrayArgError(ctx, 0, fn)
		if ctx.HasThrown() {
			return
		}
	}
	for _, de := range m.ObjectDumpEntries(o) {
		if de.Uninit {
			continue
		}
		key := de.Key
		switch {
		case de.Suffix == ":protected":
			key = "\x00*\x00" + de.Key
		case de.Suffix != "":
			cls := strings.TrimSuffix(strings.TrimPrefix(de.Suffix, `:"`), `":private`)
			key = "\x00" + cls + "\x00" + de.Key
		}
		cell := vm.NewRef(de.Val)
		callArgs := []vm.Slot{cell, vm.MakeString(key)}
		if hasExt {
			callArgs = append(callArgs, ext)
		}
		_, err := m.CallCallable(ctx, cb, callArgs)
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return
		}
		if werr := m.SetPropertyChecked(s, de.Key, cell, false); werr != nil {
			throwCallable(ctx, werr)
			if ctx.HasThrown() {
				return
			}
		}
	}
}

func walkRecursive(ctx *vm.Context, a *vm.Array, cb, ext vm.Slot, hasExt bool) {
	for k, v := range a.All() {
		if v.Type() == vm.ArrayType {
			sub := v.AsArray().Unshare()
			walkRecursive(ctx, sub, cb, ext, hasExt)
			if ctx.HasThrown() {
				return
			}
			a.Set(k, vm.MakeArray(sub))
			continue
		}
		cell := vm.NewRef(v)
		callArgs := []vm.Slot{cell, arrayKeySlot(k)}
		if hasExt {
			callArgs = append(callArgs, ext)
		}
		_, err := ctx.VMInstance().CallCallable(ctx, cb, callArgs)
		throwCallable(ctx, err)
		if ctx.HasThrown() {
			return
		}
		a.Set(k, cell.Ref().Value())
	}
}

// --- внутренний указатель массива: key/current/next/prev/reset/end/each ---
//
// reset/end/next/prev/each объявлены как by-ref (ByRefArgs), поэтому массив
// приходит Ref-слотом. Операции с указателем мутируют массив (COW): берём
// Unshare() и записываем результат обратно в Ref-ячейку.
// key/current — только чтение (не by-ref).

// refArrayMut возвращает массив-аргумент, пригодный для мутации по месту
// (Unshare), и функцию записи результата обратно в Ref-ячейку.
func refArrayMut(ctx *vm.Context, i int, fn string) (*vm.Array, func(*vm.Array)) {
	s := arg(ctx, i)
	if s.Type() != vm.ArrayType {
		arrayArgError(ctx, i, fn)
		if ctx.HasThrown() {
			return *new(*vm.Array), *new(func(*vm.Array))
		}
	}
	if s.IsRef() && s.Ref() != nil {
		r := s.Ref()
		a := r.Value().AsArray().Unshare()
		return a, func(na *vm.Array) { r.Set(vm.MakeArray(na)) }
	}
	return s.AsArray().Unshare(), func(*vm.Array) {}
}

func arrayKeyPtrFn(ctx *vm.Context) vm.Value {
	s := arg(ctx, 0)
	if s.Type() == vm.ObjectType {
		objectIterDeprecated(ctx, "key")
		entries, _ := objectIterEntries(ctx, s)
		o := ctx.VM().SlotObject(s)
		if !o.IterValid() {
			return vm.MakeNull()
		}
		if p := o.IterPos(); p >= 0 && p < len(entries) {
			return vm.MakeString(entries[p].Key)
		}
		return vm.MakeNull()
	}
	if s.Type() != vm.ArrayType {
		arrayArgError(ctx, 0, "key")
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	if k, ok := s.AsArray().Key(); ok {
		return arrayKeySlot(k)
	}
	return vm.MakeNull()
}

func arrayCurrentFn(ctx *vm.Context) vm.Value {
	s := arg(ctx, 0)
	if s.Type() == vm.ObjectType {
		objectIterDeprecated(ctx, "current")
		return objectCurrent(ctx, s)
	}
	if s.Type() != vm.ArrayType {
		arrayArgError(ctx, 0, "current")
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	return arrayCurrentOrFalse(ctx, s.AsArray())
}

// objectIterEntries возвращает инициализированные свойства объекта в порядке
// объявления (для current/reset/next/...). Второй результат — false, если
// аргумент не объект.
func objectIterEntries(ctx *vm.Context, s vm.Slot) ([]vm.DumpEntry, bool) {
	o := ctx.VM().SlotObject(s)
	if o == nil {
		return nil, false
	}
	all := ctx.VM().ObjectDumpEntries(o)
	res := make([]vm.DumpEntry, 0, len(all))
	for _, e := range all {
		if !e.Uninit {
			res = append(res, e)
		}
	}
	return res, true
}

func objectIterDeprecated(ctx *vm.Context, fn string) {
	ctx.VM().Deprecated(fn + "(): Calling " + fn + "() on an object is deprecated")
}

func objectCurrent(ctx *vm.Context, s vm.Slot) vm.Value {
	o := ctx.VM().SlotObject(s)
	if !o.IterValid() {
		return vm.Bool(false)
	}
	entries, _ := objectIterEntries(ctx, s)
	if p := o.IterPos(); p >= 0 && p < len(entries) {
		return entries[p].Val
	}
	return vm.Bool(false)
}

func arrayCurrentOrFalse(ctx *vm.Context, a *vm.Array) vm.Value {
	if v, ok := a.Current(); ok {
		return v
	}
	return vm.Bool(false)
}

func arrayResetFn(ctx *vm.Context) vm.Value {
	s := arg(ctx, 0)
	if s.Type() == vm.ObjectType {
		objectIterDeprecated(ctx, "reset")
		ctx.VM().SlotObject(s).SetIterPos(0)
		return objectCurrent(ctx, s)
	}
	a, set := refArrayMut(ctx, 0, "reset")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	a.Reset()
	set(a)
	return arrayCurrentOrFalse(ctx, a)
}

func arrayEndFn(ctx *vm.Context) vm.Value {
	s := arg(ctx, 0)
	if s.Type() == vm.ObjectType {
		objectIterDeprecated(ctx, "end")
		entries, _ := objectIterEntries(ctx, s)
		ctx.VM().SlotObject(s).SetIterPos(len(entries) - 1)
		return objectCurrent(ctx, s)
	}
	a, set := refArrayMut(ctx, 0, "end")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	a.End()
	set(a)
	return arrayCurrentOrFalse(ctx, a)
}

func arrayNextFn(ctx *vm.Context) vm.Value {
	s := arg(ctx, 0)
	if s.Type() == vm.ObjectType {
		objectIterDeprecated(ctx, "next")
		o := ctx.VM().SlotObject(s)
		entries, _ := objectIterEntries(ctx, s)
		p := o.IterPos() + 1
		o.SetIterPos(p)
		if p >= 0 && p < len(entries) {
			return entries[p].Val
		}
		return vm.Bool(false)
	}
	a, set := refArrayMut(ctx, 0, "next")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	a.Next()
	set(a)
	return arrayCurrentOrFalse(ctx, a)
}

func arrayPrevFn(ctx *vm.Context) vm.Value {
	s := arg(ctx, 0)
	if s.Type() == vm.ObjectType {
		objectIterDeprecated(ctx, "prev")
		o := ctx.VM().SlotObject(s)
		entries, _ := objectIterEntries(ctx, s)
		p := o.IterPos() - 1
		o.SetIterPos(p)
		if p >= 0 && p < len(entries) {
			return entries[p].Val
		}
		return vm.Bool(false)
	}
	a, set := refArrayMut(ctx, 0, "prev")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	// PHP: prev() с первого элемента (и из невалидного состояния) возвращает
	// false и оставляет указатель невалидным — НЕ заворачивает на последний.
	if _, ok := a.Current(); !ok {
		set(a)
		return vm.Bool(false)
	}
	k, _ := a.Key()
	elems := arrayElems(a)
	if len(elems) == 0 || elems[0].k == k {
		a.End()  // последний живой...
		a.Next() // ...и сразу за конец — невалидный указатель
	} else {
		a.Prev()
	}
	set(a)
	return arrayCurrentOrFalse(ctx, a)
}

// arrayEachFn — each(): классическое поведение PHP 7 (в 8.0 функция удалена,
// но реализация безопасна и совместима с прежними тестами). Возвращает пару
// key/value, продвигает указатель; при выходе за конец — false.
func arrayEachFn(ctx *vm.Context) vm.Value {
	a, set := refArrayMut(ctx, 0, "each")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	k, ok := a.Key()
	if !ok {
		return vm.Bool(false)
	}
	v, _ := a.Current()
	a.Next()
	set(a)
	out := vm.NewArray()
	out.SetInt(1, v)
	out.SetStr("value", v)
	out.SetInt(0, arrayKeySlot(k))
	out.SetStr("key", arrayKeySlot(k))
	return vm.MakeArray(out)
}

// --- мутирующие array-функции (array_shift/pop/push/unshift/splice) ---

// reindexElems перестраивает пары (ключ, значение): строковые ключи
// сохраняются, целочисленные перенумеровываются подряд (как PHP при
// array_shift/array_unshift/array_splice).
func reindexElems(elems []arrayElem) *vm.Array {
	out := vm.NewArray()
	for _, e := range elems {
		if e.k.IsStr() {
			out.SetStr(e.k.StrVal(), e.v)
		} else {
			out.Append(e.v)
		}
	}
	return out
}

// preserveElemsNF перестраивает пары (ключ, значение), сохраняя ВСЕ ключи
// (как array_pop/natsort: нумерация не сбрасывается) и явный nextFree (состояние
// авто-индекса при pop/push/sort).
func preserveElemsNF(elems []arrayElem, nextFree int64) *vm.Array {
	out := vm.NewArray()
	for _, e := range elems {
		if e.k.IsStr() {
			out.SetStr(e.k.StrVal(), e.v)
		} else {
			out.SetInt(e.k.IntVal(), e.v)
		}
	}
	out.SetNextFree(nextFree)
	return out
}

func arrayShiftFn(ctx *vm.Context) vm.Value {
	a, set := refArrayArg(ctx, 0, "array_shift")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	elems := arrayElems(a)
	if len(elems) == 0 {
		set(vm.NewArray())
		return vm.MakeNull()
	}
	first := elems[0].v
	ctx.VM().RemapArrayShiftIters(arg(ctx, 0))
	set(reindexElems(elems[1:]))
	return first
}

func arrayPopFn(ctx *vm.Context) vm.Value {
	a, set := refArrayArg(ctx, 0, "array_pop")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	elems := arrayElems(a)
	if len(elems) == 0 {
		set(vm.NewArray())
		return vm.MakeNull()
	}
	lastElem := elems[len(elems)-1]
	nf := a.NextFree()
	if !lastElem.k.IsStr() && lastElem.k.IntVal() == nf-1 {
		nf--
	}
	set(preserveElemsNF(elems[:len(elems)-1], nf))
	return lastElem.v
}

func arrayPushFn(ctx *vm.Context) vm.Value {
	a, set := refArrayArg(ctx, 0, "array_push")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	// Аргумент пришёл настоящей Ref-ячейкой и массив не разделён — дописываем
	// на месте: иначе каждый вызов копировал бы весь массив и типичный цикл
	// `array_push($a, $i)` вырождался бы в O(n^2).
	out := a
	if !arg(ctx, 0).IsRef() || a.Shared() {
		out = preserveElemsNF(arrayElems(a), a.NextFree())
	}
	for i := 1; i < len(ctx.Args()); i++ {
		if _, err := out.Append(arg(ctx, i)); err != nil {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	set(out)
	return vm.Int(int64(out.Len()))
}

func arrayUnshiftFn(ctx *vm.Context) vm.Value {
	a, set := refArrayArg(ctx, 0, "array_unshift")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	out := vm.NewArray()
	for i := 1; i < len(ctx.Args()); i++ {
		if _, err := out.Append(arg(ctx, i)); err != nil {
			ctx.Throw(vm.NewThrowable("Error", err.Error()))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	for _, e := range arrayElems(a) {
		if e.k.IsStr() {
			out.SetStr(e.k.StrVal(), e.v)
		} else {
			out.Append(e.v)
		}
	}
	ctx.VM().RemapArrayUnshiftIters(arg(ctx, 0), len(ctx.Args())-1)
	set(out)
	return vm.Int(int64(out.Len()))
}

// arraySpliceFn — array_splice($array, $offset, $length = null, $replacement = []).
// Числовые ключи удалённых и оставшихся элементов перенумеровываются,
// строковые сохраняются; значения replacement вставляются как список.
func arraySpliceFn(ctx *vm.Context) vm.Value {
	a, set := refArrayArg(ctx, 0, "array_splice")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	elems := arrayElems(a)
	n64 := int64(len(elems))
	// Все вычисления смещения/длины — в int64, чтобы не было переполнения
	// при PHP_INT_MIN/PHP_INT_MAX (gh18480).
	off := arg(ctx, 1).ToInt()
	if off < 0 {
		off += n64
		if off < 0 {
			off = 0
		}
	}
	if off > n64 {
		off = n64
	}
	length := n64 - off
	if len(ctx.Args()) >= 3 && !arg(ctx, 2).IsNull() {
		l := arg(ctx, 2).ToInt()
		if l < 0 {
			end := n64 + l
			if l < -n64 {
				end = 0
			}
			if end < off {
				end = off
			}
			length = end - off
		} else {
			length = l
			if length > n64-off {
				length = n64 - off
			}
		}
	}
	if length < 0 {
		length = 0
	}
	end := off + length
	if end > n64 {
		end = n64
	}
	offi, endi := int(off), int(end)
	removed := reindexElems(elems[offi:endi])
	out := vm.NewArray()
	for _, e := range elems[:offi] {
		if e.k.IsStr() {
			out.SetStr(e.k.StrVal(), e.v)
		} else {
			out.Append(e.v)
		}
	}
	replLen := 0
	if len(ctx.Args()) >= 4 {
		rep := arg(ctx, 3)
		if rep.Type() == vm.ArrayType {
			for _, v := range rep.AsArray().All() {
				out.Append(v)
				replLen++
			}
		} else if !rep.IsNull() {
			out.Append(rep)
			replLen++
		}
	}
	for _, e := range elems[endi:] {
		if e.k.IsStr() {
			out.SetStr(e.k.StrVal(), e.v)
		} else {
			out.Append(e.v)
		}
	}
	ctx.VM().RemapArraySpliceIters(arg(ctx, 0), offi, endi-offi, replLen)
	set(out)
	return vm.MakeArray(removed)
}

// --- натуральная сортировка (мутирует массив, сохраняет ключи) ---

func natSortImpl(ctx *vm.Context, fold bool, name string) vm.Value {
	a, set := refArrayArg(ctx, 0, name)
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	elems := arrayElems(a)
	sort.SliceStable(elems, func(i, j int) bool {
		return strNatCmp(sortStringOf(ctx, elems[i].v), sortStringOf(ctx, elems[j].v), fold) < 0
	})
	set(preserveElemsNF(elems, a.NextFree()))
	return vm.Bool(true)
}

func natSortFn(ctx *vm.Context) vm.Value     { return natSortImpl(ctx, false, "natsort") }
func natCaseSortFn(ctx *vm.Context) vm.Value { return natSortImpl(ctx, true, "natcasesort") }

// multisortErr бросает ошибку array_multisort() в формате PHP: для аргумента #1
// добавляется имя параметра ($array).
func multisortErr(ctx *vm.Context, i int, class, msg string) {
	name := ""
	if i == 0 {
		name = " ($array)"
	}
	ctx.Throw(vm.NewThrowable(class,
		fmt.Sprintf("array_multisort(): Argument #%d%s %s", i+1, name, msg)))
	if ctx.HasThrown() {
		return
	}
}

// --- array_multisort ---
//
// Базовая, но корректная реализация: аргументы-массивы сортируются
// лексикографически по столбцам (первый массив — старший ключ сравнения),
// последующие скаляры задают флаги сортировки для непосредственно
// предшествующего массива (SORT_ASC/SORT_DESC и тип SORT_REGULAR/NUMERIC/
// STRING/NATURAL|FLAG_CASE). Числовые ключи перенумеровываются, строковые
// сохраняются. Проверка аргументов повторяет PHP (валидность флагов, запрет
// двух флагов одного вида, равенство размеров массивов).
func arrayMultisortFn(ctx *vm.Context) vm.Value {
	args := ctx.Args()
	if len(args) == 0 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError", "array_multisort() expects at least 1 argument, 0 given"))
		if ctx.HasThrown() {
			return *new(vm.Value)
		}
	}
	type col struct {
		elems []arrayElem
		set   func(*vm.Array)
		flags int64
		desc  bool
	}
	var cols []col
	// parse_state: true — флаг данного вида ещё допустим для текущего массива.
	typeAllowed, orderAllowed := false, false
	for i := 0; i < len(args); i++ {
		if args[i].Type() == vm.ArrayType {
			arr, set := refArrayArg(ctx, i, "array_multisort")
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			cols = append(cols, col{elems: arrayElems(arr), set: set})
			typeAllowed, orderAllowed = true, true
			continue
		}
		if args[i].Type() != vm.IntType {
			multisortErr(ctx, i, "TypeError", "must be an array or a sort flag")
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
		v := args[i].ToInt()
		switch v &^ 8 { // PHP: Z_LVAL & ~SORT_FLAG_CASE
		case 3, 4: // SORT_DESC / SORT_ASC
			if !orderAllowed {
				multisortErr(ctx, i, "TypeError", "must be an array or a sort flag that has not already been specified")
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			c := &cols[len(cols)-1]
			c.desc = v&^8 == 3
			orderAllowed = false
		case 0, 1, 2, 5, 6: // SORT_REGULAR/NUMERIC/STRING/NATURAL/LOCALE_STRING
			if !typeAllowed {
				multisortErr(ctx, i, "TypeError", "must be an array or a sort flag that has not already been specified")
				if ctx.HasThrown() {
					return *new(vm.Value)
				}
			}
			c := &cols[len(cols)-1]
			c.flags |= v
			typeAllowed = false
		default:
			multisortErr(ctx, i, "ValueError", "must be a valid sort flag")
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	// Все массивы должны быть одного размера.
	n := len(cols[0].elems)
	for ci := 1; ci < len(cols); ci++ {
		if len(cols[ci].elems) != n {
			// PHP: zend_value_error без префикса функции.
			ctx.Throw(vm.NewThrowable("ValueError", "Array sizes are inconsistent"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
		}
	}
	perm := make([]int, n)
	for i := range perm {
		perm[i] = i
	}
	// Zend конвертирует элементы в строку при SORT_STRING/SORT_NATURAL: объект
	// без __toString — Error (array_multisort_exception).
	for ci := range cols {
		c := &cols[ci]
		if f := c.flags &^ 8; f != 2 && f != 6 {
			continue
		}
		for _, e := range c.elems {
			v := derefSlot(e.v)
			if v.Type() != vm.ObjectType {
				continue
			}
			if _, ok := objectToStringVia(ctx, v); ok {
				continue
			}
			ci2 := ctx.VM().SlotClassInfo(v)
			name := "object"
			if ci2 != nil {
				name = vm.ClassDisplayName(ci2.Name())
			}
			ctx.Throw(vm.NewThrowable("Error", "Object of class "+name+" could not be converted to string"))
			if ctx.HasThrown() {
				return *new(vm.Value)
			}
			return vm.Bool(false)
		}
	}
	sort.SliceStable(perm, func(i, j int) bool {
		pi, pj := perm[i], perm[j]
		for ci := range cols {
			c := &cols[ci]
			var cv int
			switch {
			case pi < len(c.elems) && pj < len(c.elems):
				cv = compareWithFlags(ctx, c.elems[pi].v, c.elems[pj].v, c.flags)
			case pi < len(c.elems):
				cv = -1
			case pj < len(c.elems):
				cv = 1
			}
			if cv != 0 {
				if c.desc {
					cv = -cv
				}
				return cv < 0
			}
		}
		return false
	})
	for ci := range cols {
		c := &cols[ci]
		na := vm.NewArray()
		for _, idx := range perm {
			if idx >= len(c.elems) {
				continue
			}
			e := c.elems[idx]
			if e.k.IsStr() {
				na.SetStr(e.k.StrVal(), e.v)
			} else {
				na.Append(e.v)
			}
		}
		c.set(na)
	}
	return vm.Bool(true)
}

// --- callable-версии array_diff / array_intersect ---
//
// Общий движок для семейства. Используются либо пользовательские компараторы
// (возврат 0 == «равно»), либо встроенные правила: значения сравниваются по
// строковому представлению, ключи — по точному совпадению (уже каноническому).

// phpCmpInt приводит результат пользовательского компаратора к int по
// правилам PHP (zval_get_long): bool -> 0/1, массив -> (непустой ? 1 : 0),
// объект -> 1, null -> 0, прочее — обычное числовое приведение.
func phpCmpInt(s vm.Slot) int64 {
	switch s.Type() {
	case vm.NullType:
		return 0
	case vm.BoolType:
		if s.ToBool() {
			return 1
		}
		return 0
	case vm.ArrayType:
		if s.AsArray().Len() > 0 {
			return 1
		}
		return 0
	case vm.ObjectType, vm.ClosureType:
		return 1
	}
	return s.ToInt()
}

// callCmpEq вызывает компаратор и сообщает, равны ли значения (результат 0).
func callCmpEq(ctx *vm.Context, cb, a, b vm.Slot) bool {
	res, err := ctx.VMInstance().CallCallable(ctx, cb, []vm.Slot{a, b})
	throwCallable(ctx, err)
	if ctx.HasThrown() {
		return *new(bool)
	}
	return phpCmpInt(res) == 0
}

func setOpValueEq(ctx *vm.Context, valCb, a, b vm.Slot) bool {
	if !valCb.IsNull() {
		return callCmpEq(ctx, valCb, a, b)
	}
	return a.ToString() == b.ToString()
}

func setOpKeyEq(ctx *vm.Context, keyCb vm.Slot, a, b vm.ArrayKey) bool {
	if !keyCb.IsNull() {
		return callCmpEq(ctx, keyCb, arrayKeySlot(a), arrayKeySlot(b))
	}
	if a.IsStr() != b.IsStr() {
		return false
	}
	if a.IsStr() {
		return a.StrVal() == b.StrVal()
	}
	return a.IntVal() == b.IntVal()
}

// runSetOp выполняет diff (intersect=false) или intersect (true).
//   - useValues/useKeys — какие поля сравнивать;
//   - valCb/keyCb — пользовательские компараторы или null (встроенные правила).
func runSetOp(ctx *vm.Context, arrays []*vm.Array, valCb, keyCb vm.Slot, useValues, useKeys, intersect bool) vm.Value {
	out := vm.NewArray()
	if len(arrays) == 0 || !useValues && !useKeys {
		return vm.MakeArray(out)
	}
	first := arrays[0]
	others := arrays[1:]
	matches := func(o *vm.Array, k vm.ArrayKey, v vm.Slot) bool {
		for k2, v2 := range o.All() {
			if useKeys && !setOpKeyEq(ctx, keyCb, k, k2) {
				continue
			}
			if useValues && !setOpValueEq(ctx, valCb, v, v2) {
				continue
			}
			return true
		}
		return false
	}
	for k, v := range first.All() {
		present := false
		if !intersect {
			for _, o := range others {
				if matches(o, k, v) {
					present = true
					break
				}
			}
			if !present {
				out.Set(k, derefLoneRef(ctx, v))
			}
			continue
		}
		inAll := true
		for _, o := range others {
			if !matches(o, k, v) {
				inAll = false
				break
			}
		}
		if inAll {
			out.Set(k, derefLoneRef(ctx, v))
		}
	}
	return vm.MakeArray(out)
}

// splitArraysArgs возвращает массивы (все array-аргументы, кроме последних
// nCallbacks) и сами callbacks в порядке следования. Проверяет типы: сначала
// массивы (как PHP — слева направо), затем валидность callbacks.
func splitArraysArgs(ctx *vm.Context, nCallbacks int, fn string) ([]*vm.Array, []vm.Slot) {
	args := ctx.Args()
	if len(args) < nCallbacks+1 {
		ctx.Throw(vm.NewThrowable("ArgumentCountError",
			fmt.Sprintf("%s() expects at least %d arguments, %d given", fn, nCallbacks+1, len(args))))
		if ctx.HasThrown() {
			return *new([]*vm.Array), *new([]vm.Slot)
		}
	}
	nArr := len(args) - nCallbacks
	// PHP сначала проверяет trailing callable-аргументы, затем массивы.
	cbs := make([]vm.Slot, nCallbacks)
	copy(cbs, args[nArr:])
	for j := 0; j < nCallbacks; j++ {
		requireCallableArg(ctx, nArr+j, fn)
		if ctx.HasThrown() {
			return *new([]*vm.Array), *new([]vm.Slot)
		}
	}
	arrays := make([]*vm.Array, 0, nArr)
	for i := 0; i < nArr; i++ {
		if args[i].Type() != vm.ArrayType {
			arrayArgError(ctx, i, fn)
			if ctx.HasThrown() {
				return *new([]*vm.Array), *new([]vm.Slot)
			}
		}
		arrays = append(arrays, args[i].AsArray())
	}
	return arrays, cbs
}

func arrayUdiffFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_udiff")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, cbs[0], vm.MakeNull(), true, false, false)
}

func arrayUdiffAssocFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_udiff_assoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, cbs[0], vm.MakeNull(), true, true, false)
}

func arrayUdiffUassocFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 2, "array_udiff_uassoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	// cbs[0] — value_compare_func, cbs[1] — key_compare_func.
	return runSetOp(ctx, arrs, cbs[0], cbs[1], true, true, false)
}

func arrayDiffUassocFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_diff_uassoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, vm.MakeNull(), cbs[0], true, true, false)
}

func arrayDiffUkeyFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_diff_ukey")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, vm.MakeNull(), cbs[0], false, true, false)
}

func arrayUintersectFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_uintersect")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, cbs[0], vm.MakeNull(), true, false, true)
}

func arrayUintersectAssocFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_uintersect_assoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, cbs[0], vm.MakeNull(), true, true, true)
}

func arrayUintersectUassocFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 2, "array_uintersect_uassoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, cbs[0], cbs[1], true, true, true)
}

func arrayIntersectUassocFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_intersect_uassoc")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, vm.MakeNull(), cbs[0], true, true, true)
}

func arrayIntersectUkeyFn(ctx *vm.Context) vm.Value {
	arrs, cbs := splitArraysArgs(ctx, 1, "array_intersect_ukey")
	if ctx.HasThrown() {
		return *new(vm.Value)
	}
	return runSetOp(ctx, arrs, vm.MakeNull(), cbs[0], false, true, true)
}
