package vm

// execCall.go — вызов функций, замыкания, return и yield.

import "strings"

func (vm *VM) opCall(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	packed := uint32(in.C)
	fn := vm.functions[packed&0xFFFFFF]
	argc := fn.NumArgs
	// Точное число фактически переданных аргументов упаковано в старший
	// байт C (argc+1); 0 — легаси-режим (без упаковки). PHP разрешает
	// передавать больше аргументов, чем объявлено, — лишние доступны
	// через func_get_args(). Поэтому берём фактическое число, а не
	// объявленную арность.
	if raw := int(packed >> 24); raw > 0 {
		argc = raw - 1
	} else if fn.NumArgs < 0 {
		argc = -1 // вариативно без упаковки: передаём все регистры
	}
	base := int(in.A)
	// Аргументы — срез по регистрам вызывающего кадра, без копирования
	// и без промежуточного буфера. Кадр ещё не сменился, поэтому
	// regs указывает на кадр вызывающего.
	var args []Slot
	if argc >= 0 {
		if argc > 0 {
			args = regs[in.B : int(in.B)+argc]
		}
	} else {
		args = regs[in.B:] // variadic: передаём все доступные регистры
	}
	res, err := vm.invoke(fn, args)
	if err != nil {
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	{
		old := vm.cur.regs[base]
		wv := res
		vm.cur.regs[base] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(base), old)
		}
	}
	shareSlot(res)

	return execResult{}
}

func (vm *VM) opCallArr(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	// Аргументы упакованы в массив (для вызовов с числом аргументов
	// больше регистрового файла).
	argArr := deref(regs[in.B])
	var args []Slot
	fn := vm.functions[int(in.C)]
	if argArr.typ == ArrayType {
		a := argArr.AsArray()
		if fn != nil && a != nil && a.HasStringKeys() {
			vm.namedUnpacking++
			bound, berr := vm.BindNamedArgs(fn, a)
			vm.namedUnpacking--
			if berr != nil {
				if !vm.handleThrow(chunk, berr) {
					return execResult{err: berr}
				}
				return execResult{next: true}
			}
			args = bound
		} else {
			args = a.Values()
		}
	}
	if fn.NumArgs >= 0 && len(args) > fn.NumArgs {
		args = args[:fn.NumArgs]
	}
	res, err := vm.invoke(fn, args)
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

func (vm *VM) opCallDyn(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	base := int(in.B)
	argc := int(in.C)
	var args []Slot
	if argc > 0 {
		args = regs[base+1 : base+1+argc]
	}
	// Замыкание: вызываем с сохранённым окружением.
	if cl, ok := regs[base].asClosureOpt(); ok {
		res, err := vm.invokeClosure(cl, args)
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
	// Объект с __invoke.
	if s := deref(regs[base]); s.typ == ObjectType {
		if ci := vm.classInfo(s); ci != nil {
			if res, ok, err := vm.callMagicMethod(ci, s, "__invoke", args); ok {
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
			err := NewThrowable("Error", "Object of type "+ClassDisplayName(ci.Name())+" is not callable")
			if !vm.handleThrow(chunk, err) {
				return execResult{err: err}
			}
			return execResult{next: true}
		}
	}
	// Массив-вызываемое ([obj, method] / [class, method]).
	if d := deref(regs[base]); d.typ == ArrayType {
		res, err := vm.callArrayCallable(d, args, true)
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
	// Не-вызываемые скаляры и ресурсы: Zend различает строку (ищет функцию)
	// и остальные типы ("Value of type null is not callable").
	if src := deref(regs[base]); src.typ <= FloatType || src.typ == ResourceType {
		err := NewThrowable("Error", "Value of type "+src.typ.String()+" is not callable")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// Динамический вызов scope-introspection функций запрещён.
	if sn, ok := scopeIntrospectionName(regs[base]); ok {
		err := NewThrowable("Error", "Cannot call "+sn+"() dynamically")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	if d := deref(regs[base]); d.typ != StringType {
		err := NewThrowable("Error", "Value of type "+callableTypeName(vm, d)+" is not callable")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	name := regs[base].ToString()
	// "Class::method" строкой — статический вызов метода.
	if i := strings.Index(name, "::"); i >= 0 {
		res, err := vm.callStaticStringCallable(name[:i], name[i+2:], args, true)
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
	// PHP допускает ведущий "\" в строковом имени функции: '\id' — это id.
	// Снимается ровно один обратный слэш; в диагностике имя остаётся исходным.
	lookup := name
	if strings.HasPrefix(lookup, "\\") {
		lookup = lookup[1:]
	}
	ic := vm.icAt(chunk)
	var fn *Function
	if ic != nil && ic.state == icMono && ic.class == nil && ic.name == lookup {
		fn = ic.fn
	}
	if fn == nil {
		if vm.funcIndex == nil {
			idx := make(map[string]*Function, len(vm.functions))
			for _, f := range vm.functions {
				if f.Disabled {
					continue
				}
				k := strings.ToLower(f.Name)
				if _, ok := idx[k]; !ok {
					idx[k] = f
				}
			}
			vm.funcIndex = idx
		}
		fn = vm.funcIndex[strings.ToLower(lookup)]
		if fn != nil {
			icFillFunc(ic, lookup, fn)
		}
	}
	if fn == nil {
		// Строковое вызываемое "Class::method" (Zend: INIT_STATIC_METHOD_CALL
		// по строке). Резолвим общим путём строковых callable: он умеет
		// автозагрузку, __callStatic и снятие ведущего "\".
		if strings.IndexByte(name, ':') >= 0 {
			res, cerr := vm.callStringCallable(name, args, true)
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
		err := NewThrowable("Error", "Call to undefined function "+strings.TrimPrefix(name, "\\")+"()")
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	res, err := vm.invoke(fn, args)
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

func (vm *VM) opRet(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	vm.retVal = regs[in.A]
	vm.retReg = int(in.A)
	vm.retImplicit = in.C == 1
	if l := chunk.LineAt(vm.ip - 1); l > 0 {
		vm.retLine = l
	}
	return execResult{err: retSignal{}}

}

func (vm *VM) opYield(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	// Принудительное закрытие генератора: yield запрещён (Zend:
	// "Cannot yield from finally in a force-closed generator").
	if vm.forceClosing != nil {
		msg := "Cannot yield from finally in a force-closed generator"
		if in.C&YieldFromBit != 0 {
			msg = `Cannot use "yield from" in a force-closed generator`
		}
		err := NewThrowable("Error", msg)
		if !vm.handleThrow(chunk, err) {
			return execResult{err: err}
		}
		return execResult{next: true}
	}
	// Приостанавливаем генератор: сообщаем наверх текущую пару и
	// регистр результата yield-выражения. Кадр сохранит advance.
	val := regs[in.A]
	var key Slot
	hasKey := in.B != YieldNoKey
	if hasKey {
		key = regs[in.B]
	}
	return execResult{err: yieldSignal{
		val:       val,
		key:       key,
		hasKey:    hasKey,
		from:      in.C&YieldFromBit != 0,
		resumeReg: int(in.C) & 0xFF,
	}}

}

func (vm *VM) opClosure(regs []Slot, consts []Slot, chunk *Chunk, in Instruction) execResult {
	fi := int(in.C)
	if fi < 0 || fi >= len(vm.functions) {
		return execResult{err: errUnknownFunc}
	}
	fn := vm.functions[fi]
	n := fn.NumCaptures
	var caps []Slot
	if n > 0 {
		caps = make([]Slot, n)
		copy(caps, regs[in.B:int(in.B)+n])
		for i := range caps {
			shareSlot(caps[i])
		}
	}
	bt := vm.ctx.this
	if fn.IsStatic {
		bt = Slot{}
	}
	cl := &Closure{Fn: fn, Captures: caps, BoundThis: bt,
		Scope: vm.ctx.self, Called: vm.ctx.called}
	vm.markClosure(cl)
	{
		old := regs[in.A]
		wv := MakeClosure(cl)
		regs[in.A] = wv
		if trackedType(old.typ) || trackedType(wv.typ) {
			vm.wRegSlow(int(in.A), old)
		}
	}

	return execResult{}
}
