package vm

import "strings"

// execMethod.go — вызовы методов/статических, instanceof, static prop/const.

// closureMethodCall обрабатывает методы, вызываемые на замыкании
// (Closure::call, Closure::bindTo, Closure::bind). Возвращает ok=false, если
// метод не относится к замыканию.
func (vm *VM) closureMethodCall(cl *Closure, mname string, args []Slot) (Slot, bool, error) {
	if cl == nil {
		return Slot{}, false, nil
	}
	switch strings.ToLower(mname) {
	case "fromcallable":
		cb := MakeNull()
		if len(args) > 0 {
			cb = args[0]
		}
		cl2, err := vm.closureFromCallable(cb)
		if err != nil {
			return Slot{}, true, err
		}
		return cl2, true, nil
	case "bindto":
		var newThis, scope Slot
		if len(args) > 0 {
			newThis = args[0]
		}
		if len(args) > 1 {
			scope = args[1]
		}
		res, err := vm.closureBind(cl, newThis, scope, len(args) > 1)
		return res, true, err
	case "__invoke":
		res, err := vm.invokeClosure(cl, args)
		return res, true, err
	case "call":
		res, err := vm.closureCall(cl, args)
		return res, true, err
	}
	return Slot{}, false, nil
}

func (vm *VM) opMethodCallDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	// Имя метода обязано быть строкой: Zend бросает Error даже при наличии
	// __call (objects_025: $a->$b() с $b = NULL).
	if n := deref(regs[in.C&0xFF]); n.typ != StringType {
		err := NewThrowable("Error", "Method name must be a string")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	recv := deref(regs[in.B])
	// Вызов метода на замыкании (Closure::call/bindTo/...).
	if recv.typ == ClosureType {
		mname := deref(regs[in.C&0xFF]).ToString()
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
		mn := deref(regs[in.C&0xFF]).ToString()
		err := NewThrowable("Error", "Call to a member function "+mn+"() on "+memberCallTypeName(recv))
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	mname := deref(regs[in.C&0xFF]).ToString()
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
	if ic != nil && ic.state == icMono && ic.class == ci && ic.name == mname {
		m, decl, vis = ic.fn, ic.decl, ic.vis
	}
	if m == nil {
		if pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname); ok {
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
	if vis != Public && !vm.methodAccessibleFromProto(ci, decl, methodNameForProto(m, mname), vis, vm.ctx.self) {
		pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname)
		if !ok {
			// Zend zend_std_get_method: недоступный метод при наличии __call
			// вызывает __call, а не бросает Error.
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

func (vm *VM) opStaticCall(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	if cls.IsTrait() {
		vm.Deprecated("Calling static trait method " + cls.Name() + "::" + consts[in.C&0xFFFF].StringVal() + " is deprecated, it should only be called on a class using the trait")
	}
	argc := int(in.C >> 16)
	ic := vm.icAt(chunk)
	var m *Function
	var decl ClassInfo
	var vis Visibility
	var mname string
	if ic != nil && ic.state == icMono && ic.class == cls {
		m, decl, vis = ic.fn, ic.decl, ic.vis
	}
	if m == nil {
		mname = consts[in.C&0xFFFF].StringVal()
		m, decl, vis = cls.methodInfo(mname)
		if m == nil && mname == "__construct" {
			if rc, ok := asRuntimeClass(cls); ok {
				c, cd, cv := rc.ctorInfo()
				m, decl, vis = c, cd, cv
			} else {
				decl = cls
				vis = Public
			}
		}
		if m != nil {
			icFillMethod(ic, cls, mname, m, decl, vis)
		}
	}
	if m == nil {
		if cls.hasAbstract(mname) {
			aerr := NewThrowable("Error", "Cannot call abstract method "+cls.Name()+"::"+mname+"()")
			if !vm.handleThrow(chunk, aerr) {
				return execResult{err: aerr}
			}
			return execResult{next: true}
		}
		var passed []Slot
		if argc > 0 && int(in.A)+1+argc <= len(regs) {
			passed = regs[int(in.A)+1 : int(in.A)+1+argc]
		}
		calledM := cls
		if in.B <= 2 && vm.ctx.called != nil {
			calledM = vm.ctx.called
		}
		if sres, ok, err := vm.callMagicUndefStatic(cls, calledM, mname, passed); ok {
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := sres
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			shareSlot(sres)
			return execResult{}
		}
		err := NewThrowable("Error", "Call to undefined method "+cls.Name()+"::"+mname+"()")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if vis != Public && !vm.methodAccessibleFromProto(cls, decl, methodNameForProto(m, mname), vis, vm.ctx.self) {
		if mname == "" {
			mname = consts[in.C&0xFFFF].StringVal()
		}
		pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname)
		if !ok {
			// Zend zend_std_get_static_method: недоступный статический метод
			// при наличии __callStatic вызывает __callStatic, а не Error.
			if mname != "__construct" && mname != "__destruct" {
				var passed []Slot
				if argc > 0 && int(in.A)+1+argc <= len(regs) {
					passed = regs[int(in.A)+1 : int(in.A)+1+argc]
				}
				calledM := cls
				if in.B <= 2 && vm.ctx.called != nil {
					calledM = vm.ctx.called
				}
				if sres, mok, merr := vm.callMagicUndefStatic(cls, calledM, mname, passed); mok {
					if merr != nil {
						if !vm.handleThrow(chunk, merr) {
							return execResult{err: merr}
						}
						return execResult{next: true}
					}
					{
						old := regs[in.A]
						wv := sres
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
					shareSlot(sres)
					return execResult{}
				}
			}
			var err error
			if mname == "__construct" || mname == "__destruct" {
				err = NewThrowable("Error", "Cannot call "+visWord(vis)+" "+classDisplayName(decl)+"::"+mname+"()")
			} else {
				err = vm.accessError(decl, mname, vis)
			}
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		m, decl, vis = pm, pdecl, pvis
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
	var sargs []Slot
	if argc > 0 && int(in.A)+1+argc <= len(regs) {
		sargs = regs[int(in.A)+1 : int(in.A)+1+argc]
	}
	// receiver — текущий $this (для parent::__construct и т.п.).
	if err := vm.checkStaticCall(m, cls); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	called := cls
	if in.B <= 2 && vm.ctx.called != nil {
		// scoped-вызов (self::/static::/parent::): вызванный класс сохраняется
		// (Zend: get_called_class видит исходный late-static-binding).
		called = vm.ctx.called
	} else if !m.IsStatic && vm.ctx.called != nil {
		// Нестатический метод, вызванный статически с совместимым $this
		// (A::m() из метода наследника): Zend сохраняет текущий called scope.
		if ti := vm.thisClassInfo(); ti != nil && ti.IsA(cls) {
			called = vm.ctx.called
		}
	}
	sres, err := vm.invokeMethod(m, vm.staticCallRecv(cls), called, sargs)
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := sres
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(sres)

	return execResult{}
}

func (vm *VM) opStaticCallDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	mname := deref(regs[in.C&0xFF]).ToString()
	if cls.IsTrait() {
		vm.Deprecated("Calling static trait method " + cls.Name() + "::" + mname + " is deprecated, it should only be called on a class using the trait")
	}
	argc := int(in.C >> 16)
	ic := vm.icAt(chunk)
	var m *Function
	var decl ClassInfo
	var vis Visibility
	if ic != nil && ic.state == icMono && ic.class == cls && ic.name == mname {
		m, decl, vis = ic.fn, ic.decl, ic.vis
	}
	if m == nil {
		m, decl, vis = cls.methodInfo(mname)
		if m != nil {
			icFillMethod(ic, cls, mname, m, decl, vis)
		}
	}
	if m == nil {
		if cls.hasAbstract(mname) {
			aerr := NewThrowable("Error", "Cannot call abstract method "+cls.Name()+"::"+mname+"()")
			if !vm.handleThrow(chunk, aerr) {
				return execResult{err: aerr}
			}
			return execResult{next: true}
		}
		var passed []Slot
		if argc > 0 && int(in.A)+1+argc <= len(regs) {
			passed = regs[int(in.A)+1 : int(in.A)+1+argc]
		}
		calledM := cls
		if in.B <= 2 && vm.ctx.called != nil {
			calledM = vm.ctx.called
		}
		if sres, ok, err := vm.callMagicUndefStatic(cls, calledM, mname, passed); ok {
			if err != nil {
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := sres
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			shareSlot(sres)
			return execResult{}
		}
		err := NewThrowable("Error", "Call to undefined method "+cls.Name()+"::"+mname+"()")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if vis != Public && !vm.methodAccessibleFromProto(cls, decl, methodNameForProto(m, mname), vis, vm.ctx.self) {
		pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname)
		if !ok {
			// Недоступный статический метод + __callStatic → __callStatic.
			if mname != "__construct" && mname != "__destruct" {
				var passed []Slot
				if argc > 0 && int(in.A)+1+argc <= len(regs) {
					passed = regs[int(in.A)+1 : int(in.A)+1+argc]
				}
				calledM := cls
				if in.B <= 2 && vm.ctx.called != nil {
					calledM = vm.ctx.called
				}
				if sres, mok, merr := vm.callMagicUndefStatic(cls, calledM, mname, passed); mok {
					if merr != nil {
						if !vm.handleThrow(chunk, merr) {
							return execResult{err: merr}
						}
						return execResult{next: true}
					}
					{
						old := regs[in.A]
						wv := sres
						regs[in.A] = wv
						if trackedType(old.typ) || trackedType(wv.typ) {
							vm.wRegSlow(int(in.A), old)
						}
					}
					shareSlot(sres)
					return execResult{}
				}
			}
			err := vm.accessError(decl, mname, vis)
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		m, decl, vis = pm, pdecl, pvis
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
	var sargs []Slot
	if argc > 0 && int(in.A)+1+argc <= len(regs) {
		sargs = regs[int(in.A)+1 : int(in.A)+1+argc]
	}
	if err := vm.checkStaticCall(m, cls); err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	sres, err := vm.invokeMethod(m, vm.staticCallRecv(cls), cls, sargs)
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := sres
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	shareSlot(sres)

	return execResult{}
}

func (vm *VM) opThis(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if deref(vm.cur.this).typ != ObjectType {
		err := NewThrowable("Error", "Using $this when not in object context")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := vm.cur.this
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opInstanceOf(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	ci := vm.classInfo(regs[in.B])
	if ci == nil {
		if s := deref(regs[in.B]); s.typ == ClosureType {
			ci = vm.findClass("Closure")
		}
	}
	res := ci != nil && int(in.C) < len(vm.classes) && ci.isA(vm.classes[in.C])
	if !res && ci != nil && int(in.C) < len(vm.classes) {
		// Неявный Stringable: класс с __toString (в т.ч. из трейта) проходит
		// проверку instanceof Stringable.
		if t := vm.classes[in.C]; t != nil && strings.EqualFold(t.Name(), "Stringable") &&
			ci.lookupMethod("__toString") != nil {
			res = true
		}
	}
	{
		old := regs[in.A]
		wv := MakeBool(res)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opClassOf(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	ci := vm.classInfo(regs[in.B])
	if ci != nil {
		{
			old := regs[in.A]
			wv := MakeString(ci.Name())
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	} else {
		{
			old := regs[in.A]
			wv := MakeString("")
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}

	return execResult{}
}

func (vm *VM) opClassName(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	if in.B == 255 {
		// __CLASS__: имя области класса; вне класса — пустая строка.
		name := ""
		if ci := vm.ctx.self; ci != nil {
			name = ci.Name()
		}
		{
			old := regs[in.A]
			wv := MakeString(name)
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	ci := vm.classSel(in.B)
	if ci != nil {
		{
			old := regs[in.A]
			wv := MakeString(ci.Name())
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
		return execResult{}
	}
	if in.B < 3 {
		// `self::class`/`static::class`/`parent::class` без области класса:
		// Zend бросает "Cannot use \"X\" in the global scope", а не
		// "Cannot access ..." (class_name_as_scalar_error_005/006/007).
		// В контексте константного выражения (C=1) текст иной:
		// "Cannot use \"X\" when no class scope is active".
		kw := ""
		switch in.B {
		case 0:
			kw = "self"
		case 1:
			kw = "static"
		case 2:
			kw = "parent"
		}
		msg := `Cannot use "` + kw + `" in the global scope`
		if in.B == 2 && vm.ctx.self != nil {
			// parent::class в классе без родителя (ошибка отложена из
			// трейта): Zend сообщает про отсутствие родителя.
			msg = `Cannot use "parent" when current class scope has no parent`
		} else if in.C == 1 {
			msg = `Cannot use "` + kw + `" when no class scope is active`
		}
		err := NewThrowable("Error", msg)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := regs[in.A]
		wv := MakeString("")
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opClone(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	ci := vm.classInfo(regs[in.B])
	if ci == nil {
		err := NewThrowable("Error", "__clone method called on non-object")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if ci.IsEnum() || ci.Name() == "Generator" || ci.Name() == "Fiber" {
		err := NewThrowable("Error", "Trying to clone an uncloneable object of class "+ci.Name())
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	src := deref(regs[in.B])
	cloned := ci.cloneObject(vm.objectOf(src))
	if cloned == nil {
		return execResult{err: errNotObject}
	}
	// Клон — новый объект: хендл, учёт ссылок и флаги сборщика не переносятся
	// (Zend выдаёт клону собственный #N из free-list; nativeClone копирует
	// Go-структуру целиком, включая шапку Object).
	cloned.objID = 0
	cloned.refs = 0
	cloned.dead = false
	cloned.gcPending = false
	// Скопированные ссылки типизированных свойств получают источник клона
	// (Zend zend_objects_clone_members).
	vm.addCloneRefSources(cloned)
	slot := ci.objectSlot(cloned, int(src.num))
	// __clone вызывается на клонированном объекте; внутри него
	// readonly-свойства можно переинициализировать по одному разу.
	if m := ci.lookupMethod("__clone"); m != nil {
		_, cmDecl, cmVis := ci.methodInfo("__clone")
		if !canAccess(cmVis, cmDecl, vm.ctx.self) {
			err := vm.ctorAccessError(cmVis, classDisplayName(cmDecl), "__clone")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		_, cerr := vm.invokeMethod(m, slot, ci, nil)
		cloned.readonlyReinit = nil
		if cerr != nil {
			if !vm.handleThrow(chunk, cerr) {
				return execResult{err: cerr}
			}
			return execResult{next: true}
		}
	} else {
		cloned.readonlyReinit = nil
	}
	{
		old := regs[in.A]
		wv := slot
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}

func (vm *VM) opStaticGet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.staticGetCommon(regs, consts, chunk, in, false)
}

// opStaticGetRW — чтение статического свойства как базы записи массива
// (Zend BP_VAR_W + ZEND_FETCH_DIM_WRITE): неинициализированное типизированное
// свойство не даёт ошибку доступа, но тип без array — auto-init TypeError.
func (vm *VM) opStaticGetRW(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.staticGetCommon(regs, consts, chunk, in, true)
}

func (vm *VM) staticGetCommon(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, rw bool) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		if in.B < 3 {
			return vm.throwScopeClass(chunk, in.B)
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
	if s := cls.lookupStatic(consts[in.C].StringVal()); s != nil {
		name := consts[in.C].StringVal()
		if cls.IsTrait() {
			vm.Deprecated("Accessing static trait property " + cls.Name() + "::$" + name + " is deprecated, it should only be accessed on a class using the trait")
		}
		if aerr := vm.staticPropAccessErr(cls, name); aerr != nil {
			if !vm.handleThrow(chunk, aerr) {
				return execResult{err: aerr}
			}
			return execResult{next: true}
		}
		if cls.staticIsUninit(name) {
			if !rw {
				err := staticUninitErr(cls, name)
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			if derr := vm.dimWriteStaticCheck(cls, name, Slot{}, false); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
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
		if rw {
			if derr := vm.dimWriteStaticCheck(cls, name, *s, true); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
			}
		}
		{
			old := regs[in.A]
			wv := *s
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	} else {
		err := NewThrowable("Error", "Access to undeclared static property "+cls.Name()+"::$"+consts[in.C].StringVal())
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}

	return execResult{}
}

// opStaticIsset реализует isset(<selector>::const[C]): несуществующее или
// неинициализированное (typed) свойство → false, значение null → false.
func (vm *VM) opStaticIsset(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		if in.B < 3 {
			return vm.throwScopeClass(chunk, in.B)
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
	name := consts[in.C].StringVal()
	s := cls.lookupStatic(name)
	if s == nil || cls.staticIsUninit(name) || vm.staticPropAccessErr(cls, name) != nil {
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
	{
		old := regs[in.A]
		wv := MakeBool(deref(*s).typ != NullType)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}
	return execResult{}
}

// staticRefWrite выполняет запись значения в ячейку-ссылку статического
// свойства. Для типизированного свойства Zend сначала проверяет значение по
// объявленному типу свойства (zend_assign_to_typed_prop, «plain»-сообщение),
// затем zend_assign_to_variable_ex для ячейки-ссылки с источниками выполняет
// zend_assign_to_typed_ref: значение проверяется/приводится по всем источникам.
// Возвращает записанное значение.
func (vm *VM) staticRefWrite(cls ClassInfo, name string, r *Ref, val Slot) (Slot, error) {
	strict := vm.chunk != nil && vm.chunk.Strict
	typed := false
	if rc, ok := cls.(*runtimeClass); ok {
		if pd, _ := rc.propDeclOf(name); pd != nil && pd.hasType {
			typed = true
		}
	}
	if !typed {
		if werr := vm.writeRef(r, val, strict); werr != nil {
			return Slot{}, werr
		}
		return r.v, nil
	}
	out, verr := vm.coerceStaticProp(cls, name, deref(val))
	if verr != nil {
		return Slot{}, verr
	}
	if r.hasSources() {
		out2, verr2 := vm.verifyRefAssignable(r, out, strict)
		if verr2 != nil {
			return Slot{}, verr2
		}
		out = out2
	}
	vm.noteDropped(r.v)
	vm.noteStored(out)
	r.v = out
	return out, nil
}

func (vm *VM) opStaticSet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	name := consts[in.C].StringVal()
	if cls.IsTrait() {
		vm.Deprecated("Accessing static trait property " + cls.Name() + "::$" + name + " is deprecated, it should only be accessed on a class using the trait")
	}
	s := cls.lookupStatic(name)
	if s == nil {
		err := NewThrowable("Error", "Access to undeclared static property "+cls.Name()+"::$"+name)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if aerr := vm.staticPropAccessErr(cls, name); aerr != nil {
		if !vm.handleThrow(chunk, aerr) {
			return execResult{err: aerr}
		}
		return execResult{next: true}
	}
	// Статическое свойство уже ссылка — писать сквозь неё, не заменяя ячейку.
	// Для *типизированного* свойства Zend сначала проверяет значение по типу
	// самого свойства («plain»-формулировка), затем zend_assign_to_variable_ex
	// для ячейки-ссылки с источниками выполняет zend_assign_to_typed_ref
	// (проверка ещё и по всем источникам ссылки).
	if s.typ == RefType {
		if r := s.Ref(); r != nil {
			out, werr := vm.staticRefWrite(cls, name, r, regs[in.A])
			if werr != nil {
				if !vm.handleThrow(chunk, werr) {
					return execResult{err: werr}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := out
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
	}
	val, verr := vm.coerceStaticProp(cls, name, deref(regs[in.A]))
	if verr != nil {
		if !vm.handleThrow(chunk, verr) {
			return execResult{err: verr}
		}
		return execResult{next: true}
	}
	vm.noteDropped(*s)
	vm.noteStored(val)
	*s = val
	cls.clearStaticUninit(name)
	// Выражение присваивания возвращает уже приведённое значение.
	if in.A != in.B {
		{
			old := regs[in.A]
			wv := val
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}

	return execResult{}
}

func (vm *VM) opStaticGetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.staticGetDynCommon(regs, consts, chunk, in, false)
}

// opStaticGetRWDyn — то же, что opStaticGetRW, для динамического имени.
func (vm *VM) opStaticGetRWDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	return vm.staticGetDynCommon(regs, consts, chunk, in, true)
}

func (vm *VM) staticGetDynCommon(regs []Slot, consts []Slot, chunk *Chunk, in Instruction, rw bool) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		if in.B < 3 {
			return vm.throwScopeClass(chunk, in.B)
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
	if s := cls.lookupStatic(deref(regs[in.C]).ToString()); s != nil {
		name := deref(regs[in.C]).ToString()
		if aerr := vm.staticPropAccessErr(cls, name); aerr != nil {
			if !vm.handleThrow(chunk, aerr) {
				return execResult{err: aerr}
			}
			return execResult{next: true}
		}
		if cls.staticIsUninit(name) {
			if !rw {
				err := staticUninitErr(cls, name)
				if !vm.handleThrow(chunk, err) {
					return execResult{err: err}
				}
				return execResult{next: true}
			}
			if derr := vm.dimWriteStaticCheck(cls, name, Slot{}, false); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
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
		if rw {
			if derr := vm.dimWriteStaticCheck(cls, name, *s, true); derr != nil {
				if !vm.handleThrow(chunk, derr) {
					return execResult{err: derr}
				}
				return execResult{next: true}
			}
		}
		{
			old := regs[in.A]
			wv := *s
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	} else {
		if !rw {
			// Динамическое имя: чтение несуществующего статического свойства —
			// Error (Foo::${42}; int_static_prop_name).
			err := NewThrowable("Error", "Access to undeclared static property "+
				cls.Name()+"::$"+deref(regs[in.C]).ToString())
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
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

func (vm *VM) opStaticSetDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	name := deref(regs[in.C]).ToString()
	s := cls.lookupStatic(name)
	if s == nil {
		err := NewThrowable("Error", "Access to undeclared static property "+cls.Name()+"::$"+name)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if aerr := vm.staticPropAccessErr(cls, name); aerr != nil {
		if !vm.handleThrow(chunk, aerr) {
			return execResult{err: aerr}
		}
		return execResult{next: true}
	}
	if s.typ == RefType {
		if r := s.Ref(); r != nil {
			out, werr := vm.staticRefWrite(cls, name, r, regs[in.A])
			if werr != nil {
				if !vm.handleThrow(chunk, werr) {
					return execResult{err: werr}
				}
				return execResult{next: true}
			}
			{
				old := regs[in.A]
				wv := out
				regs[in.A] = wv
				if trackedType(old.typ) || trackedType(wv.typ) {
					vm.wRegSlow(int(in.A), old)
				}
			}
			return execResult{}
		}
	}
	val, verr := vm.coerceStaticProp(cls, name, deref(regs[in.A]))
	if verr != nil {
		if !vm.handleThrow(chunk, verr) {
			return execResult{err: verr}
		}
		return execResult{next: true}
	}
	vm.noteDropped(*s)
	vm.noteStored(val)
	*s = val
	cls.clearStaticUninit(name)
	if in.A != in.B {
		{
			old := regs[in.A]
			wv := val
			regs[in.A] = wv
			if trackedType(old.typ) || trackedType(wv.typ) {
				vm.wRegSlow(int(in.A), old)
			}
		}
	}

	return execResult{}
}

func (vm *VM) opStaticSetRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	return vm.staticSetRefCommon(regs, chunk, cls, consts[in.C].StringVal(), in.A)
}

func (vm *VM) opStaticSetRefDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	return vm.staticSetRefCommon(regs, chunk, cls, deref(regs[in.C]).ToString(), in.A)
}

func (vm *VM) staticSetRefCommon(regs []Slot, chunk *Chunk, cls ClassInfo, name string, dst uint8) execResult {
	rc, ok := cls.(*runtimeClass)
	if !ok {
		return execResult{err: errUnknownClass}
	}
	s := rc.lookupStatic(name)
	if s == nil {
		err := NewThrowable("Error", "Access to undeclared static property "+cls.Name()+"::$"+name)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	cell := regs[dst]
	if cell.typ != RefType {
		cell = MakeRef(&Ref{v: cell})
	}
	val, verr := vm.coerceStaticPropRef(cls, name, cell)
	if verr != nil {
		if !vm.handleThrow(chunk, verr) {
			return execResult{err: verr}
		}
		return execResult{next: true}
	}
	// Старое значение статического свойства теряет ссылку (Zend уничтожает
	// его в ASSIGN_REF): списываем оценку ДО подмены. Новую ячейку-ссылку не
	// учитываем (incRefs разворачивает ссылку и удвоил бы счётчик объекта,
	// который уже учтён в исходном месте).
	vm.noteDropped(*s)
	if pd, dc := rc.propDeclOf(name); pd != nil && pd.hasType {
		vm.detachRefGuard(*s, val, dc, name)
	} else {
		vm.detachRefGuard(*s, val, nil, name)
	}
	*s = val
	rc.clearStaticUninit(name)
	{
		old := regs[dst]
		wv := val
		regs[dst] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(dst), old)
		}
	}
	return execResult{}
}

func (vm *VM) opStaticRef(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	return vm.staticRefCommon(regs, chunk, in, cls, consts[in.C].StringVal())
}

func (vm *VM) opStaticRefDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		return vm.throwScopeClass(chunk, in.B)
	}
	return vm.staticRefCommon(regs, chunk, in, cls, deref(regs[in.C]).ToString())
}

func (vm *VM) staticRefCommon(regs []Slot, chunk *Chunk, in Instruction, cls ClassInfo, name string) execResult {
	cell, err := vm.staticRefCell(cls, name)
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

func (vm *VM) opConstGet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	cls := vm.classSel(in.B)
	if cls == nil {
		var msg string
		switch in.B {
		case 0:
			msg = `Cannot access "self" when no class scope is active`
		case 1:
			msg = `Cannot access "static" when no class scope is active`
		case 2:
			if vm.ctx.self == nil {
				msg = `Cannot access "parent" when no class scope is active`
			} else {
				msg = `Cannot access "parent" when current class scope has no parent`
			}
		}
		if msg != "" {
			err := NewThrowable("Error", msg)
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
		return execResult{err: errUnknownClass}
	}
	if et, msg := vm.EnumCaseDupErr(cls, consts[in.C].StringVal()); msg != "" {
		err := NewThrowable(et, msg)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// Внутри вычисления константного выражения запоминаем исходное написание
	// ссылки (`self::B` / `D::B`) — для диагностики self-referencing constant.
	if vm.thunkEvalDepth > 0 {
		disp := consts[in.C].StringVal()
		switch in.B {
		case 0:
			disp = "self::" + disp
		case 1:
			disp = "static::" + disp
		case 2:
			disp = "parent::" + disp
		default:
			disp = cls.Name() + "::" + disp
		}
		vm.constFetchPath = append(vm.constFetchPath, disp)
		defer func() { vm.constFetchPath = vm.constFetchPath[:len(vm.constFetchPath)-1] }()
	}
	v, ok, err := vm.classConstValue(cls, consts[in.C].StringVal())
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if !ok {
		// В рантайме Zend печатает имя класса («Undefined constant B::Z»);
		// при вычислении отложенного константного выражения (атрибуты) —
		// исходный селектор («Undefined constant self::FOO»).
		prefix := cls.Name()
		if chunk.constExpr {
			switch in.B {
			case 0:
				prefix = "self"
			case 1:
				prefix = "static"
			case 2:
				prefix = "parent"
			}
		}
		err := NewThrowable("Error", "Undefined constant "+prefix+"::"+consts[in.C].StringVal())
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
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
