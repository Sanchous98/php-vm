package vm

import "math"
import "sort"
import "strconv"
import "strings"

// Арифметика и сравнения над Slot. Семантика — PHP 8.x, сверяется с живым php.
//
// Операции не нуждаются в heap: скаляры лежат в Slot.num, а строки адресуются
// указателем в Slot.ptr и читаются через StringVal. Числовые fast-path
// (int/float) heap НЕ трогают и не аллоцируют.

// intDomain сообщает, что операнд участвует в целочисленной арифметике PHP:
// int или bool (bool приводится к 0/1, результат остаётся целым).
func intDomain(s Slot) bool {
	return s.typ == IntType || s.typ == BoolType || s.typ == NullType
}

// intVal возвращает целое значение операнда целочисленного домена.
func intVal(s Slot) int64 { return int64(s.num) }

// arithOperand приводит строковый операнд арифметики к числовому слоту по
// правилам PHP: целочисленный numeric-префикс -> int, иначе -> float.
// Полностью не-числовая строка -> 0 (как ведущий префикс).
func arithOperand(s Slot) Slot {
	if s.typ != StringType {
		return s
	}
	pfx, _, ok := numericPrefix(s.StringVal())
	if !ok {
		return MakeInt(0)
	}
	if !strings.ContainsAny(pfx, ".eE") {
		if i, err := strconv.ParseInt(pfx, 10, 64); err == nil {
			return MakeInt(i)
		}
	}
	if f, err := strconv.ParseFloat(pfx, 64); err == nil {
		return MakeFloat(f)
	}
	return MakeInt(0)
}

// coerceArith приводит операнды арифметической операции (+, -, *, /, %) к
// числам по правилам PHP 8. Полностью нечисловая строка -> TypeError
// "Unsupported operand types: X op Y". Ведуще-числовая (но не вполне числовая)
// строка -> число с Warning "A non-numeric value encountered".
func (vm *VM) coerceArith(a, b Slot, op string) (Slot, Slot, error) {
	a, b = deref(a), deref(b)
	// Массивы: `+` — объединение массивов (union), для остальных операций —
	// TypeError. Объекты и ресурсы в арифметике недопустимы (PHP 8):
	// "Unsupported operand types".
	if a.typ == ArrayType && b.typ == ArrayType && op == "+" {
		return a, b, nil
	}
	if a.typ == ResourceType || b.typ == ResourceType ||
		a.typ == ArrayType || b.typ == ArrayType ||
		a.typ == ObjectType || b.typ == ObjectType {
		return Slot{}, Slot{}, bitwiseTypeError(a, b, op)
	}
	aBad := a.typ == StringType && !IsNumericString(a.StringVal())
	bBad := b.typ == StringType && !IsNumericString(b.StringVal())
	if aBad || bBad {
		aNo := aBad
		if aNo {
			_, _, aNo = numericPrefix(a.StringVal())
			aNo = !aNo
		}
		bNo := bBad
		if bNo {
			_, _, bNo = numericPrefix(b.StringVal())
			bNo = !bNo
		}
		if aNo || bNo {
			return Slot{}, Slot{}, bitwiseTypeError(a, b, op)
		}
		vm.Warn("A non-numeric value encountered")
	}
	return arithOperand(a), arithOperand(b), nil
}

// Neg — унарный минус. PHP для нечисловой строки бросает
// "Unsupported operand types: string * int", для ведуще-числовой — Warning.
func (vm *VM) Neg(a Slot) (Slot, error) {
	ca, _, err := vm.coerceArith(a, MakeInt(0), "*")
	if err != nil {
		return Slot{}, err
	}
	if ca.typ == IntType {
		i := int64(ca.num)
		if i == math.MinInt64 {
			return MakeFloat(-float64(i)), nil
		}
		return MakeInt(-i), nil
	}
	return MakeFloat(-ca.ToFloat()), nil
}

// Add — оператор +. Для двух массивов — объединение (union): ключи правого
// добавляются, если их нет в левом.
func Add(a, b Slot) Slot {
	a, b = deref(a), deref(b)
	if a.typ == ArrayType && b.typ == ArrayType {
		return MakeArray(arrayUnion(a.AsArray(), b.AsArray()))
	}
	a, b = arithOperand(a), arithOperand(b)
	if intDomain(a) && intDomain(b) {
		ai, bi := intVal(a), intVal(b)
		if r, ok := addInt64(ai, bi); ok {
			return MakeInt(r)
		}
		return MakeFloat(float64(ai) + float64(bi))
	}
	return MakeFloat(a.ToFloat() + b.ToFloat())
}

// arrayUnion — объединение массивов оператором `+`: результат содержит все
// элементы левого, затем отсутствующие в нём элементы правого (порядок PHP).
func arrayUnion(x, y *Array) *Array {
	if x == nil {
		x = newArray()
	}
	out := x.Unshare()
	if y == nil {
		return out
	}
	for k, v := range y.All() {
		if _, exists := out.Get(k); exists {
			continue
		}
		_ = out.Set(k, v)
	}
	return out
}

// Sub — оператор -.
func Sub(a, b Slot) Slot {
	a, b = arithOperand(deref(a)), arithOperand(deref(b))
	if intDomain(a) && intDomain(b) {
		ai, bi := intVal(a), intVal(b)
		if r, ok := subInt64(ai, bi); ok {
			return MakeInt(r)
		}
		return MakeFloat(float64(ai) - float64(bi))
	}
	return MakeFloat(a.ToFloat() - b.ToFloat())
}

// Mul — оператор *.
func Mul(a, b Slot) Slot {
	a, b = arithOperand(deref(a)), arithOperand(deref(b))
	if intDomain(a) && intDomain(b) {
		ai, bi := intVal(a), intVal(b)
		if r, ok := mulInt64(ai, bi); ok {
			return MakeInt(r)
		}
		return MakeFloat(float64(ai) * float64(bi))
	}
	return MakeFloat(a.ToFloat() * b.ToFloat())
}

// Div — оператор /. PHP: int, если целочисленные операнды и делится нацело.
func Div(a, b Slot) (Slot, error) {
	a, b = arithOperand(deref(a)), arithOperand(deref(b))
	bf := b.ToFloat()
	if bf == 0 {
		return Slot{}, errDivByZero
	}
	if intDomain(a) && intDomain(b) {
		ai, bi := intVal(a), intVal(b)
		if ai == math.MinInt64 && bi == -1 {
			return MakeFloat(float64(ai) / float64(bi)), nil // переполнение -> float
		}
		if ai%bi == 0 {
			return MakeInt(ai / bi), nil
		}
	}
	return MakeFloat(a.ToFloat() / bf), nil
}

// Mod — оператор %. PHP: только целые (floats приводятся к int).
func Mod(a, b Slot) (Slot, error) {
	a, b = deref(a), deref(b)
	bi := b.ToInt()
	if bi == 0 {
		return Slot{}, errModByZero
	}
	return MakeInt(a.ToInt() % bi), nil
}

// Pow — оператор **.
func Pow(a, b Slot) Slot {
	a, b = deref(a), deref(b)
	if intDomain(a) && intDomain(b) {
		base, exp := intVal(a), intVal(b)
		if exp >= 0 {
			if r, ok := powInt64(base, exp); ok {
				return MakeInt(r)
			}
			r := PowPrecise(float64(base), float64(exp))
			if base < 0 && exp&1 == 1 && exp > (1<<53) {
				r = -r
			}
			return MakeFloat(r)
		}
	}
	// libm-совместимое округление (PHP-эталон — glibc): Go math.Pow может
	// отличаться на 1 ulp (pow(27, 1/3)).
	return MakeFloat(PowPrecise(a.ToFloat(), b.ToFloat()))
}

// PowOp — оператор ** с полным приведением операндов (экспорт для встроенной
// pow(), которая в Zend использует ту же семантику оператора).
func (vm *VM) PowOp(a, b Slot) (Slot, error) { return vm.powSlow(a, b) }

// powInt64 — точное возведение в неотрицательную степень с обнаружением
// переполнения (exponentiation by squaring, O(log exp)).
func powInt64(base, exp int64) (int64, bool) {
	result := int64(1)
	for exp > 0 {
		if exp&1 == 1 {
			r, ok := mulInt64(result, base)
			if !ok {
				return 0, false
			}
			result = r
		}
		exp >>= 1
		if exp > 0 {
			r, ok := mulInt64(base, base)
			if !ok {
				return 0, false
			}
			base = r
		}
	}
	return result, true
}

// Neg — унарный минус.
func Neg(a Slot) Slot {
	a = deref(a)
	if a.typ == IntType {
		if a.Int() == math.MinInt64 {
			return MakeFloat(-float64(a.Int()))
		}
		return MakeInt(-a.Int())
	}
	return MakeFloat(-a.ToFloat())
}

// Not — логическое !.
func Not(a Slot) Slot { return MakeBool(!a.ToBool()) }

// Concat — оператор . (строка).
func Concat(a, b Slot) Slot { return MakeString(a.ToString() + b.ToString()) }

// --- Сравнения ---

func Equal(a, b Slot) Slot { return MakeBool(looseEqual(nil, a, b)) }

// EqualValues — равенство с VM-контекстом (для Stringable-объектов: PHP
// сравнивает их как строки, вызывая __toString; ошибки метода при этом
// игнорируются, как при `==`).
func (vm *VM) EqualValues(a, b Slot) Slot { return MakeBool(looseEqual(vm, a, b)) }

func NotEqual(a, b Slot) Slot {
	return MakeBool(!looseEqual(nil, a, b))
}
func Identical(a, b Slot) Slot { return MakeBool(identical(a, b)) }
func NotIdentical(a, b Slot) Slot {
	return MakeBool(!identical(a, b))
}
func Less(a, b Slot) Slot { return LessVM(nil, a, b) }

func LessVM(vm *VM, a, b Slot) Slot {
	if nanOperand(a) || nanOperand(b) {
		return MakeBool(false)
	}
	if _, inv := enumRelCompare(a, b); inv {
		return MakeBool(false)
	}
	return MakeBool(compareSlots(vm, a, b) < 0)
}
func LessOrEqual(a, b Slot) Slot { return LessOrEqualVM(nil, a, b) }

func LessOrEqualVM(vm *VM, a, b Slot) Slot {
	if nanOperand(a) || nanOperand(b) {
		return MakeBool(false)
	}
	if eq, inv := enumRelCompare(a, b); inv {
		return MakeBool(eq)
	}
	return MakeBool(compareSlots(vm, a, b) <= 0)
}
func Greater(a, b Slot) Slot { return GreaterVM(nil, a, b) }

func GreaterVM(vm *VM, a, b Slot) Slot {
	if nanOperand(a) || nanOperand(b) {
		return MakeBool(false)
	}
	if _, inv := enumRelCompare(a, b); inv {
		return MakeBool(false)
	}
	return MakeBool(compareSlots(vm, a, b) > 0)
}
func GreaterEqual(a, b Slot) Slot { return GreaterEqualVM(nil, a, b) }

func GreaterEqualVM(vm *VM, a, b Slot) Slot {
	if nanOperand(a) || nanOperand(b) {
		return MakeBool(false)
	}
	if eq, inv := enumRelCompare(a, b); inv {
		return MakeBool(eq)
	}
	return MakeBool(compareSlots(vm, a, b) >= 0)
}

// enumRelCompare — специальные правила сравнения enum (Zend): <, > всегда
// false, <=, >= — true только для одного и того же кейса. involved=false,
// если ни один операнд не является enum-кейсом.
func enumRelCompare(a, b Slot) (eq, involved bool) {
	aa, bb := deref(a), deref(b)
	ea, eb := enumCaseObj(aa), enumCaseObj(bb)
	if !ea && !eb {
		return false, false
	}
	if ea && eb {
		return aa.obj() == bb.obj(), true
	}
	return false, true
}

// enumCaseObj сообщает, что слот — объект-кейс перечисления.
func enumCaseObj(s Slot) bool {
	if s.typ != ObjectType {
		return false
	}
	o := s.obj()
	return o != nil && o.class != nil && o.class.IsEnum()
}

// nanOperand сообщает, что слот — это float NaN (сравнения с NaN дают false).
func nanOperand(s Slot) bool {
	s = deref(s)
	return s.typ == FloatType && math.IsNaN(s.Float())
}

// --- Реализация сравнений ---

func looseEqual(vm *VM, a, b Slot) bool {
	a, b = deref(a), deref(b)
	if a.typ == BoolType || b.typ == BoolType {
		return a.ToBool() == b.ToBool()
	}
	if a.typ == NullType && b.typ == NullType {
		return true
	}
	if a.typ == NullType || b.typ == NullType {
		other := a
		if a.typ == NullType {
			other = b
		}
		switch other.typ {
		case StringType:
			return other.StringVal() == ""
		case IntType:
			return other.Int() == 0
		case FloatType:
			return other.Float() == 0
		case ArrayType:
			return other.AsArray().Len() == 0
		}
		return false
	}
	if a.typ == ResourceType || b.typ == ResourceType {
		if a.typ == ResourceType && b.typ == ResourceType {
			return a.ptr == b.ptr
		}
		return a.ToInt() == b.ToInt()
	}
	if a.typ == ObjectType || b.typ == ObjectType {
		if a.typ == ObjectType && b.typ == ObjectType {
			return compareObjects(vm, a, b) == 0
		}
		return compareSlots(vm, a, b) == 0
	}
	if a.typ == ArrayType || b.typ == ArrayType {
		if a.typ == ArrayType && b.typ == ArrayType {
			return arraysEqual(vm, a.AsArray(), b.AsArray())
		}
		return false
	}
	aNum := numericCompare(a)
	bNum := numericCompare(b)
	if aNum && bNum {
		return a.ToFloat() == b.ToFloat()
	}
	if a.typ == StringType && b.typ == StringType {
		return a.StringVal() == b.StringVal()
	}
	// Число против нечисловой строки: в PHP 8 число приводится к строке.
	if a.typ == IntType || a.typ == FloatType {
		return a.ToString() == b.StringVal()
	}
	if b.typ == IntType || b.typ == FloatType {
		return a.StringVal() == b.ToString()
	}
	return a.ToString() == b.ToString()
}

// arraysEqual — нестрогое равенство массивов (==): одинаковый набор пар
// ключ-значение, порядок не важен.
func arraysEqual(vm *VM, x, y *Array) bool {
	if cmpDepth >= cmpDepthMax {
		return false
	}
	cmpDepth++
	defer func() { cmpDepth-- }()
	if x.Len() != y.Len() {
		return false
	}
	for k, v := range x.All() {
		w, ok := y.Get(k)
		if !ok || !looseEqual(vm, v, w) {
			return false
		}
	}
	return true
}

func identical(a, b Slot) bool {
	a, b = deref(a), deref(b)
	if a.typ != b.typ {
		return false
	}
	switch a.typ {
	case NullType:
		return true
	case BoolType, IntType:
		return a.num == b.num
	case FloatType:
		return a.Float() == b.Float()
	case StringType:
		return a.StringVal() == b.StringVal()
	case ObjectType:
		return a.ptr == b.ptr
	case ArrayType:
		return arraysIdentical(a.AsArray(), b.AsArray())
	case ClosureType, ResourceType:
		return a.ptr == b.ptr
	}
	return false
}

// arraysIdentical — тождество массивов (===): одинаковые пары ключ-значение в
// том же порядке.
func arraysIdentical(x, y *Array) bool {
	if x.Len() != y.Len() {
		return false
	}
	type kv struct {
		k ArrayKey
		v Slot
	}
	xa := make([]kv, 0, x.Len())
	for k, v := range x.All() {
		xa = append(xa, kv{k, v})
	}
	i := 0
	for k, v := range y.All() {
		if i >= len(xa) || xa[i].k != k || !identical(xa[i].v, v) {
			return false
		}
		i++
	}
	return true
}

// compareSlots возвращает -1/0/1 по правилам упорядочивания PHP.
func compareSlots(vm *VM, a, b Slot) int {
	a, b = deref(a), deref(b)
	if a.typ == BoolType || b.typ == BoolType {
		ab, bb := a.ToBool(), b.ToBool()
		switch {
		case ab == bb:
			return 0
		case !ab:
			return -1
		default:
			return 1
		}
	}
	if a.typ == NullType || b.typ == NullType {
		if a.typ == NullType && b.typ == NullType {
			return 0
		}
		// null сравнивается с любым значением как bool (null -> false).
		if a.typ == NullType {
			if b.ToBool() {
				return -1
			}
			return 0
		}
		if a.ToBool() {
			return 1
		}
		return 0
	}
	// Объекты: два объекта одного класса сравниваются по свойствам; объект с
	// не-объектом приводится к типу правого операнда (PHP 8).
	if a.typ == ObjectType && b.typ == ObjectType {
		return compareObjects(vm, a, b)
	}
	if a.typ == ObjectType || b.typ == ObjectType {
		objLHS := a.typ == ObjectType
		obj := a
		other := b
		if !objLHS {
			obj, other = b, a
		}
		switch other.typ {
		case StringType:
			// Stringable-объект сравнивается со строкой как строка
			// (__toString вызывается; при исключении в нём Zend считает
			// объект больше — как для не-Stringable).
			if vm != nil {
				if s, ok := vm.tryToString(obj); ok {
					if objLHS {
						return compareSlots(vm, MakeString(s), b)
					}
					return compareSlots(vm, a, MakeString(s))
				}
			}
		case ArrayType:
			// Zend: объект всегда больше массива (независимо от содержимого).
			if objLHS {
				return 1
			}
			return -1
		case IntType, FloatType:
			// Zend: сравнение объекта с числом приводит объект к числу,
			// печатая Notice (в отличие от явного каста — там Warning).
			if vm != nil {
				if other.typ == FloatType {
					vm.Notice("Object of class " + vm.slotClassName(obj) + " could not be converted to float")
				} else {
					vm.Notice("Object of class " + vm.slotClassName(obj) + " could not be converted to int")
				}
			}
			one := MakeInt(1)
			if other.typ == FloatType {
				one = MakeFloat(1)
			}
			if objLHS {
				return compareSlots(vm, one, other)
			}
			return compareSlots(vm, other, one)
		}
		if objLHS {
			return 1
		}
		return -1
	}
	// Массивы: сравнение по количеству элементов, затем поэлементно. Массив
	// всегда больше не-массива (PHP).
	if a.typ == ArrayType || b.typ == ArrayType {
		if a.typ == ArrayType && b.typ == ArrayType {
			return compareArrays(vm, a, b)
		}
		if a.typ == ArrayType {
			return 1
		}
		return -1
	}
	if a.typ == StringType && b.typ == StringType {
		if IsNumericString(a.StringVal()) && IsNumericString(b.StringVal()) {
			return cmpFloat(a.ToFloat(), b.ToFloat())
		}
		return strCompare(a.StringVal(), b.StringVal())
	}
	if numericCompare(a) && numericCompare(b) {
		return cmpFloat(a.ToFloat(), b.ToFloat())
	}
	return strCompare(a.ToString(), b.ToString())
}

// numericCompare сообщает, что слот участвует в числовом сравнении: число
// (int/float) либо числовая строка.
func numericCompare(s Slot) bool {
	switch s.typ {
	case IntType, FloatType:
		return true
	case StringType:
		return IsNumericString(s.StringVal())
	}
	return false
}

// compareObjects сравнивает два объекта по правилам PHP: один и тот же объект
// равен; разные классы несравнимы (1); иначе — поэлементное сравнение свойств в
// порядке объявления.
// cmpDepth — защита от бесконечной рекурсии при сравнении циклических
// структур (объект/массив, ссылающийся на себя). Тесты однопоточные.
var cmpDepth int

const cmpDepthMax = 512

func compareObjects(vm *VM, a, b Slot) int {
	if cmpDepth >= cmpDepthMax {
		return 1
	}
	cmpDepth++
	defer func() { cmpDepth-- }()
	oa, ob := (*Object)(a.ptr), (*Object)(b.ptr)
	if oa == nil || ob == nil {
		return 1
	}
	if oa == ob {
		return 0
	}
	if oa.class != ob.class {
		return 1
	}
	va, da := objectPropSlots(oa)
	vb, db := objectPropSlots(ob)
	if len(va) != len(vb) {
		if len(va) < len(vb) {
			return -1
		}
		return 1
	}
	for i := range va {
		if da[i] != db[i] {
			return 1
		}
		if !da[i] {
			continue
		}
		if c := compareSlots(vm, va[i], vb[i]); c != 0 {
			return c
		}
	}
	return 0
}

// objectPropSlots возвращает значения свойств объекта в порядке PHP и признак
// их инициализированности (uninitialized/unset свойства — false).
func objectPropSlots(o *Object) ([]Slot, []bool) {
	var out []Slot
	var def []bool
	add := func(key string) {
		if v, ok := o.propValueRaw(key); ok {
			out = append(out, v)
			def = append(def, true)
		} else {
			out = append(out, MakeNull())
			def = append(def, false)
		}
	}
	rc, ok := asRuntimeClass(o.class)
	if !ok || rc == nil {
		names := make([]string, 0, len(o.props))
		for k := range o.props {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, name := range names {
			add(name)
		}
		return out, def
	}
	var chain []*runtimeClass
	for k := rc; k != nil; k = k.parent {
		chain = append(chain, k)
	}
	seen := map[string]bool{}
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		for _, name := range c.propOrder {
			if seen[name] {
				continue
			}
			seen[name] = true
			if pd, ok := c.ownProps[name]; ok && pd.vis == Private {
				add(mangledPropKey(c, name))
				continue
			}
			add(name)
		}
	}
	for _, name := range o.dynOrder {
		if !seen[name] {
			if _, ok := o.propValueRaw(name); ok {
				seen[name] = true
				add(name)
			}
		}
	}
	var rest []string
	for name := range o.props {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		add(name)
	}
	return out, def
}

// objectPropsArray строит массив из свойств объекта (для приведения объекта к
// массиву при сравнении).
func objectPropsArray(s Slot) *Array {
	o := (*Object)(s.ptr)
	arr := NewArray()
	if o == nil {
		return arr
	}
	vals, def := objectPropSlots(o)
	// Имена свойств нужны как ключи; повторим обход, чтобы получить пары.
	names := objectPropNames(o)
	for i, name := range names {
		if i < len(vals) && def[i] {
			if n, err := strconv.ParseInt(name, 10, 64); err == nil {
				arr.SetInt(n, vals[i])
			} else {
				arr.SetStr(name, vals[i])
			}
		}
	}
	return arr
}

// objectPropNames возвращает имена свойств объекта в порядке PHP.
func objectPropNames(o *Object) []string {
	rc, ok := asRuntimeClass(o.class)
	if !ok || rc == nil {
		names := make([]string, 0, len(o.props))
		for k := range o.props {
			names = append(names, k)
		}
		sort.Strings(names)
		return names
	}
	var chain []*runtimeClass
	for k := rc; k != nil; k = k.parent {
		chain = append(chain, k)
	}
	var out []string
	seen := map[string]bool{}
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		for _, name := range c.propOrder {
			if seen[name] {
				continue
			}
			seen[name] = true
			if pd, ok := c.ownProps[name]; ok && pd.vis == Private {
				key := mangledPropKey(c, name)
				seen[key] = true
				if _, ok := o.propValueRaw(key); ok {
					out = append(out, name)
				}
				continue
			}
			out = append(out, name)
		}
	}
	for _, name := range o.dynOrder {
		if !seen[name] {
			if _, ok := o.props[name]; ok {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	var rest []string
	for name := range o.props {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	sort.Strings(rest)
	for _, name := range rest {
		if pn, _, ok := splitMangledProp(name); ok {
			out = append(out, pn)
			continue
		}
		out = append(out, name)
	}
	return out
}

func compareArrays(vm *VM, a, b Slot) int {
	aa, ba := a.AsArray(), b.AsArray()
	if aa.Len() != ba.Len() {
		if aa.Len() < ba.Len() {
			return -1
		}
		return 1
	}
	va, vb := aa.Values(), ba.Values()
	for i := range va {
		if c := compareSlots(vm, va[i], vb[i]); c != 0 {
			return c
		}
	}
	return 0
}

func cmpFloat(af, bf float64) int {
	switch {
	case af < bf:
		return -1
	case af > bf:
		return 1
	default:
		return 0
	}
}

func strCompare(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// --- Целочисленная арифметика с обнаружением переполнения ---

func addInt64(a, b int64) (int64, bool) {
	r := a + b
	if (b > 0 && r < a) || (b < 0 && r > a) {
		return 0, false
	}
	return r, true
}

func subInt64(a, b int64) (int64, bool) {
	r := a - b
	if (b < 0 && r < a) || (b > 0 && r > a) {
		return 0, false
	}
	return r, true
}

func mulInt64(a, b int64) (int64, bool) {
	if a == 0 || b == 0 {
		return 0, true
	}
	ua, ub := uint64(a), uint64(b)
	neg := false
	if a < 0 {
		ua = uint64(-(a + 1)) + 1
		neg = !neg
	}
	if b < 0 {
		ub = uint64(-(b + 1)) + 1
		neg = !neg
	}
	hi, lo := mul64(ua, ub)
	if hi != 0 {
		return 0, false
	}
	if neg {
		if lo > 1<<63 {
			return 0, false
		}
		return -int64(lo), true
	}
	if lo > 1<<63-1 {
		return 0, false
	}
	return int64(lo), true
}

// FloatShort — кратчайшее round-trip представление float (экспорт для
// диагностик в пакете php).
func FloatShort(f float64) string { return phpFloatShort(f) }

// phpFloatShort форматирует float как PHP при serialize_precision=-1
// (кратчайшее round-trip представление) — используется в диагностиках.
func phpFloatShort(f float64) string {
	if math.IsInf(f, 0) {
		if f < 0 {
			return "-INF"
		}
		return "INF"
	}
	if math.IsNaN(f) {
		return "NAN"
	}
	if f == 0 {
		if math.Signbit(f) {
			return "-0"
		}
		return "0"
	}
	neg := math.Signbit(f)
	s := strconv.FormatFloat(math.Abs(f), 'e', -1, 64)
	ei := strings.IndexByte(s, 'e')
	exp10, _ := strconv.Atoi(s[ei+1:])
	digits := strings.Replace(s[:ei], ".", "", 1)
	return gcvtFormatShort(digits, exp10+1, neg)
}

func gcvtFormatShort(digits string, decpt int, neg bool) string {
	if digits == "" {
		digits = "0"
	}
	var b strings.Builder
	if neg {
		b.WriteByte('-')
	}
	if (decpt >= 0 && decpt > 17) || decpt < -3 {
		e := decpt - 1
		esign := byte('+')
		if e < 0 {
			esign = '-'
			e = -e
		}
		b.WriteByte(digits[0])
		b.WriteByte('.')
		if len(digits) == 1 {
			b.WriteByte('0')
		} else {
			b.WriteString(digits[1:])
		}
		b.WriteByte('E')
		b.WriteByte(esign)
		b.WriteString(strconv.Itoa(e))
		return b.String()
	}
	if decpt < 0 {
		b.WriteString("0.")
		for i := 0; i < -decpt; i++ {
			b.WriteByte('0')
		}
		b.WriteString(digits)
		return b.String()
	}
	for i := 0; i < decpt; i++ {
		if i < len(digits) {
			b.WriteByte(digits[i])
		} else {
			b.WriteByte('0')
		}
	}
	if decpt == 0 {
		b.WriteByte('0')
	}
	if len(digits) > decpt {
		b.WriteByte('.')
		b.WriteString(digits[decpt:])
	}
	return b.String()
}
