package vm

// DebugRefHolders — число «внешних» ячеек (глобальные переменные, динамические
// локальные и помеченные регистры текущего кадра), напрямую удерживающих ссылку
// r. Используется debug_zval_dump для печати refcount ячейки-ссылки.
func (vm *VM) DebugRefHolders(r *Ref) int {
	if r == nil {
		return 0
	}
	return vm.debugSlotHolders(func(s Slot) bool { return s.typ == RefType && s.Ref() == r })
}

// DebugArrayHolders — то же для массива (refcount массива-аргумента
// debug_zval_dump: переменная-владелец + копия аргумента).
func (vm *VM) DebugArrayHolders(a *Array) int {
	if a == nil {
		return 0
	}
	return vm.debugSlotHolders(func(s Slot) bool { return s.typ == ArrayType && s.AsArray() == a })
}

// debugSlotHolders считает ячейки таблицы переменных и регистров текущего
// кадра, для которых pred(slot) истинно.
func (vm *VM) debugSlotHolders(pred func(Slot) bool) int {
	n := 0
	for k, s := range vm.globals {
		// $GLOBALS — зеркало, не отдельный владелец.
		if k == "$GLOBALS" {
			continue
		}
		if pred(s) {
			n++
		}
	}
	if f := vm.cur; f != nil {
		for k, s := range f.dynLocals {
			if k == "$GLOBALS" {
				continue
			}
			if pred(s) {
				n++
			}
		}
		for i := range f.regs {
			if f.tracked[i>>6]&(uint64(1)<<uint(i&63)) == 0 {
				continue
			}
			if pred(f.regs[i]) {
				n++
			}
		}
	}
	return n
}

// DebugArrayRefCensus — «держатели» массива a в куче: zend_reference-ячейки
// (каждая считается один раз — Zend учитывает саму ячейку, а не число её
// указателей) и прямые ячейки-элементы/свойства контейнеров. Обход идёт от
// слотов кадра (globals/dynLocals/регистры) по массивам и объектам.
// Zend в refcount массива для debug_zval_dump учитывает ровно такие zval'ы
// (плюс holder-слоты, их считает DebugArrayHolders).
func (vm *VM) DebugArrayRefCensus(a *Array) int {
	if a == nil {
		return 0
	}
	n := 0
	refs := map[*Ref]bool{}
	arrs := map[*Array]bool{}
	objs := map[*Object]bool{}
	var walk func(s Slot)
	count := func(s Slot) {
		if s.typ == RefType {
			r := s.Ref()
			if r != nil && !refs[r] {
				refs[r] = true
				if v := deref(r.Value()); v.typ == ArrayType && v.AsArray() == a {
					n++
				}
			}
		} else if s.typ == ArrayType && s.AsArray() == a {
			n++
		}
	}
	walk = func(s Slot) {
		switch s.typ {
		case RefType:
			r := s.Ref()
			if r == nil || refs[r] {
				return
			}
			refs[r] = true
			if v := deref(r.Value()); v.typ == ArrayType && v.AsArray() == a {
				n++
			}
			walk(r.Value())
		case ArrayType:
			arr := s.AsArray()
			if arr == nil || arrs[arr] {
				return
			}
			arrs[arr] = true
			for _, v := range arr.All() {
				count(v)
				walk(v)
			}
		case ObjectType:
			o := vm.SlotObject(s)
			if o == nil || objs[o] {
				return
			}
			objs[o] = true
			o.forEachPropValue(func(v Slot) {
				count(v)
				walk(v)
			})
		}
	}
	for k, s := range vm.globals {
		if k == "$GLOBALS" {
			continue
		}
		walk(s)
	}
	if f := vm.cur; f != nil {
		for k, s := range f.dynLocals {
			if k == "$GLOBALS" {
				continue
			}
			walk(s)
		}
		for i := range f.regs {
			if f.tracked[i>>6]&(uint64(1)<<uint(i&63)) == 0 {
				continue
			}
			walk(f.regs[i])
		}
	}
	return n
}
