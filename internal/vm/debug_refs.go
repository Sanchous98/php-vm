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
