package vm

import (
	"strings"
)

// execData.go — глобальные и ссылочные опкоды (global*/ref*).

// staticKeyRemap перенаправляет ключ статической ячейки для копии метода
// трейта (Function.RebindStaticKeys): инструкции несут исходный константный
// ключ, а значение копии живёт в отдельной ячейке таблицы globals.
func (vm *VM) staticKeyRemap(key string) string {
	if n := len(vm.callStack); n > 0 {
		if m := vm.callStack[n-1].callee.StaticRemap; m != nil {
			if nk, ok := m[key]; ok {
				return nk
			}
		}
	}
	return key
}

func (vm *VM) opGlobalGet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	name := vm.staticKeyRemap(consts[in.C].StringVal())

	if name == "$GLOBALS" {
		if _, ok := vm.globals["$GLOBALS"]; !ok {
			r := vm.GlobalsSnapshot()
			vm.globals["$GLOBALS"] = r
		}
		r := deref(vm.globals["$GLOBALS"])
		{
			old := regs[in.A]
			wv := r
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(r)
		return execResult{}
	}
	if v, ok := vm.globals[name]; ok {
		r := deref(v)
		{
			old := regs[in.A]
			wv := r
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(r)
	} else {
		vm.warnUndefVar(name)
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}
	return execResult{}
}

// isSuperglobalName сообщает, что имя — авто-глобальный массив (суперглобал).
func isSuperglobalName(name string) bool {
	switch name {
	case "$_SERVER", "$_GET", "$_POST", "$_COOKIE", "$_ENV", "$_REQUEST", "$_FILES":
		return true
	}
	return false
}

// warnUndefVar печатает диагностику обращения к несуществующей переменной.
// Для суперглобалов Zend говорит "Undefined global variable".
func (vm *VM) warnUndefVar(name string) {
	if isSuperglobalName(name) {
		vm.Warn("Undefined global variable " + displayVarKey(name))
		return
	}
	vm.Warn("Undefined variable " + displayVarKey(name))
}

// displayVarKey — имя переменной для диагностик: служебный ключ несуществующей
// переменной в функции ("\x00undef\x00$x") печатается как обычное имя, а
// ключ статического слота ("Func\x00$x") — как исходная переменная.
func displayVarKey(name string) string {
	const marker = "\x00undef\x00"
	if strings.HasPrefix(name, marker) {
		return name[len(marker):]
	}
	if i := strings.LastIndexByte(name, 0); i >= 0 {
		return name[i+1:]
	}
	return name
}

func (vm *VM) opGlobalSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	v := deref(regs[in.A])
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	gname := vm.staticKeyRemap(consts[in.C].StringVal())
	if cur, ok := vm.globals[gname]; ok && cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if err := vm.writeRef(r, v, vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
			}
			{
				old := regs[in.A]
				wv := deref(cur)
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			vm.syncGlobalsSnapshot(gname)
			return execResult{}
		}
	}
	if cur, ok := vm.globals[gname]; ok {
		vm.noteDropped(cur)
	}
	vm.noteStored(v)
	// Значение записывается в таблицу глобальных, т.е. получает второго
	// владельца-массив: помечаем разделённым (как OpStoreVar/OpMove), иначе
	// запись в источник и в глобальную переменную вела бы один и тот же массив
	// и COW не разделял бы их (foreach по ссылке над свойством/элементом).
	shareSlot(v)
	vm.globals[gname] = v
	if gname == "$GLOBALS" {
		vm.syncGlobalsFromSnapshot()
	}
	vm.syncGlobalsSnapshot(gname)
	return execResult{}
}

func (vm *VM) opGlobalUnset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	uname := vm.staticKeyRemap(consts[in.C].StringVal())
	if cur, ok := vm.globals[uname]; ok {
		// unset() незавершённого генератора/файбера закрывает его (finally)
		// до удаления: компилятор не делает отдельной загрузки значения для
		// глобальных переменных (иначе копия удерживала бы объект).
		if err := vm.CloseIfGenerator(cur); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		if err := vm.CloseIfFiber(cur); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		vm.noteDropped(cur)
	}
	delete(vm.globals, uname)
	vm.unsyncGlobalsSnapshot(uname)
	return execResult{}
}

// dynLocalSlot ищет переменную с динамическим именем в текущем кадре функции:
// именованный регистр, global/static-переменная (таблица глобалов) или
// runtime-таблица динамических локальных переменных (обращение к переменной,
// которой не было в тексте функции). Второй результат — определена ли она.
func (vm *VM) dynLocalSlot(chunk *Chunk, name string) (Slot, bool) {
	if chunk != nil && vm.cur != nil {
		if reg, ok := chunk.DynVarReg(name); ok && reg >= 0 && reg < len(vm.cur.regs) {
			return vm.cur.regs[reg], true
		}
		if key, ok := chunk.GlobalVarKey(name); ok {
			v, ok := vm.globals[key]
			return v, ok
		}
	}
	if vm.cur != nil {
		if v, ok := vm.cur.dynLocals[name]; ok {
			return v, true
		}
	}
	return Slot{}, false
}

func (vm *VM) opGlobalGetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	name := deref(regs[in.C]).ToString()
	if vm.inFunction {
		// `${"this"}` в объектном контексте — связанный $this.
		if name == "this" && deref(vm.cur.this).typ == ObjectType {
			r := vm.cur.this
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			shareSlot(r)
			return execResult{}
		}
		// Локальная область функции (CV/global/runtime-таблица) — z1b;
		// fallback — таблица символов кадра (z3).
		v, ok := vm.dynLocalSlot(chunk, name)
		if !ok {
			v, ok = vm.SymtabGet(name)
		}
		if ok {
			r := deref(v)
			{
				old := regs[in.A]
				wv := r
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			shareSlot(r)
			return execResult{}
		}
		vm.warnUndefVar("$" + name)
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	key := "$" + name
	if v, ok := vm.globals[key]; ok {
		r := deref(v)
		{
			old := regs[in.A]
			wv := r
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(r)
	} else {
		vm.warnUndefVar(key)
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}
	return execResult{}
}

func (vm *VM) opGlobalSetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	name := deref(regs[in.C]).ToString()
	if name == "this" {
		// `$$a = ...` с $a == "this": Zend запрещает переприсваивать $this.
		err := NewThrowable("Error", "Cannot re-assign $this")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	v := deref(regs[in.A])
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(v)
	// Как opGlobalSet: у массива появляется второй владелец — пометка COW.
	shareSlot(v)
	if vm.inFunction && chunk != nil && vm.cur != nil {
		// Динамическое имя в функции адресует локальный кадр (CV, global/
		// static или runtime-таблицу динамических локальных), а не глобалы.
		if reg, ok := chunk.DynVarReg(name); ok && reg >= 0 && reg < len(vm.cur.regs) {
			if v.typ == RefType {
				// Привязка ссылки (`$$n = &$x`): переменная становится ячейкой.
				old := vm.cur.regs[reg]
				vm.cur.regs[reg] = v
				if trackedType(old.typ) || trackedType(v.typ) {
					vm.wRegSlow(reg, old)
				}
				vm.clearDefFlag(name)
				return execResult{}
			}
			if cur := vm.cur.regs[reg]; cur.typ == RefType {
				if r := cur.Ref(); r != nil {
					if err := vm.writeRef(r, v, chunk.Strict); err != nil {
						if !vm.handleThrow(chunk, err) {
							return execResult{err: err}
						}
						return execResult{next: true}
					}
					return execResult{}
				}
			}
			old := vm.cur.regs[reg]
			vm.cur.regs[reg] = v
			if trackedType(old.typ) || trackedType(v.typ) {
				vm.wRegSlow(reg, old)
			}
			vm.clearDefFlag(name)
			return execResult{}
		}
		if key, ok := chunk.GlobalVarKey(name); ok {
			if v.typ == RefType {
				if vm.globals == nil {
					vm.globals = map[string]Slot{}
				}
				vm.globals[key] = v
				vm.syncGlobalsSnapshot(key)
				return execResult{}
			}
			if err := vm.globalAssign(key, v); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			return execResult{}
		}
		if vm.cur != nil {
			if vm.cur.dynLocals == nil {
				vm.cur.dynLocals = map[string]Slot{}
			}
			if _, exists := vm.cur.dynLocals[name]; !exists {
				vm.cur.dynOrder = append(vm.cur.dynOrder, name)
			}
			if v.typ == RefType {
				vm.cur.dynLocals[name] = v
				return execResult{}
			}
			if cur, ok := vm.cur.dynLocals[name]; ok && cur.typ == RefType {
				if r := cur.Ref(); r != nil {
					if err := vm.writeRef(r, v, chunk.Strict); err != nil {
						if !vm.handleThrow(chunk, err) {
							return execResult{err: err}
						}
						return execResult{next: true}
					}
					return execResult{}
				}
			}
			vm.cur.dynLocals[name] = v
		}
		return execResult{}
	}
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	key := "$" + name
	vm.globals[key] = v
	vm.syncGlobalsSnapshot(key)
	return execResult{}
}

func (vm *VM) opGlobalIsset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	_, ok := vm.globals[vm.staticKeyRemap(consts[in.C].StringVal())]
	{
		old := regs[in.A]
		wv := MakeBool(ok)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opGlobalAppend(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	name := vm.staticKeyRemap(consts[in.C].StringVal())
	v := deref(vm.globals[name])
	if v.typ == ObjectType {
		// ArrayAccess: $obj[] = $v -> offsetSet(null, $v).
		if ci := vm.classInfo(v); ci != nil {
			if m := ci.lookupMethod("offsetSet"); m != nil {
				if _, err := vm.invokeMethod(m, v, ci, []Slot{MakeNull(), regs[in.A]}); err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				shareSlot(regs[in.A])
				return execResult{}
			}
		}
		err := vm.objectAsArrayErr(v)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if v.typ != ArrayType {
		var werr *Throwable
		v, werr = vm.writeBaseArray(v, true)
		if werr != nil {
			if !vm.handleThrow(chunk, werr) {
				return execResult{err: werr}
			}
			return execResult{next: true}
		}
	}
	a := v.AsArray().Unshare()
	vm.noteStored(regs[in.A])
	vm.noteSelfRefArray(a, regs[in.A])
	if _, err := a.Append(regs[in.A]); err != nil {
		if err == errNextOccupied {
			err = NewThrowable("Error", err.Error())
		}
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	// Запись сквозь Ref-ячейку (`global $arr` или by-ref ячейка вызывающего):
	// замена записи таблицы сломала бы алиасы.
	vm.storeGlobalArray(name, a)
	shareSlot(regs[in.A])
	return execResult{}
}

func (vm *VM) opGlobalIndexSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	name := consts[in.C].StringVal()
	if name == "$GLOBALS" {
		k, ok := vm.KeyOfWarn(regs[in.A])
		if !ok {
			err := vm.offsetTypeErr("access", regs[in.A])
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		if vm.globals == nil {
			vm.globals = map[string]Slot{}
		}
		key := k.PHPString()
		gname := "$" + key
		// Как в PHP: $GLOBALS['x'] = v присваивает глобальной $x.
		val := deref(regs[in.B])
		if cur, ok := vm.globals[gname]; ok && cur.typ == RefType {
			if r := cur.Ref(); r != nil {
				_ = vm.writeRef(r, val, vm.chunk != nil && vm.chunk.Strict)
			}
		} else {
			vm.globals[gname] = val
		}
		// И синхронизируем массив-снимок $GLOBALS (с записью в ссылки).
		vm.syncGlobalsSnapshot(gname)
		shareSlot(regs[in.B])
		return execResult{}
	}
	v := deref(vm.globals[name])
	if v.typ == ObjectType {
		// ArrayAccess: $obj[$k] = $v -> offsetSet($k, $v).
		if ci := vm.classInfo(v); ci != nil {
			if m := ci.lookupMethod("offsetSet"); m != nil {
				if _, err := vm.invokeMethod(m, v, ci, []Slot{regs[in.A], regs[in.B]}); err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				shareSlot(regs[in.B])
				return execResult{}
			}
		}
		err := vm.objectAsArrayErr(v)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if v.typ == StringType {
		ns, _, serr := vm.stringOffsetSet(v, regs[in.A], regs[in.B])
		if serr != nil {
			if !vm.handleThrow(chunk, serr) {
				return execResult{err: serr}
			}
			return execResult{next: true}
		}
		vm.storeGlobalValue(name, ns)
		shareSlot(regs[in.B])
		return execResult{}
	}
	if v.typ != ArrayType {
		var werr *Throwable
		v, werr = vm.writeBaseArray(v, false)
		if werr != nil {
			if !vm.handleThrow(chunk, werr) {
				return execResult{err: werr}
			}
			return execResult{next: true}
		}
	}
	k, ok := vm.KeyOfWarn(regs[in.A])
	if !ok {
		err := vm.offsetTypeErr("access", regs[in.A])
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	a := v.AsArray().Unshare()
	// Запись в элемент, который уже является ссылкой, идёт сквозь ячейку
	// (как opIndexSet): $b['k'] = v, где $b содержит ссылку на элемент.
	if cur, found := a.Get(k); found && cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if err := vm.writeRef(r, regs[in.B], vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			vm.storeGlobalArray(name, a)
			shareSlot(regs[in.B])
			return execResult{}
		}
	}
	vm.noteStored(regs[in.B])
	vm.noteSelfRefArray(a, regs[in.B])
	if err := a.Set(k, regs[in.B]); err != nil {
		return execResult{err: err}
	}
	vm.storeGlobalArray(name, a)
	shareSlot(regs[in.B])

	return execResult{}
}

// storeGlobalArray сохраняет изменённый массив обратно в глобальную
// переменную name, «проходя сквозь» Ref-ячейку, если переменная — ссылка.
func (vm *VM) storeGlobalArray(name string, a *Array) {
	vm.storeGlobalValue(name, MakeArray(a))
}

// storeGlobalValue сохраняет значение обратно в глобальную переменную name,
// «проходя сквозь» Ref-ячейку, если переменная — ссылка.
func (vm *VM) storeGlobalValue(name string, v Slot) {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	if cur, ok := vm.globals[name]; ok && cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			r.Set(v)
			return
		}
	}
	vm.globals[name] = v
}

func (vm *VM) opNewRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	shareSlot(regs[in.B])
	{
		old := regs[in.A]
		wv := MakeRef(&Ref{v: regs[in.B]})
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

func (vm *VM) opRefGet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	v := deref(regs[in.B])
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(v)
	return execResult{}
}

// opRefGetRaw — чтение ячейки-ссылки (или self-регистра ref-переменной) БЕЗ
// пометки массива разделённым. Используется только для баз записи: результат
// немедленно записывается обратно в ту же ячейку (OpRefSetRaw), поэтому
// второго владельца массива не возникает, а shareSlot здесь лишь заставлял бы
// Unshare копировать весь массив на каждой записи (O(n^2)).
func (vm *VM) opRefGetRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	// База записи через ссылку: при авто-вивификации массива (значение
	// null/false) источники ссылки должны допускать array (Zend
	// zend_fetch_dimension_address -> zend_verify_ref_array_assignable).
	if c := regs[in.B]; c.typ == RefType {
		if r := c.Ref(); r != nil && r.hasSources() && promotesToArray(r.v) {
			if err := vm.verifyRefArrayAssignable(r); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
		}
	}
	{
		old := regs[in.A]
		wv := deref(regs[in.B])
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

// opRefSetRaw — запись значения в ячейку-ссылку без пометки разделённости.
// Отличие от OpRefSet только в отсутствии shareSlot: значение остаётся
// единственным владельцем массива в ячейке.
func (vm *VM) opRefSetRaw(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cell := regs[in.A]
	if cell.typ == RefType {
		if r := cell.Ref(); r != nil {
			if err := vm.writeRef(r, regs[in.B], vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			return execResult{}
		}
	}
	{
		old := regs[in.A]
		wv := regs[in.B]
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

func (vm *VM) opRefSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cell := regs[in.A]
	if cell.typ == RefType {
		if r := cell.Ref(); r != nil {
			if err := vm.writeRef(r, regs[in.B], vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
		} else {
			{
				old := regs[in.A]
				wv := regs[in.B]
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
		}
	} else {
		{
			old := regs[in.A]
			wv := regs[in.B]
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}
	shareSlot(regs[in.B])
	return execResult{}
}

func (vm *VM) opRefBind(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	{
		old := regs[in.A]
		wv := regs[in.B]
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(regs[in.A])
	return execResult{}
}

func (vm *VM) opRefEnsure(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if regs[in.A].typ != RefType {
		{
			old := regs[in.A]
			wv := MakeRef(&Ref{v: regs[in.A]})
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}
	return execResult{}
}

func (vm *VM) opGlobalRefGet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if v, ok := vm.globals[vm.staticKeyRemap(consts[in.C].StringVal())]; ok {
		r := deref(v)
		{
			old := regs[in.A]
			wv := r
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(r)
	} else {
		{
			old := regs[in.A]
			wv := MakeNull()
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}
	return execResult{}
}

func (vm *VM) opGlobalRefSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	key := vm.staticKeyRemap(consts[in.C].StringVal())
	if v, ok := vm.globals[key]; ok && v.typ == RefType {
		if r := v.Ref(); r != nil {
			if err := vm.writeRef(r, regs[in.A], vm.chunk != nil && vm.chunk.Strict); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			// Снимок $GLOBALS должен видеть запись в глобальную ссылку
			// (переменная-ссылка и $GLOBALS['x'] — одна ячейка).
			vm.syncGlobalsSnapshot(key)
		} else {
			vm.globals[key] = regs[in.A]
		}
	} else {
		vm.globals[key] = regs[in.A]
	}
	shareSlot(regs[in.A])
	return execResult{}
}

func (vm *VM) opGlobalRefAddr(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	key := vm.staticKeyRemap(consts[in.C].StringVal())
	v, ok := vm.globals[key]
	if !ok {
		v = MakeNull()
	}
	if v.typ != RefType {
		v = MakeRef(&Ref{v: v})
	}
	vm.globals[key] = v
	{
		old := regs[in.A]
		wv := v
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

func (vm *VM) opGlobalRefBind(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if vm.globals == nil {
		vm.globals = map[string]Slot{}
	}
	key := vm.staticKeyRemap(consts[in.C].StringVal())
	if old, ok := vm.globals[key]; ok {
		vm.noteDropped(old)
	}
	vm.globals[key] = regs[in.A]

	return execResult{}
}
