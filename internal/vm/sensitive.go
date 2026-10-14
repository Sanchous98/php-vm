package vm

import "unsafe"

// sensitive.go — встроенный класс SensitiveParameterValue. Экземпляр хранит
// исходное значение параметра, помеченного #[SensitiveParameter]; значение
// лежит в Go-поле, а не в PHP-свойстве: var_dump/(array) показывают 0 свойств,
// а getValue() возвращает значение.

// sensitiveValueData — нативное хранилище SensitiveParameterValue.
type sensitiveValueData struct {
	Object
	value Slot
}

// sensitiveValuePropDecl — синтетическое объявление приватного readonly
// свойства value: значение лежит в Go-поле, но Reflection его видит
// (ReflectionClass::getProperty('value'), getValue(), isReadOnly()).
var sensitiveValuePropDecl = &propDecl{vis: Private, readonly: true}

// sensitiveValueClass сообщает, что класс — встроенный SensitiveParameterValue.
func sensitiveValueClass(ci ClassInfo) bool {
	return ci != nil && ci.Name() == "SensitiveParameterValue"
}

// sentinelValueProp сообщает, что запрошено синтетическое свойство value
// SensitiveParameterValue.
func sentinelValueProp(ci ClassInfo, name string) bool {
	return name == "value" && sensitiveValueClass(ci)
}

// NewSensitiveParameterValueClass создаёт дескриптор SensitiveParameterValue.
func NewSensitiveParameterValueClass() ClassInfo {
	ci := RegisterClass[sensitiveValueData]("SensitiveParameterValue", nil)
	MarkInternal(ci)
	ci.SetFinal()
	ci.SetNoDynamicProps()
	ci.SetNotSerializable()
	ci.Constructor(&Function{Name: "__construct", NumArgs: 1, NumRequired: 1,
		ParamNames: []string{"value"},
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[sensitiveValueData]()
			if d != nil && len(args) > 0 {
				d.value = args[0]
			}
			return MakeNull(), nil
		}}})
	ci.Method("getValue", &Function{Name: "getValue", NumArgs: 0,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			d := ctx.This[sensitiveValueData]()
			if d == nil {
				return MakeNull(), nil
			}
			return d.value, nil
		}}})
	ci.Method("__debugInfo", &Function{Name: "__debugInfo", NumArgs: 0,
		Executable: &NativeFunction{fn: func(ctx *Context, args []Slot) (Slot, error) {
			return MakeArray(NewArray()), nil
		}}})
	return ci
}

// makeSensitiveValue создаёт SensitiveParameterValue, оборачивающий v.
func (vm *VM) makeSensitiveValue(v Slot) Slot {
	ci := vm.findClass("SensitiveParameterValue")
	rc, _ := asRuntimeClass(ci)
	if rc == nil {
		return v
	}
	o := rc.nativeNew(rc)
	if o == nil {
		return v
	}
	(*sensitiveValueData)(unsafe.Pointer(o)).value = v
	return rc.objectSlot(o, vm.classIndex(rc))
}

// isSensitiveValue сообщает, что слот — объект SensitiveParameterValue.
func isSensitiveValue(s Slot) bool {
	d := deref(s)
	if d.typ != ObjectType {
		return false
	}
	o := d.obj()
	return o != nil && o.class != nil && o.class.Name() == "SensitiveParameterValue"
}
