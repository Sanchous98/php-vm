package vm

// Встроенные классы Reflection (ReflectionClass, ReflectionMethod, ...).
//
// Реализованы как нативные классы: данные объекта — общая Go-структура
// reflectionData, встраивающая Object. Дескрипторы целей (ClassInfo/*Function)
// хранятся прямо в структуре; Go GC сканирует их как обычные указатели.

import (
	"fmt"
	"sort"
	"strings"
	"unsafe"
)

// refKind — разновидность рефлектируемой сущности.
type refKind uint8

const (
	rkClass refKind = iota
	rkObject
	rkEnum
	rkMethod
	rkProperty
	rkFunction
	rkParameter
	rkClassConst
	rkNamedType
	rkUnionType
	rkAttr
	rkEnumCase
	rkEnumBackedCase
	rkGenerator
)

// reflectionData — данные любого Reflection-объекта.
type reflectionData struct {
	Object
	kind    refKind
	class   ClassInfo // цель: класс/интерфейс/трейт/enum
	fn      *Function // цель: метод/функция
	name    string    // имя члена/функции/типа
	decl    ClassInfo // класс-объявитель (для методов/свойств/констант)
	vis     Visibility
	this    Slot // объект-цель (ReflectionObject / getValue/setValue)
	index   int  // индекс параметра / атрибута / enum-кейса
	isFunc  bool // fn — обычная функция (не метод)
	closure bool // цель — замыкание
	// closureSlot — слот замыкания (текущая область может отличаться от fn.Scope
	// после bindTo).
	closureSlot Slot
	// Для типов.
	typeName       string
	nullable       bool
	memberTypes    []string
	isIntersection bool
	// Для атрибутов.
	attrName     string
	attrArgs     []Slot
	attrNamed    []NamedAttrArg
	attrArgFns   []*Function
	attrNamedFns []NamedAttrFn
	attrEvaled   bool
	attrEvalErr  error
	// attrScope — вычисленная при создании область атрибута (замыкания/трейты
	// не выводятся из самого ReflectionAttribute).
	attrScope    ClassInfo
	attrScopeSet bool
	attrRepeated bool
	attrTarget   int
	attrFile     string
	attrLine     int
	// attrStrict — файл, объявивший атрибут, был strict_types=1: конструктор
	// при newInstance() вызывается в этом режиме (030_strict_types).
	attrStrict bool
	// Для enum-кейсов.
	caseSlot Slot
}

// AttributeInfo — разобранный атрибут `#[Name(args)]`.
type AttributeInfo struct {
	Name     string
	Args     []Slot
	Named    []NamedAttrArg
	Repeated bool
	// File/Line — место объявления атрибута (dummy-кадр newInstance).
	File string
	Line int
	// Strict — файл, объявивший атрибут, был с declare(strict_types=1).
	// Конструктор при newInstance() вызывается в этом режиме (030_strict_types:
	// strict_types use-site).
	Strict bool
	// ArgFns/NamedArgFns — отложенные выражения аргументов (собранные
	// компилятором константные выражения). Если заданы, Args/Named не
	// заполнены и вычисляются при первом обращении в области объявления
	// (self::/forward-константы; см. reflGetAttributes).
	ArgFns      []*Function
	NamedArgFns []NamedAttrFn
}

// NamedAttrFn — отложенный именованный аргумент атрибута.
type NamedAttrFn struct {
	Name string
	Fn   *Function
}

// NamedAttrArg — именованный аргумент атрибута.
type NamedAttrArg struct {
	Name string
	Val  Slot
}

// IsAttributeClass сообщает, что класс объявлен как `#[Attribute]`.
func (c *runtimeClass) IsAttributeClass() bool { return c != nil && c.attrClass }

// MarkAttributeClass помечает класс как `#[Attribute]` с указанными целями.
func (c *runtimeClass) MarkAttributeClass(flags uint32) ClassInfo {
	if c != nil {
		c.attrClass = true
		c.attrFlags = flags
	}
	return c
}

// MarkAttributeClassDynamic помечает класс как `#[Attribute(expr)]`:
// флаги вычисляются отложенно (thunk) при newInstance (Zend).
func (c *runtimeClass) MarkAttributeClassDynamic(th *Thunk) ClassInfo {
	if c != nil {
		c.attrClass = true
		c.attrFlagsThunk = th
	}
	return c
}

// AttributeFlagsEval вычисляет и проверяет цели класса-атрибута.
func (vm *VM) AttributeFlagsEval(ci ClassInfo) (uint32, error) {
	rc := asRC(ci)
	if rc == nil || !rc.attrClass {
		return 0, nil
	}
	if rc.attrFlagsThunk != nil {
		v, err := vm.evalThunk(rc.attrFlagsThunk)
		if err != nil {
			return 0, err
		}
		v = deref(v)
		if v.typ != IntType {
			return 0, NewThrowable("Error", "Attribute::__construct(): Argument #1 ($flags) must be of type int, "+valueTypeName(vm, v)+" given")
		}
		n := v.ToInt()
		if n < 0 || n & ^int64(0x7f) != 0 {
			return 0, NewThrowable("Error", "Invalid attribute flags specified")
		}
		return uint32(n), nil
	}
	if rc.attrFlags&^0x7f != 0 {
		return 0, NewThrowable("Error", "Invalid attribute flags specified")
	}
	return rc.attrFlags, nil
}

// AttrTargetNames повторяет zend_get_attribute_target_names.
func AttrTargetNames(flags int) string {
	names := []string{"class", "function", "method", "property", "class constant", "parameter"}
	var parts []string
	for i, n := range names {
		if flags&(1<<i) != 0 {
			parts = append(parts, n)
		}
	}
	return strings.Join(parts, ", ")
}

// AttributeFlags возвращает цели класса-атрибута (0 — не атрибут).
func AttributeFlags(ci ClassInfo) (uint32, bool) {
	if rc := asRC(ci); rc != nil && rc.attrClass {
		return rc.attrFlags, true
	}
	return 0, false
}

// SetClassAttrs задаёт атрибуты класса.
func SetClassAttrs(ci ClassInfo, attrs []AttributeInfo) {
	if rc := asRC(ci); rc != nil {
		rc.attrs = attrs
	}
}

// SetMethodAttrs задаёт атрибуты метода.
func SetMethodAttrs(ci ClassInfo, name string, attrs []AttributeInfo) {
	rc := asRC(ci)
	if rc == nil || len(attrs) == 0 {
		return
	}
	if rc.methAttrs == nil {
		rc.methAttrs = map[string][]AttributeInfo{}
	}
	rc.methAttrs[name] = attrs
}

// SetPropAttrs задаёт атрибуты свойства.
func SetPropAttrs(ci ClassInfo, name string, attrs []AttributeInfo) {
	rc := asRC(ci)
	if rc == nil || len(attrs) == 0 {
		return
	}
	if rc.propAttrs == nil {
		rc.propAttrs = map[string][]AttributeInfo{}
	}
	rc.propAttrs[name] = attrs
}

// SetConstAttrs задаёт атрибуты константы.
func SetConstAttrs(ci ClassInfo, name string, attrs []AttributeInfo) {
	rc := asRC(ci)
	if rc == nil || len(attrs) == 0 {
		return
	}
	if rc.constAttrs == nil {
		rc.constAttrs = map[string][]AttributeInfo{}
	}
	rc.constAttrs[name] = attrs
}

// SetParamAttrs задаёт атрибуты параметра метода.
func SetParamAttrs(ci ClassInfo, method string, idx int, attrs []AttributeInfo) {
	rc := asRC(ci)
	if rc == nil || len(attrs) == 0 {
		return
	}
	if rc.paramAttrs == nil {
		rc.paramAttrs = map[string]map[int][]AttributeInfo{}
	}
	m := rc.paramAttrs[method]
	if m == nil {
		m = map[int][]AttributeInfo{}
		rc.paramAttrs[method] = m
	}
	m[idx] = attrs
}

// --- Метаданные рефлексии на runtimeClass ---

// MarkInternal помечает класс как встроенный.
func (c *runtimeClass) MarkInternal() {
	if c != nil {
		c.internal = true
	}
}

// MarkInternal помечает дескриптор встроенным (экспорт для builtins).
func MarkInternal(ci ClassInfo) {
	if rc, ok := asRuntimeClass(ci); ok && rc != nil {
		rc.internal = true
	}
}

// MarkFinal помечает класс как final (для ReflectionClass::isFinal и
// запрета наследования).
func MarkFinal(ci ClassInfo) {
	if rc, ok := asRuntimeClass(ci); ok && rc != nil {
		rc.final = true
	}
}

// SetClassDefinition задаёт расположение объявления класса (рефлексия).
func SetClassDefinition(ci ClassInfo, file string, start, end int) {
	if rc, ok := asRuntimeClass(ci); ok && rc != nil {
		rc.defFile = file
		rc.defStart = start
		rc.defEnd = end
	}
}

// SetDefinition задаёт расположение объявления класса.
func (c *runtimeClass) SetDefinition(file string, start, end int) {
	c.defFile = file
	c.defStart = start
	c.defEnd = end
}

// ClassDeclPosition возвращает (файл, строку объявления) класса.
func ClassDeclPosition(ci ClassInfo) (string, int) {
	rc := asRC(ci)
	if rc == nil {
		return "", 0
	}
	return rc.defFile, rc.defStart
}

// SetMethodFinal помечает метод final.
func (c *runtimeClass) SetMethodFinal(name string) {
	if c.methFinal == nil {
		c.methFinal = map[string]bool{}
	}
	c.methFinal[name] = true
}

// methodFinal сообщает, помечен ли метод final (с учётом наследования).
func (c *runtimeClass) methodFinal(name string) bool {
	for k := c; k != nil; k = k.parent {
		if k.methFinal[name] {
			return true
		}
		if m, ok := k.methods[name]; ok && m.IsFinal {
			return true
		}
	}
	return false
}

// --- Регистрация классов ---

// NewReflectionClasses создаёт дескрипторы всех классов Reflection.
func NewReflectionClasses() []ClassInfo {
	attrClass := NewClass("Attribute", nil)
	MarkInternal(attrClass)
	attrClass.MarkAttributeClass(1) // внутренний Attribute: только TARGET_CLASS
	for _, kv := range []struct {
		n string
		v int64
	}{
		{"TARGET_CLASS", 1}, {"TARGET_FUNCTION", 2}, {"TARGET_METHOD", 4},
		{"TARGET_PROPERTY", 8}, {"TARGET_CLASS_CONSTANT", 16}, {"TARGET_PARAMETER", 32},
		{"TARGET_ALL", 63}, {"IS_REPEATABLE", 64},
	} {
		attrClass.DefineConst(kv.n, MakeInt(kv.v))
	}

	rc := RegisterClass[reflectionData]("ReflectionClass", nil)
	ro := RegisterClass[reflectionData]("ReflectionObject", rc)
	re := RegisterClass[reflectionData]("ReflectionEnum", rc)
	rm := RegisterClass[reflectionData]("ReflectionMethod", nil)
	rp := RegisterClass[reflectionData]("ReflectionProperty", nil)
	rf := RegisterClass[reflectionData]("ReflectionFunction", nil)
	rfa := RegisterClass[reflectionData]("ReflectionFunctionAbstract", nil)
	rpar := RegisterClass[reflectionData]("ReflectionParameter", nil)
	rcc := RegisterClass[reflectionData]("ReflectionClassConstant", nil)
	rnt := RegisterClass[reflectionData]("ReflectionNamedType", nil)
	rut := RegisterClass[reflectionData]("ReflectionUnionType", nil)
	rit := RegisterClass[reflectionData]("ReflectionIntersectionType", nil)
	rat := RegisterClass[reflectionData]("ReflectionAttribute", nil)
	rat.DefineConst("IS_INSTANCEOF", MakeInt(2))
	rec := RegisterClass[reflectionData]("ReflectionEnumUnitCase", nil)
	reb := RegisterClass[reflectionData]("ReflectionEnumBackedCase", rec)
	rg := RegisterClass[reflectionData]("ReflectionGenerator", nil)

	all := []ClassInfo{
		rc, ro, re, rm, rp, rf, rfa, rpar, rcc, rnt, rut, rit, rat, rec, reb, rg,
	}
	for _, ci := range all {
		MarkInternal(ci)
	}
	registerReflectionClassMembers(rc)
	registerReflectionClassMembers(ro)
	registerReflectionEnumMembers(re)
	registerReflectionMethodMembers(rm)
	registerReflectionFunctionMembers(rf)
	registerReflectionFunctionMembers(rfa)
	registerReflectionPropertyMembers(rp)
	registerReflectionParameterMembers(rpar)
	registerReflectionClassConstantMembers(rcc)
	registerReflectionTypeMembers(rnt)
	registerReflectionUnionMembers(rut)
	registerReflectionUnionMembers(rit)
	registerReflectionAttributeMembers(rat)
	registerReflectionEnumCaseMembers(rec)
	registerReflectionEnumCaseMembers(reb)
	registerReflectionGeneratorMembers(rg)
	return append(all, attrClass)
}

// --- Вспомогательные ---

// reflObj создаёт Reflection-объект класса className и возврашает его данные.
func (vm *VM) reflObj(className string) (*reflectionData, Slot) {
	ci := vm.findClass(className)
	if ci == nil {
		return nil, MakeNull()
	}
	o := ci.New(&vm.ctx, nil)
	if o == nil {
		return nil, MakeNull()
	}
	vm.markObj(o)
	idx := vm.classIndex(ci)
	if idx < 0 {
		idx = 0
	}
	slot := Slot{typ: ObjectType, num: uint64(idx), ptr: unsafe.Pointer(o)}
	return (*reflectionData)(unsafe.Pointer(o)), slot
}

// reflNewClass создаёт ReflectionClass по цели (или ReflectionEnum для enum).
func (vm *VM) reflNewClass(ci ClassInfo) Slot {
	if ci == nil {
		return MakeNull()
	}
	if ci.IsEnum() {
		return vm.reflNewEnum(ci)
	}
	d, s := vm.reflObj("ReflectionClass")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkClass
	d.class = ci
	return s
}

// reflNewObject создаёт ReflectionObject для объекта.
func (vm *VM) reflNewObject(ci ClassInfo, obj Slot) Slot {
	d, s := vm.reflObj("ReflectionObject")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkObject
	d.class = ci
	d.this = obj
	return s
}

// reflNewEnum создаёт ReflectionEnum.
func (vm *VM) reflNewEnum(ci ClassInfo) Slot {
	d, s := vm.reflObj("ReflectionEnum")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkEnum
	d.class = ci
	return s
}

// reflNewMethod создаёт ReflectionMethod.
func (vm *VM) reflNewMethod(ci ClassInfo, name string, fn *Function, decl ClassInfo, vis Visibility) Slot {
	d, s := vm.reflObj("ReflectionMethod")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkMethod
	d.class = ci
	d.name = name
	d.fn = fn
	d.decl = decl
	d.vis = vis
	return s
}

// reflNewProperty создаёт ReflectionProperty.
func (vm *VM) reflNewProperty(ci ClassInfo, name string, decl ClassInfo, vis Visibility) Slot {
	d, s := vm.reflObj("ReflectionProperty")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkProperty
	d.class = ci
	d.name = name
	d.decl = decl
	d.vis = vis
	return s
}

// reflNewFunction создаёт ReflectionFunction.
func (vm *VM) reflNewFunction(name string, fn *Function) Slot {
	d, s := vm.reflObj("ReflectionFunction")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkFunction
	d.name = name
	d.fn = fn
	d.isFunc = true
	return s
}

// reflNewClassConst создаёт ReflectionClassConstant.
func (vm *VM) reflNewClassConst(ci ClassInfo, name string, decl ClassInfo, vis Visibility) Slot {
	d, s := vm.reflObj("ReflectionClassConstant")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkClassConst
	d.class = ci
	d.name = name
	d.decl = decl
	d.vis = vis
	return s
}

// reflNewParameter создаёт ReflectionParameter.
func (vm *VM) reflNewParameter(owner Slot, index int) Slot {
	d, s := vm.reflObj("ReflectionParameter")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkParameter
	d.this = owner
	d.index = index
	return s
}

// reflArgClass извлекает класс из аргумента (строка|объект) с автозагрузкой.
func (vm *VM) reflArgClass(ctx *Context, arg Slot, kind string) (ClassInfo, Slot, error) {
	a := deref(arg)
	if a.typ == ObjectType {
		ci := vm.classInfo(a)
		if ci == nil {
			return nil, Slot{}, NewThrowable("ReflectionException", "Class does not exist")
		}
		return ci, a, nil
	}
	name := a.ToString()
	ci := vm.FindClass(name)
	if ci == nil {
		if _, err := vm.Autoload(ctx, name); err != nil {
			return nil, Slot{}, err
		}
		ci = vm.FindClass(name)
	}
	if ci == nil {
		if kind == "Interface" {
			return nil, Slot{}, NewThrowable("ReflectionException", "Interface \""+name+"\" does not exist")
		}
		return nil, Slot{}, NewThrowable("ReflectionException", "Class \""+name+"\" does not exist")
	}
	return ci, Slot{}, nil
}

// reflMethodReg регистрирует метод Reflection-класса.
func reflMethodReg(ci ClassInfo, name string, fn func(ctx *Context, d *reflectionData, args []Slot) (Slot, error)) {
	ci.Method(name, &Function{Name: name, NumArgs: -1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[reflectionData]()
			if d == nil {
				return Slot{}, NewThrowable("Error", "Reflection object is not initialized")
			}
			return fn(ctx, d, args)
		},
	}})
}

// reflMethodRegVar регистрирует нативный метод с именем вариативного
// параметра (Reflection::invoke(newInstance) — `...$args`): extra named
// arguments собираются в variadic и доступны через TakeExtraNamedParams.
func reflMethodRegVar(ci ClassInfo, name string, paramNames []string, fn func(ctx *Context, d *reflectionData, args []Slot) (Slot, error)) {
	reflMethodReg(ci, name, fn)
	if rc, ok := ci.(*runtimeClass); ok {
		if m := rc.lookupMethod(name); m != nil {
			m.ParamNames = paramNames
			m.NativeVariadic = true
			m.AcceptsExtraNamed = true
		}
	}
}

// reflConstructor регистрирует конструктор Reflection-класса.
func reflConstructor(ci ClassInfo, fn func(ctx *Context, d *reflectionData, args []Slot) error) {
	ci.Constructor(&Function{Name: "__construct", NumArgs: -1, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[reflectionData]()
			if d == nil {
				return Slot{}, NewThrowable("Error", "Reflection object is not initialized")
			}
			if err := fn(ctx, d, args); err != nil {
				return Slot{}, err
			}
			return MakeNull(), nil
		},
	}})
}

// --- ReflectionClass ---

func registerReflectionClassMembers(ci ClassInfo) {
	defineReflConsts(ci)
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		if len(args) == 0 || deref(args[0]).typ == NullType {
			return NewThrowable("ReflectionException", ArgTypeError("ReflectionClass::__construct", 1, "objectOrClass", "object|string", "null", "", 0))
		}
		target, obj, err := ctx.vm.reflArgClass(ctx, args[0], "Class")
		if err != nil {
			return err
		}
		d.class = target
		d.this = obj
		if obj.typ == ObjectType {
			d.kind = rkObject
		} else {
			d.kind = rkClass
		}
		return nil
	})

	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.class.Name()), nil
	})
	reflMethodReg(ci, "getShortName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		n := d.class.Name()
		if i := strings.LastIndexByte(n, '\\'); i >= 0 {
			n = n[i+1:]
		}
		return MakeString(n), nil
	})
	reflMethodReg(ci, "getDocComment", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.class != nil {
			if doc := d.class.DocComment(); doc != "" {
				return MakeString(doc), nil
			}
		}
		return MakeBool(false), nil
	})
	reflMethodReg(ci, "getNamespaceName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		n := d.class.Name()
		if i := strings.LastIndexByte(n, '\\'); i >= 0 {
			return MakeString(n[:i]), nil
		}
		return MakeString(""), nil
	})
	reflMethodReg(ci, "isInternal", reflBool(func(d *reflectionData) bool { return reflIsInternal(d.class) }))
	reflMethodReg(ci, "isUserDefined", reflBool(func(d *reflectionData) bool { return !reflIsInternal(d.class) }))
	reflMethodReg(ci, "isInstantiable", reflBool(func(d *reflectionData) bool {
		c := asRC(d.class)
		if c == nil {
			return false
		}
		return !c.iface && !c.IsTrait() && !c.abstract && !c.isEnum
	}))
	reflMethodReg(ci, "isInterface", reflBool(func(d *reflectionData) bool { return d.class != nil && d.class.IsInterface() }))
	reflMethodReg(ci, "isTrait", reflBool(func(d *reflectionData) bool { return d.class != nil && d.class.IsTrait() }))
	reflMethodReg(ci, "isEnum", reflBool(func(d *reflectionData) bool { return d.class != nil && d.class.IsEnum() }))
	reflMethodReg(ci, "isAbstract", reflBool(func(d *reflectionData) bool { return d.class != nil && d.class.IsAbstract() }))
	reflMethodReg(ci, "isFinal", reflBool(func(d *reflectionData) bool { return IsFinalClass(d.class) }))
	reflMethodReg(ci, "isReadonly", reflBool(func(d *reflectionData) bool { return isReadonlyClass(d.class) }))
	reflMethodReg(ci, "isAnonymous", reflBool(func(d *reflectionData) bool {
		return d.class != nil && strings.HasPrefix(d.class.Name(), "\x00anon#")
	}))
	reflMethodReg(ci, "getParentClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		p := d.class.Parent()
		if p == nil {
			return MakeBool(false), nil
		}
		return ctx.vm.reflNewClass(p), nil
	})
	reflMethodReg(ci, "getInterfaceNames", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		names := d.class.InterfaceNames()
		return strListSlot(names), nil
	})
	reflMethodReg(ci, "getInterfaces", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		for _, n := range d.class.InterfaceNames() {
			iface := ctx.vm.FindClass(n)
			if iface == nil {
				continue
			}
			arr.SetStr(n, ctx.vm.reflNewClass(iface))
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "implementsInterface", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return Slot{}, NewThrowable("ArgumentCountError", "ReflectionClass::implementsInterface() expects exactly 1 argument, 0 given")
		}
		name := args[0].ToString()
		iface := ctx.vm.FindClass(name)
		if iface == nil {
			if _, err := ctx.vm.Autoload(ctx, name); err != nil {
				return Slot{}, err
			}
			iface = ctx.vm.FindClass(name)
		}
		if iface == nil {
			return Slot{}, NewThrowable("ReflectionException", "Interface \""+name+"\" does not exist")
		}
		if !iface.IsInterface() {
			return Slot{}, NewThrowable("ReflectionException", name+" is not an interface")
		}
		return MakeBool(d.class.IsA(iface)), nil
	})
	reflMethodReg(ci, "isSubclassOf", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return MakeBool(false), nil
		}
		name := args[0].ToString()
		target := ctx.vm.FindClass(name)
		if target == nil {
			return MakeBool(false), nil
		}
		if d.class == target {
			return MakeBool(false), nil
		}
		return MakeBool(d.class.IsA(target)), nil
	})
	reflMethodReg(ci, "isInstance", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 || deref(args[0]).typ != ObjectType {
			return Slot{}, NewThrowable("TypeError", ArgTypeError("ReflectionClass::isInstance", 1, "object", "object", "", "", 0))
		}
		oc := ctx.vm.classInfo(args[0])
		if oc == nil {
			return MakeBool(false), nil
		}
		return MakeBool(oc.IsA(d.class)), nil
	})
	reflMethodRegVar(ci, "newInstance", []string{"args"}, func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if named := ctx.vm.TakeExtraNamedParams(); named != nil {
			arr := newArray()
			for _, a := range args {
				arr.Append(a)
			}
			for k, v := range named.All() {
				_ = arr.Set(k, v)
			}
			return ctx.vm.reflInstantiateNamed(d.class, arr)
		}
		return ctx.vm.reflInstantiate(d.class, args, false)
	})
	reflMethodReg(ci, "newInstanceArgs", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		var cargs []Slot
		if len(args) > 0 {
			if a := deref(args[0]).AsArray(); a != nil {
				if a.HasStringKeys() {
					return ctx.vm.reflInstantiateNamed(d.class, a)
				}
				cargs = a.Values()
			}
		}
		return ctx.vm.reflInstantiate(d.class, cargs, false)
	})
	reflMethodReg(ci, "newInstanceWithoutConstructor", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		return ctx.vm.reflInstantiate(d.class, nil, true)
	})
	reflMethodReg(ci, "getConstructor", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		c := asRC(d.class)
		if c == nil {
			return MakeNull(), nil
		}
		ctor := c.lookupCtor()
		if ctor == nil {
			return MakeNull(), nil
		}
		var decl ClassInfo = d.class
		for k := c; k != nil; k = k.parent {
			if k.ctor == ctor {
				decl = k
				break
			}
		}
		return ctx.vm.reflNewMethod(d.class, "__construct", ctor, decl, Public), nil
	})
	reflMethodReg(ci, "hasMethod", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return MakeBool(false), nil
		}
		return MakeBool(d.class.HasMethod(args[0].ToString())), nil
	})
	reflMethodReg(ci, "getMethod", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return Slot{}, NewThrowable("ArgumentCountError", "ReflectionClass::getMethod() expects exactly 1 argument, 0 given")
		}
		name := args[0].ToString()
		fn, decl, vis, ok := findMethodOrSig(d.class, name)
		if !ok {
			return Slot{}, NewThrowable("ReflectionException", "Method "+d.class.Name()+"::"+name+"() does not exist")
		}
		return ctx.vm.reflNewMethod(d.class, name, fn, decl, vis), nil
	})
	reflMethodReg(ci, "getMethods", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		filter := int64(0)
		if len(args) > 0 {
			filter = deref(args[0]).ToInt()
		}
		arr := NewArray()
		for _, name := range d.class.AllMethodNames() {
			fn, decl, vis, ok := findMethodOrSig(d.class, name)
			if !ok {
				continue
			}
			if !reflVisMatch(filter, vis) {
				continue
			}
			arr.Append(ctx.vm.reflNewMethod(d.class, name, fn, decl, vis))
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "hasProperty", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return MakeBool(false), nil
		}
		return MakeBool(d.class.HasProperty(args[0].ToString())), nil
	})
	reflMethodReg(ci, "getProperty", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return Slot{}, NewThrowable("ArgumentCountError", "ReflectionClass::getProperty() expects exactly 1 argument, 0 given")
		}
		name := args[0].ToString()
		if i := strings.Index(name, "::"); i >= 0 {
			// "Class::prop": Zend автозагружает класс (в нижнем регистре) и
			// ищет свойство в нём.
			clsName := strings.ToLower(strings.TrimLeft(name[:i], "\\"))
			propName := name[i+2:]
			if ctx.vm.FindClass(clsName) == nil {
				if _, aerr := ctx.vm.Autoload(ctx, clsName); aerr != nil {
					return Slot{}, aerr
				}
			}
			c2 := ctx.vm.FindClass(clsName)
			if c2 == nil {
				return Slot{}, NewThrowable("ReflectionException", "Class \""+clsName+"\" does not exist")
			}
			if decl2, vis2, ok2 := findProperty(c2, propName); ok2 {
				return ctx.vm.reflNewProperty(c2, propName, decl2, vis2), nil
			}
			return Slot{}, NewThrowable("ReflectionException", "Property "+clsName+"::$"+propName+" does not exist")
		}
		decl, vis, ok := findProperty(d.class, name)
		if !ok {
			return Slot{}, NewThrowable("ReflectionException", "Property "+d.class.Name()+"::$"+name+" does not exist")
		}
		return ctx.vm.reflNewProperty(d.class, name, decl, vis), nil
	})
	reflMethodReg(ci, "getProperties", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		filter := int64(0)
		if len(args) > 0 {
			filter = deref(args[0]).ToInt()
		}
		arr := NewArray()
		for _, name := range allPropertyNames(d.class) {
			decl, vis, ok := findProperty(d.class, name)
			if !ok {
				continue
			}
			static := isStaticProperty(d.class, name)
			if static && !reflStaticMatch(filter) {
				continue
			}
			if !static && (filter&16) != 0 {
				continue
			}
			if !reflVisMatch(filter, vis) {
				continue
			}
			arr.Append(ctx.vm.reflNewProperty(d.class, name, decl, vis))
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "hasConstant", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return MakeBool(false), nil
		}
		return MakeBool(d.class.HasConst(args[0].ToString())), nil
	})
	reflMethodReg(ci, "getConstant", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return MakeBool(false), nil
		}
		v, ok := d.class.ConstValue(args[0].ToString())
		if !ok {
			return MakeBool(false), nil
		}
		return valueSlot(v), nil
	})
	reflMethodReg(ci, "getConstants", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		for _, name := range allConstNames(d.class) {
			v, ok := d.class.ConstValue(name)
			if !ok {
				continue
			}
			arr.SetStr(name, valueSlot(v))
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "getReflectionConstant", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return Slot{}, NewThrowable("ArgumentCountError", "ReflectionClass::getReflectionConstant() expects exactly 1 argument, 0 given")
		}
		name := args[0].ToString()
		if _, ok := d.class.ConstValue(name); !ok {
			return MakeBool(false), nil
		}
		decl, vis := constDeclVis(d.class, name)
		return ctx.vm.reflNewClassConst(d.class, name, decl, vis), nil
	})
	reflMethodReg(ci, "getReflectionConstants", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		for _, name := range allConstNames(d.class) {
			decl, vis := constDeclVis(d.class, name)
			arr.Append(ctx.vm.reflNewClassConst(d.class, name, decl, vis))
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "getFileName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		c := asRC(d.class)
		if c == nil || c.defFile == "" {
			return MakeBool(false), nil
		}
		return MakeString(c.defFile), nil
	})
	reflMethodReg(ci, "getStartLine", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		c := asRC(d.class)
		if c == nil || c.defStart == 0 {
			return MakeBool(false), nil
		}
		return MakeInt(int64(c.defStart)), nil
	})
	reflMethodReg(ci, "getEndLine", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		c := asRC(d.class)
		if c == nil || c.defEnd == 0 {
			return MakeBool(false), nil
		}
		return MakeInt(int64(c.defEnd)), nil
	})
	reflMethodReg(ci, "getModifiers", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		var m int64
		if IsFinalClass(d.class) {
			m |= 32
		}
		if d.class.IsAbstract() {
			m |= 64
		}
		return MakeInt(m), nil
	})
	// getTraitAliases: таблица псевдонимов трейтов (у нас не отслеживается —
	// как Zend при отсутствии `as`-правил, отдаём пустой массив).
	reflMethodReg(ci, "getTraitAliases", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeArray(NewArray()), nil
	})
	reflMethodReg(ci, "getDefaultProperties", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		c := asRC(d.class)
		// PHP возвращает статические свойства перед обычными.
		for k := c; k != nil; k = k.parent {
			for _, name := range k.staticOrder {
				s := k.lookupStatic(name)
				if s == nil {
					continue
				}
				if k.staticIsUninit(name) {
					continue
				}
				arr.SetStr(name, *s)
			}
		}
		for k := c; k != nil; k = k.parent {
			for _, name := range k.propOrder {
				if _, seen := arr.Get(MakeStrKey(name)); seen {
					continue
				}
				if v, ok := k.defaults[name]; ok {
					arr.SetStr(name, valueSlot(v))
				} else if th, ok := k.lazyDefaults[name]; ok {
					if v, err := k.resolveThunk(th); err == nil {
						arr.SetStr(name, valueSlot(v))
					}
				}
			}
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "getTraitNames", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return strListSlot(d.class.TraitNames()), nil
	})
	reflMethodReg(ci, "getTraits", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		for _, n := range d.class.TraitNames() {
			t := ctx.vm.FindClass(n)
			if t != nil {
				arr.SetStr(n, ctx.vm.reflNewClass(t))
			}
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo { return classAttrs(d.class) }))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString("Class [ <user> class " + d.class.Name() + " ] {\n}\n"), nil
	})
}

func reflBool(f func(*reflectionData) bool) func(*Context, *reflectionData, []Slot) (Slot, error) {
	return func(_ *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeBool(f(d)), nil
	}
}

// defineReflConsts объявляет константы ReflectionClass/ReflectionMethod/...
func defineReflConsts(ci ClassInfo) {
	for _, kv := range []struct {
		n string
		v int64
	}{
		{"IS_IMPLICIT_ABSTRACT", 16}, {"IS_EXPLICIT_ABSTRACT", 64}, {"IS_FINAL", 32}, {"IS_READONLY", 65536},
	} {
		ci.DefineConst(kv.n, MakeInt(kv.v))
	}
}

func defineMemberConsts(ci ClassInfo) {
	for _, kv := range []struct {
		n string
		v int64
	}{
		{"IS_STATIC", 16}, {"IS_PUBLIC", 1}, {"IS_PROTECTED", 2}, {"IS_PRIVATE", 4},
		{"IS_ABSTRACT", 64}, {"IS_FINAL", 32}, {"IS_READONLY", 128},
	} {
		ci.DefineConst(kv.n, MakeInt(kv.v))
	}
}

// --- ReflectionEnum ---

func registerReflectionEnumMembers(ci ClassInfo) {
	reflMethodReg(ci, "isBacked", reflBool(func(d *reflectionData) bool { return d.class != nil && d.class.EnumBacking() != "" }))
	reflMethodReg(ci, "getBackingType", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		b := d.class.EnumBacking()
		if b == "" {
			return MakeNull(), nil
		}
		return ctx.vm.reflNewNamedType(b), nil
	})
	reflMethodReg(ci, "getCases", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return ctx.vm.reflEnumCases(d.class), nil
	})
	reflMethodReg(ci, "hasCase", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if len(args) == 0 {
			return MakeBool(false), nil
		}
		c := asRC(d.class)
		if c == nil {
			return MakeBool(false), nil
		}
		_, ok := c.enumByName[args[0].ToString()]
		return MakeBool(ok), nil
	})
	reflMethodReg(ci, "getCase", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		c := asRC(d.class)
		if c == nil || len(args) == 0 {
			return Slot{}, NewThrowable("ReflectionException", "Case does not exist")
		}
		name := args[0].ToString()
		if _, ok := c.enumByName[name]; !ok {
			return Slot{}, NewThrowable("ReflectionException", "Case "+d.class.Name()+"::"+name+" does not exist")
		}
		return ctx.vm.reflEnumCase(d.class, name), nil
	})
}

func (vm *VM) reflEnumCases(ci ClassInfo) Slot {
	c := asRC(ci)
	arr := NewArray()
	if c == nil {
		return MakeArray(arr)
	}
	for _, s := range c.EnumCases() {
		name := ""
		if o := vm.objectOf(s); o != nil {
			if v, ok := o.GetProp("name"); ok {
				name = v.ToString()
			}
		}
		arr.Append(vm.reflEnumCase(ci, name))
	}
	return MakeArray(arr)
}

func (vm *VM) reflEnumCase(ci ClassInfo, name string) Slot {
	className := "ReflectionEnumUnitCase"
	if ci.EnumBacking() != "" {
		className = "ReflectionEnumBackedCase"
	}
	d, s := vm.reflObj(className)
	if d == nil {
		return MakeNull()
	}
	d.kind = rkEnumCase
	d.class = ci
	d.name = name
	if c := asRC(ci); c != nil {
		d.caseSlot = c.enumByName[name]
	}
	return s
}

// --- ReflectionMethod ---

func registerReflectionMethodMembers(ci ClassInfo) {
	defineMemberConsts(ci)
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		var classArg Slot
		var methodName string
		if len(args) == 1 {
			raw := strings.TrimLeft(args[0].ToString(), "\\")
			i := strings.Index(raw, "::")
			if i < 0 {
				return NewThrowable("ArgumentCountError", "ReflectionMethod::__construct() expects exactly 2 arguments")
			}
			classArg = MakeString(raw[:i])
			methodName = raw[i+2:]
		} else if len(args) >= 2 {
			classArg = args[0]
			methodName = args[1].ToString()
		} else {
			return NewThrowable("ArgumentCountError", "ReflectionMethod::__construct() expects exactly 2 arguments")
		}
		target, _, err := ctx.vm.reflArgClass(ctx, classArg, "Class")
		if err != nil {
			return err
		}
		name := methodName
		fn, decl, vis, ok := findMethodOrSig(target, name)
		if !ok {
			return NewThrowable("ReflectionException", "Method "+target.Name()+"::"+name+"() does not exist")
		}
		d.kind = rkMethod
		d.class = target
		d.name = name
		d.fn = fn
		d.decl = decl
		d.vis = vis
		return nil
	})
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.name), nil
	})
	reflMethodReg(ci, "getDeclaringClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		decl := d.decl
		if decl == nil {
			decl = d.class
		}
		return ctx.vm.reflNewClass(decl), nil
	})
	reflMethodReg(ci, "isInternal", reflBool(func(d *reflectionData) bool { return reflIsInternal(d.decl) }))
	reflMethodReg(ci, "isUserDefined", reflBool(func(d *reflectionData) bool { return !reflIsInternal(d.decl) }))
	reflMethodReg(ci, "isStatic", reflBool(func(d *reflectionData) bool { return d.fn != nil && d.fn.IsStatic }))
	reflMethodReg(ci, "isPublic", reflBool(func(d *reflectionData) bool { return d.vis == Public }))
	reflMethodReg(ci, "isProtected", reflBool(func(d *reflectionData) bool { return d.vis == Protected }))
	reflMethodReg(ci, "isPrivate", reflBool(func(d *reflectionData) bool { return d.vis == Private }))
	reflMethodReg(ci, "isAbstract", reflBool(func(d *reflectionData) bool {
		c := asRC(d.class)
		if c == nil {
			return false
		}
		if d.fn != nil {
			return false
		}
		return c.hasAbstract(d.name)
	}))
	reflMethodReg(ci, "isFinal", reflBool(func(d *reflectionData) bool {
		if d.fn != nil && d.fn.IsFinal {
			return true
		}
		c := asRC(d.class)
		return c != nil && c.methodFinal(d.name)
	}))
	reflMethodReg(ci, "isConstructor", reflBool(func(d *reflectionData) bool { return strings.EqualFold(d.name, "__construct") }))
	reflMethodReg(ci, "isDestructor", reflBool(func(d *reflectionData) bool { return strings.EqualFold(d.name, "__destruct") }))
	reflMethodReg(ci, "getModifiers", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		var m int64
		switch d.vis {
		case Public:
			m |= 1
		case Protected:
			m |= 2
		case Private:
			m |= 4
		}
		if d.fn != nil && d.fn.IsStatic {
			m |= 16
		}
		if c := asRC(d.class); c != nil && d.fn == nil && c.hasAbstract(d.name) {
			m |= 64
		}
		if d.fn != nil && d.fn.IsFinal {
			m |= 32
		}
		return MakeInt(m), nil
	})
	reflMethodReg(ci, "getNumberOfParameters", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeInt(int64(paramCount(d.fn))), nil
	})
	reflMethodReg(ci, "getNumberOfRequiredParameters", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil {
			return MakeInt(0), nil
		}
		return MakeInt(int64(d.fn.NumRequired)), nil
	})
	registerFunctionAbstractMembers(ci, true)
	reflMethodRegVar(ci, "invoke", []string{"object", "args"}, func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		return ctx.vm.reflInvokeMethod(d, args, false)
	})
	reflMethodReg(ci, "invokeArgs", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		return ctx.vm.reflInvokeMethod(d, args, true)
	})
	reflMethodReg(ci, "setAccessible", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		return MakeNull(), nil
	})
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(reflMethodString(ctx.vm, d)), nil
	})
}

func (vm *VM) reflInvokeMethod(d *reflectionData, args []Slot, arrayArgs bool) (Slot, error) {
	if d.fn == nil {
		return Slot{}, NewThrowable("Error", "Cannot invoke abstract method "+d.class.Name()+"::"+d.name+"()")
	}
	named := vm.TakeExtraNamedParams()
	var cargs []Slot
	recv := Slot{}
	if !arrayArgs {
		if len(args) > 0 {
			recv = args[0]
			cargs = args[1:]
		}
	} else {
		if len(args) > 0 {
			recv = args[0]
		}
		if len(args) > 1 {
			if a := deref(args[1]).AsArray(); a != nil {
				if a.HasStringKeys() {
					return vm.InvokeMethodNamed(d.fn, recv, d.class, a)
				}
				cargs = a.Values()
			}
		}
	}
	if named != nil {
		arr := newArray()
		for _, a := range cargs {
			arr.Append(a)
		}
		for k, v := range named.All() {
			_ = arr.Set(k, v)
		}
		return vm.InvokeMethodNamed(d.fn, recv, d.class, arr)
	}
	if d.fn.IsStatic || recv.typ != ObjectType {
		return vm.invokeMethod(d.fn, Slot{}, d.class, cargs)
	}
	return vm.invokeMethod(d.fn, recv, d.class, cargs)
}

// --- ReflectionFunction / ReflectionFunctionAbstract ---

func registerReflectionFunctionMembers(ci ClassInfo) {
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		if len(args) == 0 {
			return NewThrowable("ArgumentCountError", "ReflectionFunction::__construct() expects exactly 1 argument, 0 given")
		}
		a := deref(args[0])
		switch a.typ {
		case ClosureType:
			cl := a.asClosure()
			if cl == nil || cl.Fn == nil {
				return NewThrowable("ReflectionException", "Closure is not valid")
			}
			d.kind = rkFunction
			d.fn = cl.Fn
			d.name = "{closure}"
			d.isFunc = true
			d.closure = true
			d.closureSlot = a
			return nil
		default:
			name := strings.TrimLeft(a.ToString(), "\\")
			if i := strings.Index(name, "::"); i >= 0 {
				clsName := name[:i]
				mname := name[i+2:]
				cls := ctx.vm.FindClass(clsName)
				if cls == nil {
					return NewThrowable("ReflectionException", "Class \""+clsName+"\" does not exist")
				}
				fn, decl, vis, ok := findMethodOrSig(cls, mname)
				if !ok {
					return NewThrowable("ReflectionException", "Method "+clsName+"::"+mname+"() does not exist")
				}
				d.kind = rkMethod
				d.class = cls
				d.name = mname
				d.fn = fn
				d.decl = decl
				d.vis = vis
				return nil
			}
			fn := ctx.vm.functionByName(name)
			if fn == nil {
				return NewThrowable("ReflectionException", "Function "+name+"() does not exist")
			}
			d.kind = rkFunction
			d.name = name
			d.fn = fn
			d.isFunc = true
			return nil
		}
	})
	registerFunctionAbstractMembers(ci, false)
	reflMethodRegVar(ci, "invoke", []string{"args"}, func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if d.fn == nil {
			return Slot{}, NewThrowable("Error", "Cannot invoke function")
		}
		if named := ctx.vm.TakeExtraNamedParams(); named != nil {
			arr := newArray()
			for _, a := range args {
				arr.Append(a)
			}
			for k, v := range named.All() {
				_ = arr.Set(k, v)
			}
			if d.kind == rkMethod {
				return ctx.vm.reflInvokeMethod(d, []Slot{ctx.vm.ctx.this, MakeArray(arr)}, true)
			}
			return ctx.vm.InvokeNamed(d.fn, arr)
		}
		if d.kind == rkMethod {
			return ctx.vm.reflInvokeMethod(d, args, false)
		}
		return ctx.vm.invoke(d.fn, args)
	})
	reflMethodReg(ci, "invokeArgs", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if d.fn == nil {
			return Slot{}, NewThrowable("Error", "Cannot invoke function")
		}
		var cargs []Slot
		if d.kind == rkMethod {
			return ctx.vm.reflInvokeMethod(d, args, true)
		}
		if len(args) > 0 {
			if a := deref(args[0]).AsArray(); a != nil && a.HasStringKeys() {
				return ctx.vm.InvokeNamed(d.fn, a)
			}
		}
		if len(args) > 0 {
			if a := deref(args[0]).AsArray(); a != nil {
				cargs = a.Values()
			}
		}
		return ctx.vm.invoke(d.fn, cargs)
	})
	reflMethodReg(ci, "__invoke", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if d.fn == nil {
			return Slot{}, NewThrowable("Error", "Cannot invoke function")
		}
		if d.kind == rkMethod {
			return ctx.vm.reflInvokeMethod(d, args, false)
		}
		return ctx.vm.invoke(d.fn, args)
	})
	reflMethodReg(ci, "isClosure", reflBool(func(d *reflectionData) bool { return d.closure }))
	reflMethodReg(ci, "isStatic", reflBool(func(d *reflectionData) bool { return d.fn != nil && d.fn.IsStatic }))
	reflMethodReg(ci, "isGenerator", reflBool(func(d *reflectionData) bool {
		if d.fn == nil {
			return false
		}
		if cf, ok := d.fn.Executable.(*CompiledFunction); ok {
			return cf.IsGenerator()
		}
		return false
	}))
	reflMethodReg(ci, "getStaticVariables", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeArray(NewArray()), nil
	})
	reflMethodReg(ci, "getClosure", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil {
			return MakeNull(), nil
		}
		return Slot{}, NewThrowable("Error", "Cannot create closure for this function")
	})
	reflMethodReg(ci, "getClosureScopeClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeNull(), nil
	})
	reflMethodReg(ci, "getClosureThis", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeNull(), nil
	})
	reflMethodReg(ci, "getClosureCalledClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeNull(), nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo {
		if d.kind == rkMethod {
			if d.fn != nil && len(d.fn.Attrs) > 0 {
				return d.fn.Attrs
			}
			return methodAttrs(d.class, d.name)
		}
		if d.fn != nil {
			return d.fn.Attrs
		}
		return nil
	}))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(reflFnString(ctx.vm, d)), nil
	})
}

// registerFunctionAbstractMembers — общие методы ReflectionFunction/ReflectionMethod.
func registerFunctionAbstractMembers(ci ClassInfo, isMethod bool) {
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.name), nil
	})
	reflMethodReg(ci, "isUserDefined", reflBool(func(d *reflectionData) bool {
		if d.fn == nil {
			return true
		}
		if _, ok := d.fn.Executable.(*CompiledFunction); ok {
			return true
		}
		return false
	}))
	reflMethodReg(ci, "isInternal", reflBool(func(d *reflectionData) bool {
		if d.fn == nil {
			return false
		}
		if _, ok := d.fn.Executable.(*NativeFunction); ok {
			return true
		}
		return false
	}))
	reflMethodReg(ci, "getFileName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil || d.fn.DefFile == "" {
			return MakeBool(false), nil
		}
		return MakeString(d.fn.DefFile), nil
	})
	reflMethodReg(ci, "getStartLine", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil || d.fn.DefLine == 0 {
			return MakeBool(false), nil
		}
		return MakeInt(int64(d.fn.DefLine)), nil
	})
	reflMethodReg(ci, "getEndLine", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil || d.fn.DefLine == 0 {
			return MakeBool(false), nil
		}
		return MakeInt(int64(d.fn.DefLine)), nil
	})
	reflMethodReg(ci, "hasReturnType", reflBool(func(d *reflectionData) bool { return d.fn != nil && TypeString(d.fn.RetType) != "" }))
	reflMethodReg(ci, "getReturnType", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil {
			return MakeNull(), nil
		}
		return ctx.vm.reflTypeSlot(d.fn.RetType), nil
	})
	reflMethodReg(ci, "getParameters", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		if d.fn == nil {
			return MakeArray(arr), nil
		}
		owner := ctx.vm.reflOwnerSlot(d)
		for i := 0; i < paramCount(d.fn); i++ {
			arr.Append(ctx.vm.reflNewParameter(owner, i))
		}
		return MakeArray(arr), nil
	})
	// getDocComment: doc-блоки не сохраняются — как Zend для методов без
	// комментария, отдаём false.
	reflMethodReg(ci, "getDocComment", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeBool(false), nil
	})
	reflMethodReg(ci, "getNumberOfParameters", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeInt(int64(paramCount(d.fn))), nil
	})
	reflMethodReg(ci, "getNumberOfRequiredParameters", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.fn == nil {
			return MakeInt(0), nil
		}
		return MakeInt(int64(d.fn.NumRequired)), nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo {
		if d.kind == rkMethod {
			if d.fn != nil && len(d.fn.Attrs) > 0 {
				return d.fn.Attrs
			}
			return methodAttrs(d.class, d.name)
		}
		if d.fn != nil {
			return d.fn.Attrs
		}
		return nil
	}))
}

// reflOwnerSlot возвращает слот ReflectionMethod/ReflectionFunction, соответствующий d
// (для ReflectionParameter, которому нужен владелец).
func (vm *VM) reflOwnerSlot(d *reflectionData) Slot {
	if d.kind == rkMethod {
		return vm.reflNewMethod(d.class, d.name, d.fn, d.decl, d.vis)
	}
	s := vm.reflNewFunction(d.name, d.fn)
	// Замыкание: переносим текущую область (bindTo), чтобы атрибуты
	// параметров вычислялись в актуальном scope.
	if d.closure && deref(s).ptr != nil {
		if od := (*reflectionData)(unsafe.Pointer(deref(s).ptr)); od != nil {
			od.closure = true
			od.closureSlot = d.closureSlot
		}
	}
	return s
}

// --- ReflectionProperty ---

func registerReflectionPropertyMembers(ci ClassInfo) {
	defineMemberConsts(ci)
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		if len(args) < 2 {
			return NewThrowable("ArgumentCountError", "ReflectionProperty::__construct() expects exactly 2 arguments")
		}
		target, _, err := ctx.vm.reflArgClass(ctx, args[0], "Class")
		if err != nil {
			return err
		}
		name := args[1].ToString()
		decl, vis, ok := findProperty(target, name)
		if !ok {
			return NewThrowable("ReflectionException", "Property "+target.Name()+"::$"+name+" does not exist")
		}
		d.kind = rkProperty
		d.class = target
		d.name = name
		d.decl = decl
		d.vis = vis
		return nil
	})
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.name), nil
	})
	reflMethodReg(ci, "getDeclaringClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		decl := d.decl
		if decl == nil {
			decl = d.class
		}
		return ctx.vm.reflNewClass(decl), nil
	})
	reflMethodReg(ci, "isStatic", reflBool(func(d *reflectionData) bool { return isStaticProperty(d.class, d.name) }))
	reflMethodReg(ci, "isPublic", reflBool(func(d *reflectionData) bool { return d.vis == Public }))
	reflMethodReg(ci, "isProtected", reflBool(func(d *reflectionData) bool { return d.vis == Protected }))
	reflMethodReg(ci, "isPrivate", reflBool(func(d *reflectionData) bool { return d.vis == Private }))
	reflMethodReg(ci, "isReadOnly", reflBool(reflPropReadonly))
	reflMethodReg(ci, "isReadonly", reflBool(reflPropReadonly))
	reflMethodReg(ci, "isPromoted", reflBool(func(d *reflectionData) bool { return false }))
	reflMethodReg(ci, "hasType", reflBool(func(d *reflectionData) bool {
		if pd, _ := propMeta(d.class, d.name); pd != nil {
			return pd.hasType
		}
		return false
	}))
	reflMethodReg(ci, "getType", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		pd, _ := propMeta(d.class, d.name)
		if pd == nil || !pd.hasType {
			return MakeNull(), nil
		}
		return ctx.vm.reflTypeSlot(pd.typ), nil
	})
	reflMethodReg(ci, "getModifiers", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		var m int64
		switch d.vis {
		case Public:
			m |= 1
		case Protected:
			m |= 2
		case Private:
			m |= 4
		}
		if isStaticProperty(d.class, d.name) {
			m |= 16
		}
		if pd, _ := propMeta(d.class, d.name); pd != nil && pd.readonly {
			m |= 128
		}
		return MakeInt(m), nil
	})
	reflMethodReg(ci, "hasDefaultValue", reflBool(func(d *reflectionData) bool {
		if isStaticProperty(d.class, d.name) {
			c := asRC(d.class)
			return c != nil && c.lookupStatic(d.name) != nil
		}
		_, ok := d.class.DefaultValue(d.name)
		return ok
	}))
	reflMethodReg(ci, "getDefaultValue", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if isStaticProperty(d.class, d.name) {
			if v, ok := d.class.StaticValue(d.name); ok {
				return valueSlot(v), nil
			}
			return MakeNull(), nil
		}
		if v, ok := d.class.DefaultValue(d.name); ok {
			return valueSlot(v), nil
		}
		return MakeNull(), nil
	})
	reflMethodReg(ci, "getValue", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if isStaticProperty(d.class, d.name) {
			if v, ok := d.class.StaticValue(d.name); ok {
				return valueSlot(v), nil
			}
			return MakeNull(), nil
		}
		if len(args) == 0 || deref(args[0]).typ != ObjectType {
			return Slot{}, NewThrowable("TypeError", ArgTypeError("ReflectionProperty::getValue", 1, "object", "object", "", "", 0))
		}
		obj := ctx.vm.objectOf(args[0])
		if obj == nil {
			return MakeNull(), nil
		}
		if d.name == "value" && sensitiveValueClass(d.class) {
			if sd := (*sensitiveValueData)(unsafe.Pointer(obj)); sd != nil {
				return sd.value, nil
			}
		}
		if v, ok := obj.GetProp(d.name); ok {
			return valueSlot(v), nil
		}
		return MakeNull(), nil
	})
	reflMethodReg(ci, "setValue", func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		if isStaticProperty(d.class, d.name) {
			c := asRC(d.class)
			if c != nil && len(args) > 0 {
				c.setStaticSlot(d.name, args[len(args)-1])
			}
			return MakeNull(), nil
		}
		if len(args) < 2 || deref(args[0]).typ != ObjectType {
			return Slot{}, NewThrowable("TypeError", ArgTypeError("ReflectionProperty::setValue", 1, "object", "object", "", "", 0))
		}
		obj := ctx.vm.objectOf(args[0])
		if obj != nil {
			obj.SetProp(d.name, args[1])
		}
		return MakeNull(), nil
	})
	reflMethodReg(ci, "setAccessible", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeNull(), nil })
	// Zend показывает у ReflectionProperty виртуальные свойства name/class
	// (var_dump/print_r), в отличие от прочих Reflection-объектов.
	reflMethodReg(ci, "__debugInfo", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		_ = arr.SetStr("name", MakeString(d.name))
		cls := ""
		if d.decl != nil {
			cls = d.decl.Name()
		} else if d.class != nil {
			cls = d.class.Name()
		}
		_ = arr.SetStr("class", MakeString(cls))
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo { return propAttrs(d.class, d.name) }))
	reflMethodReg(ci, "getDocComment", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeBool(false), nil
	})
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString("Property [ " + visWord(d.vis) + " $" + d.name + " ]\n"), nil
	})
}

func reflPropReadonly(d *reflectionData) bool {
	if pd, _ := propMeta(d.class, d.name); pd != nil {
		return pd.readonly
	}
	return isReadonlyClass(d.class)
}

// setStaticSlot присваивает значение статическому свойству (для ReflectionProperty).
func (c *runtimeClass) setStaticSlot(name string, v Slot) {
	if s := c.lookupStatic(name); s != nil {
		*s = v
		c.clearStaticUninit(name)
	}
}

// --- ReflectionParameter ---

func registerReflectionParameterMembers(ci ClassInfo) {
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		if len(args) < 2 {
			return NewThrowable("ArgumentCountError", "ReflectionParameter::__construct() expects exactly 2 arguments")
		}
		var owner Slot
		first := deref(args[0])
		switch first.typ {
		case ObjectType:
			if first.IsClosure() {
				cl := first.asClosure()
				if cl == nil || cl.Fn == nil {
					return NewThrowable("ReflectionException", "The parameter does not exist")
				}
				owner = ctx.vm.reflNewFunction("{closure}", cl.Fn)
			} else {
				ci := ctx.vm.classInfo(first)
				if ci == nil {
					return NewThrowable("ReflectionException", "The parameter does not exist")
				}
				fn, decl, vis, ok := findMethodOrSig(ci, "__invoke")
				if !ok {
					return NewThrowable("ReflectionException", "Method "+ci.Name()+"::__invoke() does not exist")
				}
				owner = ctx.vm.reflNewMethod(ci, "__invoke", fn, decl, vis)
			}
		case ArrayType:
			arr := first.AsArray()
			if arr.Len() != 2 {
				return NewThrowable("ReflectionException", "The parameter does not exist")
			}
			clsSlot, _ := arr.Get(MakeIntKey(0))
			mname, _ := arr.Get(MakeIntKey(1))
			var tci ClassInfo
			a0 := deref(clsSlot)
			if a0.typ == ObjectType {
				tci = ctx.vm.classInfo(a0)
			} else {
				var err error
				tci, _, err = ctx.vm.reflArgClass(ctx, clsSlot, "Class")
				if err != nil {
					return err
				}
			}
			if tci == nil {
				return NewThrowable("ReflectionException", "The parameter does not exist")
			}
			mn := mname.ToString()
			fn, decl, vis, ok := findMethodOrSig(tci, mn)
			if !ok {
				return NewThrowable("ReflectionException", "Method "+tci.Name()+"::"+mn+"() does not exist")
			}
			owner = ctx.vm.reflNewMethod(tci, mn, fn, decl, vis)
		case StringType:
			raw := first.StringVal()
			if i := strings.Index(raw, "::"); i >= 0 {
				tci := ctx.vm.FindClass(raw[:i])
				if tci == nil {
					if _, err := ctx.vm.Autoload(ctx, raw[:i]); err != nil {
						return err
					}
					tci = ctx.vm.FindClass(raw[:i])
				}
				if tci == nil {
					return NewThrowable("ReflectionException", "Class \""+raw[:i]+"\" does not exist")
				}
				mn := raw[i+2:]
				fn, decl, vis, ok := findMethodOrSig(tci, mn)
				if !ok {
					return NewThrowable("ReflectionException", "Method "+tci.Name()+"::"+mn+"() does not exist")
				}
				owner = ctx.vm.reflNewMethod(tci, mn, fn, decl, vis)
			} else {
				fn := ctx.vm.functionByName(raw)
				if fn == nil {
					return NewThrowable("ReflectionException", "Function "+raw+"() does not exist")
				}
				owner = ctx.vm.reflNewFunction(raw, fn)
			}
		default:
			return NewThrowable("ReflectionException", "The parameter does not exist")
		}
		od := (*reflectionData)(unsafe.Pointer(deref(owner).ptr))
		if od == nil || od.fn == nil {
			return NewThrowable("ReflectionException", "The parameter does not exist")
		}
		pname := deref(args[1])
		idx := -1
		if pname.typ == IntType {
			idx = int(pname.ToInt())
		} else {
			nm := pname.ToString()
			for i, pn := range od.fn.ParamNames {
				if pn == nm {
					idx = i
					break
				}
			}
		}
		if idx < 0 || idx >= paramCount(od.fn) {
			return NewThrowable("ReflectionException", "The parameter does not exist")
		}
		d.this = owner
		d.index = idx
		return nil
	})
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		fn := paramOwnerFn(d)
		if fn == nil || d.index >= len(fn.ParamNames) {
			return MakeString("param" + itoa(d.index+1)), nil
		}
		return MakeString(fn.ParamNames[d.index]), nil
	})
	reflMethodReg(ci, "getPosition", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeInt(int64(d.index)), nil
	})
	reflMethodReg(ci, "isOptional", reflBool(func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		return fn == nil || d.index >= fn.NumRequired
	}))
	reflMethodReg(ci, "isDefaultValueAvailable", reflBool(func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		return fn != nil && d.index >= fn.NumRequired
	}))
	reflMethodReg(ci, "getDefaultValue", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeNull(), nil
	})
	reflMethodReg(ci, "isDefaultValueConstant", reflBool(func(d *reflectionData) bool { return false }))
	reflMethodReg(ci, "getDefaultValueConstantName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeNull(), nil })
	reflMethodReg(ci, "isVariadic", reflBool(func(d *reflectionData) bool { return false }))
	reflMethodReg(ci, "isPassedByReference", reflBool(func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		return fn != nil && d.index < len(fn.ByRefArgs) && fn.ByRefArgs[d.index]
	}))
	reflMethodReg(ci, "canBePassedByValue", reflBool(func(d *reflectionData) bool { return true }))
	reflMethodReg(ci, "hasType", reflBool(func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		return fn != nil && d.index < len(fn.ParamTypes) && TypeString(fn.ParamTypes[d.index]) != ""
	}))
	reflMethodReg(ci, "getType", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		fn := paramOwnerFn(d)
		if fn == nil || d.index >= len(fn.ParamTypes) {
			return MakeNull(), nil
		}
		return ctx.vm.reflTypeSlot(fn.ParamTypes[d.index]), nil
	})
	reflMethodReg(ci, "allowsNull", reflBool(func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		if fn == nil || d.index >= len(fn.ParamTypes) {
			return true
		}
		return typeAllowsNullStr(fn.ParamTypes[d.index])
	}))
	reflMethodReg(ci, "isCallable", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		ctx.vm.Deprecated("Method ReflectionParameter::isCallable() is deprecated")
		fn := paramOwnerFn(d)
		if fn == nil || d.index >= len(fn.ParamTypes) {
			return MakeBool(false), nil
		}
		return MakeBool(strings.EqualFold(fn.ParamTypes[d.index], "callable")), nil
	})
	reflMethodReg(ci, "isArray", reflBool(reflParamIs("array")))
	reflMethodReg(ci, "isNull", reflBool(func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		return fn != nil && d.index < len(fn.ParamTypes) && strings.EqualFold(fn.ParamTypes[d.index], "null")
	}))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		fn := paramOwnerFn(d)
		nm := "param" + itoa(d.index+1)
		if fn != nil && d.index < len(fn.ParamNames) {
			nm = fn.ParamNames[d.index]
		}
		return MakeString("Parameter #" + itoa(d.index) + " [ <required> $" + nm + " ]\n"), nil
	})
	reflMethodReg(ci, "getDeclaringFunction", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return d.this, nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo {
		if fn := paramOwnerFn(d); fn != nil && d.index < len(fn.ParamAttrs) && len(fn.ParamAttrs[d.index]) > 0 {
			return fn.ParamAttrs[d.index]
		}
		od := ownerReflData(d)
		if od == nil {
			return nil
		}
		return paramAttrsOf(od.class, od.name, d.index)
	}))
}

// ownerReflData возвращает reflectionData владельца параметра.
func ownerReflData(d *reflectionData) *reflectionData {
	owner := deref(d.this)
	if owner.typ != ObjectType {
		return nil
	}
	return (*reflectionData)(unsafe.Pointer(deref(owner).ptr))
}

// paramAttrsOf ищет атрибуты параметра idx метода name с учётом наследования.
func paramAttrsOf(ci ClassInfo, name string, idx int) []AttributeInfo {
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil || rc.paramAttrs == nil {
			continue
		}
		if m, ok := rc.paramAttrs[name]; ok {
			if a, ok := m[idx]; ok {
				return a
			}
		}
	}
	return nil
}

func reflParamIs(t string) func(*reflectionData) bool {
	return func(d *reflectionData) bool {
		fn := paramOwnerFn(d)
		return fn != nil && d.index < len(fn.ParamTypes) && strings.EqualFold(fn.ParamTypes[d.index], t)
	}
}

// paramOwnerFn возвращает *Function параметра по владельцу (ReflectionMethod/Function).
func paramOwnerFn(d *reflectionData) *Function {
	owner := deref(d.this)
	if owner.typ != ObjectType {
		return nil
	}
	od := (*reflectionData)(unsafe.Pointer(deref(owner).ptr))
	if od == nil {
		return nil
	}
	return od.fn
}

// --- ReflectionClassConstant ---

func registerReflectionClassConstantMembers(ci ClassInfo) {
	defineMemberConsts(ci)
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		if len(args) < 2 {
			return NewThrowable("ArgumentCountError", "ReflectionClassConstant::__construct() expects exactly 2 arguments")
		}
		target, _, err := ctx.vm.reflArgClass(ctx, args[0], "Class")
		if err != nil {
			return err
		}
		name := args[1].ToString()
		if _, ok := target.ConstValue(name); !ok {
			return NewThrowable("ReflectionException", "Constant "+target.Name()+"::"+name+" does not exist")
		}
		decl, vis := constDeclVis(target, name)
		d.kind = rkClassConst
		d.class = target
		d.name = name
		d.decl = decl
		d.vis = vis
		return nil
	})
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.name), nil
	})
	reflMethodReg(ci, "getValue", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		v, ok := d.class.ConstValue(d.name)
		if !ok {
			return MakeNull(), nil
		}
		return valueSlot(v), nil
	})
	reflMethodReg(ci, "getDeclaringClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		decl := d.decl
		if decl == nil {
			decl = d.class
		}
		return ctx.vm.reflNewClass(decl), nil
	})
	reflMethodReg(ci, "isPublic", reflBool(func(d *reflectionData) bool { return d.vis == Public }))
	reflMethodReg(ci, "isProtected", reflBool(func(d *reflectionData) bool { return d.vis == Protected }))
	reflMethodReg(ci, "isPrivate", reflBool(func(d *reflectionData) bool { return d.vis == Private }))
	reflMethodReg(ci, "isFinal", reflBool(func(d *reflectionData) bool { return false }))
	reflMethodReg(ci, "getModifiers", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		var m int64
		switch d.vis {
		case Public:
			m |= 1
		case Protected:
			m |= 2
		case Private:
			m |= 4
		}
		return MakeInt(m), nil
	})
	reflMethodReg(ci, "getDocComment", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if doc := constDoc(d.class, d.name); doc != "" {
			return MakeString(doc), nil
		}
		return MakeBool(false), nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo { return constAttrs(d.class, d.name) }))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString("Constant [ " + visWord(d.vis) + " " + d.name + " ]\n"), nil
	})
}

// --- ReflectionNamedType / UnionType ---

func registerReflectionTypeMembers(ci ClassInfo) {
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.typeName), nil
	})
	reflMethodReg(ci, "allowsNull", reflBool(func(d *reflectionData) bool {
		return d.nullable || d.typeName == "null" || d.typeName == "mixed"
	}))
	reflMethodReg(ci, "isBuiltin", reflBool(func(d *reflectionData) bool { return isBuiltinTypeName(d.typeName) }))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if d.nullable && d.typeName != "mixed" && d.typeName != "null" {
			return MakeString("?" + d.typeName), nil
		}
		return MakeString(d.typeName), nil
	})
}

func registerReflectionUnionMembers(ci ClassInfo) {
	reflMethodReg(ci, "getTypes", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		arr := NewArray()
		for _, t := range d.memberTypes {
			arr.Append(ctx.vm.reflNewNamedType(t))
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "allowsNull", reflBool(func(d *reflectionData) bool {
		for _, t := range d.memberTypes {
			if t == "null" {
				return true
			}
		}
		return false
	}))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(strings.Join(d.memberTypes, "|")), nil
	})
}

// reflTypeSlot строит ReflectionNamedType/UnionType/IntersectionType из строки типа.
func (vm *VM) reflTypeSlot(t string) Slot {
	if t == "" {
		return MakeNull()
	}
	canon := TypeString(t)
	if canon == "" {
		return MakeNull()
	}
	// ?T -> T|null (Zend: ReflectionNamedType с allowsNull).
	nullable := false
	if strings.HasPrefix(canon, "?") {
		nullable = true
		canon = canon[1:]
	}
	if topLevelSplit(canon, '|') >= 0 {
		parts := strings.Split(canon, "|")
		if nullable {
			parts = append(parts, "null")
		}
		d, s := vm.reflObj("ReflectionUnionType")
		if d == nil {
			return MakeNull()
		}
		d.kind = rkUnionType
		d.memberTypes = parts
		return s
	}
	if topLevelSplit(canon, '&') >= 0 {
		parts := strings.Split(canon, "&")
		d, s := vm.reflObj("ReflectionIntersectionType")
		if d == nil {
			return MakeNull()
		}
		d.kind = rkUnionType
		d.isIntersection = true
		d.memberTypes = parts
		return s
	}
	return vm.reflNewNamedType(strings.TrimSpace(t))
}

// reflNewNamedType создаёт ReflectionNamedType.
func (vm *VM) reflNewNamedType(name string) Slot {
	name = strings.TrimSpace(name)
	d, s := vm.reflObj("ReflectionNamedType")
	if d == nil {
		return MakeNull()
	}
	d.kind = rkNamedType
	if strings.HasPrefix(name, "?") {
		d.nullable = true
		name = name[1:]
	}
	d.typeName = name
	return s
}

// --- ReflectionAttribute ---

func registerReflectionAttributeMembers(ci ClassInfo) {
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.attrName), nil
	})
	reflMethodReg(ci, "getArguments", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		args, named, err := ctx.vm.attrArguments(d)
		if err != nil {
			return Slot{}, err
		}
		arr := NewArray()
		for _, a := range args {
			arr.Append(a)
		}
		for _, a := range named {
			arr.SetStr(a.Name, a.Val)
		}
		return MakeArray(arr), nil
	})
	reflMethodReg(ci, "isRepeated", reflBool(func(d *reflectionData) bool { return d.attrRepeated }))
	reflMethodReg(ci, "getTarget", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeInt(int64(d.attrTarget)), nil
	})
	reflMethodReg(ci, "newInstance", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		ci := ctx.vm.FindClass(d.attrName)
		if ci == nil {
			if _, err := ctx.vm.Autoload(ctx, d.attrName); err != nil {
				return Slot{}, err
			}
			ci = ctx.vm.FindClass(d.attrName)
		}
		if ci == nil {
			return Slot{}, ctx.vm.reflThrow("Error", "Attribute class \""+d.attrName+"\" not found")
		}
		rc := asRC(ci)
		if rc == nil || !rc.attrClass {
			return Slot{}, ctx.vm.reflThrow("Error",
				"Attempting to use non-attribute class \""+d.attrName+"\" as attribute")
		}
		if !rc.internal {
			flags, err := ctx.vm.AttributeFlagsEval(ci)
			if err != nil {
				return Slot{}, err
			}
			if d.attrTarget != 0 && d.attrTarget&int(flags) == 0 {
				return Slot{}, ctx.vm.reflThrow("Error", fmt.Sprintf(
					"Attribute \"%s\" cannot target %s (allowed targets: %s)",
					d.attrName, AttrTargetNames(d.attrTarget), AttrTargetNames(int(flags))))
			}
			if flags&64 == 0 && d.attrRepeated {
				return Slot{}, ctx.vm.reflThrow("Error", "Attribute \""+d.attrName+"\" must not be repeated")
			}
		}
		if rc.internal {
			// Встроенный Attribute: создаётся напрямую, flags — из аргумента.
			slot := ci.New(&ctx.vm.ctx, nil)
			if slot == nil {
				return Slot{}, ctx.vm.reflThrow("Error", "Cannot instantiate class "+ci.Name())
			}
			if evArgs, _, evErr := ctx.vm.attrArguments(d); evErr != nil {
				return Slot{}, evErr
			} else if len(evArgs) > 0 {
				slot.SetProp("flags", valueSlot(evArgs[0]))
			}
			return MakeObjectOf(ctx.vm, slot), nil
		}
		args, named, evErr := ctx.vm.attrArguments(d)
		if evErr != nil {
			return Slot{}, evErr
		}
		hasArgs := len(args) > 0 || len(named) > 0
		ctor := ci.lookupCtor()
		if ctor != nil {
			if _, _, vis := ci.CtorInfo(); vis != Public {
				return Slot{}, ctx.vm.reflThrow("Error", "Attribute constructor of class "+ci.Name()+" must be public")
			}
		} else if hasArgs {
			return Slot{}, ctx.vm.reflThrow("Error", "Attribute class "+ci.Name()+" does not have a constructor, cannot pass arguments")
		}
		callArgs := append([]Slot(nil), args...)
		if len(named) > 0 {
			// Именованные аргументы атрибута раскладываются по параметрам
			// конструктора (Zend: обычный вызов).
			arr := NewArray()
			for _, a := range args {
				arr.Append(a)
			}
			for _, a := range named {
				arr.SetStr(a.Name, a.Val)
			}
			bound, berr := ctx.vm.BindNamedArgs(ctor, arr)
			if berr != nil {
				return Slot{}, berr
			}
			callArgs = bound
		}
		return ctx.vm.WithAttrLocation(d.attrFile, d.attrLine, d.attrStrict, func() (Slot, error) {
			return ctx.vm.reflInstantiate(ci, callArgs, false)
		})
	})
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString("Attribute [ " + d.attrName + " ]\n"), nil
	})
}

// attrArguments возвращает аргументы атрибута, вычисляя отложенные выражения
// в области объявления. Результат кэшируется в объекте ReflectionAttribute.
func (vm *VM) attrArguments(d *reflectionData) ([]Slot, []NamedAttrArg, error) {
	if d.attrEvaled {
		return d.attrArgs, d.attrNamed, d.attrEvalErr
	}
	d.attrEvaled = true
	if len(d.attrArgFns) == 0 && len(d.attrNamedFns) == 0 {
		return d.attrArgs, d.attrNamed, nil
	}
	scope := d.attrScope
	if !d.attrScopeSet {
		scope = attrEvalScope(d)
	}
	mat := func(fn *Function) *Function {
		if cf, ok := fn.Executable.(*CompiledFunction); ok && cf.chunk != nil {
			cf.chunk.materialize()
		}
		if scope == nil {
			return fn
		}
		// Копия с нужной областью: self/static в константном выражении
		// резолвятся по fn.Scope (invokeMethod выставляет ctx.self из него).
		fc := *fn
		fc.Scope = scope
		return &fc
	}
	args := make([]Slot, 0, len(d.attrArgFns))
	for _, fn := range d.attrArgFns {
		v, err := vm.invokeMethod(mat(fn), Slot{}, scope, nil)
		if err != nil {
			d.attrEvalErr = err
			return nil, nil, err
		}
		args = append(args, v)
	}
	var named []NamedAttrArg
	for _, nf := range d.attrNamedFns {
		v, err := vm.invokeMethod(mat(nf.Fn), Slot{}, scope, nil)
		if err != nil {
			d.attrEvalErr = err
			return nil, nil, err
		}
		named = append(named, NamedAttrArg{Name: nf.Name, Val: v})
	}
	d.attrArgs, d.attrNamed = args, named
	return args, named, nil
}

// attrEvalScope выбирает класс области вычисления аргументов: объявление
// функции (для унаследованных методов — предок), но для методов трейта —
// использующий класс (Zend перепривязывает трейт-методы), а для замыканий —
// текущая область объекта (Closure::bindTo).
func attrEvalScope(d *reflectionData) ClassInfo {
	if d.kind == rkParameter {
		if od := ownerReflData(d); od != nil {
			return attrEvalScope(od)
		}
	}
	if d.closure {
		if cl, ok := deref(d.closureSlot).asClosureOpt(); ok && cl != nil && cl.Scope != nil {
			return cl.Scope
		}
	}
	if d.fn != nil && d.fn.Scope != nil {
		if !d.fn.Scope.IsTrait() {
			return d.fn.Scope
		}
		if d.class != nil {
			return d.class
		}
		return d.fn.Scope
	}
	if d.class != nil {
		return d.class
	}
	return nil
}

// reflGetAttributes создаёт реализацию getAttributes(name, flags).
func reflGetAttributes(get func(*reflectionData) []AttributeInfo) func(*Context, *reflectionData, []Slot) (Slot, error) {
	return func(ctx *Context, d *reflectionData, args []Slot) (Slot, error) {
		arr := NewArray()
		filter := ""
		flags := int64(0)
		if len(args) > 0 && deref(args[0]).typ != NullType {
			filter = deref(args[0]).ToString()
		}
		if len(args) > 1 {
			flags = deref(args[1]).ToInt()
		}
		if flags & ^int64(2) != 0 {
			return Slot{}, NewThrowable("ValueError", reflGetAttributesOwner(d)+"::getAttributes(): Argument #2 ($flags) must be a valid attribute filter flag")
		}
		attrs := get(d)
		// Повторяющиеся атрибуты (по имени) помечаются все (Zend).
		type cnt struct{ n int }
		counts := map[string]int{}
		for _, a := range attrs {
			counts[strings.ToLower(a.Name)]++
		}
		target := attrTargetFor(d)
		var base ClassInfo
		if filter != "" && flags&2 != 0 {
			base = ctx.vm.FindClass(filter)
			if base == nil {
				if _, err := ctx.vm.Autoload(ctx, filter); err != nil {
					return Slot{}, err
				}
				base = ctx.vm.FindClass(filter)
			}
			if base == nil {
				return Slot{}, ctx.vm.reflThrow("Error", "Class \""+filter+"\" not found")
			}
		}
		for _, a := range attrs {
			if filter != "" {
				if base != nil {
					ci := ctx.vm.FindClass(a.Name)
					if ci == nil || !ci.IsA(base) {
						continue
					}
				} else if !strings.EqualFold(a.Name, filter) {
					continue
				}
			}
			ad, s := ctx.vm.reflObj("ReflectionAttribute")
			if ad == nil {
				continue
			}
			ad.kind = rkAttr
			ad.attrName = a.Name
			ad.attrArgs = a.Args
			ad.attrNamed = a.Named
			ad.attrArgFns = a.ArgFns
			ad.attrNamedFns = a.NamedArgFns
			// Цель атрибута — область вычисления отложенных аргументов.
			ad.fn = d.fn
			ad.class = d.class
			ad.attrScope = attrEvalScope(d)
			ad.attrScopeSet = true
			ad.attrRepeated = a.Repeated || (counts[strings.ToLower(a.Name)] > 1 && !strings.Contains(a.Name, "\\"))
			ad.attrTarget = target
			ad.attrFile = a.File
			ad.attrLine = a.Line
			ad.attrStrict = a.Strict
			arr.Append(s)
		}
		return MakeArray(arr), nil
	}
}

// reflGetAttributesOwner — класс, в котором объявлен метод getAttributes
// (для текста ValueError).
func reflGetAttributesOwner(d *reflectionData) string {
	switch d.kind {
	case rkFunction, rkMethod:
		return "ReflectionFunctionAbstract"
	case rkProperty:
		return "ReflectionProperty"
	case rkClassConst:
		return "ReflectionClassConstant"
	case rkParameter:
		return "ReflectionParameter"
	case rkEnumCase, rkEnumBackedCase:
		return "ReflectionEnumUnitCase"
	}
	return "ReflectionClass"
}

// attrTargetFor возвращает цель атрибута для рефлектируемой сущности
// (ZEND_ATTRIBUTE_TARGET_*).
func attrTargetFor(d *reflectionData) int {
	switch d.kind {
	case rkMethod:
		return 4
	case rkProperty:
		return 8
	case rkClassConst:
		return 16
	case rkParameter:
		return 32
	case rkFunction:
		return 2
	}
	return 1 // class/object/enum
}

// --- ReflectionEnumUnitCase / BackedCase ---

func registerReflectionEnumCaseMembers(ci ClassInfo) {
	reflMethodReg(ci, "getName", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString(d.name), nil
	})
	reflMethodReg(ci, "getValue", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		if deref(d.caseSlot).typ == ObjectType {
			return d.caseSlot, nil
		}
		return MakeNull(), nil
	})
	reflMethodReg(ci, "getEnum", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return ctx.vm.reflNewEnum(d.class), nil
	})
	reflMethodReg(ci, "getDeclaringClass", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return ctx.vm.reflNewClass(d.class), nil
	})
	reflMethodReg(ci, "getAttributes", reflGetAttributes(func(d *reflectionData) []AttributeInfo { return nil }))
	reflMethodReg(ci, "__toString", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) {
		return MakeString("Constant [ public " + d.class.Name() + " " + d.name + " ] { Object }\n"), nil
	})
}

// --- ReflectionGenerator / ReflectionReference ---

func registerReflectionGeneratorMembers(ci ClassInfo) {
	reflConstructor(ci, func(ctx *Context, d *reflectionData, args []Slot) error {
		if len(args) == 0 || deref(args[0]).typ != ObjectType {
			return NewThrowable("TypeError", ArgTypeError("ReflectionGenerator::__construct", 1, "generator", "Generator", "", "", 0))
		}
		d.kind = rkGenerator
		d.this = args[0]
		return nil
	})
	reflMethodReg(ci, "getExecutingFile", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeString(""), nil })
	reflMethodReg(ci, "getExecutingLine", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeInt(0), nil })
	reflMethodReg(ci, "getFunction", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeNull(), nil })
	reflMethodReg(ci, "getThis", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeNull(), nil })
	reflMethodReg(ci, "getTrace", func(ctx *Context, d *reflectionData, _ []Slot) (Slot, error) { return MakeArray(NewArray()), nil })
}

// --- Общие помощники рефлексии ---

func asRC(ci ClassInfo) *runtimeClass {
	rc, _ := asRuntimeClass(ci)
	return rc
}

func reflIsInternal(ci ClassInfo) bool {
	if rc := asRC(ci); rc != nil {
		for k := rc; k != nil; k = k.parent {
			if k.internal {
				return true
			}
		}
	}
	return false
}

// reflIsInternalScope — класс считается внутренним для привязки scope
// замыкания (zend_valid_closure_binding): помимо флага internal учитываются
// встроенные классы без объявления в userland (stdClass, исключения, SPL) —
// у них defFile пуст.
func reflIsInternalScope(ci ClassInfo) bool {
	if reflIsInternal(ci) {
		return true
	}
	if rc := asRC(ci); rc != nil {
		return rc.defFile == ""
	}
	return false
}

// findMethodOrSig ищет метод (реализацию или сигнатуру) с учётом наследования,
// возвращая функцию (nil для сигнатуры), класс-объявитель и видимость.
func findMethodOrSig(ci ClassInfo, name string) (*Function, ClassInfo, Visibility, bool) {
	for k := ci; k != nil; k = k.Parent() {
		if m := k.lookupMethod(name); m != nil {
			rc := asRC(k)
			if rc != nil {
				if fm, ok := rc.allMethods[name]; ok {
					return fm.fn, fm.decl, fm.vis, true
				}
			}
		}
	}
	// Сигнатуры.
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		for kk := rc; kk != nil; kk = kk.parent {
			if m, ok := kk.methods[name]; ok {
				return m, kk, kk.visibility(name), true
			}
			if name == "__construct" && kk.ctor != nil {
				return kk.ctor, kk, Public, true
			}
			if v, ok := kk.sigs[name]; ok {
				// Метаданные сигнатуры (может не быть) — для рефлексии.
				return kk.sigFns[name], kk, v, true
			}
		}
		for _, i := range rc.ifaces {
			if fn, decl, vis, ok := findMethodOrSig(i, name); ok {
				return fn, decl, vis, true
			}
		}
	}
	return nil, nil, Public, false
}

func (c *runtimeClass) visibility(name string) Visibility {
	if c.methVis != nil {
		if v, ok := c.methVis[name]; ok {
			return v
		}
	}
	return Public
}

// findProperty ищет свойство (объявленное или статическое) с учётом наследования.
func findProperty(ci ClassInfo, name string) (ClassInfo, Visibility, bool) {
	if sentinelValueProp(ci, name) {
		return ci, Private, true
	}
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		if pd, ok := rc.ownProps[name]; ok {
			return k, pd.vis, true
		}
		if _, ok := rc.staticProps[name]; ok {
			return k, Public, true
		}
		if _, ok := rc.lazyStatics[name]; ok {
			return k, Public, true
		}
	}
	return nil, Public, false
}

func propMeta(ci ClassInfo, name string) (*propDecl, ClassInfo) {
	if sentinelValueProp(ci, name) {
		return sensitiveValuePropDecl, ci
	}
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		if pd, ok := rc.ownProps[name]; ok {
			return pd, k
		}
	}
	return nil, nil
}

func isStaticProperty(ci ClassInfo, name string) bool {
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		if _, ok := rc.staticProps[name]; ok {
			return true
		}
		if _, ok := rc.lazyStatics[name]; ok {
			return true
		}
	}
	return false
}

// allPropertyNames — имена свойств (сначала объявленные по иерархии, затем статические).
func allPropertyNames(ci ClassInfo) []string {
	var out []string
	seen := map[string]bool{}
	if sensitiveValueClass(ci) {
		seen["value"] = true
		out = append(out, "value")
	}
	var chain []*runtimeClass
	for k := ci; k != nil; k = k.Parent() {
		if rc := asRC(k); rc != nil {
			chain = append(chain, rc)
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		for _, n := range c.propOrder {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		c := chain[i]
		for _, n := range c.staticOrder {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	return out
}

func allConstNames(ci ClassInfo) []string {
	var out []string
	seen := map[string]bool{}
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		for _, n := range rc.constOrder {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		// Константы интерфейсов.
		for _, i := range rc.ifaces {
			for _, n := range allConstNames(i) {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
	}
	return out
}

// constDeclVis находит класс-объявитель и видимость константы.
func constDeclVis(ci ClassInfo, name string) (ClassInfo, Visibility) {
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		if _, ok := rc.consts[name]; ok {
			return k, rc.constVis[name]
		}
		if _, ok := rc.lazyConsts[name]; ok {
			return k, rc.constVis[name]
		}
	}
	for k := ci; k != nil; k = k.Parent() {
		rc := asRC(k)
		if rc == nil {
			continue
		}
		for _, i := range rc.ifaces {
			if decl, vis := constDeclVis(i, name); decl != nil {
				return decl, vis
			}
		}
	}
	return ci, Public
}

func constDoc(ci ClassInfo, name string) string {
	for k := ci; k != nil; k = k.Parent() {
		if rc := asRC(k); rc != nil {
			if rc.constDoc != nil {
				if d, ok := rc.constDoc[name]; ok {
					return d
				}
			}
		}
	}
	return ""
}

// --- Атрибуты (заглушки до разбора #[...]) ---

func classAttrs(ci ClassInfo) []AttributeInfo {
	if ci != nil && ci.Name() == "Attribute" {
		// Внутренний класс Attribute помечен #[Attribute(TARGET_CLASS)].
		return []AttributeInfo{{Name: "Attribute", Args: []Slot{MakeInt(1)}}}
	}
	if rc := asRC(ci); rc != nil {
		return rc.attrs
	}
	return nil
}

func methodAttrs(ci ClassInfo, name string) []AttributeInfo {
	if rc := asRC(ci); rc != nil && rc.methAttrs != nil {
		return rc.methAttrs[name]
	}
	return nil
}

func propAttrs(ci ClassInfo, name string) []AttributeInfo {
	if rc := asRC(ci); rc != nil && rc.propAttrs != nil {
		return rc.propAttrs[name]
	}
	return nil
}

func constAttrs(ci ClassInfo, name string) []AttributeInfo {
	if rc := asRC(ci); rc != nil && rc.constAttrs != nil {
		return rc.constAttrs[name]
	}
	return nil
}

// --- Разное ---

func reflVisMatch(filter int64, vis Visibility) bool {
	if filter&(1|2|4) == 0 {
		return true
	}
	switch vis {
	case Public:
		return filter&1 != 0
	case Protected:
		return filter&2 != 0
	case Private:
		return filter&4 != 0
	}
	return true
}

func reflStaticMatch(filter int64) bool {
	return filter&16 != 0 || filter&(1|2|4) == 0
}

func paramCount(fn *Function) int {
	if fn == nil {
		return 0
	}
	if n := len(fn.ParamNames); n > len(fn.ParamTypes) {
		return n
	}
	return len(fn.ParamTypes)
}

func strListSlot(names []string) Slot {
	arr := NewArray()
	for _, n := range names {
		arr.Append(MakeString(n))
	}
	return MakeArray(arr)
}

func visWord(v Visibility) string {
	switch v {
	case Private:
		return "private"
	case Protected:
		return "protected"
	}
	return "public"
}

func isBuiltinTypeName(n string) bool {
	switch strings.ToLower(n) {
	case "int", "float", "string", "bool", "array", "object", "callable",
		"iterable", "mixed", "void", "never", "null", "false", "true", "static", "self", "parent":
		return true
	}
	return false
}

func (vm *VM) reflInstantiate(ci ClassInfo, args []Slot, withoutCtor bool) (Slot, error) {
	if ci == nil {
		return Slot{}, vm.reflThrow("Error", "Cannot instantiate class")
	}
	if ci.IsInterface() || ci.IsTrait() || ci.IsAbstract() || ci.IsEnum() {
		return Slot{}, vm.reflThrow(cannotInstantiateErr(ci).Class, cannotInstantiateErr(ci).Message)
	}
	idx := vm.classIndex(ci)
	if idx < 0 {
		return Slot{}, vm.reflThrow("Error", "Cannot instantiate class "+ci.Name())
	}
	obj := ci.New(&vm.ctx, nil)
	if obj == nil {
		return Slot{}, vm.reflThrow(cannotInstantiateErr(ci).Class, cannotInstantiateErr(ci).Message)
	}
	slot := Slot{typ: ObjectType, num: uint64(idx), ptr: unsafe.Pointer(obj)}
	if !withoutCtor {
		if ctor := ci.lookupCtor(); ctor != nil {
			vm.MarkNoDestruct(obj)
			if _, err := vm.invokeMethod(ctor, slot, ci, args); err != nil {
				return Slot{}, err
			}
			vm.UnmarkNoDestruct(obj)
		}
	}
	return slot, nil
}

// reflInstantiateNamed — newInstance с массивом аргументов, строковые ключи
// которого — именованные аргументы конструктора.
func (vm *VM) reflInstantiateNamed(ci ClassInfo, arr *Array) (Slot, error) {
	if ci == nil {
		return Slot{}, vm.reflThrow("Error", "Cannot instantiate class")
	}
	if ci.IsInterface() || ci.IsTrait() || ci.IsAbstract() || ci.IsEnum() {
		return Slot{}, vm.reflThrow(cannotInstantiateErr(ci).Class, cannotInstantiateErr(ci).Message)
	}
	idx := vm.classIndex(ci)
	if idx < 0 {
		return Slot{}, vm.reflThrow("Error", "Cannot instantiate class "+ci.Name())
	}
	obj := ci.New(&vm.ctx, nil)
	if obj == nil {
		return Slot{}, vm.reflThrow(cannotInstantiateErr(ci).Class, cannotInstantiateErr(ci).Message)
	}
	slot := Slot{typ: ObjectType, num: uint64(idx), ptr: unsafe.Pointer(obj)}
	if ctor := ci.lookupCtor(); ctor != nil {
		vm.MarkNoDestruct(obj)
		if _, err := vm.InvokeMethodNamed(ctor, slot, ci, arr); err != nil {
			return Slot{}, err
		}
		vm.UnmarkNoDestruct(obj)
	}
	return slot, nil
}

// reflThrow создаёт исключение с трейсом на момент вызова (включая кадр
// нативного метода Reflection).
func (vm *VM) reflThrow(class, msg string) *Throwable {
	th := NewThrowable(class, msg)
	th.Trace = vm.captureExceptionTrace()
	if n := len(th.Trace); n > 0 {
		th.LocFile = th.Trace[n-1].callerFile
		th.LocLine = th.Trace[n-1].callerLine
	}
	return th
}

// reflMethodString и reflFnString формируют __toString для ReflectionMethod/Function.
func reflMethodString(vm *VM, d *reflectionData) string {
	var b strings.Builder
	b.WriteString("Method [ <")
	if reflIsInternal(d.decl) {
		b.WriteString("internal")
	} else {
		b.WriteString("user")
	}
	if strings.EqualFold(d.name, "__construct") {
		b.WriteString(", ctor")
	}
	b.WriteString("> ")
	if d.fn != nil && d.fn.IsFinal {
		b.WriteString("final ")
	}
	if c := asRC(d.class); c != nil && d.fn == nil && c.hasAbstract(d.name) {
		b.WriteString("abstract ")
	}
	b.WriteString(visWord(d.vis))
	b.WriteString(" method ")
	b.WriteString(d.name)
	b.WriteString(" ] {\n  @@ ")
	if d.fn != nil && d.fn.DefFile != "" {
		b.WriteString(d.fn.DefFile)
		b.WriteString(" ")
	}
	if d.fn != nil && d.fn.DefLine > 0 {
		b.WriteString(itoa(d.fn.DefLine))
		b.WriteString(" - ")
		b.WriteString(itoa(d.fn.DefLine))
	}
	b.WriteString("\n}\n")
	return b.String()
}

// internalFnSig — описание сигнатуры встроенной функции для ReflectionFunction.
type internalFnSig struct {
	Ext    string
	Params []string
	Return string
}

var internalFnSigs = map[string]internalFnSig{}

// SetInternalFnSig регистрирует сигнатуру встроенной функции (для вывода
// ReflectionFunction::__toString и связанных).
func SetInternalFnSig(name, ext string, params []string, ret string) {
	internalFnSigs[name] = internalFnSig{Ext: ext, Params: params, Return: ret}
}

func reflFnString(vm *VM, d *reflectionData) string {
	if sig, ok := internalFnSigs[d.name]; ok {
		var b strings.Builder
		b.WriteString("Function [ <internal:")
		b.WriteString(sig.Ext)
		b.WriteString("> function ")
		b.WriteString(d.name)
		b.WriteString(" ] {\n")
		if len(sig.Params) > 0 {
			b.WriteString("\n  - Parameters [")
			b.WriteString(itoa(len(sig.Params)))
			b.WriteString("] {\n")
			for i, p := range sig.Params {
				b.WriteString("    Parameter #")
				b.WriteString(itoa(i))
				b.WriteString(" [ ")
				b.WriteString(p)
				b.WriteString(" ]\n")
			}
			b.WriteString("  }\n")
		}
		if sig.Return != "" {
			b.WriteString("  - Return [ ")
			b.WriteString(sig.Return)
			b.WriteString(" ]\n")
		}
		b.WriteString("}\n")
		return b.String()
	}
	return "Function [ <user> function " + d.name + " ] {\n}\n"
}

// itoa — простая конвертация int в строку без импорта strconv (быстро).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// typeAllowsNullStr сообщает, допускает ли тип null.
func typeAllowsNullStr(t string) bool {
	if t == "" || t == "mixed" || t == "null" {
		return true
	}
	if strings.HasPrefix(t, "?") {
		return true
	}
	for _, part := range strings.Split(t, "|") {
		if strings.EqualFold(strings.TrimSpace(part), "null") {
			return true
		}
	}
	return false
}

// sortStrings — обёртка (используется при необходимости).
func sortStrings(s []string) []string {
	sort.Strings(s)
	return s
}
