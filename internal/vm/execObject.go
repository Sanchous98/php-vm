package vm

import (
	"fmt"
	"strings"
)

// execObject.go — new, clone, свойства объектов (prop*).

// validClassAutoName сообщает, допустимо ли имя класса для передачи
// автозагрузчикам (Zend не вызывает autoload для имён с недопустимыми
// символами, напр. "../BUG").
func validClassAutoName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '_', c == '\\':
		case c >= 0x80:
		default:
			return false
		}
	}
	return true
}

func (vm *VM) opNew(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	classIdx := int(in.C) & 0xFFFF
	if classIdx >= len(vm.classes) {
		return execResult{err: errUnknownClass}
	}
	ci := vm.classes[classIdx]
	if ci == nil {
		return execResult{err: NewThrowable("Error", "Cannot instantiate class")}
	}
	if ci.IsEnum() {
		return execResult{err: NewThrowable("Error", "Cannot instantiate enum "+ci.Name())}
	}
	if ci.Name() == "Generator" {
		return execResult{err: NewThrowable("Error", `The "Generator" class is reserved for internal use and cannot be manually instantiated`)}
	}
	if ci.Name() == "FiberError" {
		return execResult{err: NewThrowable("Error", `The "FiberError" class is reserved for internal use and cannot be manually instantiated`)}
	}
	if ci.Name() == "Closure" {
		return execResult{err: NewThrowable("Error", `Instantiation of class Closure is not allowed`)}
	}
	if ci.IsInterface() || ci.IsTrait() {
		return execResult{err: cannotInstantiateErr(ci)}
	}
	if ci.IsAbstract() {
		return execResult{err: NewThrowable("Error", "Cannot instantiate abstract class "+ci.Name())}
	}
	// disable_classes: предупреждение, объект создаётся как обычно (Zend).
	vm.warnDisabledClass(ci)
	if cerr := vm.resolveAllLazyConsts(ci); cerr != nil {
		if !vm.handleThrow(chunk, cerr) {
			return execResult{err: cerr}
		}
		return execResult{next: true}
	}
	obj := ci.New(&vm.ctx, nil)
	if obj == nil {
		// интерфейс/трейт/абстрактный или незарегистрированный класс
		return execResult{err: cannotInstantiateErr(ci)}
	}
	// Пока конструктор выполняется, объект помечен как «не построенный»:
	// при исключении из __construct уничтожение идёт без __destruct
	// (Zend: ctor_failure). После успешного вызова пометка снимается.
	vm.MarkNoDestruct(obj)
	if derr := vm.takePendingDefaultError(); derr != nil {
		if !vm.handleThrow(chunk, derr) {
			return execResult{err: derr}
		}
		return execResult{next: true}
	}
	// argc упакован в старшие 16 бит C как (argc+1): 0 — легаси-байткод без
	// упакованного argc (ручные чанки в тестах), тогда для конструктора с
	// необязательными параметрами читаем ctor.NumArgs регистров (старое
	// поведение). Компилятор всегда упаковывает argc+1, поэтому `new C` без
	// аргументов не подхватывает устаревшие регистры.
	rawArgc := int(in.C >> 16)
	argc := rawArgc - 1
	if rawArgc == 0 {
		argc = 0
		if ctor := ci.lookupCtor(); ctor != nil && ctor.NumArgs > 0 && ctor.NumRequired == 0 {
			argc = ctor.NumArgs
		}
	}
	{
		old := regs[in.A]
		wv := ci.objectSlot(obj, classIdx)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	if ctor, ctorDecl, ctorVis := ci.CtorInfo(); ctor != nil {
		if !canAccess(ctorVis, ctorDecl, vm.ctx.self) {
			err := vm.ctorAccessError(ctorVis, classDisplayName(ctorDecl), "__construct")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		var cargs []Slot
		if argc > 0 {
			cargs = regs[int(in.B) : int(in.B)+argc]
		}
		if _, err := vm.invokeMethod(ctor, regs[in.A], ci, cargs); err != nil {
			// Брошенный конструктор: объект не построен, __destruct не
			// вызывается (пометка MarkNoDestruct остаётся).
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}
	vm.UnmarkNoDestruct(obj)

	return execResult{}
}

func (vm *VM) opNewDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	base := int(in.B)
	// Zend: объект как имя класса — его класс (__toString игнорируется),
	// строка — имя, остальные типы — Error "Class name must be a valid
	// object or a string".
	baseSlot := deref(regs[base])
	var name string
	switch baseSlot.typ {
	case ObjectType, ClosureType:
		if ci := vm.classInfo(baseSlot); ci != nil {
			name = ci.Name()
		}
	case StringType:
		// Ведущие '\' отбрасываются (bug46665: new $name с '\Foo\Bar\Baz'
		// после автозагрузки должен найти объявленный Foo\Bar\Baz).
		name = strings.TrimLeft(baseSlot.ToString(), "\\")
	default:
		err := NewThrowable("Error", "Class name must be a valid object or a string")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	argc := int(in.C)
	classIdx := -1
	for i, c := range vm.classes {
		if ClassHidden(c) {
			continue
		}
		if c.Name() == name {
			classIdx = i
			break
		}
	}
	if classIdx < 0 {
		for i, c := range vm.classes {
			if ClassHidden(c) {
				continue
			}
			if strings.EqualFold(c.Name(), name) {
				classIdx = i
				break
			}
		}
	}
	if classIdx < 0 {
		// Псевдонимы class_alias: имя могло быть зарегистрировано
		// динамически (class_alias_007: new bar после class_alias('foo','bar')).
		if ci := vm.FindClass(name); ci != nil {
			classIdx = vm.classIndex(ci)
		}
	}
	if classIdx < 0 {
		// Автозагрузка: неизвестный класс может быть определён
		// зарегистрированным spl_autoload_register callable. Zend вызывает
		// автозагрузчики только для корректных имён классов.
		if !validClassAutoName(name) {
			var derr error
			if name == "" {
				derr = NewThrowable("Error", "Class name must be a valid object or a string")
			} else {
				derr = NewThrowable("Error", "Class \""+name+"\" not found")
			}
			if !vm.handleThrow(chunk, derr) {
				return execResult{err: derr}
			}
			return execResult{next: true}
		}
		if _, aerr := vm.Autoload(&vm.ctx, name); aerr != nil {
			if !vm.handleThrow(chunk, aerr) {
				return execResult{err: aerr}
			}
			return execResult{next: true}
		}
		for i, c := range vm.classes {
			if ClassHidden(c) {
				continue
			}
			if strings.EqualFold(c.Name(), name) {
				classIdx = i
				break
			}
		}
	}
	if classIdx < 0 {
		var derr error
		if name == "" {
			derr = NewThrowable("Error", "Class name must be a valid object or a string")
		} else {
			derr = NewThrowable("Error", "Class \""+name+"\" not found")
		}
		if !vm.handleThrow(chunk, derr) {
			return execResult{err: derr}
		}
		return execResult{next: true}
	}
	ci := vm.classes[classIdx]
	if ci == nil {
		return execResult{err: NewThrowable("Error", "Cannot instantiate class")}
	}
	if ci.IsEnum() {
		return execResult{err: NewThrowable("Error", "Cannot instantiate enum "+ci.Name())}
	}
	if ci.Name() == "Generator" {
		return execResult{err: NewThrowable("Error", `The "Generator" class is reserved for internal use and cannot be manually instantiated`)}
	}
	if ci.Name() == "FiberError" {
		return execResult{err: NewThrowable("Error", `The "FiberError" class is reserved for internal use and cannot be manually instantiated`)}
	}
	if ci.Name() == "Closure" {
		return execResult{err: NewThrowable("Error", `Instantiation of class Closure is not allowed`)}
	}
	if ci.IsInterface() || ci.IsTrait() {
		return execResult{err: cannotInstantiateErr(ci)}
	}
	if ci.IsAbstract() {
		return execResult{err: NewThrowable("Error", "Cannot instantiate abstract class "+ci.Name())}
	}
	// disable_classes: предупреждение, объект создаётся как обычно (Zend).
	vm.warnDisabledClass(ci)
	if cerr := vm.resolveAllLazyConsts(ci); cerr != nil {
		if !vm.handleThrow(chunk, cerr) {
			return execResult{err: cerr}
		}
		return execResult{next: true}
	}
	obj := ci.New(&vm.ctx, nil)
	if obj == nil {
		return execResult{err: cannotInstantiateErr(ci)}
	}
	// Пока конструктор выполняется, объект помечен как «не построенный»:
	// при исключении из __construct уничтожение идёт без __destruct
	// (Zend: ctor_failure). После успешного вызова пометка снимается.
	vm.MarkNoDestruct(obj)
	var args []Slot
	if argc > 0 {
		args = regs[base+1 : base+1+argc]
	}
	{
		old := regs[in.A]
		wv := ci.objectSlot(obj, classIdx)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	if ctor, ctorDecl, ctorVis := ci.CtorInfo(); ctor != nil {
		if !canAccess(ctorVis, ctorDecl, vm.ctx.self) {
			err := vm.ctorAccessError(ctorVis, classDisplayName(ctorDecl), "__construct")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		if _, err := vm.invokeMethod(ctor, regs[in.A], ci, args); err != nil {
			// Брошенный конструктор: объект не построен — __destruct не
			// вызывается (пометка MarkNoDestruct остаётся).
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}
	vm.UnmarkNoDestruct(obj)

	return execResult{}
}

// incompleteWriteGuard проверяет, что запись свойства идёт не в incomplete-объект.
// Возвращает результат-ошибку и true, если обработку нужно прервать.
func (vm *VM) incompleteWriteGuard(o Slot, chunk *Chunk) (execResult, bool) {
	if cls, ok := incompleteObjName(vm.objectOf(o)); ok {
		err := incompleteModifyErr(cls)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}, true
		}
		return execResult{next: true}, true
	}
	return execResult{}, false
}

// incompleteReadGuard печатает warning и кладёт NULL, если объект incomplete.
func (vm *VM) incompleteReadGuard(recv Slot, dst *Slot, reg int) (execResult, bool) {
	if o := vm.objectOf(recv); o != nil {
		if cls, ok := incompleteObjName(o); ok {
			vm.incompleteReadWarn(o, cls)
			vm.wDropReg(reg)
			*dst = MakeNull()
			return execResult{}, true
		}
	}
	return execResult{}, false
}

// propGetDeclaredFast — быстрый путь чтения объявленного свойства из слота
// (без map-lookup и боксинга Value). handled=true, если результат уже записан.
func (vm *VM) propGetDeclaredFast(ci ClassInfo, recv Slot, pname string, dst *Slot, reg int, chunk *Chunk) (execResult, bool) {
	ic := vm.icAt(chunk)
	pd, decl, key, idx, ok := vm.icProp(ci, pname, chunk)
	if !ok || idx < 0 {
		return execResult{}, false
	}
	ob := vm.objectOf(recv)
	if ob == nil || idx >= len(ob.slots) || ob.IsUnset(key) {
		return execResult{}, false
	}
	if pd.vis != Public && !vm.icPropAccess(ic) {
		return execResult{}, false
	}
	s := ob.slots[idx]
	if s.typ == slotUninit {
		if pd.readonly || pd.hasType {
			verr := NewThrowable("Error", "Typed property "+classDisplayName(decl)+"::$"+pname+" must not be accessed before initialization")
			if !vm.handleThrow(chunk, verr) {
				return execResult{err: verr}, true
			}
			return execResult{next: true}, true
		}
		s = MakeNull()
	}
	old := *dst
	*dst = s
	if trackedType(old.typ) || trackedType(s.typ) {
		vm.wRegSlow(reg, old)
	}
	shareSlot(*dst)
	return execResult{}, true
}

// propGetForIncDec — общий вход баз ++/-- на свойстве: не-объект даёт Error
// "Attempt to increment/decrement property ..." (Zend FETCH_OBJ_INC/DEC не
// печатает warning чтения), иначе — обычный OpPropGet.
func (vm *VM) propGetForIncDec(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, name string) execResult {
	recv := deref(regs[in.B])
	if vm.classInfo(recv) == nil {
		err := NewThrowable("Error", fmt.Sprintf("Attempt to increment/decrement property %q on %s", name, valueTypeName(vm, recv)))
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	return vm.opPropGet(regs, consts, chunk, in)
}

func (vm *VM) opPropGetIncDec(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.propGetForIncDec(regs, consts, chunk, in, consts[in.C].StringVal())
}

func (vm *VM) opPropGetIncDecDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	if vm.classInfo(recv) == nil {
		name := deref(regs[in.C]).ToString()
		err := NewThrowable("Error", fmt.Sprintf("Attempt to increment/decrement property %q on %s", name, valueTypeName(vm, recv)))
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	return vm.opPropGetDyn(regs, consts, chunk, in)
}

func (vm *VM) opPropGet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	pname := consts[in.C].StringVal()
	ci := vm.classInfo(recv)
	if ci == nil {
		vm.Warn(fmt.Sprintf("Attempt to read property %q on %s", pname, valueTypeName(vm, recv)))
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
	// Чтение любого свойства incomplete-объекта даёт warning и NULL (даже если
	// свойство было восстановлено из сериализованных данных).
	if recv.typ == ObjectType && (*Object)(recv.ptr).incomplete {
		if r, ok := vm.incompleteReadGuard(recv, &regs[in.A], int(in.A)); ok {
			return r
		}
	}
	// Ультра-быстрый путь (IC прогрет): публичное объявленное свойство в слоте.
	if ic := vm.icAt(chunk); ic != nil && ic.propState == icMono && ic.propClass == ci && ic.propName == pname && ic.propScope == vm.ctx.self && ic.propOk && ic.propIdx >= 0 {
		if pd := ic.propPd; pd != nil && (pd.vis == Public || ic.propAccess) {
			if ob := vm.objectOf(recv); ob != nil && ic.propIdx < len(ob.slots) && !ob.IsUnset(ic.propKey) {
				if s := ob.slots[ic.propIdx]; s.typ != slotUninit {
					old := regs[in.A]
					regs[in.A] = s
					if trackedType(old.typ) || trackedType(s.typ) {
						vm.wRegSlow(int(in.A), old)
					}
					shareSlot(s)
					return execResult{}
				}
			}
		}
	}
	if r, ok := vm.propGetDeclaredFast(ci, recv, pname, &regs[in.A], int(in.A), chunk); ok {
		return r
	}
	if gres, handled, mok, gerr := vm.magicGetInaccessible(ci, recv, pname); handled && mok {
		if gerr != nil {
			if !vm.handleThrow(chunk, gerr) {
				return execResult{err: gerr}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.A]
			wv := gres
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(gres)
		return execResult{}
	}
	v, found, verr := vm.checkPropReadVal(ci, recv, pname)
	if verr != nil {
		if !vm.handleThrow(chunk, verr) {
			return execResult{err: verr}
		}
		return execResult{next: true}
	}
	if found {
		{
			old := regs[in.A]
			wv := v
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(regs[in.A])
		return execResult{}
	}
	if v, ok := vm.getProp(recv, pname); ok {
		if gres, stop := vm.staticShadowGuard(ci, pname, "__get", chunk); stop {
			return gres
		}
		// Свойство-ссылка при чтении разыменовывается (Zend FETCH_OBJ_R):
		// значение фиксируется в момент чтения (bug72543_3).
		if sl, ok := v.(Slot); ok {
			v = Deref(sl)
		}
		old := regs[in.A]
		SlotOf(&regs[in.A], v)
		if trackedType(old.typ) || trackedType(regs[in.A].typ) {
			vm.wRegSlow(int(in.A), old)
		}
		shareSlot(regs[in.A])
		return execResult{}
	}
	if res, ok, err := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		res, gerr := vm.checkMagicGet(ci, pname, res)
		if gerr != nil {
			if !vm.handleThrow(chunk, gerr) {
				return execResult{err: gerr}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.A]
			wv := res
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(res)
		return execResult{}
	}
	if uerr := vm.uninitPropErr(ci, pname); uerr != nil {
		if !vm.handleThrow(chunk, uerr) {
			return execResult{err: uerr}
		}
		return execResult{next: true}
	}
	if r, ok := vm.incompleteReadGuard(recv, &regs[in.A], int(in.A)); ok {
		return r
	}
	if gres, stop := vm.staticShadowGuard(ci, pname, "__get", chunk); stop {
		return gres
	}
	vm.Warn(fmt.Sprintf("Undefined property: %s::$%s", ci.Name(), pname))
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

func (vm *VM) opPropGetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	pname := deref(regs[in.C]).ToString()
	ci := vm.classInfo(recv)
	if ci == nil {
		if err := vm.propNulNameErr(pname); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		vm.Warn(fmt.Sprintf("Attempt to read property %q on %s", pname, valueTypeName(vm, recv)))
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
	// Чтение любого свойства incomplete-объекта даёт warning и NULL (даже если
	// свойство было восстановлено из сериализованных данных).
	if recv.typ == ObjectType && (*Object)(recv.ptr).incomplete {
		if r, ok := vm.incompleteReadGuard(recv, &regs[in.A], int(in.A)); ok {
			return r
		}
	}
	// Ультра-быстрый путь (IC прогрет): публичное объявленное свойство в слоте.
	if ic := vm.icAt(chunk); ic != nil && ic.propState == icMono && ic.propClass == ci && ic.propName == pname && ic.propScope == vm.ctx.self && ic.propOk && ic.propIdx >= 0 {
		if pd := ic.propPd; pd != nil && (pd.vis == Public || ic.propAccess) {
			if ob := vm.objectOf(recv); ob != nil && ic.propIdx < len(ob.slots) && !ob.IsUnset(ic.propKey) {
				if s := ob.slots[ic.propIdx]; s.typ != slotUninit {
					old := regs[in.A]
					regs[in.A] = s
					if trackedType(old.typ) || trackedType(s.typ) {
						vm.wRegSlow(int(in.A), old)
					}
					shareSlot(s)
					return execResult{}
				}
			}
		}
	}
	if r, ok := vm.propGetDeclaredFast(ci, recv, pname, &regs[in.A], int(in.A), chunk); ok {
		return r
	}
	if gres, handled, mok, gerr := vm.magicGetInaccessible(ci, recv, pname); handled && mok {
		if gerr != nil {
			if !vm.handleThrow(chunk, gerr) {
				return execResult{err: gerr}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.A]
			wv := gres
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(gres)
		return execResult{}
	}
	v, found, verr := vm.checkPropReadVal(ci, recv, pname)
	if verr != nil {
		if !vm.handleThrow(chunk, verr) {
			return execResult{err: verr}
		}
		return execResult{next: true}
	}
	if found {
		{
			old := regs[in.A]
			wv := v
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(regs[in.A])
		return execResult{}
	}
	if v, ok := vm.getProp(recv, pname); ok {
		if gres, stop := vm.staticShadowGuard(ci, pname, "__get", chunk); stop {
			return gres
		}
		// Свойство-ссылка при чтении разыменовывается (Zend FETCH_OBJ_R):
		// значение фиксируется в момент чтения (bug72543_3).
		if sl, ok := v.(Slot); ok {
			v = Deref(sl)
		}
		old := regs[in.A]
		SlotOf(&regs[in.A], v)
		if trackedType(old.typ) || trackedType(regs[in.A].typ) {
			vm.wRegSlow(int(in.A), old)
		}
		shareSlot(regs[in.A])
		return execResult{}
	}
	if res, ok, err := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		res, gerr := vm.checkMagicGet(ci, pname, res)
		if gerr != nil {
			if !vm.handleThrow(chunk, gerr) {
				return execResult{err: gerr}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.A]
			wv := res
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(res)
		return execResult{}
	}
	if uerr := vm.uninitPropErr(ci, pname); uerr != nil {
		if !vm.handleThrow(chunk, uerr) {
			return execResult{err: uerr}
		}
		return execResult{next: true}
	}
	if r, ok := vm.incompleteReadGuard(recv, &regs[in.A], int(in.A)); ok {
		return r
	}
	if gres, stop := vm.staticShadowGuard(ci, pname, "__get", chunk); stop {
		return gres
	}
	// \0-имя: Error, если магия не сработала (внутри __get — bug52484_3).
	if err := vm.propNulNameErr(pname); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	vm.Warn(fmt.Sprintf("Undefined property: %s::$%s", ci.Name(), pname))
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

func (vm *VM) opPropRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if r, ok := vm.incompleteWriteGuard(deref(regs[in.B]), chunk); ok {
		return r
	}
	cell, err := vm.propRefCell(deref(regs[in.B]), consts[in.C].StringVal())
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := cell
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(cell)

	return execResult{}
}

func (vm *VM) opPropRefDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if r, ok := vm.incompleteWriteGuard(deref(regs[in.B]), chunk); ok {
		return r
	}
	cell, err := vm.propRefCell(deref(regs[in.B]), deref(regs[in.C]).ToString())
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := cell
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(cell)

	return execResult{}
}

func (vm *VM) opPropUnrefDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	o := deref(regs[in.A])
	pname := deref(regs[in.B]).ToString()
	if v, ok := vm.getProp(o, pname); ok {
		if s := valueSlot(v); s.typ == RefType {
			vm.setProp(o, pname, deref(s))
		}
	}

	return execResult{}
}

func (vm *VM) opPropGetRW(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	// OpPropGetRWRead — режим составного присваивания: значение читается,
	// readonly-диагностику выдаёт последующая запись (variation.phpt).
	// OpPropGetRWInterm — промежуточное звено цепочки записи: readonly допустим
	// при инициализации, при пустом — «Cannot indirectly modify» (Zend).
	readMode := in.Op == OpPropGetRWRead
	interm := in.Op == OpPropGetRWInterm
	arrMode := in.Op == OpPropGetRWArr || in.Op == OpPropGetRWArrDyn
	// compound — промежуточное звено `+=`/`++`: как Interm, но
	// неинициализированное типизированное свойство — ошибка доступа.
	compound := in.Op == OpPropGetCompound || in.Op == OpPropGetCompoundDyn
	var pname string
	if in.Op == OpPropGetRWDyn || in.Op == OpPropGetRWArrDyn || in.Op == OpPropGetCompoundDyn {
		pname = deref(regs[in.C]).ToString()
	} else {
		pname = consts[in.C].StringVal()
	}
	ci := vm.classInfo(recv)
	if ci == nil {
		// Чтение-для-записи в не-объект: Zend бросает Error (не Warning).
		err := NewThrowable("Error", fmt.Sprintf("Attempt to modify property %q on %s", pname, valueTypeName(vm, recv)))
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// Быстрый путь: инициализированное объявленное не-readonly свойство
	// в слоте (для `+=` и промежуточных звеньев цепочек записи).
	if !arrMode {
		if ic := vm.icAt(chunk); ic != nil && ic.propState == icMono && ic.propClass == ci && ic.propName == pname && ic.propScope == vm.ctx.self && ic.propOk && ic.propIdx >= 0 {
			if pd := ic.propPd; pd != nil && (pd.vis == Public || ic.propAccess) && !pd.readonly {
				if ob := vm.objectOf(recv); ob != nil && ic.propIdx < len(ob.slots) && !ob.IsUnset(ic.propKey) {
					if s := ob.slots[ic.propIdx]; s.typ != slotUninit {
						old := regs[in.A]
						regs[in.A] = s
						if trackedType(old.typ) || trackedType(s.typ) {
							vm.wRegSlow(int(in.A), old)
						}
						shareSlot(regs[in.A])
						return execResult{}
					}
				}
			}
		}
		if pd, _, key, idx, declared := vm.icProp(ci, pname, chunk); declared && !pd.readonly && idx >= 0 {
			if ob := vm.objectOf(recv); ob != nil && idx < len(ob.slots) && !ob.IsUnset(key) {
				if pd.vis == Public || vm.icPropAccess(vm.icAt(chunk)) {
					if s := ob.slots[idx]; s.typ != slotUninit {
						old := regs[in.A]
						regs[in.A] = s
						if trackedType(old.typ) || trackedType(s.typ) {
							vm.wRegSlow(int(in.A), old)
						}
						shareSlot(regs[in.A])
						return execResult{}
					}
				}
			}
		}
	}
	pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname)
	key := vm.propKey(pd, decl, pname)
	if declared {
		if ob := vm.objectOf(recv); ob != nil && ob.IsUnset(key) {
			declared = false
		}
	}
	if declared {
		if err := vm.propAccessErr(pd, decl, pname); err != nil {
			// Недоступное объявленное свойство + __get: Zend берёт значение
			// через __get и не пишет временную копию назад (Zend
			// zend_fetch_property_address, ветка overloaded). By-ref __get
			// даёт реальную ячейку — тогда значение пишется в свойство.
			g, mok, merr := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)})
			if mok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				real := g.typ == RefType
				val := deref(g)
				if !real {
					if a := val.AsArray(); a != nil {
						a.MarkShared()
					}
				}
				vm.pushWbSkip(vm.objectOf(recv), pname, real)
				{
					old := regs[in.A]
					wv := val
					regs[in.A] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.A), old)
					}
				}
				shareSlot(val)
				return execResult{}
			}
			// Недоступное из текущей области свойство с __get читается через
			// magic-метод (Zend magic_get_set); без __get — ошибка доступа.
			if m := ci.lookupMethod("__get"); m == nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			declared = false
		}
		if declared && pd.readonly && !readMode && !compound {
			// Косвенная запись в readonly-свойство:
			// - промежуточное звено (OpPropGetRWInterm): инициализированное
			//   свойство допустимо (модифицируется вложенный объект), пустое —
			//   «Cannot indirectly modify» (variation_nested);
			// - прямой контейнер (OpPropGetRW): пустое — «indirectly», а
			//   инициализированное — «Cannot modify» (variation.phpt).
			if _, init := vm.getProp(recv, key); !init {
				err := NewThrowable("Error", "Cannot indirectly modify readonly property "+decl.Name()+"::$"+pname)
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			if !interm {
				err := NewThrowable("Error", "Cannot modify readonly property "+classDisplayName(decl)+"::$"+pname)
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
		}
	}
	if v, ok := vm.getProp(recv, key); ok {
		if arrMode {
			if derr := vm.dimWritePropCheck(pd, decl, pname, valueSlot(v), true); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
			}
		}
		old := regs[in.A]
		SlotOf(&regs[in.A], v)
		if trackedType(old.typ) || trackedType(regs[in.A].typ) {
			vm.wRegSlow(int(in.A), old)
		}
		shareSlot(regs[in.A])
		return execResult{}
	}
	if declared {
		if readMode || compound {
			// Составное присваивание сначала читает значение: неинициализированное
			// типизированное свойство — ошибка доступа (variation.phpt).
			if uerr := vm.uninitPropErr(ci, pname); uerr != nil {
				if !vm.handleThrow(chunk, uerr) {
					return execResult{err: uerr}
				}
				return execResult{next: true}
			}
		}
		if arrMode {
			if derr := vm.dimWritePropCheck(pd, decl, pname, Slot{}, false); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
			}
		}
		// Объявленное неинициализированное не-readonly свойство —
		// база для авто-вивификации (`$this->a[] = 1`).
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
	if res, ok, err := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		res, gerr := vm.checkMagicGet(ci, pname, res)
		if gerr != nil {
			if !vm.handleThrow(chunk, gerr) {
				return execResult{err: gerr}
			}
			return execResult{next: true}
		}
		// Необъявленное свойство с __get: запись-обратно не выполняется
		// (Zend: модификация временной копии без эффекта).
		if !readMode {
			real := res.typ == RefType
			if !real {
				if a := deref(res).AsArray(); a != nil {
					a.MarkShared()
				}
			}
			vm.pushWbSkip(vm.objectOf(recv), pname, real)
		}
		{
			old := regs[in.A]
			wv := res
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(res)
		return execResult{}
	}
	if uerr := vm.uninitPropErr(ci, pname); uerr != nil {
		if !vm.handleThrow(chunk, uerr) {
			return execResult{err: uerr}
		}
		return execResult{next: true}
	}
	// Чтение-для-записи свойства incomplete-объекта — Error (Zend:
	// "tried to modify a property").
	if o := vm.objectOf(recv); o != nil {
		if cls, ok := incompleteObjName(o); ok {
			err := incompleteModifyErr(cls)
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}
	// Необъявленное свойство как база записи: для классов без динамических
	// свойств создание запрещено (WeakMap: "Cannot create dynamic property").
	if !declared {
		if derr := checkDynamicProp(ci, pname); derr != nil {
			if !vm.handleThrow(chunk, derr) {
				return execResult{err: derr}
			}
			return execResult{next: true}
		}
	}
	vm.Warn(fmt.Sprintf("Undefined property: %s::$%s", ci.Name(), pname))
	// Обработчик ошибки мог создать свойство (oss_fuzz_61712b: `$c->a %= 10`
	// при set_error_handler, добавляющем $a): Zend читает значение после
	// предупреждения, поэтому оно уже существует.
	if v, ok := vm.getProp(recv, pname); ok {
		{
			old := regs[in.A]
			wv := deref(valueSlot(v))
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		shareSlot(regs[in.A])
		return execResult{}
	}
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

// opPropSetOverload — writeback контейнера `$obj->prop[...] = v`:
// свойство могло быть получено через __get без ссылки — тогда запись не
// имеет эффекта и печатается Notice (для объекта), __set не вызывается.
func (vm *VM) opPropSetOverload(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.propSetOverloadCommon(regs, consts, chunk, in, false)
}

// opPropSetOverloadDyn — как opPropSetOverload для динамического имени.
func (vm *VM) opPropSetOverloadDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.propSetOverloadCommon(regs, consts, chunk, in, true)
}

func (vm *VM) propSetOverloadCommon(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, dyn bool) execResult {
	obj := deref(regs[in.A])
	if ci := vm.classInfo(obj); ci != nil {
		name := consts[in.C].StringVal()
		if dyn {
			name = deref(regs[in.C]).ToString()
		}
		if overloaded, byRef := vm.propRefOverloaded(ci, obj, name, true); overloaded && !byRef {
			// Запись-обратно в overloaded-свойство не выполняется; снимаем
			// оставленную чтением запись из стека wbSkip, чтобы её не
			// «съел» посторонний поход в то же свойство (bug40833).
			vm.dropWbSkip(vm.objectOf(obj), name)
			if v := deref(regs[in.B]); v.typ != ObjectType {
				vm.Notice("Indirect modification of overloaded property " + classDisplayName(ci) + "::$" + name + " has no effect")
			}
			return execResult{}
		}
	}
	if dyn {
		return vm.opPropSetDyn(regs, consts, chunk, in)
	}
	return vm.opPropSet(regs, consts, chunk, in)
}

// propSetDeclaredFast — быстрый путь записи объявленного свойства в слот.
// Обрабатывает только полностью «простые» случаи (доступно, не снято unset,
// не readonly, ссылка — сквозь ячейку); остальное — общий путь.
func (vm *VM) propSetDeclaredFast(ci ClassInfo, o Slot, pname string, valReg int, regs []Slot, chunk *Chunk) (execResult, bool) {
	ic := vm.icAt(chunk)
	pd, decl, key, idx, ok := vm.icProp(ci, pname, chunk)
	if !ok || idx < 0 || pd.readonly {
		return execResult{}, false
	}
	ob := vm.objectOf(o)
	if ob == nil || idx >= len(ob.slots) || ob.IsUnset(key) {
		return execResult{}, false
	}
	if pd.vis != Public && !vm.icPropAccess(ic) {
		return execResult{}, false
	}
	cur := ob.slots[idx]
	if cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			out, werr := vm.writeRefPropValue(ci, pname, r, regs[valReg], chunk != nil && chunk.Strict)
			if werr != nil {
				if !vm.handleThrow(chunk, werr) {
					return execResult{err: werr}, true
				}
				return execResult{next: true}, true
			}
			old := regs[valReg]
			regs[valReg] = out
			if trackedType(old.typ) || trackedType(out.typ) {
				vm.wRegSlow(valReg, old)
			}
			return execResult{}, true
		}
	}
	plain := deref(regs[valReg])
	val, verr := vm.coercePropValue(pd, decl, pname, plain, chunk != nil && chunk.Strict, false)
	if verr == nil {
		if derr := vm.releasedObjWriteErr(o, ci, pname, plain); derr != nil {
			if !vm.handleThrow(chunk, derr) {
				return execResult{err: derr}, true
			}
			return execResult{next: true}, true
		}
	}
	if verr != nil {
		if !vm.handleThrow(chunk, verr) {
			return execResult{err: verr}, true
		}
		return execResult{next: true}, true
	}
	if cur.typ != slotUninit {
		vm.noteDropped(cur)
	}
	vm.noteStored(val)
	ob.slots[idx] = val
	old := regs[valReg]
	regs[valReg] = val
	if trackedType(old.typ) || trackedType(val.typ) {
		vm.wRegSlow(valReg, old)
	}
	return execResult{}, true
}

func (vm *VM) opPropSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	ci := vm.classInfo(regs[in.A])
	if ci == nil {
		err := vm.propWriteNonObjectErr(regs[in.A], consts[in.C].StringVal())
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	pname := consts[in.C].StringVal()
	o := regs[in.A]
	if d := deref(o); d.typ == ObjectType && (*Object)(d.ptr).incomplete {
		if r, ok := vm.incompleteWriteGuard(o, chunk); ok {
			return r
		}
	}
	if ci.IsEnum() {
		err := vm.enumWriteError(ci, o, pname)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// Запись-обратно после дим-записи в перегруженное/недоступное свойство:
	// Zend не пишет временную копию (модификация без эффекта); для by-ref
	// __get значение ложится прямо в объявленное свойство (обход проверки
	// видимости — реальная ячейка уже выдана). Стек wbSkip потребляется
	// только настоящим writeback-опкодом: обычный OpPropSet в то же свойство
	// (например, из чужого __set) не должен «съедать» запись (bug40833).
	if in.Op == OpPropSetOverload || in.Op == OpPropSetOverloadDyn {
		if n := len(vm.wbSkip); n > 0 {
			if e := vm.wbSkip[n-1]; e.ob == vm.objectOf(o) && e.name == pname {
				vm.wbSkip = vm.wbSkip[:n-1]
				if !e.real && deref(regs[in.B]).typ != ObjectType {
					// Zend печатает notice в точке записи. Для значения-объекта
					// запись-обратно не нужна и notice не печатается: мутация
					// объекта видна напрямую (bug40833).
					vm.Notice("Indirect modification of overloaded property " + ci.Name() + "::$" + pname + " has no effect")
				}
				if e.real {
					pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname)
					if declared {
						key := vm.propKey(pd, decl, pname)
						val, verr := vm.coercePropValue(pd, decl, pname, deref(regs[in.B]), vm.chunk != nil && vm.chunk.Strict, false)
						if verr != nil {
							if !vm.handleThrow(chunk, verr) {
								return execResult{err: verr}
							}
							return execResult{next: true}
						}
						vm.setProp(o, key, val)
					} else {
						vm.setProp(o, pname, deref(regs[in.B]))
					}
				}
				return execResult{}
			}
		}
	}
	// Ультра-быстрый путь (IC прогрет): публичное нетипизированное
	// не-readonly свойство, лежащее в слоте; не снято unset, не ссылка.
	if ic := vm.icAt(chunk); ic != nil && ic.propState == icMono && ic.propClass == ci && ic.propName == pname && ic.propScope == vm.ctx.self && ic.propOk && ic.propIdx >= 0 {
		if pd := ic.propPd; pd != nil && (pd.vis == Public || ic.propAccess) && !pd.readonly && !pd.hasType {
			if ob := vm.objectOf(o); ob != nil && ic.propIdx < len(ob.slots) && !ob.IsUnset(ic.propKey) {
				cur := ob.slots[ic.propIdx]
				if cur.typ != RefType && cur.typ != slotUninit {
					val := deref(regs[in.B])
					ob.slots[ic.propIdx] = val
					vm.noteDropped(cur)
					vm.noteStored(val)
					old := regs[in.B]
					regs[in.B] = val
					if trackedType(old.typ) || trackedType(val.typ) {
						vm.wRegSlow(int(in.B), old)
					}
					return execResult{}
				}
			}
		}
	}
	// Быстрый путь: объявленное свойство, доступное, не снятое unset,
	// не readonly — прямая запись в слот.
	if r, handled := vm.propSetDeclaredFast(ci, o, pname, int(in.B), regs, chunk); handled {
		return r
	}
	// Целевое свойство уже ссылка — писать сквозь неё (с guard типизированного
	// свойства), а не заменять ячейку. Ключ хранения: private — по классу
	// объявления (манглированное имя).
	declKey := propKeyForScope(vm.ctx.self, ci, pname)
	if existing, ok := vm.getProp(o, declKey); ok {
		if s := valueSlot(existing); s.typ == RefType {
			if r := s.Ref(); r != nil {
				out, werr := vm.writeRefPropValue(ci, pname, r, regs[in.B], vm.chunk != nil && vm.chunk.Strict)
				if werr != nil {
					if !vm.handleThrow(chunk, werr) {
						return execResult{err: werr}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.B]
					wv := out
					regs[in.B] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.B), old)
					}
				}
				return execResult{}
			}
		}
	}
	plain := deref(regs[in.B])
	if pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname); declared {
		// Объявленное, но недоступное из текущей области свойство при наличии
		// __set() записывается через него (PHP-семантика magic-метода), а не
		// отвергается как "Cannot access ... property".
		if vm.propAccessErr(pd, decl, pname) != nil {
			if _, ok, merr := vm.callMagicMethod(ci, o, "__set", []Slot{MakeString(pname), plain}); ok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				return execResult{}
			}
		}
		// После unset() объявленное свойство переходит в «magic»-состояние:
		// присваивание вызывает __set (если он определён), а не инициализацию.
		// Проверка идёт до checkPropWrite: для readonly-свойства из чужой
		// области Zend тоже уходит в __set (magic_get_set).
		if ob := vm.objectOf(o); ob != nil && ob.IsUnset(declKey) {
			if _, ok, merr := vm.callMagicMethod(ci, o, "__set", []Slot{MakeString(pname), plain}); ok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				return execResult{}
			}
		}
		if err := vm.checkPropWrite(ci, o, pname); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		val, verr := vm.coercePropValue(pd, decl, pname, plain, vm.chunk != nil && vm.chunk.Strict, false)
		if verr == nil {
			if derr := vm.releasedObjWriteErr(o, ci, pname, plain); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
			}
		}
		if verr != nil {
			if !vm.handleThrow(chunk, verr) {
				return execResult{err: verr}
			}
			return execResult{next: true}
		}
		if ob := vm.objectOf(o); ob != nil {
			ob.ClearUnset(declKey)
		}
		vm.setProp(o, declKey, val)
		// Выражение присваивания возвращает уже приведённое значение.
		{
			old := regs[in.B]
			wv := val
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
		return execResult{}
	}
	if handled, herr := vm.propSetExisting(o, pname, plain); handled {
		if herr != nil {
			if !vm.handleThrow(chunk, herr) {
				return execResult{err: herr}
			}
			return execResult{next: true}
		}
		if gres, stop := vm.staticShadowGuard(ci, pname, "__set", chunk); stop {
			return gres
		}
		return execResult{}
	}
	if _, ok, err := vm.callMagicMethod(ci, regs[in.A], "__set", []Slot{MakeString(pname), plain}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		return execResult{}
	}
	if gres, stop := vm.staticShadowGuard(ci, pname, "__set", chunk); stop {
		return gres
	}
	if err := checkDynamicProp(ci, pname); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	vm.setProp(o, pname, plain)

	return execResult{}
}

func (vm *VM) opPropSetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	// Имя свойства-объект приводится через __toString; исключение из него
	// всплывает наружу (exception_from_toString: $obj->{$badStr} = $v).
	// \0-имя проверяется ПОСЛЕ магии: Zend вызывает __set для такого имени
	// и падает уже внутри него (bug52484_2).
	nameSlot := deref(regs[in.C])
	pname := ""
	if nameSlot.typ == ObjectType || nameSlot.typ == ClosureType {
		sp, serr := vm.ToStringValue(nameSlot)
		if serr != nil {
			if !vm.handleThrow(chunk, serr) {
				return execResult{err: serr}
			}
			return execResult{next: true}
		}
		pname = sp
	} else {
		pname = nameSlot.ToString()
	}
	ci := vm.classInfo(regs[in.A])
	if ci == nil {
		err := vm.propWriteNonObjectErr(regs[in.A], pname)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	o := regs[in.A]
	declKey := propKeyForScope(vm.ctx.self, ci, pname)
	if d := deref(o); d.typ == ObjectType && (*Object)(d.ptr).incomplete {
		if r, ok := vm.incompleteWriteGuard(o, chunk); ok {
			return r
		}
	}
	if ci.IsEnum() {
		err := vm.enumWriteError(ci, o, pname)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// Быстрый путь: объявленное свойство, доступное, не снятое unset,
	// не readonly — прямая запись в слот.
	if r, handled := vm.propSetDeclaredFast(ci, o, pname, int(in.B), regs, chunk); handled {
		return r
	}
	if existing, ok := vm.getProp(o, declKey); ok {
		if s := valueSlot(existing); s.typ == RefType {
			if r := s.Ref(); r != nil {
				out, werr := vm.writeRefPropValue(ci, pname, r, regs[in.B], vm.chunk != nil && vm.chunk.Strict)
				if werr != nil {
					if !vm.handleThrow(chunk, werr) {
						return execResult{err: werr}
					}
					return execResult{next: true}
				}
				{
					old := regs[in.B]
					wv := out
					regs[in.B] = wv
					if trackedType(old.typ) || trackedType(wv.typ) {
						vm.wRegSlow(int(in.B), old)
					}
				}
				return execResult{}
			}
		}
	}
	plain := deref(regs[in.B])
	if pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname); declared {
		// Объявленное, но недоступное из текущей области свойство при наличии
		// __set() записывается через него (PHP-семантика magic-метода), а не
		// отвергается как "Cannot access ... property".
		if vm.propAccessErr(pd, decl, pname) != nil {
			if _, ok, merr := vm.callMagicMethod(ci, o, "__set", []Slot{MakeString(pname), plain}); ok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				return execResult{}
			}
		}
		// После unset() объявленное свойство переходит в «magic»-состояние:
		// присваивание вызывает __set (если он определён), а не инициализацию.
		// Проверка идёт до checkPropWrite: для readonly-свойства из чужой
		// области Zend тоже уходит в __set (magic_get_set).
		if ob := vm.objectOf(o); ob != nil && ob.IsUnset(declKey) {
			if _, ok, merr := vm.callMagicMethod(ci, o, "__set", []Slot{MakeString(pname), plain}); ok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				return execResult{}
			}
		}
		if err := vm.checkPropWrite(ci, o, pname); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		val, verr := vm.coercePropValue(pd, decl, pname, plain, vm.chunk != nil && vm.chunk.Strict, false)
		if verr != nil {
			if !vm.handleThrow(chunk, verr) {
				return execResult{err: verr}
			}
			return execResult{next: true}
		}
		if ob := vm.objectOf(o); ob != nil {
			ob.ClearUnset(declKey)
		}
		vm.setProp(o, declKey, val)
		// Выражение присваивания возвращает уже приведённое значение.
		{
			old := regs[in.B]
			wv := val
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
		return execResult{}
	}
	if handled, herr := vm.propSetExisting(o, pname, plain); handled {
		if herr != nil {
			if !vm.handleThrow(chunk, herr) {
				return execResult{err: herr}
			}
			return execResult{next: true}
		}
		if gres, stop := vm.staticShadowGuard(ci, pname, "__set", chunk); stop {
			return gres
		}
		return execResult{}
	}
	if _, ok, err := vm.callMagicMethod(ci, regs[in.A], "__set", []Slot{MakeString(pname), plain}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		return execResult{}
	}
	if err := vm.propNulNameErr(pname); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if gres, stop := vm.staticShadowGuard(ci, pname, "__set", chunk); stop {
		return gres
	}
	if err := checkDynamicProp(ci, pname); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	vm.setProp(o, pname, plain)

	return execResult{}
}

// propNulNameErr — Error доступа к свойству с именем, начинающимся с NUL
// (Zend: зарезервировано для mangled-имён private/protected).
func (vm *VM) propNulNameErr(name string) *Throwable {
	if !strings.HasPrefix(name, "\x00") {
		return nil
	}
	return NewThrowable("Error", `Cannot access property starting with "\0"`)
}

// propModifyNonObjectErr — Error при записи свойства по ссылке в не-объект
// (Zend: «Attempt to modify property», в отличие от «assign» у значения).
func (vm *VM) propModifyNonObjectErr(recv Slot, name string) *Throwable {
	return NewThrowable("Error", fmt.Sprintf("Attempt to modify property %q on %s", name, valueTypeName(vm, deref(recv))))
}

// propWriteNonObjectErr — Error при записи свойства в не-объект.
func (vm *VM) propWriteNonObjectErr(recv Slot, name string) *Throwable {
	return NewThrowable("Error", fmt.Sprintf("Attempt to assign property %q on %s", name, valueTypeName(vm, deref(recv))))
}

// opPropSetRef — присваивание свойства по ссылке (`$o->p =& ...`): значение
// B сохраняется как ссылка (для типизированного свойства на неё вешается guard).
func (vm *VM) opPropSetRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	o := regs[in.A]
	ci := vm.classInfo(o)
	if ci == nil {
		err := vm.propModifyNonObjectErr(o, consts[in.C].StringVal())
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	pname := consts[in.C].StringVal()
	return vm.propSetRefCommon(regs, chunk, in, ci, o, pname)
}

func (vm *VM) opPropSetRefDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	o := regs[in.A]
	ci := vm.classInfo(o)
	if ci == nil {
		err := vm.propModifyNonObjectErr(o, deref(regs[in.C]).ToString())
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	pname := deref(regs[in.C]).ToString()
	if ns := deref(regs[in.C]); ns.typ == ObjectType {
		if str, ok := vm.tryToString(ns); ok {
			pname = str
		}
	}
	return vm.propSetRefCommon(regs, chunk, in, ci, o, pname)
}

func (vm *VM) propSetRefCommon(regs []Slot, chunk *Chunk, in Instruction, ci ClassInfo, o Slot, pname string) execResult {
	if r, ok := vm.incompleteWriteGuard(o, chunk); ok {
		return r
	}
	if ci.IsEnum() {
		err := vm.enumWriteError(ci, o, pname)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if overloaded, byRef := vm.propRefOverloaded(ci, o, pname, true); overloaded && !byRef {
		// `$obj->prop =& $x` для свойства из __get без ссылки: Zend печатает
		// Notice и бросает Error (ссылка в overloaded-свойство невозможна).
		vm.Notice("Indirect modification of overloaded property " + classDisplayName(ci) + "::$" + pname + " has no effect")
		err := NewThrowable("Error", "Cannot assign by reference to overloaded object")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	cell := regs[in.B]
	if cell.typ != RefType {
		{
			old := regs[in.B]
			wv := MakeRef(&Ref{v: cell})
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
		cell = regs[in.B]
	}
	if pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname); declared {
		// Привязка ссылки к readonly-свойству запрещена (gh7942): для
		// инициализированного — «Cannot modify», для пустого — «Cannot
		// indirectly modify» (readonly_modification).
		if pd.readonly {
			if err := vm.propAccessErr(pd, decl, pname); err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			var err error
			if _, init := vm.getProp(o, vm.propKey(pd, decl, pname)); init {
				err = NewThrowable("Error", "Cannot modify readonly property "+classDisplayName(decl)+"::$"+pname)
			} else {
				err = NewThrowable("Error", "Cannot indirectly modify readonly property "+decl.Name()+"::$"+pname)
			}
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		if err := vm.checkPropWrite(ci, o, pname); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		key := vm.propKey(pd, decl, pname)
		// Привязка ссылки (`=&`) приводит скаляры по режиму strict текущего
		// файла (Zend zend_verify_prop_assignable_by_ref): в слабом режиме
		// значение ячейки меняется на месте.
		val, verr := vm.coercePropValue(pd, decl, pname, cell, vm.chunk != nil && vm.chunk.Strict, true)
		if verr != nil {
			if !vm.handleThrow(chunk, verr) {
				return execResult{err: verr}
			}
			return execResult{next: true}
		}
		if ob := vm.objectOf(o); ob != nil {
			ob.ClearUnset(key)
		}
		if old, ok := vm.getProp(o, key); ok {
			vm.detachRefGuard(valueSlot(old), val, decl, pname)
		}
		if val.typ == RefType && val.Ref().hasSources() {
			vm.markTypedRefHolder(o)
		}
		vm.setProp(o, key, val)
		{
			old := regs[in.B]
			wv := val
			regs[in.B] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.B), old)
			}
		}
		return execResult{}
	}
	// Свойство существовало как ссылка — перепривязка алиасит на новую ячейку.
	if gres, stop := vm.staticShadowGuard(ci, pname, "", chunk); stop {
		return gres
	}
	// Класс без динамических свойств: создание нового через ссылку запрещено.
	if derr := checkDynamicProp(ci, pname); derr != nil {
		return execResult{err: derr}
	}
	key := propKeyForScope(vm.ctx.self, ci, pname)
	if old, ok := vm.getProp(o, key); ok {
		if pd2, dc2, ok2 := vm.scopedProp(vm.ctx.self, ci, pname); ok2 && pd2 != nil && pd2.hasType {
			vm.detachRefGuard(valueSlot(old), cell, dc2, pname)
		} else {
			vm.detachRefGuard(valueSlot(old), cell, nil, pname)
		}
	}
	vm.setProp(o, key, cell)
	return execResult{}
}

func (vm *VM) opPropGetSafe(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	return vm.propGetSafeCommon(regs, chunk, in, recv, consts[in.C].StringVal())
}

func (vm *VM) opPropGetSafeDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	pname := deref(regs[in.C]).ToString()
	if ns := deref(regs[in.C]); ns.typ == ObjectType {
		if str, ok := vm.tryToString(ns); ok {
			pname = str
		}
	}
	return vm.propGetSafeCommon(regs, chunk, in, recv, pname)
}

// propGetSafeCommon — чтение свойства в IS-режиме (промежуточные звенья
// цепочек isset/??): отсутствующее или неинициализированное свойство даёт
// null без диагностик; для несуществующего/снятого/недоступного свойства
// вызывается __isset, и при true — __get (Zend FETCH_OBJ_IS).
func (vm *VM) propGetSafeCommon(regs []Slot, chunk *Chunk, in Instruction, recv Slot, pname string) execResult {
	var res Slot = MakeNull()
	ci := vm.classInfo(recv)
	if ci != nil {
		resolved := false
		// Быстрый путь: инициализированное объявленное свойство в слоте.
		if pd, _, key, idx, declared := vm.icProp(ci, pname, chunk); declared && idx >= 0 {
			if ob := vm.objectOf(recv); ob != nil && idx < len(ob.slots) && !ob.IsUnset(key) {
				if pd.vis == Public || vm.icPropAccess(vm.icAt(chunk)) {
					if s := ob.slots[idx]; s.typ != slotUninit {
						res = s
						resolved = true
					}
				}
			}
		}
		if resolved {
			goto safeWrite
		}
		pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname)
		key := vm.propKey(pd, decl, pname)
		ob := vm.objectOf(recv)
		isUnset := ob != nil && ob.IsUnset(key)
		if !declared || isUnset {
			// Необъявленное или снятое свойство: значение — из хранилища
			// (если есть), иначе магия.
			if v, ok := vm.getProp(recv, key); ok && !isUnset {
				SlotOf(&res, v)
				resolved = true
			}
		} else if v, ok := vm.getProp(recv, key); ok {
			SlotOf(&res, v)
			resolved = true
		} else if vm.propAccessErr(pd, decl, pname) == nil {
			// Объявленное неинициализированное свойство: null.
			resolved = true
		}
		if !resolved {
			if r, ok, err := vm.callMagicMethod(ci, recv, "__isset", []Slot{MakeString(pname)}); ok {
				if err != nil {
					if !vm.handleThrow(chunk, err) {
						return execResult{err: err}
					}
					return execResult{next: true}
				}
				if deref(r).ToBool() {
					if g, ok2, err2 := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)}); ok2 {
						if err2 != nil {
							if !vm.handleThrow(chunk, err2) {
								return execResult{err: err2}
							}
							return execResult{next: true}
						}
						chk, gerr := vm.checkMagicGet(ci, pname, g)
						if gerr != nil {
							if !vm.handleThrow(chunk, gerr) {
								return execResult{err: gerr}
							}
							return execResult{next: true}
						}
						res = chk
					}
				}
			}
		}
	}
safeWrite:
	{
		old := regs[in.A]
		regs[in.A] = res
		if trackedType(old.typ) || trackedType(res.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(res)
	return execResult{}
}

func (vm *VM) opPropIsset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	ci := vm.classInfo(recv)
	if ci == nil {
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	pname := consts[in.C].StringVal()
	return vm.propIssetCommon(regs, chunk, in, ci, recv, pname)
}

func (vm *VM) opPropIssetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	ci := vm.classInfo(recv)
	if ci == nil {
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	pname := deref(regs[in.C]).ToString()
	return vm.propIssetCommon(regs, chunk, in, ci, recv, pname)
}

// staticShadowGuard обрабатывает доступ к объявленному статическому свойству
// через синтаксис экземпляра ($obj->staticProp). Если соответствующего
// магического метода (magic) нет: для недоступного свойства поднимается Error
// "Cannot access ... property", для доступного печатается Notice "Accessing
// static property ... as non static". Возвращает (результат, true), если
// обработку нужно прервать.
func (vm *VM) staticShadowGuard(ci ClassInfo, pname, magic string, chunk *Chunk) (execResult, bool) {
	rc, ok := asRuntimeClass(ci)
	if !ok || rc == nil {
		return execResult{}, false
	}
	pd, decl, found := rc.staticShadowInfo(pname)
	if !found {
		return execResult{}, false
	}
	if magic != "" && ci.lookupMethod(magic) != nil {
		return execResult{}, false
	}
	if err := vm.propAccessErr(pd, decl, pname); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}, true
		}
		return execResult{next: true}, true
	}
	vm.Notice(fmt.Sprintf("Accessing static property %s::$%s as non static", classDisplayName(decl), pname))
	return execResult{}, false
}

// propIssetCommon реализует isset() над свойством объекта с Zend-семантикой:
// объявленное недоступное из текущей области свойство уходит в __isset, даже
// если оно установлено (например protected снаружи); объявленное типизированное
// без значения даёт false без __isset; после unset() свойство переходит в
// «magic»-состояние.
func (vm *VM) propIssetCommon(regs []Slot, chunk *Chunk, in Instruction, ci ClassInfo, recv Slot, pname string) execResult {
	// isset() над свойством incomplete-объекта: warning + false.
	if r, ok := vm.incompleteReadGuard(recv, &regs[in.A], int(in.A)); ok {
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return r
	}
	pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname)
	ob := vm.objectOf(recv)
	key := vm.propKey(pd, decl, pname)
	isUnset := ob != nil && ob.IsUnset(key)
	if declared && !isUnset && vm.propAccessErr(pd, decl, pname) != nil {
		// Недоступное объявленное свойство: поведение __isset (или false).
		if res, ok, err := vm.callMagicMethod(ci, recv, "__isset", []Slot{MakeString(pname)}); ok {
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := MakeBool(deref(res).ToBool())
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if v, ok := vm.getProp(recv, key); ok {
		var tmp Slot
		SlotOf(&tmp, v)
		{
			old := regs[in.A]
			wv := MakeBool(deref(tmp).typ != NullType)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if declared && !isUnset {
		{
			old := regs[in.A]
			wv := MakeBool(false)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if res, ok, err := vm.callMagicMethod(ci, recv, "__isset", []Slot{MakeString(pname)}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		{
			old := regs[in.A]
			wv := MakeBool(deref(res).ToBool())
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	{
		old := regs[in.A]
		wv := MakeBool(false)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opPropUnset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.A])
	ci := vm.classInfo(recv)
	if ci == nil {
		return execResult{}
	}
	pname := consts[in.C].StringVal()
	if r, ok := vm.incompleteWriteGuard(recv, chunk); ok {
		return r
	}
	if ci.IsEnum() {
		err := vm.enumUnsetError(ci, recv, pname)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if _, _, declared := vm.scopedProp(vm.ctx.self, ci, pname); declared {
		key := propKeyForScope(vm.ctx.self, ci, pname)
		// Свойство в состоянии unset (уже снято): Zend уходит в __unset,
		// даже если оно readonly (magic_get_set).
		if ob := vm.objectOf(recv); ob != nil && ob.IsUnset(key) {
			if _, ok, merr := vm.callMagicMethod(ci, recv, "__unset", []Slot{MakeString(pname)}); ok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				return execResult{}
			}
		}
		if err := vm.checkPropUnset(ci, recv, pname); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		// unset снимает привязку ссылки к типизированному свойству: ссылка,
		// живущая в переменной, перестаёт проверяться по типу (Zend).
		vm.detachPropRefGuard(recv, pname, key)
		if ob := vm.objectOf(recv); ob != nil {
			ob.MarkUnset(key)
		}
		vm.unsetProp(recv, key)
		return execResult{}
	}
	if _, ok := vm.getProp(recv, pname); ok {
		if gres, stop := vm.staticShadowGuard(ci, pname, "__unset", chunk); stop {
			return gres
		}
		vm.unsetProp(recv, pname)
		return execResult{}
	}
	if _, ok, err := vm.callMagicMethod(ci, recv, "__unset", []Slot{MakeString(pname)}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		return execResult{}
	}
	if gres, stop := vm.staticShadowGuard(ci, pname, "__unset", chunk); stop {
		return gres
	}

	return execResult{}
}

func (vm *VM) opPropUnsetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.A])
	// Zend сначала уходит в __unset (если свойство отсутствует), и лишь
	// недоступная по имени "\0" запись внутри магического метода даёт Error
	// (bug52484: в трейсе остаётся кадр A->__unset('\x00')); проверка имени
	// делается после попытки магии (см. ниже).
	ci := vm.classInfo(recv)
	if ci == nil {
		return execResult{}
	}
	pname := deref(regs[in.C]).ToString()
	if r, ok := vm.incompleteWriteGuard(recv, chunk); ok {
		return r
	}
	if ci.IsEnum() {
		err := vm.enumUnsetError(ci, recv, pname)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if _, _, declared := vm.scopedProp(vm.ctx.self, ci, pname); declared {
		key := propKeyForScope(vm.ctx.self, ci, pname)
		// Свойство в состоянии unset (уже снято): Zend уходит в __unset,
		// даже если оно readonly (magic_get_set).
		if ob := vm.objectOf(recv); ob != nil && ob.IsUnset(key) {
			if _, ok, merr := vm.callMagicMethod(ci, recv, "__unset", []Slot{MakeString(pname)}); ok {
				if merr != nil {
					if !vm.handleThrow(chunk, merr) {
						return execResult{err: merr}
					}
					return execResult{next: true}
				}
				return execResult{}
			}
		}
		if err := vm.checkPropUnset(ci, recv, pname); err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		// unset снимает привязку ссылки к типизированному свойству: ссылка,
		// живущая в переменной, перестаёт проверяться по типу (Zend).
		vm.detachPropRefGuard(recv, pname, key)
		if ob := vm.objectOf(recv); ob != nil {
			ob.MarkUnset(key)
		}
		vm.unsetProp(recv, key)
		return execResult{}
	}
	if _, ok := vm.getProp(recv, pname); ok {
		if gres, stop := vm.staticShadowGuard(ci, pname, "__unset", chunk); stop {
			return gres
		}
		vm.unsetProp(recv, pname)
		return execResult{}
	}
	if _, ok, err := vm.callMagicMethod(ci, recv, "__unset", []Slot{MakeString(pname)}); ok {
		if err != nil {
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		return execResult{}
	}
	if err := vm.propNulNameErr(pname); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if gres, stop := vm.staticShadowGuard(ci, pname, "__unset", chunk); stop {
		return gres
	}

	return execResult{}
}

func (vm *VM) opMethodCall(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	recv := deref(regs[in.B])
	// Вызов метода на замыкании (Closure::call/bindTo/...).
	if recv.typ == ClosureType {
		mname := consts[in.C&0xFFFF].StringVal()
		argc := int(in.C >> 16)
		var passed []Slot
		if argc > 0 && int(in.B)+1+argc <= len(regs) {
			passed = regs[int(in.B)+1 : int(in.B)+1+argc]
		}
		if res, ok, cerr := vm.closureMethodCall(recv.asClosure(), mname, passed); ok {
			if cerr != nil {
				if !vm.handleThrow(chunk, cerr) {
					return execResult{err: cerr}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := res
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			shareSlot(res)
			return execResult{}
		}
	}
	ci := vm.classInfo(recv)
	if ci == nil {
		mn := consts[in.C&0xFFFF].StringVal()
		err := NewThrowable("Error", "Call to a member function "+mn+"() on "+memberCallTypeName(recv))
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if cls, ok := incompleteObjName(vm.objectOf(recv)); ok {
		err := incompleteMethodErr(cls)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	argc := int(in.C >> 16)
	ic := vm.icAt(chunk)
	var m *Function
	var decl ClassInfo
	var vis Visibility
	var mname string
	if ic != nil && ic.state == icMono && ic.class == ci {
		m, decl, vis = ic.fn, ic.decl, ic.vis
	}
	if m == nil {
		mname = consts[in.C&0xFFFF].StringVal()
		if pm, pdecl, pvis, ok := vm.scopePrivateMethod(ci, mname); ok {
			m, decl, vis = pm, pdecl, pvis
		} else {
			m, decl, vis = ci.methodInfo(mname)
			if m == nil && strings.EqualFold(mname, "__construct") {
				// __construct хранится отдельно (ci.Constructor), но Zend
				// допускает явный вызов $obj->__construct().
				m, decl, vis = ci.CtorInfo()
			}
			if m != nil {
				icFillMethod(ic, ci, mname, m, decl, vis)
			}
		}
	}
	if m == nil {
		if ci.hasAbstract(mname) {
			aerr := NewThrowable("Error", "Cannot call abstract method "+ci.Name()+"::"+mname+"()")
			if !vm.handleThrow(chunk, aerr) {
				return execResult{err: aerr}
			}
			return execResult{next: true}
		}
		var passed []Slot
		if argc > 0 && int(in.B)+1+argc <= len(regs) {
			passed = regs[int(in.B)+1 : int(in.B)+1+argc]
		}
		if res, ok, err := vm.callMagicMethod(ci, recv, "__call", []Slot{MakeString(mname), argsArray(passed)}); ok {
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := res
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			shareSlot(res)
			return execResult{}
		}
		err := NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if vis != Public {
		if mname == "" && m != nil {
			mname = m.Name
		}
		if !vm.methodAccessibleFromProto(ci, decl, mname, vis, vm.ctx.self) {
			pm, pdecl, pvis, ok := vm.scopePrivateMethod(ci, mname)
			if !ok {
				// Zend zend_std_get_method: недоступный метод при наличии
				// __call вызывает __call, а не бросает Error.
				var passed []Slot
				if argc > 0 && int(in.B)+1+argc <= len(regs) {
					passed = regs[int(in.B)+1 : int(in.B)+1+argc]
				}
				if res, mok, merr := vm.callMagicMethod(ci, recv, "__call", []Slot{MakeString(mname), argsArray(passed)}); mok {
					if merr != nil {
						if !vm.handleThrow(chunk, merr) {
							return execResult{err: merr}
						}
						return execResult{next: true}
					}
					{
						old := regs[in.A]
						wv := res
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
					shareSlot(res)
					return execResult{}
				}
				err := vm.accessError(decl, mname, vis)
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			m, decl, vis = pm, pdecl, pvis
		}
	}
	if m.IsTrivial() {
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
	var args []Slot
	if argc > 0 && int(in.B)+1+argc <= len(regs) {
		args = regs[int(in.B)+1 : int(in.B)+1+argc]
	}
	// Статический метод, вызванный как $obj->m(): $this внутри недоступен
	// (Zend передаёт NULL this, сохраняя scope класса) — bug27304.
	if m.IsStatic {
		recv = MakeNull()
	}
	res, err := vm.invokeMethod(m, recv, ci, args)
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := res
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(res)

	return execResult{}
}
