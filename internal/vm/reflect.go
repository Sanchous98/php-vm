package vm

// Рефлексия классов/объектов: экспортные аксессоры к неэкспортным картам
// ClassInfo/Object для builtins (get_class_methods, get_object_vars и т.п.).

import (
	"strings"
)

// IsInterface сообщает, что дескриптор описывает интерфейс.
func (c *runtimeClass) IsInterface() bool { return c != nil && c.iface }

// IsTrait сообщает, что дескриптор используется как трейт (кем-то примешан).
// Отдельного флага «trait» в рантайме нет: трейты компилируются как обычные
// классы, поэтому объявленный, но нигде не использованный трейт неотличим от
// класса.
func (c *runtimeClass) IsTrait() bool { return c != nil && (c.isTrait || c.declTrait) }

// SetDeclaredTrait помечает дескриптор как объявленный через `trait`.
func (c *runtimeClass) SetDeclaredTrait() ClassInfo { c.declTrait = true; return c }

// SetDocComment задаёт doc-комментарий объявления класса.
func (c *runtimeClass) SetDocComment(doc string) ClassInfo {
	c.docComment = doc
	return c
}

// DocComment возвращает doc-комментарий объявления класса.
func (c *runtimeClass) DocComment() string { return c.docComment }

// ParentName возвращает имя родительского класса ("" если нет).
func (c *runtimeClass) ParentName() string {
	if c == nil || c.parent == nil {
		return ""
	}
	return c.parent.name
}

// HasMethod сообщает, что метод name объявлен или унаследован (без учёта
// видимости — как method_exists()). Сигнатуры (интерфейс/abstract) тоже
// считаются объявленными. Имена методов регистронезависимы.
func (c *runtimeClass) HasMethod(name string) bool {
	if c == nil {
		return false
	}
	ln := strings.ToLower(name)
	for k := c; k != nil; k = k.parent {
		if _, ok := k.methods[name]; ok {
			return true
		}
		if ln != name {
			if _, ok := k.methods[ln]; ok {
				return true
			}
		}
		for mname := range k.methods {
			if strings.EqualFold(mname, name) {
				return true
			}
		}
		if k.sigs != nil {
			if _, ok := k.sigs[name]; ok {
				return true
			}
			if ln != name {
				if _, ok := k.sigs[ln]; ok {
					return true
				}
			}
		}
		for _, i := range k.ifaces {
			if i.HasMethod(name) {
				return true
			}
		}
		if name == "__construct" && k.ctor != nil {
			return true
		}
	}
	return false
}

// MethodVisibility возвращает видимость метода name (с учётом наследования).
// Имена методов регистронезависимы.
func (c *runtimeClass) MethodVisibility(name string) (Visibility, bool) {
	return c.methodVisibilityScan(name)
}

// MethodDecl возвращает класс объявления и видимость метода name. Нужен
// проверкам доступа из builtins: private-метод родителя не должен считаться
// доступным в наследнике (bug29210).
func (c *runtimeClass) MethodDecl(name string) (ClassInfo, Visibility, bool) {
	if m, decl, vis := c.methodInfo(name); decl != nil && m != nil {
		return decl, vis, true
	}
	if vis, ok := c.MethodVisibility(name); ok {
		return c, vis, true
	}
	return nil, Public, false
}

func (c *runtimeClass) methodVisibilityScan(name string) (Visibility, bool) {
	ln := strings.ToLower(name)
	for k := c; k != nil; k = k.parent {
		if _, ok := k.methods[name]; ok {
			v := Public
			if k.methVis != nil {
				if vv, ok := k.methVis[name]; ok {
					v = vv
				}
			}
			return v, true
		}
		if ln != name {
			if _, ok := k.methods[ln]; ok {
				v := Public
				if k.methVis != nil {
					if vv, ok := k.methVis[ln]; ok {
						v = vv
					}
				}
				return v, true
			}
		}
		for mname := range k.methods {
			if strings.EqualFold(mname, name) {
				v := Public
				if k.methVis != nil {
					if vv, ok := k.methVis[mname]; ok {
						v = vv
					}
				}
				return v, true
			}
		}
		if k.sigs != nil {
			if v, ok := k.sigs[name]; ok {
				return v, true
			}
			if ln != name {
				if v, ok := k.sigs[ln]; ok {
					return v, true
				}
			}
		}
	}
	return Public, false
}

// MethodNames возвращает методы, видимые из scope caller, в порядке PHP:
// собственные объявленные методы, затем унаследованные (private/protected
// фильтруются по canAccess). caller == nil — глобальная область (только public).
func (c *runtimeClass) MethodNames(caller ClassInfo) []string {
	if c == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for k := c; k != nil; k = k.parent {
		for _, name := range k.methodOrder {
			if seen[name] {
				continue
			}
			vis := Public
			if _, ok := k.methods[name]; ok {
				if k.methVis != nil {
					if vv, ok := k.methVis[name]; ok {
						vis = vv
					}
				}
			} else if name == "__construct" && k.ctor != nil {
				// ок
			} else {
				continue
			}
			if !canAccess(vis, k, caller) {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
		// Сигнатуры (abstract/interface) — видны рефлексии как контракты.
		for _, name := range k.sigOrder {
			if seen[name] {
				continue
			}
			vis := Public
			if k.sigs != nil {
				if vv, ok := k.sigs[name]; ok {
					vis = vv
				}
			}
			if !canAccess(vis, k, caller) {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// AllMethodNames возвращает все объявленные и унаследованные методы без
// фильтра видимости, в порядке PHP.
func (c *runtimeClass) AllMethodNames() []string {
	if c == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for k := c; k != nil; k = k.parent {
		for _, name := range k.methodOrder {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
		for _, name := range k.sigOrder {
			if seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// ConstNames возвращает собственные константы класса в порядке объявления.
func (c *runtimeClass) ConstNames() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.constOrder...)
}

// HasConst сообщает, есть ли константа с учётом наследования.
func (c *runtimeClass) HasConst(name string) bool {
	_, ok := c.ConstValue(name)
	return ok
}

// ConstValue возвращает константу с учётом наследования.
func (c *runtimeClass) ConstValue(name string) (Value, bool) {
	if c == nil {
		return nil, false
	}
	return c.lookupConst(name)
}

// InterfaceConstValue ищет константу среди констант реализуемых интерфейсов
// (транзитивно). Видимость констант рантайм не хранит, поэтому используется
// там, где важно не отдать собственные приватные константы класса
// (defined()/constant()).
func (c *runtimeClass) InterfaceConstValue(name string) (Value, bool) {
	if c == nil {
		return nil, false
	}
	var walk func(ci *runtimeClass, seen map[ClassInfo]bool) (Value, bool)
	walk = func(ci *runtimeClass, seen map[ClassInfo]bool) (Value, bool) {
		for k := ci; k != nil; k = k.parent {
			for _, iface := range k.ifaces {
				if seen[iface] {
					continue
				}
				seen[iface] = true
				if v, ok := iface.lookupConst(name); ok {
					return v, true
				}
				if ir, ok := iface.(*runtimeClass); ok {
					if v, ok := walk(ir, seen); ok {
						return v, true
					}
				}
			}
		}
		return nil, false
	}
	return walk(c, map[ClassInfo]bool{})
}

// InstancePropertyNames возвращает собственные нестатические свойства в
// порядке объявления.
func (c *runtimeClass) InstancePropertyNames() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.propOrder...)
}

// StaticPropertyNames возвращает собственные статические свойства в порядке
// объявления.
func (c *runtimeClass) StaticPropertyNames() []string {
	if c == nil {
		return nil
	}
	return append([]string(nil), c.staticOrder...)
}

// ClassVarsForScope возвращает значения свойств класса для get_class_vars() с
// учётом видимости из текущей области видимости: private — только своего
// класса, protected — при доступе; неинициализированные типизированные
// свойства попадают как NULL. Порядок: нестатические (свои, затем
// унаследованные), затем статические.
func (vm *VM) ClassVarsForScope(ci ClassInfo) *Array {
	out := NewArray()
	rc, ok := asRuntimeClass(ci)
	if !ok || rc == nil {
		return out
	}
	scope := vm.ctx.self
	visible := func(pd *propDecl, decl ClassInfo) bool {
		if pd == nil {
			return true
		}
		switch pd.vis {
		case Private:
			return scope == decl
		case Protected:
			return canAccess(Protected, decl, scope)
		}
		return true
	}
	seen := map[string]bool{}
	for k := rc; k != nil; k = k.parent {
		for _, n := range k.propOrder {
			if seen[n] {
				continue
			}
			seen[n] = true
			if !visible(k.ownProps[n], k) {
				continue
			}
			if v, ok := ci.DefaultValue(n); ok {
				_ = out.SetStr(n, valueSlot(v))
			} else {
				_ = out.SetStr(n, MakeNull())
			}
		}
	}
	for k := rc; k != nil; k = k.parent {
		for _, n := range k.staticOrder {
			if seen[n] {
				continue
			}
			seen[n] = true
			if !visible(k.ownProps[n], k) {
				continue
			}
			if v, ok := ci.StaticValue(n); ok {
				_ = out.SetStr(n, valueSlot(v))
			} else {
				_ = out.SetStr(n, MakeNull())
			}
		}
	}
	return out
}

// SplArrayContents возвращает внутреннее хранилище ArrayObject/ArrayIterator
// (в т.ч. подклассов) для приведения к массиву. ok=false — другой класс.
func (vm *VM) SplArrayContents(o *Object) (*Array, bool) {
	if o == nil {
		return nil, false
	}
	ci := o.Class()
	if ci == nil {
		return nil, false
	}
	ao := vm.findClass("ArrayObject")
	ai := vm.findClass("ArrayIterator")
	if (ao == nil || !ci.isA(ao)) && (ai == nil || !ci.isA(ai)) {
		return nil, false
	}
	d := objectAs[splArrayData](o, ci)
	if d == nil || d.arr == nil {
		return NewArray(), true
	}
	return d.arr, true
}

// MangledObjectVars возвращает свойства объекта под их внутренними
// (манглированными) именами — get_mangled_object_vars().
func (vm *VM) MangledObjectVars(o *Object) *Array {
	out := NewArray()
	if o == nil {
		return out
	}
	rc, _ := asRuntimeClass(o.Class())
	syntheticStorage := false
	if rc != nil {
		switch rc.Name() {
		case "ArrayObject", "ArrayIterator", "SplObjectStorage":
			syntheticStorage = true
		}
	}
	for _, de := range vm.ObjectDumpEntries(o) {
		if de.Uninit {
			continue
		}
		if syntheticStorage && de.Key == "storage" {
			continue
		}
		key := de.Key
		switch {
		case de.Suffix == ":protected":
			key = "\x00*\x00" + de.Key
		case strings.HasPrefix(de.Suffix, `:"`) && strings.HasSuffix(de.Suffix, `":private`):
			cls := de.Suffix[2 : len(de.Suffix)-len(`":private`)]
			key = "\x00" + cls + "\x00" + de.Key
		}
		if n, ok := canonicalIntKey(key); ok {
			_ = out.SetInt(n, de.Val)
		} else {
			_ = out.SetStr(key, de.Val)
		}
	}
	return out
}

// SplObjectID возвращает PHP-хендл объекта (#N), присваивая его при
// необходимости (spl_object_id).
func (vm *VM) SplObjectID(s Slot) int64 {
	o := vm.objectOf(deref(s))
	if o == nil {
		return 0
	}
	vm.markObj(o)
	return int64(o.ID())
}

// ObjectVarKey возвращает ключ хранения свойства name над объектом o для
// builtins (get_object_vars): ok=false, если свойство недоступно из текущей
// области видимости (private другого класса).
func (vm *VM) ObjectVarKey(o *Object, name string) (string, bool) {
	if o == nil {
		return name, false
	}
	ci := o.Class()
	scope := vm.ctx.self
	if pd, decl, ok := propInfoForScope(scope, ci, name); ok && pd != nil {
		switch pd.vis {
		case Private:
			if scope != decl {
				return name, false
			}
		case Protected:
			if !canAccess(Protected, decl, scope) {
				return name, false
			}
		}
	}
	return propKeyForScope(scope, ci, name), true
}

// ObjectVarsForScope возвращает массив видимых из текущей области видимости
// свойств объекта (для get_object_vars).
func (vm *VM) ObjectVarsForScope(o *Object) *Array {
	res := NewArray()
	if o == nil {
		return res
	}
	ci := o.Class()
	scope := vm.ctx.self
	for _, n := range o.PropertyNames() {
		if pd, decl, ok := propInfoForScope(scope, ci, n); ok && pd != nil {
			switch pd.vis {
			case Private:
				if scope != decl {
					continue
				}
			case Protected:
				if !canAccess(Protected, decl, scope) {
					continue
				}
			}
		}
		key := propKeyForScope(scope, ci, n)
		if v, ok := o.GetProp(key); ok {
			_ = res.SetStr(n, valueSlot(v))
		}
	}
	return res
}

// PropStoreKeyUnserialize выбирает ключ хранения свойства при unserialize():
// запись идёт в слот объявленного свойства, если имя (после разманлгинга)
// совпадает с объявленным в классе (Zend: is_property_visibility_changed —
// ключ заменяется на prop_info->name); иначе — динамическое свойство под
// исходным сериализованным ключом.
func (vm *VM) PropStoreKeyUnserialize(ci ClassInfo, raw, decoded string) string {
	if ci == nil {
		return raw
	}
	rc, ok := asRuntimeClass(ci)
	if !ok {
		if raw == decoded {
			return decoded
		}
		return raw
	}
	// lookupDecl ищет объявленное свойство по разманлгленному имени и
	// возвращает ключ его слота (private — манглированный).
	lookupDecl := func(name string) (string, bool) {
		for k := rc; k != nil; k = k.parent {
			if pd, ok := k.ownProps[name]; ok {
				if pd.vis == Private {
					return mangledPropKey(k, name), true
				}
				return name, true
			}
			if _, ok := k.defaults[name]; ok {
				return name, true
			}
			if _, ok := k.lazyDefaults[name]; ok {
				return name, true
			}
			if _, ok := k.propGet[name]; ok {
				return name, true
			}
		}
		return "", false
	}
	if raw != decoded && len(raw) > 2 && raw[0] == 0 {
		// Манглированная форма: \0Класс\0имя или \0*\0имя. Zend ищет
		// объявленное свойство только для класса объекта или «*».
		if i := strings.IndexByte(raw[1:], 0); i >= 0 {
			cls, name := raw[1:1+i], raw[1+i+1:]
			if name == decoded && (cls == "*" || strings.EqualFold(cls, rc.Name())) {
				if key, found := lookupDecl(name); found {
					return key
				}
			}
		}
		return raw
	}
	if raw != decoded {
		return raw
	}
	if key, found := lookupDecl(decoded); found {
		return key
	}
	return decoded
}

// HasProperty сообщает, что свойство (нестатическое или статическое) объявлено
// с учётом наследования.
func (c *runtimeClass) HasProperty(name string) bool {
	for k := c; k != nil; k = k.parent {
		if name == "value" && k.name == "SensitiveParameterValue" {
			return true
		}
		if _, ok := k.defaults[name]; ok {
			return true
		}
		if _, ok := k.lazyDefaults[name]; ok {
			return true
		}
		if _, ok := k.ownProps[name]; ok {
			return true
		}
		if _, ok := k.propGet[name]; ok {
			return true
		}
		if _, ok := k.staticProps[name]; ok {
			return true
		}
		if _, ok := k.lazyStatics[name]; ok {
			return true
		}
	}
	return false
}

// DefaultValue возвращает дефолтное значение объявленного нестатического
// свойства с учётом наследования.
func (c *runtimeClass) DefaultValue(name string) (Value, bool) {
	for k := c; k != nil; k = k.parent {
		if v, ok := k.defaults[name]; ok {
			return v, true
		}
		if th, ok := k.lazyDefaults[name]; ok {
			if v, err := c.resolveThunk(th); err == nil {
				return v, true
			}
		}
	}
	return nil, false
}

// StaticValue возвращает значение статического свойства с учётом наследования.
func (c *runtimeClass) StaticValue(name string) (Value, bool) {
	if c == nil {
		return nil, false
	}
	s := c.lookupStatic(name)
	if s == nil {
		return nil, false
	}
	return *s, true
}

// DirectInterfaces возвращает непосредственно реализуемые интерфейсы.
func (c *runtimeClass) DirectInterfaces() []ClassInfo {
	if c == nil {
		return nil
	}
	return append([]ClassInfo(nil), c.ifaces...)
}

// InterfaceNames возвращает все реализуемые интерфейсы транзитивно в порядке
// PHP: интерфейсы родительских классов (в порядке иерархии), затем собственные,
// каждый — до своих родителей.
func (c *runtimeClass) InterfaceNames() []string {
	var chain []*runtimeClass
	for k := c; k != nil; k = k.parent {
		chain = append(chain, k)
	}
	var out []string
	seen := map[ClassInfo]bool{}
	var add func(ci ClassInfo)
	add = func(ci ClassInfo) {
		if ci == nil || seen[ci] {
			return
		}
		seen[ci] = true
		out = append(out, ci.Name())
		for _, p := range ci.Interfaces() {
			add(p)
		}
	}
	for i := len(chain) - 1; i >= 0; i-- {
		for _, iface := range chain[i].ifaces {
			add(iface)
		}
	}
	return out
}

// ParentNames возвращает имена предков от ближайшего родителя к корню.
func (c *runtimeClass) ParentNames() []string {
	var out []string
	if c == nil {
		return out
	}
	for k := c.parent; k != nil; k = k.parent {
		out = append(out, k.name)
	}
	return out
}

// TraitNames возвращает имена примешанных трейтов в порядке объявления use.
func (c *runtimeClass) TraitNames() []string {
	if c == nil {
		return nil
	}
	out := make([]string, 0, len(c.usedTraits))
	for _, t := range c.usedTraits {
		out = append(out, t.Name())
	}
	return out
}

// IsA — экспортная обёртка: c это target, его наследник или реализует его.
func (c *runtimeClass) IsA(target ClassInfo) bool {
	return c.isA(target)
}

// FindClass ищет класс по имени (регистронезависимо, как PHP). Ведущий '\'
// (абсолютное имя в namespace) игнорируется.
func (vm *VM) FindClass(name string) ClassInfo {
	name = strings.TrimPrefix(name, "\\")
	if ci := vm.findClass(name); ci != nil {
		return ci
	}
	for _, ci := range vm.classes {
		if ClassHidden(ci) {
			continue
		}
		if equalsFold(ci.Name(), name) {
			return ci
		}
	}
	return nil
}

// Classes возвращает все зарегистрированные дескрипторы классов.
func (vm *VM) Classes() []ClassInfo { return vm.classes }

// equalsFold — регистронезависимое сравнение ASCII/Unicode.
func equalsFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// ClassConstAccess возвращает константу класса "Class::CONST" с проверкой
// видимости относительно текущей области (для встроенной функции constant()).
func (vm *VM) ClassConstAccess(full string) (Slot, bool, error) {
	i := strings.Index(full, "::")
	if i < 0 {
		return Slot{}, false, nil
	}
	ci := vm.FindClass(full[:i])
	if ci == nil {
		return Slot{}, false, nil
	}
	return vm.classConstValue(ci, full[i+2:])
}

// Functions возвращает таблицу функций VM (для get_defined_functions).
func (vm *VM) Functions() []*Function { return vm.functions }

// ScopedClass резолвит имя класса в контексте текущего кадра: self/static/
// parent учитывают позднее статическое связывание ($this/called), остальные
// имена ищутся без автозагрузки (для defined()/constant()/method_exists).
func (vm *VM) ScopedClass(name string) ClassInfo {
	switch strings.ToLower(strings.TrimLeft(name, "\\")) {
	case "self":
		return vm.ctx.self
	case "static":
		if vm.ctx.called != nil {
			return vm.ctx.called
		}
		return vm.ctx.self
	case "parent":
		if vm.ctx.self != nil {
			return vm.ctx.self.Parent()
		}
		return nil
	}
	return vm.FindClass(strings.TrimLeft(name, "\\"))
}
