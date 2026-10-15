package vm

import "math"

// Медленные пути арифметики, помеченные noinline.
//
// Они содержат полную семантику PHP для «редких» случаев: строки, массивы,
// ошибки. Помечены noinline, чтобы (а) не раздувать dispatch-цикл, (б) не
// мешать инлайну горячих путей, которые пишутся прямо в case.
//
// Горячие пути (int-домен, float) НЕ здесь, а inline в vm.go, потому что
// вынесенная функция (даже маленькая) не инлайнится при такой сложности и
// стоит ~2 ns на вызов. Это подтверждено замером.

//go:noinline
func (vm *VM) addSlow(a, b Slot) (Slot, error) {
	if _, _, err := vm.coerceArith(a, b, "+"); err != nil {
		return Slot{}, err
	}
	return Add(a, b), nil
}

// compoundSlow применяет бинарную операцию составного присваивания по опкоду
// операции (OpAppendCompound; op — OpAdd/OpSub/.../OpConcatApp).
func (vm *VM) compoundSlow(op Opcode, a, b Slot) (Slot, error) {
	switch op {
	case OpAdd:
		return vm.addSlow(a, b)
	case OpSub:
		return vm.subSlow(a, b)
	case OpMul:
		return vm.mulSlow(a, b)
	case OpDiv:
		return vm.divSlow(a, b)
	case OpMod:
		return vm.modSlow(a, b)
	case OpPow:
		return vm.powSlow(a, b)
	case OpConcat, OpConcatApp:
		ls, err := vm.stringify(a)
		if err != nil {
			return Slot{}, err
		}
		rs, err := vm.stringify(b)
		if err != nil {
			return Slot{}, err
		}
		return MakeString(ls + rs), nil
	case OpBitAnd:
		return vm.bitwiseSlow(a, b, bitAnd)
	case OpBitOr:
		return vm.bitwiseSlow(a, b, bitOr)
	case OpBitXor:
		return vm.bitwiseSlow(a, b, bitXor)
	case OpShl:
		return vm.shiftSlow(a, b, true)
	case OpShr:
		return vm.shiftSlow(a, b, false)
	}
	return Slot{}, NewThrowable("Error", "unsupported compound operation")
}

//go:noinline
func (vm *VM) subSlow(a, b Slot) (Slot, error) {
	if _, _, err := vm.coerceArith(a, b, "-"); err != nil {
		return Slot{}, err
	}
	return Sub(a, b), nil
}

//go:noinline
func (vm *VM) mulSlow(a, b Slot) (Slot, error) {
	if _, _, err := vm.coerceArith(a, b, "*"); err != nil {
		return Slot{}, err
	}
	return Mul(a, b), nil
}

//go:noinline
func (vm *VM) divSlow(a, b Slot) (Slot, error) {
	if _, _, err := vm.coerceArith(a, b, "/"); err != nil {
		return Slot{}, err
	}
	return Div(a, b)
}

//go:noinline
func (vm *VM) modSlow(a, b Slot) (Slot, error) {
	if _, _, err := vm.coerceArith(a, b, "%"); err != nil {
		return Slot{}, err
	}
	vm.floatToIntWarn(a)
	vm.floatToIntWarn(b)
	return Mod(a, b)
}

// FloatToIntWarn — экспорт floatToIntWarn для встроенных функций, приводящих
// аргумент к int самостоятельно.
func (vm *VM) FloatToIntWarn(s Slot) { vm.floatToIntWarn(s) }

// floatToIntWarn эмитит Deprecated, если float-операнд неявно приводится к int.
// Для числовой строки Zend использует формулировку float-string.
func (vm *VM) floatToIntWarn(s Slot) {
	switch s.typ {
	case FloatType:
		f := s.ToFloat()
		if floatIntExact(f) {
			return
		}
		vm.Deprecated("Implicit conversion from float " + phpFloatShort(f) + " to int loses precision")
	case StringType:
		str := s.StringVal()
		f, n, ok := parseLeadingNumeric(str)
		if !ok || n == 0 {
			return
		}
		// Целочисленная строка длиной до 19 знаков (в т.ч. выходящая за
		// signed int64) разбирается Zend без предупреждения.
		if intPrefixNoPrecisionLoss(str) {
			return
		}
		if floatIntExact(f) {
			return
		}
		vm.Deprecated(`Implicit conversion from float-string "` + str + `" to int loses precision`)
	}
}

// intPrefixNoPrecisionLoss сообщает, что ведущий числовой префикс строки —
// целое (без '.', 'e', 'E') не длиннее 19 знаков: Zend разбирает такие строки
// без сообщения об утере точности (значение может насыщаться до int64).
func intPrefixNoPrecisionLoss(str string) bool {
	prefix, _, ok := numericPrefix(str)
	if !ok {
		return false
	}
	digits := 0
	for i := 0; i < len(prefix); i++ {
		c := prefix[i]
		switch {
		case c == '+' || c == '-':
			continue
		case c == '.' || c == 'e' || c == 'E':
			return false
		case c >= '0' && c <= '9':
			digits++
		default:
			return false
		}
	}
	return digits > 0 && digits <= 19
}

// floatIntExact сообщает, что float точно представим как int64 (неявное
// приведение не теряет точность).
func floatIntExact(f float64) bool {
	return math.Trunc(f) == f && f >= -9223372036854775808.0 && f < 9223372036854775808.0
}

// deprecateFloatToInt эмитит Deprecated о неявном float->int, приписывая его
// строке объявления функции line (как Zend), а не месту вызова.
func (vm *VM) deprecateFloatToInt(line int, s Slot) {
	if line <= 0 {
		line = 1
	}
	vm.diagLineOverride = line
	vm.floatToIntWarn(s)
	vm.diagLineOverride = 0
}

//go:noinline
func (vm *VM) powSlow(a, b Slot) (Slot, error) {
	if _, _, err := vm.coerceArith(a, b, "**"); err != nil {
		return Slot{}, err
	}
	return Pow(a, b), nil
}

//go:noinline
func negSlow(a Slot) Slot { return Neg(a) }

//go:noinline
func (vm *VM) notSlow(a Slot) Slot {
	// Логическое отрицание не приводит float к int (приведение к int —
	// только у ~, он идёт через OpBitNot), поэтому Deprecated здесь не бывает.
	return Not(a)
}

//go:noinline
func (vm *VM) bitwiseSlow(a, b Slot, op byte) (Slot, error) {
	// PHP 8.1+: неявное float->int в битовых операциях — Deprecated. Если оба
	// операнда — строки, Zend выполняет побитовую операцию над строками и
	// приведения к int не происходит.
	if !(deref(a).typ == StringType && deref(b).typ == StringType) {
		vm.floatToIntWarn(a)
		vm.floatToIntWarn(b)
		vm.warnNonNumericBitwise(a)
		vm.warnNonNumericBitwise(b)
	}
	return BitwiseBinary(a, b, op)
}

// warnNonNumericBitwise предупреждает о ведуще-числовой (но не полностью
// числовой) строке при приведении к int в битовых операциях:
// Warning "A non-numeric value encountered" (Zend, zend_operators).
func (vm *VM) warnNonNumericBitwise(s Slot) {
	s = deref(s)
	if s.typ != StringType {
		return
	}
	v := s.StringVal()
	if IsNumericString(v) {
		return
	}
	if _, _, ok := numericPrefix(v); ok {
		vm.Warn("A non-numeric value encountered")
	}
}

//go:noinline
func (vm *VM) bitNotSlow(a Slot) (Slot, error) {
	if deref(a).typ == FloatType {
		vm.floatToIntWarn(a)
	}
	return BitNot(a)
}

//go:noinline
func (vm *VM) shiftSlow(a, b Slot, left bool) (Slot, error) {
	// PHP 8.1+: неявное float->int в сдвигах — Deprecated.
	vm.floatToIntWarn(a)
	vm.floatToIntWarn(b)
	vm.warnNonNumericBitwise(a)
	vm.warnNonNumericBitwise(b)
	if left {
		return ShiftLeft(a, b)
	}
	return ShiftRight(a, b)
}

//go:noinline
func equalSlow(vm *VM, a, b Slot) Slot { return vm.EqualValues(a, b) }

//go:noinline
func identicalSlow(a, b Slot) Slot { return Identical(a, b) }

//go:noinline
func spaceshipSlow(vm *VM, a, b Slot) Slot {
	a, b = deref(a), deref(b)
	// PHP: любое сравнение с NaN через <=> даёт 1.
	if (a.typ == FloatType && math.IsNaN(a.Float())) ||
		(b.typ == FloatType && math.IsNaN(b.Float())) {
		return MakeInt(1)
	}
	return MakeInt(int64(compareSlots(vm, a, b)))
}

//go:noinline
func compareSlow2(vm *VM, op Opcode, a, b Slot) Slot {
	switch op {
	case OpEqual:
		return vm.EqualValues(a, b)
	case OpNotEqual:
		return MakeBool(!looseEqual(vm, a, b))
	case OpIdentical:
		return Identical(a, b)
	case OpNotIdentical:
		return NotIdentical(a, b)
	case OpLess:
		return LessVM(vm, a, b)
	case OpLessOrEqual:
		return LessOrEqualVM(vm, a, b)
	case OpGreater:
		return GreaterVM(vm, a, b)
	case OpGreaterEqual:
		return GreaterEqualVM(vm, a, b)
	}
	return MakeNull()
}

// --- Вспомогательные проверки для inline-путей ---

// numIntDomain — операнд в целочисленном домене PHP (int или bool).
func numIntDomain(s Slot) bool { return s.typ == IntType || s.typ == BoolType }

// boolOf — истинность слота для типов, не требующих heap (null/bool/int/float).
func boolOf(s Slot) bool {
	switch s.typ {
	case NullType:
		return false
	case BoolType:
		return s.num != 0
	case IntType:
		return s.num != 0
	case FloatType:
		return math.Float64frombits(s.num) != 0
	}
	return true
}

// numToFloat — числовое значение слота int-домена или float, без heap.
func numToFloat(s Slot) float64 {
	switch s.typ {
	case BoolType:
		return float64(s.num)
	case IntType:
		return float64(int64(s.num))
	case FloatType:
		return math.Float64frombits(s.num)
	}
	return 0
}
