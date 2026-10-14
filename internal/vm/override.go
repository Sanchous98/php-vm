package vm

import (
	"fmt"
	"strings"
)

// hasOverrideAttr сообщает, что среди атрибутов есть #[\Override].
func hasOverrideAttr(attrs []AttributeInfo) bool {
	for _, a := range attrs {
		name := strings.TrimLeft(a.Name, "\\")
		if strings.EqualFold(name, "Override") {
			return true
		}
	}
	return false
}

// ownVis — видимость метода, объявленного непосредственно в классе.
func ownVis(c *runtimeClass, name string) Visibility {
	if c.methVis != nil {
		if v, ok := c.methVis[name]; ok {
			return v
		}
	}
	return Public
}

// ownMethodFold ищет метод, объявленный непосредственно в классе (с учётом
// регистронезависимости имён PHP). Возвращает функцию и видимость.
func ownMethodFold(c *runtimeClass, name string) (*Function, Visibility, bool) {
	if m, ok := c.methods[name]; ok {
		return m, ownVis(c, name), true
	}
	ln := strings.ToLower(name)
	if ln != name {
		if m, ok := c.methods[ln]; ok {
			return m, ownVis(c, ln), true
		}
	}
	for k, m := range c.methods {
		if strings.EqualFold(k, name) {
			return m, ownVis(c, k), true
		}
	}
	if c.ctor != nil && strings.EqualFold(name, c.ctor.Name) {
		return c.ctor, c.ctorVis, true
	}
	return nil, Public, false
}

// ownSigFold ищет сигнатуру (abstract/интерфейсный метод), объявленную в
// классе, с учётом регистронезависимости.
func ownSigFold(c *runtimeClass, name string) (Visibility, bool) {
	if c.sigs == nil {
		return Public, false
	}
	if v, ok := c.sigs[name]; ok {
		return v, true
	}
	for k, v := range c.sigs {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return Public, false
}

// ownAbstractCtor сообщает, объявлен ли в классе абстрактный конструктор.
func ownAbstractCtor(c *runtimeClass) bool {
	_, ok := ownSigFold(c, "__construct")
	return ok
}

// classHasOwnDecl сообщает, объявлен ли в классе (или классе-предке) метод
// с данным именем, и подходит ли он под правило #[\Override].
// ok=false — совпадений нет.
func overrideMatchUp(c *runtimeClass, name string) bool {
	for k := c.parent; k != nil; k = k.parent {
		if _, vis, ok := ownMethodFold(k, name); ok {
			return vis != Private
		}
		if vis, ok := ownSigFold(k, name); ok {
			return vis != Private
		}
	}
	// Абстрактный метод примешанного трейта тоже «переопределяется»
	// (zend_add_trait_method сбрасывает флаг при CHECK_PROTO).
	for _, t := range c.usedTraits {
		if tr, ok := asRuntimeClass(t); ok && traitDeclaresAbstract(tr, name, map[*runtimeClass]bool{}) {
			return true
		}
	}
	return false
}

// traitDeclaresAbstract сообщает, объявлен ли в трейте (включая вложенные)
// абстрактный метод с данным именем.
func traitDeclaresAbstract(t *runtimeClass, name string, seen map[*runtimeClass]bool) bool {
	if t == nil || seen[t] {
		return false
	}
	seen[t] = true
	if _, ok := ownSigFold(t, name); ok {
		return true
	}
	for _, u := range t.usedTraits {
		if tr, ok := asRuntimeClass(u); ok && traitDeclaresAbstract(tr, name, seen) {
			return true
		}
	}
	return false
}

// ifaceDeclares сообщает, объявлен ли метод в интерфейсе (включая
// унаследованные интерфейсы).
func ifaceDeclares(i *runtimeClass, name string) bool {
	if i == nil {
		return false
	}
	if _, vis, ok := ownMethodFold(i, name); ok && vis != Private {
		return true
	}
	if vis, ok := ownSigFold(i, name); ok && vis != Private {
		return true
	}
	if builtinIfaceDeclares(i.name, name) {
		return true
	}
	for _, pi := range i.ifaces {
		if pr, ok := asRuntimeClass(pi); ok && ifaceDeclares(pr, name) {
			return true
		}
	}
	return false
}

// builtinIfaceMethods — методы встроенных интерфейсов PHP: их дескрипторы
// намеренно созданы без объявленных методов (см. NewBuiltinInterfaces).
var builtinIfaceMethods = map[string][]string{
	"iterator":          {"current", "key", "next", "rewind", "valid"},
	"iteratoraggregate": {"getiterator"},
	"arrayaccess":       {"offsetexists", "offsetget", "offsetset", "offsetunset"},
	"countable":         {"count"},
	"stringable":        {"__tostring"},
	"jsonserializable":  {"jsonserialize"},
	"serializable":      {"serialize", "unserialize"},
	"unitenum":          {"cases"},
	"backedenum":        {"from", "tryfrom"},
}

func builtinIfaceDeclares(name, method string) bool {
	for _, m := range builtinIfaceMethods[strings.ToLower(name)] {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

// OverrideViolation повторяет zend_inheritance_check_override: каждый метод
// класса (включая примешанные из трейтов), помеченный #[\Override], обязан
// переопределять метод предка или интерфейса. Возвращает текст фатальной
// ошибки или "".
//
// Правила Zend:
//   - метод предка с private-видимостью не считается (флаг не сбрасывается);
//   - конкретный (не abstract, не из интерфейса) конструктор предка не
//     сбрасывает флаг — «переопределение» __construct требует абстрактного
//     конструктора или конструктора интерфейса;
//   - трейты не проверяются (проверка выполняется для класса, куда примешан
//     трейт).
func OverrideViolation(ci ClassInfo) string {
	c := asRC(ci)
	if c == nil || c.declTrait {
		return ""
	}
	report := func(name string, fn *Function) string {
		if fn == nil || !hasOverrideAttr(fn.Attrs) {
			return ""
		}
		isCtor := strings.EqualFold(name, "__construct")
		if isCtor {
			for k := c.parent; k != nil; k = k.parent {
				if ownAbstractCtor(k) {
					return ""
				}
			}
		} else if overrideMatchUp(c, name) {
			return ""
		}
		for _, i := range c.ifaces {
			if ir, ok := asRuntimeClass(i); ok && ifaceDeclares(ir, name) {
				return ""
			}
		}
		return fmt.Sprintf("%s::%s() has #[\\Override] attribute, but no matching parent method exists",
			classDisplayName(c), name)
	}
	for _, name := range c.methodOrder {
		m, ok := c.methods[name]
		if !ok {
			continue
		}
		if msg := report(name, m); msg != "" {
			return msg
		}
	}
	if msg := report("__construct", c.ctor); msg != "" {
		return msg
	}
	// Сигнатуры (abstract-методы, методы интерфейсов) тоже проверяются:
	// атрибуты хранятся в methAttrs, тела у них нет.
	for _, name := range c.sigOrder {
		attrs := c.methAttrs[name]
		if len(attrs) == 0 || !hasOverrideAttr(attrs) {
			continue
		}
		if _, vis, ok := ownMethodFold(c, name); ok && vis != Private {
			continue
		}
		if msg := report(name, &Function{Name: name, Attrs: attrs}); msg != "" {
			return msg
		}
	}
	return ""
}
