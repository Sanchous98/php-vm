package vm

import (
	"fmt"
	"strconv"
	"strings"
)

// --- Экспорт ссылок для встроенных функций ---

// IsRef сообщает, что слот — это Ref-ячейка (by-ref аргумент).
func (s Slot) IsRef() bool { return s.typ == RefType }

// Value возвращает значение внутри Ref-ячейки.
func (r *Ref) Value() Slot { return r.v }

// Set заменяет значение внутри Ref-ячейки.
func (r *Ref) Set(v Slot) { r.v = v }

// NewRef создаёт Ref-ячейку со значением v (для встроенных функций, которые
// передают аргумент замыканию по ссылке — напр. array_walk).
func NewRef(v Slot) Slot { return MakeRef(&Ref{v: v}) }

// CompareSlots сравнивает два слота по правилам упорядочивания PHP. Экспорт
// compareSlots для встроенных сортировок (sort/usort/...).
func CompareSlots(a, b Slot) int { return compareSlots(nil, a, b) }

// functionByName ищет зарегистрированную функцию по имени.
// functionByName возвращает userland/встроенную функцию по имени (без учёта
// регистра), пропуская отключённые через disable_functions.
func (vm *VM) functionByName(name string) *Function {
	for _, f := range vm.functions {
		if f.Disabled {
			continue
		}
		if strings.EqualFold(f.Name, name) {
			return f
		}
	}
	return nil
}

// IsStringable сообщает, что объект имеет метод __toString (неявный Stringable).
func (vm *VM) IsStringable(s Slot) bool {
	ci := vm.classInfo(s)
	return ci != nil && ci.lookupMethod("__toString") != nil
}

// methodAccessible сообщает, доступен ли метод с видимостью vis из текущего
// scope VM (zend_is_callable учитывает видимость в отличие от method_exists).
func (vm *VM) methodAccessible(ci ClassInfo, vis Visibility) bool {
	return vm.methodAccessibleFrom(ci, vis, vm.ctx.self)
}

// methodAccessibleFrom — проверка видимости из заданного scope.
func (vm *VM) methodAccessibleFrom(ci ClassInfo, vis Visibility, scope ClassInfo) bool {
	if vis == Public {
		return true
	}
	if scope == nil {
		return false
	}
	if vis == Private {
		return scope == ci
	}
	// protected: доступен из того же класса-иерархии (предок или наследник).
	return scope.IsA(ci) || ci.IsA(scope)
}

// methodNameForProto — имя метода для поиска корневого прототипа: ic-путь
// (мономорфный кэш) не хранит имя, берём его из самой функции.
func methodNameForProto(m *Function, name string) string {
	if name == "" && m != nil {
		return m.Name
	}
	return name
}

// methodAccessibleFromProto — как methodAccessibleFrom, но для protected
// дополнительно учитывает корневой прототип метода (GH-14009:
// zend_get_function_root_class): доступ разрешён области, связанной с классом
// первого объявления в цепочке переопределений. decl — класс объявления
// метода (для private/protected-проверки), ci — класс получателя (для поиска
// прототипа).
func (vm *VM) methodAccessibleFromProto(ci ClassInfo, decl ClassInfo, name string, vis Visibility, scope ClassInfo) bool {
	if canAccess(vis, decl, scope) {
		return true
	}
	if vis != Protected || scope == nil {
		return false
	}
	rc, ok := asRuntimeClass(ci)
	if !ok || rc == nil {
		return false
	}
	proto := rc.methodProto(name)
	return proto != nil && (scope.IsA(proto) || proto.IsA(scope))
}

// MethodIsAbstract сообщает, что ближайшее объявление метода name в иерархии// класса — абстрактная сигнатура (для диагностик callable).
func (vm *VM) MethodIsAbstract(ci ClassInfo, name string) bool {
	if rc, ok := asRuntimeClass(ci); ok && rc != nil {
		return rc.hasAbstract(name)
	}
	return false
}

// CallCallable вызывает callable-значение c с аргументами args.
//
// Поддерживаемые формы:
//   - строка — имя функции (или "Class::method");
//   - Closure — замыкание;
//   - [obj, "method"] / [class, "method"] — массив из двух элементов;
//   - объект с __invoke.
//
// При ошибке возвращается *Throwable (его можно пробросить через ctx.Throw).
// ClosureFromSlot создаёт замыкание из callable (Closure::fromCallable и
// синтаксис first-class callable).
func (vm *VM) ClosureFromSlot(c Slot) (Slot, error) { return vm.closureFromCallable(c) }

// NewClosureClass создаёт дескриптор встроенного класса Closure. Он нужен
// компилятору: без него статические вызовы Closure::fromCallable/Closure::bind
// уходили в рантайм-резолв «Closure::method» и падали с «class "Closure" not
// found» (Zend спец-обрабатывает класс Closure встроенно).
func NewClosureClass() ClassInfo {
	ci := NewClass("Closure", nil)
	MarkInternal(ci)
	ci.SetNoDynamicProps()
	ci.Method("fromCallable", &Function{Name: "fromCallable", NumArgs: 1, NumRequired: 1,
		IsStatic: true, ParamNames: []string{"callback"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			var cb Slot = MakeNull()
			if len(args) > 0 {
				cb = args[0]
			}
			return ctx.VM().closureFromCallable(cb)
		}}})
	ci.Method("bind", &Function{Name: "bind", NumArgs: 3, NumRequired: 2,
		IsStatic: true, ParamNames: []string{"closure", "newThis", "newScope"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			if len(args) < 2 {
				return Slot{}, NewThrowable("ArgumentCountError", "Closure::bind() expects at least 2 arguments, "+strconv.Itoa(len(args))+" given")
			}
			cl := args[0].asClosure()
			if cl == nil {
				return Slot{}, NewThrowable("TypeError", "Closure::bind(): Argument #1 ($closure) must be of type Closure, "+valueTypeName(ctx.VM(), deref(args[0]))+" given")
			}
			var scope Slot
			if len(args) > 2 {
				scope = args[2]
			}
			return ctx.VM().closureBind(cl, args[1], scope, len(args) > 2)
		}}})
	ci.Method("bindTo", &Function{Name: "bindTo", NumArgs: 2, NumRequired: 1,
		ParamNames: []string{"newThis", "newScope"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			cl := ctx.ThisSlot().asClosure()
			if cl == nil {
				return Slot{}, NewThrowable("Error", "Closure::bindTo() must be called on a Closure object")
			}
			var newThis, scope Slot
			if len(args) > 0 {
				newThis = args[0]
			}
			if len(args) > 1 {
				scope = args[1]
			}
			return ctx.VM().closureBind(cl, newThis, scope, len(args) > 1)
		}}})
	ci.Method("call", &Function{Name: "call", NumArgs: 1, NumRequired: 1,
		ParamNames: []string{"newThis", "args"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			cl := ctx.ThisSlot().asClosure()
			if cl == nil {
				return Slot{}, NewThrowable("Error", "Closure::call() must be called on a Closure object")
			}
			return ctx.VM().closureCall(cl, args)
		}}})
	ci.Method("__invoke", &Function{Name: "__invoke", NumArgs: 0})
	return ci
}

// closureBind — общая реализация Closure::bind/bindTo
// (Zend do_closure_bind/zend_valid_closure_binding).
func (vm *VM) closureBind(cl *Closure, newThis Slot, scopeArg Slot, hasScope bool) (Slot, error) {
	newThis = deref(newThis)
	if newThis.typ != NullType && newThis.typ != ObjectType {
		return Slot{}, NewThrowable("TypeError", "Closure::bindTo(): Argument #1 ($newThis) must be of type ?object, "+valueTypeName(vm, newThis)+" given")
	}
	ce := cl.Scope
	if hasScope {
		s := deref(scopeArg)
		switch s.typ {
		case NullType:
			ce = nil
		case ObjectType:
			ce = vm.classInfo(s)
		case StringType:
			name := strings.TrimLeft(s.StringVal(), "\\")
			if strings.EqualFold(name, "static") {
				ce = cl.Scope
			} else if ci := vm.findClass(name); ci != nil {
				ce = ci
			} else {
				vm.Warning(fmt.Sprintf("Class \"%s\" not found", name))
				return MakeNull(), nil
			}
		default:
			return Slot{}, NewThrowable("TypeError", "Closure::bindTo(): Argument #2 ($newScope) must be of type object|string|null, "+valueTypeName(vm, s)+" given")
		}
	}
	var fnScope ClassInfo
	if cl.Fn != nil {
		fnScope = cl.Fn.Scope
	}
	if newThis.typ == ObjectType {
		if cl.Fn != nil && cl.Fn.IsStatic {
			vm.Warning("Cannot bind an instance to a static closure")
			return MakeNull(), nil
		}
		if cl.Fake && fnScope != nil && !vm.slotInstanceOfClass(newThis, fnScope) {
			vm.Warning(fmt.Sprintf("Cannot bind method %s::%s() to object of class %s",
				ClassDisplayName(fnScope.Name()), cl.Fn.Name, ClassDisplayName(vm.classInfo(newThis).Name())))
			return MakeNull(), nil
		}
	} else {
		if cl.Fake && fnScope != nil && (cl.Fn == nil || !cl.Fn.IsStatic) {
			vm.Warning("Cannot unbind $this of method")
			return MakeNull(), nil
		}
		if !cl.Fake && cl.BoundThis.typ == ObjectType && cl.Fn != nil && cl.Fn.UsesThis {
			vm.Warning("Cannot unbind $this of closure using $this")
			return MakeNull(), nil
		}
	}
	if ce != nil && ce != cl.Scope && reflIsInternalScope(ce) {
		vm.Warning("Cannot bind closure to scope of internal class " + ClassDisplayName(ce.Name()))
		return MakeNull(), nil
	}
	if cl.Fake && ce != fnScope {
		if fnScope == nil {
			vm.Warning("Cannot rebind scope of closure created from function")
		} else {
			vm.Warning("Cannot rebind scope of closure created from method")
		}
		return MakeNull(), nil
	}
	nc := *cl
	nc.BoundThis = Slot{}
	nc.Called = ce
	if newThis.typ == ObjectType {
		nc.BoundThis = newThis
		if ci := vm.classInfo(newThis); ci != nil {
			nc.Called = ci
		}
	}
	nc.Scope = ce
	return MakeClosure(&nc), nil
}

// closureCall — Closure::call($newThis, ...$args).
func (vm *VM) closureCall(cl *Closure, args []Slot) (Slot, error) {
	if len(args) < 1 {
		return Slot{}, NewThrowable("ArgumentCountError", "Closure::call() expects at least 1 argument, 0 given")
	}
	recv := deref(args[0])
	ci := vm.classInfo(recv)
	if recv.typ != ObjectType || ci == nil {
		return Slot{}, NewThrowable("TypeError", "Closure::call(): Argument #1 ($newThis) must be of type object, "+valueTypeName(vm, recv)+" given")
	}
	if !cl.Fake && cl.Fn == nil {
		return Slot{}, NewThrowable("Error", "Closure is not valid")
	}
	if cl.Fake && cl.Fn != nil && cl.Fn.Scope != nil && !vm.slotInstanceOfClass(recv, cl.Fn.Scope) {
		vm.Warning(fmt.Sprintf("Cannot bind method %s::%s() to object of class %s",
			ClassDisplayName(cl.Fn.Scope.Name()), cl.Fn.Name, ClassDisplayName(ci.Name())))
		return MakeNull(), nil
	}
	// Смена scope на встроенный класс недопустима (zend_valid_closure_binding):
	// stdClass и прочие internal-классы не могут стать scope замыкания.
	if ci != cl.Scope && reflIsInternalScope(ci) {
		vm.Warning("Cannot bind closure to scope of internal class " + ClassDisplayName(ci.Name()))
		return MakeNull(), nil
	}
	nc := *cl
	nc.BoundThis = args[0]
	nc.Scope = ci
	nc.Called = ci
	return vm.invokeClosure(&nc, args[1:])
}

// slotInstanceOfClass сообщает, что объект принадлежит классу ci или его
// потомку.
func (vm *VM) slotInstanceOfClass(s Slot, ci ClassInfo) bool {
	s = deref(s)
	if s.typ != ObjectType {
		return false
	}
	sci := vm.classInfo(s)
	return sci != nil && sci.isA(ci)
}

// closureFromCallable — общая реализация Closure::fromCallable.
func (vm *VM) closureFromCallable(c Slot) (Slot, error) {
	c = deref(c)
	switch c.typ {
	case ClosureType:
		return c, nil
	case StringType:
		name := strings.TrimLeft(c.StringVal(), "\\")
		if i := strings.Index(name, "::"); i >= 0 {
			clsName := strings.TrimLeft(name[:i], "\\")
			mname := name[i+2:]
			if strings.EqualFold(clsName, "self") || strings.EqualFold(clsName, "parent") || strings.EqualFold(clsName, "static") {
				vm.Deprecated(`Use of "` + clsName + `" in callables is deprecated`)
			}
			ci, err := vm.fromCallableClass(clsName)
			if err != nil {
				return Slot{}, err
			}
			if err := vm.checkFromCallableMethod(ci, mname, Slot{}); err != nil {
				return Slot{}, err
			}
			if m := methodOrCtor(ci, mname); m != nil {
				return MakeClosure(&Closure{Fn: m, Scope: ci, Called: ci, Fake: true}), nil
			}
			return MakeClosure(&Closure{MagicName: mname, BoundThis: vm.staticCallRecv(ci), Scope: ci, Called: ci, Fake: true}), nil
		}
		fn := vm.functionByName(name)
		if fn == nil {
			return Slot{}, NewThrowable("TypeError", "Failed to create closure from callable: function \""+name+"\" not found or invalid function name")
		}
		return MakeClosure(&Closure{Fn: fn, Fake: true}), nil
	case ArrayType:
		a := c.AsArray()
		vals := a.Values()
		if len(vals) != 2 {
			return Slot{}, NewThrowable("TypeError", "Failed to create closure from callable: array callback must have exactly two members")
		}
		recv := deref(vals[0])
		mname := strings.TrimLeft(deref(vals[1]).ToString(), "\\")
		if recv.typ == StringType {
			rawCls := strings.TrimLeft(recv.StringVal(), "\\")
			if strings.EqualFold(rawCls, "self") || strings.EqualFold(rawCls, "parent") || strings.EqualFold(rawCls, "static") {
				vm.Deprecated(`Use of "` + rawCls + `" in callables is deprecated`)
			}
		}
		if recv.typ == ClosureType {
			// Замыкание — объект класса Closure: [Closure, "__invoke"]
			// разворачивается в само замыкание (Zend do_closure_from_callable).
			if strings.EqualFold(mname, "__invoke") {
				return recv, nil
			}
			if ci := vm.findClass("Closure"); ci != nil {
				if m := methodOrCtor(ci, mname); m != nil {
					return MakeClosure(&Closure{Fn: m, BoundThis: recv, Scope: ci, Called: ci, Fake: true}), nil
				}
			}
			return Slot{}, NewThrowable("TypeError", "Failed to create closure from callable: class Closure does not have a method \""+mname+"\"")
		}
		if recv.typ == ObjectType {
			ci := vm.classInfo(recv)
			if ci == nil {
				return Slot{}, NewThrowable("TypeError", "Failed to create closure from callable: first array member is not a valid class name or object")
			}
			if err := vm.checkFromCallableMethod(ci, mname, recv); err != nil {
				return Slot{}, err
			}
			if m := methodOrCtor(ci, mname); m != nil {
				return MakeClosure(&Closure{Fn: m, BoundThis: recv, Scope: ci, Called: ci, Fake: true}), nil
			}
			return MakeClosure(&Closure{MagicName: mname, BoundThis: recv, Scope: ci, Called: ci, Fake: true}), nil
		}
		if recv.typ == StringType {
			clsName := strings.TrimLeft(recv.StringVal(), "\\")
			ci, err := vm.fromCallableClass(clsName)
			if err != nil {
				return Slot{}, err
			}
			if err := vm.checkFromCallableMethod(ci, mname, Slot{}); err != nil {
				return Slot{}, err
			}
			if m := methodOrCtor(ci, mname); m != nil {
				return MakeClosure(&Closure{Fn: m, Scope: ci, Called: ci, Fake: true}), nil
			}
			return MakeClosure(&Closure{MagicName: mname, BoundThis: vm.staticCallRecv(ci), Scope: ci, Called: ci, Fake: true}), nil
		}
	case ObjectType:
		ci := vm.classInfo(c)
		if ci != nil {
			if m := ci.lookupMethod("__invoke"); m != nil {
				return MakeClosure(&Closure{Fn: m, BoundThis: c, Scope: ci, Called: ci, Fake: true}), nil
			}
		}
	}
	return Slot{}, NewThrowable("TypeError", "Failed to create closure from callable: value is not callable")
}

// fromCallableClass резолвит класс callable для Closure::fromCallable.
// scoped-имена (self/parent/static) разрешаются в scope вызывающего
// userland-кадра (внутри native-метода ctx.self уже равен Closure).
func (vm *VM) fromCallableClass(name string) (ClassInfo, error) {
	var ci ClassInfo
	switch {
	case vm.outerSelf != nil && strings.EqualFold(name, "self"):
		ci = vm.outerSelf
	case vm.outerSelf != nil && strings.EqualFold(name, "parent"):
		ci = vm.outerSelf.Parent()
	case vm.outerSelf != nil && strings.EqualFold(name, "static"):
		if vm.outerCalled != nil {
			ci = vm.outerCalled
		} else {
			ci = vm.outerSelf
		}
	default:
		ci = vm.resolveCallableNameToClass(name)
	}
	if ci == nil {
		return nil, NewThrowable("TypeError", "Failed to create closure from callable: class \""+name+"\" not found")
	}
	return ci, nil
}

// checkFromCallableMethod проверяет доступность метода для Closure::fromCallable
// (метод может отсутствовать — тогда нужен __call).
func (vm *VM) checkFromCallableMethod(ci ClassInfo, mname string, recv Slot) error {
	m := methodOrCtor(ci, mname)
	if m == nil {
		if ci.lookupMethod("__call") != nil {
			return nil
		}
		return NewThrowable("TypeError", "Failed to create closure from callable: class "+ci.Name()+" does not have a method \""+mname+"\"")
	}
	if _, decl, vis := ci.methodInfo(mname); decl != nil && vis != Public && !vm.methodAccessibleFrom(decl, vis, vm.fccScope()) {
		return NewThrowable("TypeError", "Failed to create closure from callable: cannot access "+visWord(vis)+" method "+ci.Name()+"::"+mname+"()")
	} else if decl == nil {
		if vis, ok := ci.MethodVisibility(mname); ok && !vm.methodAccessibleFrom(ci, vis, vm.outerSelf) {
			return NewThrowable("TypeError", "Failed to create closure from callable: cannot access "+visWord(vis)+" method "+ci.Name()+"::"+mname+"()")
		}
	}
	if !m.IsStatic && mname != "__construct" {
		hasThis := deref(recv).typ == ObjectType
		if !hasThis && deref(vm.cur.this).typ == ObjectType {
			if rci := vm.classInfo(vm.cur.this); rci != nil && rci.IsA(ci) {
				hasThis = true
			}
		}
		if !hasThis {
			return NewThrowable("TypeError", "Failed to create closure from callable: non-static method "+ci.Name()+"::"+mname+"() cannot be called statically")
		}
	}
	return nil
}

// fccScope — scope вызывающего userland-кода для проверок FCC: при вызове
// нативного метода это outerSelf, при обычном нативном вызове — ctx.self
// (встроенные функции не создают нового scope).
func (vm *VM) fccScope() ClassInfo {
	if vm.outerSelf != nil {
		return vm.outerSelf
	}
	return vm.ctx.self
}

// FCCValue создаёт first-class callable `expr(...)` (PHP 8.1) для
// произвольного callable-значения. Диагностики — как у Zend
// (zend_create_fake_closure): Error с формулировками, отличными от
// Closure::fromCallable (там TypeError с префиксом).
func (vm *VM) FCCValue(c Slot) (Slot, error) {
	c = deref(c)
	switch c.typ {
	case ClosureType:
		return c, nil
	case StringType:
		name := strings.TrimLeft(c.StringVal(), "\\")
		if i := strings.Index(name, "::"); i >= 0 {
			return vm.fccStaticName(strings.TrimLeft(name[:i], "\\"), name[i+2:])
		}
		if fn := vm.functionByName(name); fn != nil {
			return MakeClosure(&Closure{Fn: fn, Fake: true}), nil
		}
		return Slot{}, NewThrowable("Error", "Call to undefined function "+name+"()")
	case ArrayType:
		vals := c.AsArray().Values()
		if len(vals) != 2 {
			return Slot{}, NewThrowable("Error", "Array callback must have exactly two elements")
		}
		recv := deref(vals[0])
		mname := strings.TrimLeft(deref(vals[1]).ToString(), "\\")
		if recv.typ == ObjectType {
			ci := vm.classInfo(recv)
			if ci == nil {
				return Slot{}, NewThrowable("Error", "Array callback must have exactly two elements")
			}
			return vm.fccObjectMethod(ci, recv, mname)
		}
		if recv.typ == StringType {
			return vm.fccStaticName(strings.TrimLeft(recv.StringVal(), "\\"), mname)
		}
		return Slot{}, NewThrowable("Error", "Array callback must have exactly two elements")
	case ObjectType:
		ci := vm.classInfo(c)
		if ci != nil {
			if m := ci.lookupMethod("__invoke"); m != nil {
				return MakeClosure(&Closure{Fn: m, BoundThis: c, Scope: ci, Called: ci, Fake: true}), nil
			}
			return Slot{}, NewThrowable("Error", "Object of type "+ci.Name()+" is not callable")
		}
	}
	t := valueTypeName(vm, c)
	if c.typ == BoolType {
		t = "bool"
	}
	return Slot{}, NewThrowable("Error", "Value of type "+t+" is not callable")
}

// FCCStaticSlot — first-class callable для `$x::m(...)`, где класс задан
// значением (объект или строка).
func (vm *VM) FCCStaticSlot(cls Slot, mname string) (Slot, error) {
	d := deref(cls)
	switch d.typ {
	case ObjectType:
		if ci := vm.classInfo(d); ci != nil {
			return vm.fccStaticMethod(ci, mname)
		}
	case StringType:
		name := strings.TrimLeft(d.StringVal(), "\\")
		if ci := vm.resolveCallableNameToClass(name); ci != nil {
			return vm.fccStaticMethod(ci, mname)
		}
		return Slot{}, NewThrowable("Error", "Class \""+name+"\" not found")
	}
	t := valueTypeName(vm, d)
	if d.typ == BoolType {
		t = "bool"
	}
	return Slot{}, NewThrowable("Error", "Class \""+t+"\" not found")
}

// fccStaticName — FCC для строки "Class::method" и для массива [class, method].
func (vm *VM) fccStaticName(clsName, mname string) (Slot, error) {
	ci := vm.resolveCallableNameToClass(strings.TrimLeft(clsName, "\\"))
	if ci == nil {
		return Slot{}, NewThrowable("Error", "Class \""+strings.TrimLeft(clsName, "\\")+"\" not found")
	}
	return vm.fccStaticMethod(ci, mname)
}

// fccStaticMethod — общая проверка статического FCC (Class::m(...), ["C","m"](...)).
func (vm *VM) fccStaticMethod(ci ClassInfo, mname string) (Slot, error) {
	m, decl, vis := ci.methodInfo(mname)
	if m != nil && !m.IsStatic && mname != "__construct" {
		if decl != nil && vis != Public && !vm.methodAccessibleFrom(decl, vis, vm.fccScope()) {
			if ci.lookupMethod("__callStatic") != nil {
				return MakeClosure(&Closure{MagicName: mname, Scope: ci, Called: ci, BoundThis: vm.staticCallRecv(ci), Fake: true}), nil
			}
			return Slot{}, NewThrowable("Error", "Call to "+visWord(vis)+" method "+ci.Name()+"::"+mname+"() from "+scopeDesc(vm.fccScope()))
		}
		return Slot{}, NewThrowable("Error", "Non-static method "+ci.Name()+"::"+mname+"() cannot be called statically")
	}
	if m != nil && decl != nil && vis != Public && !vm.methodAccessibleFrom(decl, vis, vm.fccScope()) {
		if ci.lookupMethod("__callStatic") != nil {
			return MakeClosure(&Closure{MagicName: mname, Scope: ci, Called: ci, BoundThis: vm.staticCallRecv(ci), Fake: true}), nil
		}
		return Slot{}, NewThrowable("Error", "Call to "+visWord(vis)+" method "+ci.Name()+"::"+mname+"() from "+scopeDesc(vm.fccScope()))
	}
	if m == nil {
		if ci.lookupMethod("__callStatic") != nil {
			return MakeClosure(&Closure{MagicName: mname, Scope: ci, Called: ci, BoundThis: vm.staticCallRecv(ci), Fake: true}), nil
		}
		return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
	}
	return MakeClosure(&Closure{Fn: m, Scope: ci, Called: ci, Fake: true}), nil
}

// FCCObjectSlot — FCC для `$obj->m(...)` (диагностики при создании).
func (vm *VM) FCCObjectSlot(ci ClassInfo, recv Slot, mname string) (Slot, error) {
	return vm.fccObjectMethod(ci, recv, mname)
}

// fccObjectMethod — FCC для массива [obj, "method"].
func (vm *VM) fccObjectMethod(ci ClassInfo, recv Slot, mname string) (Slot, error) {
	if recv.typ == ClosureType && strings.EqualFold(mname, "__invoke") {
		// $f->__invoke(...) — FCC по __invoke замыкания: сама вызываемая
		// сущность (Closure::__invoke вызывает замыкание).
		if cl := recv.asClosure(); cl != nil && cl.Fn != nil {
			return recv, nil
		}
	}
	m, decl, vis := ci.methodInfo(mname)
	if m != nil && decl != nil && vis != Public && !vm.methodAccessibleFrom(decl, vis, vm.fccScope()) {
		if ci.lookupMethod("__call") != nil {
			return MakeClosure(&Closure{MagicName: mname, BoundThis: recv, Scope: ci, Called: ci, Fake: true}), nil
		}
		return Slot{}, NewThrowable("Error", "Call to "+visWord(vis)+" method "+ci.Name()+"::"+mname+"() from "+scopeDesc(vm.fccScope()))
	}
	if m == nil {
		if ci.lookupMethod("__call") != nil {
			return MakeClosure(&Closure{MagicName: mname, BoundThis: recv, Scope: ci, Called: ci, Fake: true}), nil
		}
		return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
	}
	return MakeClosure(&Closure{Fn: m, BoundThis: recv, Scope: ci, Called: ci, Fake: true}), nil
}

// FuncAt возвращает функцию по индексу таблицы (для first-class callable).
func (vm *VM) FuncAt(i int) *Function {
	if i < 0 || i >= len(vm.functions) {
		return nil
	}
	return vm.functions[i]
}

// SlotClassInfo возвращает дескриптор класса объекта или имя класса-строки.
func (vm *VM) SlotClassInfo(s Slot) ClassInfo {
	d := deref(s)
	if d.typ == ObjectType {
		return vm.classInfo(d)
	}
	if d.typ == ClosureType {
		// Замыкание — объект класса Closure (Zend): нужен для FCC
		// `$f->__invoke(...)` и `[Closure, "method"]`.
		return vm.findClass("Closure")
	}
	if d.typ == StringType {
		return vm.findClass(strings.TrimLeft(d.StringVal(), "\\"))
	}
	return nil
}

func (vm *VM) CallCallable(ctx *Context, c Slot, args []Slot) (Slot, error) {
	return vm.callCallableMode(ctx, c, args, false)
}

// scopeIntrospectionNames — функции, которые Zend запрещает вызывать динамически
// ("Cannot call %s() dynamically"): они работают с таблицей символов
// вызывающего кадра.
var scopeIntrospectionNames = map[string]bool{
	"extract": true, "compact": true, "get_defined_vars": true,
	"func_get_arg": true, "func_get_args": true, "func_num_args": true,
}

// scopeIntrospectionName возвращает каноническое имя функции, если callable —
// строковое имя scope-introspection функции (используется для запрета
// динамических вызовов).
func scopeIntrospectionName(c Slot) (string, bool) {
	d := deref(c)
	if d.typ != StringType {
		return "", false
	}
	n := strings.ToLower(strings.TrimPrefix(d.ToString(), "\\"))
	if scopeIntrospectionNames[n] {
		return n, true
	}
	return "", false
}

// CallCallableAt — CallCallable с явной позицией вызывающего для кадра
// callable (обработчик ошибок: в трейсе Zend место — точка генерации ошибки).
func (vm *VM) CallCallableAt(ctx *Context, c Slot, args []Slot, file string, chunk *Chunk, pc, line int) (Slot, error) {
	vm.nextFrameSet = true
	vm.nextFrameFile = file
	vm.nextFrameChunk = chunk
	vm.nextFramePC = pc
	vm.nextFrameLine = line
	res, err := vm.callCallableMode(ctx, c, args, false)
	vm.nextFrameSet = false
	return res, err
}

// CallTickCallback вызывает tick-обработчик из кадра, исполняющего
// ZEND_TICKS: кадр обработчика в трейсе получает файл/строку текущего опкода
// (fibers/ticks: tick-функция вызывается «из» оператора, после которого
// сработал тик), а не "[internal function]".
func (vm *VM) CallTickCallback(ctx *Context, cb Slot, args []Slot) (Slot, error) {
	pc := 0
	if vm.ip > 0 {
		pc = vm.ip - 1
	}
	return vm.CallCallableAt(ctx, cb, args, vm.curFile, vm.chunk, pc, vm.line())
}

// CallCallableUser — вызов callable с семантикой прямого вызова из
// userland-кода. Zend спец-обрабатывает call_user_func/call_user_func_array
// в компиляторе (zend_compile.c), поэтому внутренний кадр не создаётся:
// сообщения об арности указывают «in FILE on line N», трейс не содержит
// кадра внутренней функции.
func (vm *VM) CallCallableUser(ctx *Context, c Slot, args []Slot) (Slot, error) {
	// Zend не создаёт кадр call_user_func*: вызов компилируется в прямой
	// dynamic-call. Убираем собственный (нативный) кадр на время вызова
	// callback'а — тогда место вызова и трейсы совпадают с Zend.
	savedCuf := vm.cufCall
	vm.cufCall = true
	if n := len(vm.callStack); n > 0 {
		saved := vm.callStack
		vm.callStack = saved[:n-1]
		res, err := vm.callCallableMode(ctx, c, args, true)
		vm.callStack = saved
		vm.cufCall = savedCuf
		return res, err
	}
	res, err := vm.callCallableMode(ctx, c, args, true)
	vm.cufCall = savedCuf
	return res, err
}

// TakeExtraNamedParams возвращает extra named params текущего вызова
// (нативные функции, принимающие именованные аргументы: Reflection::invoke
// и т.п.) и сбрасывает их.
func (vm *VM) TakeExtraNamedParams() *Array {
	a := vm.namedVariadicTail
	vm.namedVariadicTail = nil
	return a
}

// InvokeNamed вызывает функцию с массивом аргументов, строковые ключи
// которого — именованные аргументы.
func (vm *VM) InvokeNamed(fn *Function, arr *Array) (Slot, error) {
	args, err := vm.BindNamedArgs(fn, arr)
	if err != nil {
		return Slot{}, err
	}
	return vm.invoke(fn, args)
}

// InvokeMethodNamed — то же для метода (invokeMethod с биндингом имён).
func (vm *VM) InvokeMethodNamed(fn *Function, recv Slot, called ClassInfo, arr *Array) (Slot, error) {
	args, err := vm.BindNamedArgs(fn, arr)
	if err != nil {
		return Slot{}, err
	}
	return vm.invokeMethod(fn, recv, called, args)
}

// CallCallableNamedDirect — CallCallableNamed с семантикой прямого вызова из
// userland-кода (call_user_func/call_user_func_array спец-обрабатываются
// компилятором в Zend): собственный нативный кадр убирается, поэтому место
// вызова и трейс совпадают с Zend.
func (vm *VM) CallCallableNamedDirect(ctx *Context, c Slot, arr *Array) (Slot, error) {
	savedCuf := vm.cufCall
	vm.cufCall = true
	if n := len(vm.callStack); n > 0 {
		saved := vm.callStack
		vm.callStack = saved[:n-1]
		res, err := vm.CallCallableNamed(ctx, c, arr)
		vm.callStack = saved
		vm.cufCall = savedCuf
		return res, err
	}
	res, err := vm.CallCallableNamed(ctx, c, arr)
	vm.cufCall = savedCuf
	return res, err
}

// CallCallableNamedDirectScope — как CallCallableNamedDirect, но с явным
// called scope (forward_static_call с именованными аргументами).
func (vm *VM) CallCallableNamedDirectScope(ctx *Context, c Slot, arr *Array, self, called ClassInfo) (Slot, error) {
	savedCuf := vm.cufCall
	vm.cufCall = true
	if n := len(vm.callStack); n > 0 {
		saved := vm.callStack
		vm.callStack = saved[:n-1]
		res, err := vm.CallCallableNamedCalled(ctx, c, arr, self, called)
		vm.callStack = saved
		vm.cufCall = savedCuf
		return res, err
	}
	res, err := vm.CallCallableNamedCalled(ctx, c, arr, self, called)
	vm.cufCall = savedCuf
	return res, err
}

func (vm *VM) callCallableMode(ctx *Context, c Slot, args []Slot, direct bool) (Slot, error) {
	// Динамический вызов scope-introspection функций запрещён (Zend:
	// "Cannot call %s() dynamically") — проверяем до маршалинга аргументов,
	// чтобы не печатать предупреждения о by-ref параметрах.
	if name, ok := scopeIntrospectionName(c); ok {
		return Slot{}, NewThrowable("Error", "Cannot call "+name+"() dynamically")
	}
	savedThis, savedSelf, savedCalled := vm.ctx.this, vm.ctx.self, vm.ctx.called
	savedMarkInternal := vm.markNextFrameInternal
	if !direct {
		vm.argNoLoc++
		vm.markNextFrameInternal = true
	}
	defer func() {
		if !direct {
			vm.argNoLoc--
		}
		vm.markNextFrameInternal = savedMarkInternal
		vm.ctx.this, vm.ctx.self, vm.ctx.called = savedThis, savedSelf, savedCalled
	}()

	c = deref(c)
	switch c.typ {
	case ClosureType:
		cl := c.asClosure()
		if cl == nil || cl.Fn == nil {
			return Slot{}, NewThrowable("Error", "Value is not callable")
		}
		return vm.invokeClosure(cl, args)

	case StringType:
		return vm.callStringCallable(c.StringVal(), args)

	case ArrayType:
		return vm.callArrayCallable(c, args)

	case ObjectType:
		ci := vm.classInfo(c)
		if ci != nil {
			if m := ci.lookupMethod("__invoke"); m != nil {
				return vm.invokeMethod(m, c, ci, args)
			}
			return Slot{}, NewThrowable("Error", fmt.Sprintf("Object of class %s is not callable", ci.Name()))
		}
	}

	return Slot{}, NewThrowable("Error", fmt.Sprintf("Value of type %s is not callable", c.typ))
}

// CallCallableCalled — CallCallable с явным called scope (forward_static_call:
// позднее статическое связывание сохраняет static вызывающего).
func (vm *VM) CallCallableCalled(ctx *Context, c Slot, args []Slot, self, called ClassInfo) (Slot, error) {
	savedSelf, savedCalled := vm.ctx.self, vm.ctx.called
	savedFwd := vm.forwardCalled
	savedCuf := vm.cufCall
	if vm.ctx.self == nil {
		vm.ctx.self = self
	}
	if vm.ctx.called == nil {
		vm.ctx.called = called
	}
	vm.forwardCalled = called
	vm.cufCall = true
	res, err := vm.callCallableMode(ctx, c, args, false)
	vm.cufCall = savedCuf
	vm.forwardCalled = savedFwd
	vm.ctx.self, vm.ctx.called = savedSelf, savedCalled
	return res, err
}

// CallCallableNamedCalled — CallCallableNamed с called scope forward_static_call.
func (vm *VM) CallCallableNamedCalled(ctx *Context, c Slot, arr *Array, self, called ClassInfo) (Slot, error) {
	savedSelf, savedCalled := vm.ctx.self, vm.ctx.called
	savedFwd := vm.forwardCalled
	savedCuf := vm.cufCall
	if vm.ctx.self == nil {
		vm.ctx.self = self
	}
	if vm.ctx.called == nil {
		vm.ctx.called = called
	}
	vm.forwardCalled = called
	vm.cufCall = true
	res, err := vm.CallCallableNamed(ctx, c, arr)
	vm.cufCall = savedCuf
	vm.forwardCalled = savedFwd
	vm.ctx.self, vm.ctx.called = savedSelf, savedCalled
	return res, err
}

// ForwardScope возвращает scope/called ближайшего userland-кадра — для
// forward_static_call из нативного builtin (кадр builtin scope не задаёт).
func (vm *VM) ForwardScope() (ClassInfo, ClassInfo) {
	if vm.outerSelf != nil {
		called := vm.outerCalled
		if called == nil {
			called = vm.outerSelf
		}
		return vm.outerSelf, called
	}
	called := vm.ctx.called
	if called == nil {
		called = vm.ctx.self
	}
	return vm.ctx.self, called
}

// thisClassInfo возвращает класс текущего $this (nil, если его нет).
func (vm *VM) thisClassInfo() ClassInfo {
	if deref(vm.cur.this).typ != ObjectType {
		return nil
	}
	return vm.classInfo(vm.cur.this)
}

// methodOrCtor возвращает метод класса (с учётом конструктора).
func methodOrCtor(ci ClassInfo, name string) *Function {
	if ci == nil {
		return nil
	}
	if m, _, _ := ci.methodInfo(name); m != nil {
		return m
	}
	if name == "__construct" {
		if rc := asRC(ci); rc != nil {
			if c, _, _ := rc.ctorInfo(); c != nil {
				return c
			}
		}
	}
	return nil
}

// resolveQualifiedMethod разбирает «классифицированное» имя метода array-callable
// вида ["C","Base::m"] / ["C","parent::m"]: возвращает целевой класс и метод.
// self/parent/static резолвятся относительно класса-владельца outer (Zend).
// ok=false — метод записан без `::`.
func (vm *VM) resolveQualifiedMethod(outer ClassInfo, mname string) (ClassInfo, string, bool) {
	i := strings.Index(mname, "::")
	if i < 0 {
		return nil, mname, false
	}
	inner := strings.TrimLeft(mname[:i], "\\")
	meth := mname[i+2:]
	if outer != nil {
		switch {
		case strings.EqualFold(inner, "parent"):
			return outer.Parent(), meth, true
		case strings.EqualFold(inner, "self"), strings.EqualFold(inner, "static"):
			return outer, meth, true
		}
	}
	return vm.resolveCallableNameToClass(inner), meth, true
}

// CheckNamedOrder проверяет порядок ключей массива аргументов: позиционный
// ключ после именованного — ошибка Zend (zend_handle_named_arg).
func (vm *VM) CheckNamedOrder(arr *Array) error {
	if arr == nil {
		return nil
	}
	seen := false
	for k := range arr.All() {
		if k.IsStr() {
			seen = true
			continue
		}
		if seen {
			msg := "Cannot use positional argument after named argument"
			if vm.namedUnpacking > 0 {
				msg += " during unpacking"
			}
			return NewThrowable("Error", msg)
		}
	}
	return nil
}

// BindNamedArgs раскладывает аргументы из массива с именованными ключами по
// параметрам функции (Zend: разрешение named-аргументов на рантайме).
func (vm *VM) BindNamedArgs(fn *Function, arr *Array) ([]Slot, error) {
	if arr == nil {
		return nil, nil
	}
	vm.pendingNativeProvided = -1
	vm.pendingFrameNamed = nil
	vm.namedVariadicTail = nil
	variadic := fn.NumArgs < 0
	if _, isNative := fn.Executable.(*NativeFunction); isNative {
		// У нативных функций NumArgs всегда -1 (ABI), вариативность —
		// отдельный флаг из таблицы арности.
		variadic = fn.NativeVariadic
	}
	names := fn.ParamNames
	numFixed := len(names)
	if variadic {
		numFixed = len(names) - 1
		if numFixed < 0 {
			numFixed = 0
		}
	}
	type kv struct {
		name string
		v    Slot
	}
	slots := make([]Slot, numFixed)
	filled := make([]bool, numFixed)
	var posTail []Slot
	var namedTail []kv
	pos := 0
	namedSeen := false
	sawNamed := false
	used := map[string]bool{}
	for k, v := range arr.All() {
		if !k.IsStr() {
			if namedSeen {
				msg := "Cannot use positional argument after named argument"
				if vm.namedUnpacking > 0 {
					msg += " during unpacking"
				}
				return nil, NewThrowable("Error", msg)
			}
			if pos < numFixed {
				slots[pos] = v
				filled[pos] = true
			} else {
				posTail = append(posTail, v)
			}
			pos++
			continue
		}
		name := k.StrVal()
		namedSeen = true
		sawNamed = true
		idx := -1
		for i := 0; i < numFixed; i++ {
			if names[i] == name {
				idx = i
				break
			}
		}
		if idx < 0 {
			if variadic {
				if used[name] {
					return nil, NewThrowable("Error", "Named parameter $"+name+" overwrites previous argument")
				}
				used[name] = true
				namedTail = append(namedTail, kv{name: name, v: v})
				continue
			}
			return nil, NewThrowable("Error", "Unknown named parameter $"+name)
		}
		if used[name] || filled[idx] {
			return nil, NewThrowable("Error", "Named parameter $"+name+" overwrites previous argument")
		}
		used[name] = true
		filled[idx] = true
		slots[idx] = v
	}
	// Проверка обязательных параметров: Zend печатает "not passed".
	if sawNamed {
		for i := 0; i < fn.NumRequired && i < numFixed; i++ {
			if !filled[i] {
				var b strings.Builder
				b.WriteString(argFuncDisplayName(fn))
				b.WriteString("(): Argument #")
				b.WriteString(itoa(i + 1))
				if names[i] != "" {
					b.WriteString(" ($")
					b.WriteString(names[i])
					b.WriteString(")")
				}
				b.WriteString(" not passed")
				return nil, NewThrowable("ArgumentCountError", b.String())
			}
		}
	}
	out := make([]Slot, 0, numFixed+len(posTail)+len(namedTail))
	for i := 0; i < numFixed; i++ {
		if filled[i] {
			out = append(out, slots[i])
		} else {
			out = append(out, MakeNull())
		}
	}
	out = append(out, posTail...)
	// provided — число аргументов для аричности ZPP (позиции + именованные,
	// попавшие в параметры; NULL-заглушки отсутствующих не считаются).
	provided := pos
	for i := numFixed - 1; i >= 0; i-- {
		if filled[i] {
			if i+1 > provided {
				provided = i + 1
			}
			break
		}
	}
	if sawNamed {
		vm.pendingNativeProvided = provided
	}
	// Неизвестные имена для вариативной функции попадут в вариативный массив
	// со строковыми ключами (Zend: extra named args).
	if len(namedTail) > 0 {
		named := newArray()
		for _, e := range namedTail {
			_ = named.SetStr(e.name, e.v)
		}
		vm.namedVariadicTail = named
		vm.pendingFrameNamed = named
	}
	// func_num_args/func_get_args видят только позиции до последнего
	// заполненного аргумента: лишние именованные (и именованные за
	// вариативным параметром) в счёт не идут (Zend).
	if sawNamed {
		n := pos
		for i := numFixed - 1; i >= 0; i-- {
			if filled[i] {
				if i+1 > n {
					n = i + 1
				}
				break
			}
		}
		vm.pendingNamedCount = n
	}
	return out, nil
}

// CallCallableNamed вызывает callable с массивом аргументов, где строковые
// ключи — именованные аргументы (динамический вызов `$f(a: 1)`).
func (vm *VM) CallCallableNamed(ctx *Context, c Slot, arr *Array) (Slot, error) {
	c = deref(c)
	switch c.typ {
	case ClosureType:
		cl := c.asClosure()
		if cl == nil {
			return Slot{}, NewThrowable("Error", "Value is not callable")
		}
		if cl.Fn == nil {
			// fake-замыкание: именованные ключи попадают в аргументы __call.
			if cl.MagicName != "" {
				if ci := vm.classInfo(cl.BoundThis); ci != nil {
					if res, ok, err := vm.callMagicMethod(ci, cl.BoundThis, "__call",
						[]Slot{MakeString(cl.MagicName), argsArrayNamed(arr)}); ok {
						return res, err
					}
				}
				if cl.Scope != nil {
					if res, ok, err := vm.callMagicMethodCalled(cl.Scope, cl.Called, Slot{}, "__callStatic",
						[]Slot{MakeString(cl.MagicName), argsArrayNamed(arr)}); ok {
						return res, err
					}
				}
			}
			return Slot{}, NewThrowable("Error", "Closure is not valid")
		}
		args, err := vm.BindNamedArgs(cl.Fn, arr)
		if err != nil {
			return Slot{}, err
		}
		return vm.invokeClosure(cl, args)
	case StringType:
		raw := strings.TrimLeft(c.StringVal(), "\\")
		if i := strings.Index(raw, "::"); i >= 0 {
			ci := vm.resolveCallableNameToClass(strings.TrimLeft(raw[:i], "\\"))
			if ci == nil {
				return Slot{}, NewThrowable("Error", "Class \""+raw[:i]+"\" not found")
			}
			mname := raw[i+2:]
			m := methodOrCtor(ci, mname)
			if m == nil {
				if res, ok, err := vm.callMagicMethodCalled(ci, ci, vm.cur.this, "__callStatic",
					[]Slot{MakeString(mname), argsArrayNamed(arr)}); ok {
					return res, err
				}
				return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
			}
			args, err := vm.BindNamedArgs(m, arr)
			if err != nil {
				return Slot{}, err
			}
			called := ci
			if vm.forwardCalled != nil && vm.forwardCalled.IsA(ci) {
				called = vm.forwardCalled
			}
			return vm.invokeMethod(m, vm.staticCallRecv(ci), called, args)
		}
		fn := vm.functionByName(raw)
		if fn == nil {
			return Slot{}, NewThrowable("Error", "Call to undefined function "+raw+"()")
		}
		if res, ok, err := vm.forwardNamedBuiltin(ctx, fn, arr); ok {
			return res, err
		}
		args, err := vm.BindNamedArgs(fn, arr)
		if err != nil {
			return Slot{}, err
		}
		return vm.invoke(fn, args)
	case ArrayType:
		av := c.AsArray()
		vals := av.Values()
		if len(vals) != 2 {
			return Slot{}, NewThrowable("Error", "Value is not callable")
		}
		recv := deref(vals[0])
		mname := strings.TrimLeft(deref(vals[1]).ToString(), "\\")
		if recv.typ == ClosureType {
			cl := recv.asClosure()
			if cl == nil {
				return Slot{}, NewThrowable("Error", "Value is not callable")
			}
			switch strings.ToLower(mname) {
			case "__invoke":
				args, err := vm.BindNamedArgs(cl.Fn, arr)
				if err != nil {
					return Slot{}, err
				}
				savedAlias := vm.byRefAlias
				vm.byRefAlias = "Closure::__invoke"
				res, err := vm.invokeClosure(cl, args)
				vm.byRefAlias = savedAlias
				return res, err
			case "call":
				newThis, rest, ok := splitFirstArg(arr)
				if !ok {
					return Slot{}, NewThrowable("ArgumentCountError", "Closure::call() expects at least 1 argument, 0 given")
				}
				ci := vm.classInfo(newThis)
				if ci == nil {
					return Slot{}, NewThrowable("TypeError", "Closure::call(): Argument #1 ($newThis) must be of type object, "+valueTypeName(vm, deref(newThis))+" given")
				}
				args, err := vm.BindNamedArgs(cl.Fn, rest)
				if err != nil {
					return Slot{}, err
				}
				nc := *cl
				nc.BoundThis = newThis
				nc.Scope = ci
				nc.Called = ci
				return vm.invokeClosure(&nc, args)
			case "bindto":
				posArgs, err := vm.BindNamedArgs(cl.Fn, arr)
				if err != nil {
					return Slot{}, err
				}
				res, _, err := vm.closureMethodCall(cl, mname, posArgs)
				return res, err
			}
		}
		if recv.typ == ObjectType {
			ci := vm.classInfo(recv)
			if ci == nil {
				return Slot{}, NewThrowable("Error", "Value is not callable")
			}
			m := methodOrCtor(ci, mname)
			if m == nil {
				if res, ok, err := vm.callMagicMethod(ci, recv, "__call",
					[]Slot{MakeString(mname), argsArrayNamed(arr)}); ok {
					return res, err
				}
				return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
			}
			args, err := vm.BindNamedArgs(m, arr)
			if err != nil {
				return Slot{}, err
			}
			mcalled := ci
			if vm.forwardCalled != nil && vm.forwardCalled.IsA(ci) {
				mcalled = vm.forwardCalled
			}
			return vm.invokeMethod(m, recv, mcalled, args)
		}
		if recv.typ == StringType {
			ci := vm.resolveCallableNameToClass(strings.TrimLeft(recv.StringVal(), "\\"))
			if ci == nil {
				return Slot{}, NewThrowable("Error", "Class not found")
			}
			m := methodOrCtor(ci, mname)
			if m == nil {
				if res, ok, err := vm.callMagicMethodCalled(ci, ci, vm.cur.this, "__callStatic",
					[]Slot{MakeString(mname), argsArrayNamed(arr)}); ok {
					return res, err
				}
				return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
			}
			args, err := vm.BindNamedArgs(m, arr)
			if err != nil {
				return Slot{}, err
			}
			scalled := ci
			if vm.forwardCalled != nil && vm.forwardCalled.IsA(ci) {
				scalled = vm.forwardCalled
			}
			return vm.invokeMethod(m, vm.staticCallRecv(ci), scalled, args)
		}
	case ObjectType:
		ci := vm.classInfo(c)
		if ci != nil {
			if m := ci.lookupMethod("__invoke"); m != nil {
				args, err := vm.BindNamedArgs(m, arr)
				if err != nil {
					return Slot{}, err
				}
				return vm.invokeMethod(m, c, ci, args)
			}
		}
	}
	return Slot{}, NewThrowable("Error", "Value is not callable")
}

// forwardNamedBuiltin перенаправляет именованные аргументы через
// call_user_func*/forward_static_call* (Zend передаёт extra named params
// вызываемому callable).
func (vm *VM) forwardNamedBuiltin(ctx *Context, fn *Function, arr *Array) (Slot, bool, error) {
	if fn.Scope != nil || arr == nil {
		return Slot{}, false, nil
	}
	switch fn.Name {
	case "call_user_func", "forward_static_call":
		cb, rest, ok := splitFirstArg(arr)
		if !ok {
			return Slot{}, false, nil
		}
		res, err := vm.CallCallableNamed(ctx, cb, rest)
		return res, true, err
	case "call_user_func_array", "forward_static_call_array":
		cb, rest, ok := splitFirstArg(arr)
		if !ok {
			return Slot{}, false, nil
		}
		// Единственный оставшийся аргумент — массив аргументов.
		aarr := firstArgArray(rest)
		if aarr == nil {
			return Slot{}, false, nil
		}
		res, err := vm.CallCallableNamed(ctx, cb, aarr)
		return res, true, err
	}
	return Slot{}, false, nil
}

// splitFirstArg отделяет первый позиционный аргумент (callback) от остальных.
// Позиционные ключи перенумеровываются с нуля: BindNamedArgs различает
// позиции по счётчику, а не по исходным ключам массива (Zend пересобирает
// extra-args массив).
func splitFirstArg(arr *Array) (Slot, *Array, bool) {
	rest := newArray()
	cb := Slot{}
	found := false
	for k, v := range arr.All() {
		if !found && !k.IsStr() {
			cb = v
			found = true
			continue
		}
		if k.IsStr() {
			_ = rest.SetStr(k.StrVal(), v)
		} else {
			_, _ = rest.Append(v)
		}
	}
	if !found {
		return Slot{}, nil, false
	}
	return cb, rest, true
}

// MethodCallNamed вызывает метод объекта с именованными аргументами
// (`$obj->m(a: 1)`). Повторяет резолв OpMethodCallDyn (видимость, __call,
// магические получатели), но аргументы раскладывает по именам в рантайме.
func (vm *VM) MethodCallNamed(recv Slot, mname string, arr *Array) (Slot, error) {
	recv = deref(recv)
	if recv.typ == ClosureType {
		// Методы замыканий (call/bindTo/...) — через общий array-callable путь.
		cb := newArray()
		_ = cb.Set(MakeIntKey(0), recv)
		_ = cb.Set(MakeIntKey(1), MakeString(mname))
		return vm.CallCallableNamed(&vm.ctx, MakeArray(cb), arr)
	}
	ci := vm.classInfo(recv)
	if ci == nil {
		return Slot{}, NewThrowable("Error", "Call to a member function "+mname+"() on "+memberCallTypeName(recv))
	}
	if cls, ok := incompleteObjName(vm.objectOf(recv)); ok {
		return Slot{}, incompleteMethodErr(cls)
	}
	var m *Function
	var decl ClassInfo
	var vis Visibility
	if pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname); ok {
		m, decl, vis = pm, pdecl, pvis
	} else {
		m, decl, vis = ci.methodInfo(mname)
		if m == nil && strings.EqualFold(mname, "__construct") {
			m, decl, vis = ci.CtorInfo()
		}
	}
	if m == nil {
		if ci.hasAbstract(mname) {
			return Slot{}, NewThrowable("Error", "Cannot call abstract method "+ci.Name()+"::"+mname+"()")
		}
		if res, ok, err := vm.callMagicMethod(ci, recv, "__call",
			[]Slot{MakeString(mname), argsArrayNamed(arr)}); ok {
			return res, err
		}
		return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
	}
	if vis != Public && !vm.methodAccessibleFromProto(ci, decl, mname, vis, vm.ctx.self) {
		if pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname); ok {
			m, decl, vis = pm, pdecl, pvis
		} else {
			return Slot{}, vm.accessError(decl, mname, vis)
		}
	}
	if m.IsTrivial() {
		return MakeNull(), nil
	}
	args, err := vm.BindNamedArgs(m, arr)
	if err != nil {
		return Slot{}, err
	}
	return vm.invokeMethod(m, recv, ci, args)
}

// StaticCallNamed выполняет статический вызов с именованными аргументами.
// cls — имя класса (в т.ч. self/parent/static) или объект-значение.
func (vm *VM) StaticCallNamed(cls Slot, mname string, arr *Array) (Slot, error) {
	var ci ClassInfo
	scoped := false
	d := deref(cls)
	if d.typ == StringType {
		raw := strings.TrimLeft(d.StringVal(), "\\")
		if strings.EqualFold(raw, "self") || strings.EqualFold(raw, "parent") || strings.EqualFold(raw, "static") {
			scoped = true
			ci = vm.resolveCallableNameToClass(raw)
			if ci == nil {
				return Slot{}, NewThrowable("Error", "Class \""+raw+"\" not found")
			}
		}
	}
	if ci == nil {
		var err error
		ci, err = vm.DynClassByName(cls)
		if err != nil {
			return Slot{}, err
		}
	}
	if ci.IsTrait() {
		vm.Deprecated("Calling static trait method " + ci.Name() + "::" + mname + " is deprecated, it should only be called on a class using the trait")
	}
	m, decl, vis := ci.methodInfo(mname)
	if m == nil && strings.EqualFold(mname, "__construct") {
		if rc := asRC(ci); rc != nil {
			if c, cd, cv := rc.ctorInfo(); c != nil {
				m, decl, vis = c, cd, cv
			}
		} else {
			decl, vis = ci, Public
		}
	}
	if m == nil {
		if ci.hasAbstract(mname) {
			return Slot{}, NewThrowable("Error", "Cannot call abstract method "+ci.Name()+"::"+mname+"()")
		}
		calledM := ci
		if scoped && vm.ctx.called != nil {
			calledM = vm.ctx.called
		}
		if res, ok, err := vm.callMagicMethodCalled(ci, calledM, vm.cur.this, "__callStatic",
			[]Slot{MakeString(mname), argsArrayNamed(arr)}); ok {
			return res, err
		}
		return Slot{}, NewThrowable("Error", "Call to undefined method "+ci.Name()+"::"+mname+"()")
	}
	if vis != Public && !vm.methodAccessibleFromProto(ci, decl, mname, vis, vm.ctx.self) {
		if pm, pdecl, pvis, ok := scopeMethod(vm.ctx.self, mname); ok {
			m, decl, vis = pm, pdecl, pvis
		} else {
			return Slot{}, vm.accessError(decl, mname, vis)
		}
	}
	if m.IsTrivial() {
		return MakeNull(), nil
	}
	if err := vm.checkStaticCall(m, ci); err != nil {
		return Slot{}, err
	}
	called := ci
	if scoped && vm.ctx.called != nil {
		called = vm.ctx.called
	} else if !m.IsStatic && vm.ctx.called != nil {
		if ti := vm.thisClassInfo(); ti != nil && ti.IsA(ci) {
			called = vm.ctx.called
		}
	}
	args, err := vm.BindNamedArgs(m, arr)
	if err != nil {
		return Slot{}, err
	}
	return vm.invokeMethod(m, vm.staticCallRecv(ci), called, args)
}

// NewInstanceNamed создаёт объект класса и вызывает конструктор с
// именованными аргументами; для класса без конструктора лишние именованные
// аргументы — "Unknown named parameter $x" (Zend).
func (vm *VM) NewInstanceNamed(s Slot, arr *Array) (Slot, error) {
	d := deref(s)
	var ci ClassInfo
	var idx int
	if d.typ == IntType {
		idx = int(d.Int())
		if idx < 0 || idx >= len(vm.classes) {
			return Slot{}, NewThrowable("Error", "Class not found")
		}
		ci = vm.classes[idx]
	} else {
		var err error
		ci, err = vm.DynClassByName(s)
		if err != nil {
			return Slot{}, err
		}
		idx = vm.classIndex(ci)
	}
	if ci == nil {
		return Slot{}, NewThrowable("Error", "Cannot instantiate class")
	}
	if ci.IsInterface() || ci.IsTrait() {
		return Slot{}, cannotInstantiateErr(ci)
	}
	if ci.IsAbstract() {
		return Slot{}, NewThrowable("Error", "Cannot instantiate abstract class "+ci.Name())
	}
	obj := ci.New(&vm.ctx, nil)
	if obj == nil {
		return Slot{}, cannotInstantiateErr(ci)
	}
	slot := ci.objectSlot(obj, idx)
	ctor := ci.lookupCtor()
	if ctor != nil {
		args, err := vm.BindNamedArgs(ctor, arr)
		if err != nil {
			return Slot{}, err
		}
		vm.MarkNoDestruct(obj)
		if _, err := vm.invokeMethod(ctor, slot, ci, args); err != nil {
			// Брошенный конструктор: объект уничтожается без __destruct.
			return Slot{}, err
		}
		vm.UnmarkNoDestruct(obj)
		return slot, nil
	}
	if arr != nil {
		for k := range arr.All() {
			if k.IsStr() {
				return Slot{}, NewThrowable("Error", "Unknown named parameter $"+k.StrVal())
			}
		}
	}
	return slot, nil
}

// firstArgArray возвращает первый позиционный аргумент как массив.
func firstArgArray(arr *Array) *Array {
	for k, v := range arr.All() {
		if !k.IsStr() {
			if d := deref(v); d.typ == ArrayType {
				return d.AsArray()
			}
			return nil
		}
	}
	return nil
}

// argsArrayNamed упаковывает массив аргументов (с возможными строковыми
// ключами) для $arguments магического __call.
func argsArrayNamed(arr *Array) Slot {
	if arr == nil {
		return MakeArray(newArray())
	}
	out := newArray()
	for k, v := range arr.All() {
		_ = out.Set(k, v)
	}
	return MakeArray(out)
}

// NormalizeCallableSlot заменяет self/parent/static в callable на конкретный
// класс (Zend сохраняет резолв fcc при регистрации callback'ов).
func (vm *VM) NormalizeCallableSlot(c Slot) Slot {
	d := deref(c)
	switch d.typ {
	case StringType:
		raw := d.StringVal()
		if i := strings.Index(raw, "::"); i >= 0 {
			cls := strings.TrimLeft(raw[:i], "\\")
			if strings.EqualFold(cls, "self") || strings.EqualFold(cls, "parent") || strings.EqualFold(cls, "static") {
				if ci := vm.resolveCallableNameToClass(cls); ci != nil {
					return MakeString(ci.Name() + raw[i:])
				}
			}
		}
	case ArrayType:
		a := d.AsArray()
		if a != nil && a.Len() == 2 {
			vals := a.Values()
			recv := deref(vals[0])
			if recv.typ == StringType {
				cls := strings.TrimLeft(recv.StringVal(), "\\")
				if strings.EqualFold(cls, "self") || strings.EqualFold(cls, "parent") || strings.EqualFold(cls, "static") {
					if ci := vm.resolveCallableNameToClass(cls); ci != nil {
						na := NewArray()
						_ = na.Set(MakeIntKey(0), MakeString(ci.Name()))
						_ = na.Set(MakeIntKey(1), vals[1])
						return MakeArray(na)
					}
				}
			}
		}
	}
	return c
}

// resolveCallableNameToClass резолвит self/parent/static в текущий scope для
// callable (без диагностик; deprecation выдаётся при валидации).
func (vm *VM) resolveCallableNameToClass(raw string) ClassInfo {
	if vm.ctx.self != nil {
		switch {
		case strings.EqualFold(raw, "parent"):
			return vm.ctx.self.Parent()
		case strings.EqualFold(raw, "self"):
			return vm.ctx.self
		case strings.EqualFold(raw, "static"):
			if vm.ctx.called != nil {
				return vm.ctx.called
			}
			return vm.ctx.self
		}
	}
	return vm.FindClass(raw)
}

// callStringCallable вызывает функцию по строке: "func" или "Class::method".
func (vm *VM) callStringCallable(raw string, args []Slot) (Slot, error) {
	name := strings.TrimLeft(raw, "\\")
	if i := strings.Index(name, "::"); i >= 0 {
		clsName := strings.TrimLeft(name[:i], "\\")
		mname := name[i+2:]
		ci := vm.resolveCallableNameToClass(clsName)
		if ci == nil {
			// Zend вызывает автозагрузчик при резолве "Class::method".
			if _, aerr := vm.Autoload(nil, clsName); aerr != nil {
				return Slot{}, aerr
			}
			ci = vm.resolveCallableNameToClass(clsName)
		}
		if ci == nil {
			return Slot{}, NewThrowable("Error", fmt.Sprintf("Class \"%s\" not found", clsName))
		}
		called := ci
		if vm.forwardCalled != nil && vm.forwardCalled.IsA(ci) {
			called = vm.forwardCalled
		} else if (strings.EqualFold(clsName, "self") || strings.EqualFold(clsName, "parent") || strings.EqualFold(clsName, "static")) && vm.ctx.called != nil {
			called = vm.ctx.called
		}
		m := methodOrCtor(ci, mname)
		if m == nil {
			if res, ok, err := vm.callMagicMethodCalled(ci, called, vm.cur.this, "__callStatic", []Slot{MakeString(mname), argsArray(args)}); ok {
				return res, err
			}
			return Slot{}, NewThrowable("Error", fmt.Sprintf("Call to undefined method %s::%s()", ci.Name(), mname))
		}
		return vm.invokeMethod(m, vm.staticCallRecv(ci), called, args)
	}
	if fn := vm.functionByName(name); fn != nil {
		return vm.invoke(fn, args)
	}
	return Slot{}, NewThrowable("Error", fmt.Sprintf("Call to undefined function %s()", name))
}

// callArrayCallable вызывает [obj|class, method].
func (vm *VM) callArrayCallable(c Slot, args []Slot) (Slot, error) {
	a := c.AsArray()
	if a == nil || a.Len() != 2 {
		return Slot{}, NewThrowable("Error", "Array callback must have exactly two elements")
	}
	vals := a.Values()
	recv := deref(vals[0])
	mname := strings.TrimLeft(deref(vals[1]).ToString(), "\\")

	// [Closure, "__invoke"] — вызываемое: Zend зовёт __invoke замыкания.
	if recv.typ == ClosureType {
		cl := recv.asClosure()
		if cl == nil || cl.Fn == nil {
			return Slot{}, NewThrowable("Error", "Value is not callable")
		}
		if strings.EqualFold(mname, "__invoke") {
			savedAlias := vm.byRefAlias
			vm.byRefAlias = "Closure::__invoke"
			res, err := vm.invokeClosure(cl, args)
			vm.byRefAlias = savedAlias
			return res, err
		}
		return Slot{}, NewThrowable("Error", "Call to undefined method Closure::"+mname+"()")
	}

	if recv.typ == ObjectType {
		ci := vm.classInfo(recv)
		if ci == nil {
			return Slot{}, NewThrowable("Error", "Value is not callable")
		}
		tci := ci
		if vm.forwardCalled != nil && vm.forwardCalled.IsA(ci) {
			tci = vm.forwardCalled
		}
		if strings.Contains(mname, "::") {
			if qci, meth, _ := vm.resolveQualifiedMethod(ci, mname); qci != nil {
				if m := methodOrCtor(qci, meth); m != nil {
					called := qci
					if vm.forwardCalled != nil && vm.forwardCalled.IsA(qci) {
						called = vm.forwardCalled
					}
					return vm.invokeMethod(m, recv, called, args)
				}
			}
		}
		if m := methodOrCtor(ci, mname); m != nil {
			if _, decl, vis := ci.methodInfo(mname); decl != nil && vis != Public &&
				!vm.methodAccessibleFromProto(ci, decl, mname, vis, vm.ctx.self) {
				// Недоступный метод: Zend зовёт __call, иначе Error доступа.
				if res, ok, err := vm.callMagicMethod(ci, recv, "__call", []Slot{MakeString(mname), argsArray(args)}); ok {
					return res, err
				}
				return Slot{}, vm.accessError(decl, mname, vis)
			}
			return vm.invokeMethod(m, recv, tci, args)
		}
		if res, ok, err := vm.callMagicMethod(ci, recv, "__call", []Slot{MakeString(mname), argsArray(args)}); ok {
			return res, err
		}
		return Slot{}, NewThrowable("Error", fmt.Sprintf("Call to undefined method %s::%s()", ci.Name(), mname))
	}

	if recv.typ == StringType {
		return vm.callStaticStringCallable(recv.StringVal(), mname, args)
	}

	return Slot{}, NewThrowable("Error", "Value is not callable")
}

// callStaticStringCallable — динамический вызов "Class::method" (строкой или
// массивом [Class, method]). Zend: существующий нестатический метод в такой
// позиции — ошибка "Non-static method ... cannot be called statically"; если
// метода нет, но есть instance-__call и активный $this — та же ошибка,
// иначе "Call to undefined method". __callStatic имеет приоритет, но только
// для отсутствующих (не для нестатических) методов.
func (vm *VM) callStaticStringCallable(clsName, mname string, args []Slot) (Slot, error) {
	clsName = strings.TrimLeft(clsName, "\\")
	ci := vm.resolveCallableNameToClass(clsName)
	if ci == nil {
		return Slot{}, NewThrowable("Error", fmt.Sprintf("Class \"%s\" not found", clsName))
	}
	called := ci
	if vm.forwardCalled != nil && vm.forwardCalled.IsA(ci) {
		called = vm.forwardCalled
	} else if (strings.EqualFold(clsName, "self") || strings.EqualFold(clsName, "parent") || strings.EqualFold(clsName, "static")) && vm.ctx.called != nil {
		called = vm.ctx.called
	}
	if strings.Contains(mname, "::") {
		if tci, meth, _ := vm.resolveQualifiedMethod(ci, mname); tci != nil {
			if m := methodOrCtor(tci, meth); m != nil {
				if !m.IsStatic && meth != "__construct" {
					if recv := vm.staticCallRecv(tci); deref(recv).typ == ObjectType {
						return vm.invokeMethod(m, recv, tci, args)
					}
					return Slot{}, NewThrowable("Error", "Non-static method "+tci.Name()+"::"+m.Name+"() cannot be called statically")
				}
				return vm.invokeMethod(m, Slot{}, tci, args)
			}
		}
	}
	if m := methodOrCtor(ci, mname); m != nil {
		if !m.IsStatic && mname != "__construct" {
			// Как checkStaticCall: вызов допустим, если есть активный $this
			// совместимого класса (Zend биндит метод на него).
			if recv := vm.staticCallRecv(ci); deref(recv).typ == ObjectType {
				return vm.invokeMethod(m, recv, called, args)
			}
			return Slot{}, NewThrowable("Error", "Non-static method "+ci.Name()+"::"+m.Name+"() cannot be called statically")
		}
		return vm.invokeMethod(m, Slot{}, called, args)
	}
	if res, ok, err := vm.callMagicMethodCalled(ci, called, vm.cur.this, "__callStatic", []Slot{MakeString(mname), argsArray(args)}); ok {
		return res, err
	}
	if strings.EqualFold(mname, "__construct") {
		return Slot{}, NewThrowable("Error", "Cannot call constructor")
	}
	// В объектном контексте с instance-__call Zend разрешает C::m как
	// нестатический вызов — и сообщает об ошибке "cannot be called statically".
	if t := deref(vm.ctx.this); t.typ == ObjectType && ci.lookupMethod("__call") != nil {
		if tci := vm.classInfo(t); tci != nil && tci.IsA(ci) {
			return Slot{}, NewThrowable("Error", "Non-static method "+ci.Name()+"::"+mname+"() cannot be called statically")
		}
	}
	return Slot{}, NewThrowable("Error", fmt.Sprintf("Call to undefined method %s::%s()", ci.Name(), mname))
}

// callableTypeName — имя типа для "Value of type %s is not callable"
// (bool без true/false, как get_debug_type).
func callableTypeName(vm *VM, s Slot) string {
	switch s.typ {
	case NullType:
		return "null"
	case BoolType:
		return "bool"
	case IntType:
		return "int"
	case FloatType:
		return "float"
	case StringType:
		return "string"
	case ArrayType:
		return "array"
	case ObjectType:
		if ci := vm.classInfo(s); ci != nil {
			return ClassDisplayName(ci.Name())
		}
		return "object"
	case ClosureType:
		return "Closure"
	case ResourceType:
		return "resource"
	case RefType:
		return callableTypeName(vm, deref(s))
	}
	return "unknown"
}

// ClassHasMethod сообщает, есть ли у класса метод (включая унаследованные и
// магические __call/__callStatic).
func (vm *VM) ClassHasMethod(ci ClassInfo, name string) bool {
	if ci == nil {
		return false
	}
	return ci.lookupMethod(name) != nil || ci.lookupMethod("__call") != nil || ci.lookupMethod("__callStatic") != nil
}

// MethodStatic возвращает (статичен ли метод, найден ли) с учётом наследования
// и __callStatic.
func (vm *VM) MethodStatic(ci ClassInfo, name string) (bool, bool) {
	if ci == nil {
		return false, false
	}
	if m := ci.lookupMethod(name); m != nil {
		return m.IsStatic, true
	}
	if ci.lookupMethod("__callStatic") != nil {
		return true, true
	}
	return false, false
}

// FunctionByName сообщает, существует ли функция с таким именем (без учёта регистра).
func (vm *VM) FunctionByName(name string) bool { return vm.functionByName(name) != nil }

// IsCallable сообщает, является ли c вызываемым значением (без вызова).
func (vm *VM) IsCallable(c Slot) bool {
	ok, _ := vm.IsCallableEx(c, false)
	return ok
}

// IsCallableEx повторяет zend_is_callable_ex: возвращает признак вызываемости и
// каноническое имя callback'а (для is_callable(..., &$callable_name)).
func (vm *VM) IsCallableEx(c Slot, syntaxOnly bool) (bool, string) {
	c = deref(c)
	switch c.typ {
	case ClosureType:
		cl := c.asClosure()
		return cl != nil && cl.Fn != nil, "Closure::__invoke"
	case ObjectType:
		ci := vm.classInfo(c)
		if ci != nil {
			nm := ClassDisplayName(ci.Name()) + "::__invoke"
			if m := ci.lookupMethod("__invoke"); m != nil {
				_ = m
				return true, nm
			}
			return false, nm
		}
		return false, ""
	case StringType:
		raw := c.StringVal()
		name := raw
		lookup := strings.TrimLeft(raw, "\\")
		if i := strings.Index(lookup, "::"); i >= 0 {
			cls := strings.TrimLeft(lookup[:i], "\\")
			meth := lookup[i+2:]
			if strings.EqualFold(cls, "self") || strings.EqualFold(cls, "parent") || strings.EqualFold(cls, "static") {
				vm.Deprecated(`Use of "` + cls + `" in callables is deprecated`)
			}
			cn := vm.callableClassName(cls)
			nm := cn + "::" + meth
			if syntaxOnly {
				return true, name
			}
			ci := vm.resolveCallableNameToClass(cls)
			if ci == nil {
				return false, nm
			}
			if m := methodOrCtor(ci, meth); m != nil {
				if !m.IsStatic && meth != "__construct" {
					// Нестатический метод в scoped-имени допустим при
					// совместимом активном $this (Zend: self/parent/static и
					// scoped-вызов "C::m" из метода C).
					ti := vm.thisClassInfo()
					if ti == nil || !ti.IsA(ci) {
						return false, nm
					}
				}
				if vis, ok := ci.MethodVisibility(meth); ok && vis != Public {
					decl := ci
					if _, d, dvis := ci.methodInfo(meth); d != nil && dvis != Public {
						decl, vis = d, dvis
					}
					if !vm.methodAccessibleFromProto(ci, decl, meth, vis, vm.ctx.self) {
						return false, nm
					}
				}
				return true, nm
			}
			if ci.lookupMethod("__callStatic") != nil {
				return true, nm
			}
			return false, nm
		}
		if syntaxOnly {
			return true, name
		}
		if vm.functionByName(lookup) != nil {
			return true, name
		}
		return false, name
	case ArrayType:
		a := c.AsArray()
		if a == nil || a.Len() != 2 {
			return false, "Array"
		}
		vals := a.Values()
		recv := deref(vals[0])
		mname := strings.TrimLeft(deref(vals[1]).ToString(), "\\")
		switch recv.typ {
		case ClosureType:
			// [Closure, "__invoke"] — callable (__invoke у Closure).
			nm := "Closure::" + mname
			if strings.EqualFold(mname, "__invoke") || syntaxOnly {
				return true, nm
			}
			return false, nm
		case ObjectType:
			ci := vm.classInfo(recv)
			if ci == nil {
				return false, ""
			}
			nm := vm.callableDisplayRaw(ci) + "::" + mname
			if strings.Contains(mname, "::") {
				// Проверка совместимости получателя выполняется до выдачи
				// deprecation (Zend не warning'ит невалидный callable).
				tci, meth, _ := vm.resolveQualifiedMethod(ci, mname)
				if tci == nil || !ci.IsA(tci) {
					return false, nm
				}
				vm.Deprecated(`Callables of the form ["` + ClassDisplayName(ci.Name()) + `", "` + mname + `"] are deprecated`)
				if syntaxOnly {
					return true, nm
				}
				if m := methodOrCtor(tci, meth); m != nil {
					return true, nm
				}
				if tci.lookupMethod("__callStatic") != nil || ci.lookupMethod("__call") != nil {
					return true, nm
				}
				return false, nm
			}
			if syntaxOnly {
				return true, nm
			}
			if m := methodOrCtor(ci, mname); m != nil {
				if vis, ok := ci.MethodVisibility(mname); ok && !vm.methodAccessible(ci, vis) {
					return false, nm
				}
				return true, nm
			}
			if ci.lookupMethod("__call") != nil {
				return true, nm
			}
			return false, nm
		case StringType:
			raw := strings.TrimLeft(recv.StringVal(), "\\")
			nm := raw + "::" + mname
			if strings.EqualFold(raw, "self") || strings.EqualFold(raw, "parent") || strings.EqualFold(raw, "static") {
				vm.Deprecated(`Use of "` + raw + `" in callables is deprecated`)
			}
			if strings.Contains(mname, "::") {
				outerCI := vm.resolveCallableNameToClass(raw)
				tci, meth, _ := vm.resolveQualifiedMethod(outerCI, mname)
				if outerCI == nil || tci == nil || !outerCI.IsA(tci) {
					return false, nm
				}
				vm.Deprecated(`Callables of the form ["` + raw + `", "` + mname + `"] are deprecated`)
				if syntaxOnly {
					return true, nm
				}
				if m := methodOrCtor(tci, meth); m != nil {
					return true, nm
				}
				return false, nm
			}
			if syntaxOnly {
				return true, nm
			}
			ci, _ := vm.resolveCallableClass(raw)
			if ci == nil {
				return false, nm
			}
			nm = ci.Name() + "::" + mname
			parentish := strings.EqualFold(raw, "parent") || strings.EqualFold(raw, "self") || strings.EqualFold(raw, "static")
			if m := methodOrCtor(ci, mname); m != nil {
				if !m.IsStatic && !parentish && mname != "__construct" {
					// Scoped-вызов (["C","m"] из метода, где $this — экземпляр C)
					// Zend считает callable (zend_is_callable).
					if ti := vm.thisClassInfo(); ti == nil || !ti.IsA(ci) {
						return false, nm
					}
				}
				if vis, ok := ci.MethodVisibility(mname); ok && !vm.methodAccessible(ci, vis) {
					return false, nm
				}
				return true, nm
			}
			if ci.lookupMethod("__callStatic") != nil {
				return true, nm
			}
			return false, nm
		}
		return false, "Array"
	}
	return false, callableScalarName(c)
}

// callableScalarName повторяет zval_get_string для не-вызываемых скаляров.
func callableScalarName(c Slot) string {
	switch c.typ {
	case ArrayType:
		return "Array"
	case ResourceType:
		return "Resource id #" + intToString(int64(c.num))
	case NullType:
		return ""
	}
	return c.ToString()
}

// callableClassName нормализует имя класса для callable_name строковой формы.
func (vm *VM) callableClassName(cls string) string {
	if strings.EqualFold(cls, "parent") && vm.ctx.self != nil {
		if p := vm.ctx.self.Parent(); p != nil {
			return p.Name()
		}
	}
	if (strings.EqualFold(cls, "self") || strings.EqualFold(cls, "static")) && vm.ctx.self != nil {
		return vm.ctx.self.Name()
	}
	return cls
}

// callableDisplayRaw — имя класса как в zend: для анонимного класса берётся
// внутреннее имя (чтобы call_user_func мог его разрешить).
func (vm *VM) callableDisplayRaw(ci ClassInfo) string {
	if strings.HasPrefix(ci.Name(), "\x00anon#") {
		return ci.Name()
	}
	return ClassDisplayName(ci.Name())
}

// resolveCallableClass разрешает имя класса для array-callable с учётом
// parent/self/static. Возвращает 1, если напечатана deprecation.
func (vm *VM) resolveCallableClass(raw string) (ClassInfo, int) {
	return vm.resolveCallableNameToClass(raw), 0
}
