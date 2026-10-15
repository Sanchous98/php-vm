package vm

import (
	"math"
	"strconv"
	"strings"
)

// typeAllowsNull сообщает, допускает ли объявленный тип null (nullable `?T`
// или union с компонентом null).
func typeAllowsNull(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "?") {
		return true
	}
	for _, alt := range splitTopLevelAll(t, '|') {
		if strings.EqualFold(strings.TrimSpace(alt), "null") {
			return true
		}
	}
	return false
}

// refTypesCompatible сообщает, могут ли две ссылочные типизации сосуществовать
// (Zend требует, чтобы запись через общую ссылку была валидна для обоих типов).
// Скалярные примитивы должны совпадать; классовые — пересекаться по иерархии.
func (vm *VM) refTypesCompatible(source, target string) bool {
	s := strings.TrimPrefix(strings.TrimSpace(source), "?")
	t := strings.TrimPrefix(strings.TrimSpace(target), "?")
	if s == t || s == "" || t == "" || s == "mixed" || t == "mixed" {
		return true
	}
	// Union/intersection оставляем консервативно совместимыми.
	if strings.ContainsAny(s, "|&") || strings.ContainsAny(t, "|&") {
		return true
	}
	prim := func(x string) bool {
		switch x {
		case "int", "float", "string", "bool", "array", "null", "false", "true":
			return true
		}
		return false
	}
	// array ⊆ iterable (в обе стороны для ссылки).
	if (s == "array" && t == "iterable") || (s == "iterable" && t == "array") {
		return true
	}
	if prim(s) || prim(t) {
		return false
	}
	sc := vm.resolveTypeClass(s)
	tc := vm.resolveTypeClass(t)
	if sc != nil && tc != nil {
		return sc.isA(tc) || tc.isA(sc)
	}
	return true
}

// Visibility — область видимости члена класса.
type Visibility uint8

const (
	Public Visibility = iota
	Protected
	Private
)

// NewInterface создаёт интерфейс (не инстанцируется; parents — родительские интерфейсы).
func NewInterface(name string, parents ...ClassInfo) *runtimeClass {
	ci := newClassInfo(name, nil)
	ci.iface = true
	ci.ifaces = parents
	return ci
}

// Implements объявляет, что класс реализует интерфейсы (или интерфейс расширяет).
func (c *runtimeClass) Implements(is ...ClassInfo) ClassInfo {
	c.ifaces = append(c.ifaces, is...)
	return c
}

// Use примешивает трейт: копирует его методы (с видимостью) в класс, не
// перетирая уже объявленные. (Трейт — это класс-примесь.)
func (c *runtimeClass) Use(t ClassInfo) ClassInfo {
	tr, _ := t.(*runtimeClass)
	if tr != nil {
		appendUniqueTrait(&c.usedTraits, tr)
		tr.isTrait = true
	}
	if tr == nil {
		return c
	}
	for _, name := range tr.methodOrder {
		m, ok := tr.methods[name]
		if !ok {
			continue
		}
		if _, ok := c.methods[name]; ok {
			continue
		}
		// Копируем метод с перепривязкой scope: в трейте self/__CLASS__
		// указывают на класс, в который примешан трейт.
		nf := *m
		nf.Scope = c
		c.methods[name] = &nf
		appendUnique(&c.methodOrder, name)
		if tr.methVis != nil {
			if v, ok := tr.methVis[name]; ok {
				c.setMethodVis(name, v)
			}
		}
	}
	// Конструктор трейта: перекрывает унаследованный от родителя, но не
	// собственный конструктор класса (тот уже зарегистрирован через c.ctor).
	if tr.ctor != nil && c.ctor == nil {
		nf := *tr.ctor
		nf.Scope = c
		c.ctor = &nf
	}
	mixinTraitMembers(c, tr)
	// Сигнатуры трейта (абстрактные методы) — тоже контракт для класса.
	// Если метод уже реализован в иерархии (в т.ч. унаследован от родителя),
	// абстрактное требование трейта считается выполненным.
	for _, name := range tr.sigOrder {
		if _, ok := c.methods[name]; ok {
			continue
		}
		if c.hasConcreteMethod(name) {
			continue
		}
		if c.sigs == nil {
			c.sigs = map[string]Visibility{}
		}
		if _, ok := c.sigs[name]; !ok {
			v := Public
			if tr.sigs != nil {
				if vv, ok := tr.sigs[name]; ok {
					v = vv
				}
			}
			c.sigs[name] = v
			appendUnique(&c.sigOrder, name)
		}
	}
	return c
}

func appendUniqueTrait(dst *[]ClassInfo, t ClassInfo) {
	for _, x := range *dst {
		if x == t {
			return
		}
	}
	*dst = append(*dst, t)
}

func (c *runtimeClass) setMethodVis(name string, v Visibility) {
	if c.methVis == nil {
		c.methVis = map[string]Visibility{}
	}
	c.methVis[name] = v
}

// methodInfo ищет метод с учётом наследования, возвращая объявивший класс и видимость.
// После link() используется плоская таблица allMethods (O(1)); обход цепочки
// родителей остаётся как fallback для ещё не слинкованных классов.
func (c *runtimeClass) methodInfo(name string) (*Function, ClassInfo, Visibility) {
	if c.allMethods != nil {
		if m, ok := c.allMethods[name]; ok {
			return m.fn, m.decl, m.vis
		}
		if ln := strings.ToLower(name); ln != name {
			if m, ok := c.allMethods[ln]; ok {
				return m.fn, m.decl, m.vis
			}
		}
		if m, decl, vis, ok := c.methodInfoFold(name); ok {
			return m, decl, vis
		}
		return nil, nil, Public
	}
	for k := c; k != nil; k = k.parent {
		if m, ok := k.methods[name]; ok {
			v := Public
			if k.methVis != nil {
				if vv, ok := k.methVis[name]; ok {
					v = vv
				}
			}
			return m, k, v
		}
		if ln := strings.ToLower(name); ln != name {
			if m, ok := k.methods[ln]; ok {
				v := Public
				if k.methVis != nil {
					if vv, ok := k.methVis[ln]; ok {
						v = vv
					}
				}
				return m, k, v
			}
		}
	}
	if m, decl, vis, ok := c.methodInfoFold(name); ok {
		return m, decl, vis
	}
	return nil, nil, Public
}

// methodInfoFold — поиск метода с видимостью без учёта регистра (медленный
// путь для объявлений в нестандартном регистре).
func (c *runtimeClass) methodInfoFold(name string) (*Function, ClassInfo, Visibility, bool) {
	for k := c; k != nil; k = k.parent {
		for mname, m := range k.methods {
			if strings.EqualFold(mname, name) {
				v := Public
				if k.methVis != nil {
					if vv, ok := k.methVis[mname]; ok {
						v = vv
					}
				}
				return m, k, v, true
			}
		}
	}
	return nil, nil, Public, false
}

// AddSignature регистрирует сигнатуру метода без тела (метод интерфейса или
// `abstract function`). Сигнатура — контракт: видна рефлексии и участвует в
// проверке реализации, но не является вызываемой реализацией.
func (c *runtimeClass) AddSignature(name string, vis Visibility) ClassInfo {
	if c.sigs == nil {
		c.sigs = map[string]Visibility{}
	}
	c.sigs[name] = vis
	appendUnique(&c.sigOrder, name)
	return c
}

// SetSignatureFn сохраняет метаданные сигнатуры (тип возврата, параметры) для
// рефлексии: сама сигнатура остаётся невызываемой (lookupMethod её не видит).
func SetSignatureFn(ci ClassInfo, name string, fn *Function) {
	rc, ok := ci.(*runtimeClass)
	if !ok || rc == nil {
		return
	}
	if rc.sigFns == nil {
		rc.sigFns = map[string]*Function{}
	}
	rc.sigFns[name] = fn
}

// signatureFn возвращает метаданные сигнатуры name в классе ci.
func signatureFn(ci ClassInfo, name string) *Function {
	rc, ok := ci.(*runtimeClass)
	if !ok || rc == nil {
		return nil
	}
	return rc.sigFns[name]
}

// SetAbstract помечает класс abstract.
func (c *runtimeClass) SetAbstract() { c.abstract = true }

// IsAbstract сообщает, что класс объявлен abstract.
func (c *runtimeClass) IsAbstract() bool { return c != nil && c.abstract }

// ownSignature возвращает видимость сигнатуры, объявленной в самом классе.
func (c *runtimeClass) ownSignature(name string) (Visibility, bool) {
	if c == nil || c.sigs == nil {
		return Public, false
	}
	v, ok := c.sigs[name]
	return v, ok
}

// hasAbstract сообщает, что ближайшее объявление метода name в иерархии
// (родители, затем интерфейсы) — сигнатура. Конкретный метод «перекрывает»
// сигнатуру предка; сигнатура в потомке перекрывает конкретный метод предка
// (PHP запрещает такой класс как абстрактный).
func (c *runtimeClass) hasAbstract(name string) bool {
	for k := c; k != nil; k = k.parent {
		if _, ok := k.methods[name]; ok {
			return false
		}
		if name == "__construct" && k.ctor != nil {
			return false
		}
		if _, ok := k.sigs[name]; ok {
			return true
		}
	}
	// В цепочке классов объявления нет: требование может прийти из
	// реализуемого интерфейса.
	for k := c; k != nil; k = k.parent {
		for _, i := range k.ifaces {
			if i.hasAbstract(name) {
				return true
			}
		}
	}
	return false
}

// hasConcreteMethod сообщает, что в иерархии класса (сам класс и родители) есть
// конкретная реализация метода name.
func (c *runtimeClass) hasConcreteMethod(name string) bool {
	for k := c; k != nil; k = k.parent {
		if _, ok := k.methods[name]; ok {
			return true
		}
		if name == "__construct" && k.ctor != nil {
			return true
		}
	}
	return false
}

// UnimplementedPrivateAbstract возвращает нереализованные private abstract-методы
// (PHP 8.3: их обязан реализовать сам класс, а не наследник).
func (c *runtimeClass) UnimplementedPrivateAbstract() []string {
	if c == nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for k := c; k != nil; k = k.parent {
		for _, name := range k.sigOrder {
			if seen[name] {
				continue
			}
			if k.sigs[name] != Private {
				continue
			}
			if !c.hasAbstract(name) {
				continue
			}
			seen[name] = true
			out = append(out, c.Name()+"::"+name)
		}
	}
	return out
}

// reqEntry — нереализованное требование: имя метода и класс-объявитель.
type reqEntry struct {
	cls  ClassInfo
	name string
}

// UnimplementedAbstract возвращает нереализованные сигнатуры класса в порядке
// объявления, в формате "Class::method" (как в сообщении Zend).
func (c *runtimeClass) UnimplementedAbstract() []string {
	if c == nil {
		return nil
	}
	resolved := map[string]bool{} // имя объявлено (конкретно или сигнатурой)
	var req []reqEntry            // требования-сигнатуры в порядке появления
	visited := map[*runtimeClass]bool{}
	addReq := func(cls ClassInfo, name string) {
		if !resolved[name] {
			resolved[name] = true
			req = append(req, reqEntry{cls, name})
		}
	}
	for k := c; k != nil; k = k.parent {
		for _, name := range k.methodOrder {
			if _, ok := k.methods[name]; ok {
				resolved[name] = true
			}
			if name == "__construct" && k.ctor != nil {
				resolved[name] = true
			}
		}
		for _, name := range k.sigOrder {
			addReq(k, name)
		}
		for _, i := range k.ifaces {
			collectIfaceReqs(i, resolved, visited, &req)
		}
	}
	// Оставляем только те требования, что всё ещё абстрактны.
	var out []string
	for _, e := range req {
		if c.hasAbstract(e.name) {
			if e.cls != nil {
				out = append(out, e.cls.Name()+"::"+e.name)
			} else {
				out = append(out, e.name)
			}
		}
	}
	return out
}

// collectIfaceReqs собирает сигнатуры интерфейса (рекурсивно по родительским
// интерфейсам), не объявленные ранее. visited защищает от циклов в иерархии
// интерфейсов (иначе — бесконечная рекурсия / переполнение стека).
func collectIfaceReqs(i ClassInfo, resolved map[string]bool, visited map[*runtimeClass]bool, req *[]reqEntry) {
	rc, ok := i.(*runtimeClass)
	if !ok || rc == nil {
		return
	}
	if visited[rc] {
		return
	}
	visited[rc] = true
	for _, name := range rc.sigOrder {
		if !resolved[name] {
			resolved[name] = true
			*req = append(*req, reqEntry{rc, name})
		}
	}
	for _, parent := range rc.ifaces {
		collectIfaceReqs(parent, resolved, visited, req)
	}
}

// UseExcept — как Use, но пропускает методы из skip (адаптация insteadof).
func (c *runtimeClass) UseExcept(t ClassInfo, skip map[string]bool) ClassInfo {
	tr, _ := t.(*runtimeClass)
	if tr != nil {
		appendUniqueTrait(&c.usedTraits, tr)
		tr.isTrait = true
	}
	if tr == nil {
		return c
	}
	for _, name := range tr.methodOrder {
		if skip[name] || skip[strings.ToLower(name)] {
			continue
		}
		m, ok := tr.methods[name]
		if !ok {
			continue
		}
		if _, ok := c.methods[name]; ok {
			continue
		}
		// Копируем метод с перепривязкой scope: в трейте self/__CLASS__
		// указывают на класс, в который примешан трейт.
		nf := *m
		nf.Scope = c
		c.methods[name] = &nf
		appendUnique(&c.methodOrder, name)
		if tr.methVis != nil {
			if v, ok := tr.methVis[name]; ok {
				c.setMethodVis(name, v)
			}
		}
	}
	if tr.ctor != nil && c.ctor == nil {
		nf := *tr.ctor
		nf.Scope = c
		c.ctor = &nf
	}
	mixinTraitMembers(c, tr)
	for _, name := range tr.sigOrder {
		if _, ok := c.methods[name]; ok {
			continue
		}
		if c.hasConcreteMethod(name) {
			continue
		}
		if c.sigs == nil {
			c.sigs = map[string]Visibility{}
		}
		if _, ok := c.sigs[name]; !ok {
			v := Public
			if tr.sigs != nil {
				if vv, ok := tr.sigs[name]; ok {
					v = vv
				}
			}
			c.sigs[name] = v
			appendUnique(&c.sigOrder, name)
		}
	}
	return c
}

// mixinTraitMembers переносит объявленные свойства/статические свойства и
// константы трейта в класс (включая метаданные типов/readonly).
func mixinTraitMembers(c, tr *runtimeClass) {
	// Снимок собственных свойств класса ДО примешивания: свойство, объявленное
	// в классе, перекрывает одноимённое свойство трейта целиком — включая
	// атрибуты (011_inheritance: у C5::$a не должно быть атрибутов T1::$a).
	own := make(map[string]bool, len(c.ownProps))
	for n := range c.ownProps {
		own[n] = true
	}
	for _, name := range tr.propOrder {
		if _, ok := c.ownProps[name]; ok {
			continue
		}
		if pd, ok := tr.ownProps[name]; ok {
			if c.ownProps == nil {
				c.ownProps = map[string]*propDecl{}
			}
			c.ownProps[name] = pd
			appendUnique(&c.propOrder, name)
		}
		if v, ok := tr.defaults[name]; ok {
			if c.defaults == nil {
				c.defaults = map[string]Value{}
			}
			if _, exists := c.defaults[name]; !exists {
				c.defaults[name] = v
			}
		}
	}
	for _, name := range tr.staticOrder {
		if s, ok := tr.staticProps[name]; ok {
			if c.staticProps == nil {
				c.staticProps = map[string]*Slot{}
			}
			if _, exists := c.staticProps[name]; !exists {
				c.staticProps[name] = s
				appendUnique(&c.staticOrder, name)
			}
		}
	}
	for _, name := range tr.constOrder {
		if v, ok := tr.consts[name]; ok {
			if _, exists := c.consts[name]; !exists {
				c.consts[name] = v
				appendUnique(&c.constOrder, name)
			}
		}
		if doc, ok := tr.constDoc[name]; ok {
			if c.constDoc == nil {
				c.constDoc = map[string]string{}
			}
			if _, exists := c.constDoc[name]; !exists {
				c.constDoc[name] = doc
			}
		}
		if vis, ok := tr.constVis[name]; ok {
			if c.constVis == nil {
				c.constVis = map[string]Visibility{}
			}
			if _, exists := c.constVis[name]; !exists {
				c.constVis[name] = vis
			}
		}
		if at, ok := tr.constAttrs[name]; ok {
			if c.constAttrs == nil {
				c.constAttrs = map[string][]AttributeInfo{}
			}
			if _, exists := c.constAttrs[name]; !exists {
				c.constAttrs[name] = at
			}
		}
	}
	// Атрибуты методов/свойств трейта копируем в использующий класс.
	for name, at := range tr.methAttrs {
		if c.methAttrs == nil {
			c.methAttrs = map[string][]AttributeInfo{}
		}
		if _, exists := c.methAttrs[name]; !exists {
			c.methAttrs[name] = at
		}
	}
	for name, at := range tr.propAttrs {
		if own[name] {
			continue
		}
		if c.propAttrs == nil {
			c.propAttrs = map[string][]AttributeInfo{}
		}
		if _, exists := c.propAttrs[name]; !exists {
			c.propAttrs[name] = at
		}
	}
	for name, m := range tr.paramAttrs {
		if c.paramAttrs == nil {
			c.paramAttrs = map[string]map[int][]AttributeInfo{}
		}
		if _, exists := c.paramAttrs[name]; !exists {
			c.paramAttrs[name] = m
		}
	}
	// Ленивые константы/дефолты трейта: копируем и перепривязываем scope к
	// использующему классу (PHP: __CLASS__/self:: в трейте указывают на класс,
	// в который примешан трейт).
	for _, name := range tr.constOrder {
		if th, ok := tr.lazyConsts[name]; ok {
			if c.consts == nil {
				c.consts = map[string]Value{}
			}
			if _, exists := c.consts[name]; exists {
				continue
			}
			if c.lazyConsts == nil {
				c.lazyConsts = map[string]*Thunk{}
			}
			if _, exists := c.lazyConsts[name]; exists {
				continue
			}
			c.lazyConsts[name] = cloneThunk(th, c)
			appendUnique(&c.constOrder, name)
		}
	}
	for _, name := range tr.propOrder {
		if th, ok := tr.lazyDefaults[name]; ok {
			if c.lazyDefaults == nil {
				c.lazyDefaults = map[string]*Thunk{}
			}
			if _, exists := c.lazyDefaults[name]; !exists {
				c.lazyDefaults[name] = cloneThunk(th, c)
				appendUnique(&c.propOrder, name)
			}
		}
	}
	for _, name := range tr.staticOrder {
		if th, ok := tr.lazyStatics[name]; ok {
			if c.lazyStatics == nil {
				c.lazyStatics = map[string]*Thunk{}
			}
			if _, exists := c.lazyStatics[name]; !exists {
				c.lazyStatics[name] = cloneThunk(th, c)
				appendUnique(&c.staticOrder, name)
			}
		}
	}
}

// cloneThunk создаёт копию отложенного выражения с новым классом объявления.
func cloneThunk(th *Thunk, self ClassInfo) *Thunk {
	nt := *th
	nt.self = self
	if nt.fn != nil {
		nf := *nt.fn
		nf.Scope = self
		nt.fn = &nf
	}
	return &nt
}

// MethodFunction возвращает метод (реализацию) по имени.
func (c *runtimeClass) MethodFunction(name string) (*Function, bool) {
	m, _, _ := c.methodInfo(name)
	if m == nil {
		return nil, false
	}
	return m, true
}

// AliasMethod регистрирует псевдоним метода (копия с новым именем).
func (c *runtimeClass) AliasMethod(alias string, fn *Function, vis Visibility) ClassInfo {
	nf := *fn
	nf.Name = alias
	nf.Scope = c
	c.methods[alias] = &nf
	appendUnique(&c.methodOrder, alias)
	c.setMethodVis(alias, vis)
	return c
}

// SetMethodVis меняет видимость существующего метода.
func (c *runtimeClass) SetMethodVis(name string, vis Visibility) ClassInfo {
	if _, ok := c.methods[name]; ok {
		c.setMethodVis(name, vis)
	}
	return c
}

// IsEnum сообщает, что класс — перечисление.
func (c *runtimeClass) IsEnum() bool { return c != nil && c.isEnum }

// EnumBacking возвращает базовый тип backed-enum ("" для чистого).
func (c *runtimeClass) EnumBacking() string {
	if c == nil {
		return ""
	}
	return c.enumBacking
}

// SetEnumBacking помечает класс как enum.
func (c *runtimeClass) SetEnumBacking(backing string) {
	c.isEnum = true
	c.final = true
	c.enumBacking = backing
	c.enumByName = map[string]Slot{}
	c.enumByValue = map[string]Slot{}
}

// AddEnumCase создаёт объект-синглтон case и регистрирует его как константу.
func (c *runtimeClass) AddEnumCase(name string, value Value, hasValue bool, classIdx int) ClassInfo {
	o := &Object{class: c}
	if o.props == nil {
		o.props = map[string]Value{}
	}
	o.props["name"] = MakeString(name)
	if hasValue {
		var vs Slot
		SlotOf(&vs, value)
		o.props["value"] = vs
	}
	slot := c.objectSlot(o, classIdx)
	c.DefineConst(name, slot)
	c.enumCaseSlots = append(c.enumCaseSlots, slot)
	if c.enumByName == nil {
		c.enumByName = map[string]Slot{}
		c.enumByValue = map[string]Slot{}
	}
	c.enumByName[name] = slot
	if hasValue && value != nil {
		c.checkEnumCaseType(value)
		c.registerEnumValue(name, enumValueKey(value), slot)
	}
	return c
}

// checkEnumCaseType фиксирует несоответствие типа backing-значения кейса
// базовому типу перечисления (ленивая ошибка, как в Zend).
func (c *runtimeClass) checkEnumCaseType(value Value) {
	if c.enumBacking == "" || value == nil {
		return
	}
	want := c.enumBacking
	got := value.Type().String()
	if got == want {
		return
	}
	if c.enumCaseErrMsg == "" {
		c.enumCaseErrType = "TypeError"
		c.enumCaseErrMsg = "Enum case type " + got + " does not match enum backing type " + want
	}
}

// enumSlotName возвращает имя кейса по его слоту-объекту.
func enumSlotName(s Slot) string {
	if o := s.obj(); o != nil {
		if v, ok := o.props["name"]; ok {
			return v.ToString()
		}
	}
	return ""
}

// registerEnumValue учитывает backing-значение кейса и фиксирует дубликат
// (сообщение поднимается при обращении к enum, как в Zend).
func (c *runtimeClass) registerEnumValue(name, key string, slot Slot) {
	if key == "" {
		return
	}
	if c.enumByValue == nil {
		c.enumByValue = map[string]Slot{}
	}
	if prev, ok := c.enumByValue[key]; ok {
		if enumSlotName(prev) == name {
			return
		}
		if c.enumCaseErrMsg == "" {
			c.enumCaseErrType = "Error"
			c.enumCaseErrMsg = "Duplicate value in enum " + c.name + " for cases " + enumSlotName(prev) + " and " + name
		}
		return
	}
	c.enumByValue[key] = slot
}

// AddLazyEnumCase создаёт case перечисления с отложенным значением (для
// backed-enum, значение которого вычисляется лениво).
func (c *runtimeClass) AddLazyEnumCase(name string, th *Thunk, classIdx int) ClassInfo {
	o := &Object{class: c}
	o.props = map[string]Value{"name": MakeString(name)}
	slot := c.objectSlot(o, classIdx)
	c.DefineConst(name, slot)
	c.enumCaseSlots = append(c.enumCaseSlots, slot)
	if c.enumByName == nil {
		c.enumByName = map[string]Slot{}
		c.enumByValue = map[string]Slot{}
	}
	c.enumByName[name] = slot
	if c.enumCaseLazy == nil {
		c.enumCaseLazy = map[string]*Thunk{}
	}
	c.enumCaseLazy[name] = th
	return c
}

// resolveEnumValue материализует отложенное значение enum-кейса в его объект.
func (c *runtimeClass) resolveEnumValue(o *Object) (Value, bool) {
	if o == nil || c.enumCaseLazy == nil {
		return nil, false
	}
	nm, ok := o.props["name"]
	if !ok {
		return nil, false
	}
	th := c.enumCaseLazy[nm.ToString()]
	if th == nil {
		return nil, false
	}
	v, err := c.resolveThunk(th)
	if err != nil {
		return nil, false
	}
	o.props["value"] = v
	c.checkEnumCaseType(v)
	if vk := enumValueKey(v); vk != "" {
		c.registerEnumValue(nm.ToString(), vk, c.enumByName[nm.ToString()])
	}
	return v, true
}

// resolveAllLazyEnumValues материализует все отложенные значения кейсов
// (нужно для from()/tryFrom()).
func (c *runtimeClass) resolveAllLazyEnumValues() {
	if len(c.enumCaseLazy) == 0 {
		return
	}
	for _, s := range c.enumCaseSlots {
		o := s.obj()
		if o == nil {
			continue
		}
		if _, ok := o.props["value"]; ok {
			continue
		}
		c.resolveEnumValue(o)
	}
}

// EnumCases возвращает слоты case-ов в порядке объявления.
func (c *runtimeClass) EnumCases() []Slot {
	if c == nil {
		return nil
	}
	return c.enumCaseSlots
}

// enumValueKey — ключ для поиска case по значению (int/string).
func enumValueKey(v Value) string {
	switch v.Type() {
	case IntType:
		return "i:" + strconv.FormatInt(v.ToInt(), 10)
	case StringType:
		return "s:" + v.ToString()
	}
	return ""
}

// SetupEnum регистрирует встроенные методы enum: cases(), from(), tryFrom().
func (c *runtimeClass) SetupEnum() ClassInfo {
	ci := c
	ci.Method("cases", &Function{Name: "cases", NumArgs: 0, IsStatic: true, Executable: &NativeFunction{
		fn: func(ctx *Context, args []Slot) (Slot, error) {
			ci.markAllEnumCases()
			arr := NewArray()
			for _, s := range ci.enumCaseSlots {
				arr.Append(s)
			}
			return MakeArray(arr), nil
		},
	}})
	if ci.enumBacking == "" {
		return ci
	}
	coerce := func(fnName string, v Slot) (Slot, error) {
		if ci.enumBacking == "int" {
			if v.typ == IntType {
				return v, nil
			}
			if v.typ == StringType && IsNumericString(v.StringVal()) {
				return MakeInt(stringToInt(v.StringVal())), nil
			}
			return Slot{}, NewThrowable("TypeError", ArgTypeError(ci.name+"::"+fnName, 1, "value", "int", v.typ.String(), "", 0))
		}
		// string
		if v.typ == StringType {
			return v, nil
		}
		if v.typ == IntType || v.typ == FloatType || v.typ == BoolType {
			return MakeString(v.ToString()), nil
		}
		return Slot{}, NewThrowable("TypeError", ArgTypeError(ci.name+"::"+fnName, 1, "value", "string", v.typ.String(), "", 0))
	}
	mkFrom := func(name string, nullable bool) *Function {
		return &Function{Name: name, NumArgs: 1, IsStatic: true, Executable: &NativeFunction{
			fn: func(ctx *Context, args []Slot) (Slot, error) {
				var arg Slot
				if len(args) > 0 {
					arg = deref(args[0])
				}
				cv, err := coerce(name, arg)
				if err != nil {
					return Slot{}, err
				}
				ci.resolveAllLazyEnumValues()
				if ci.enumCaseErrMsg != "" {
					return Slot{}, NewThrowable(ci.enumCaseErrType, ci.enumCaseErrMsg)
				}
				ci.markAllEnumCases()
				if s, ok := ci.enumByValue[enumValueKey(cv)]; ok {
					return s, nil
				}
				if nullable {
					return MakeNull(), nil
				}
				return Slot{}, NewThrowable("ValueError", enumValueDisplay(cv)+" is not a valid backing value for enum "+ci.name)
			},
		}}
	}
	ci.Method("from", mkFrom("from", false))
	ci.Method("tryFrom", mkFrom("tryFrom", true))
	return ci
}

// enumValueDisplay форматирует значение для сообщений enum.
func enumValueDisplay(v Slot) string {
	if v.typ == StringType {
		return "\"" + v.StringVal() + "\""
	}
	return v.ToString()
}

// InstanceOfName проверяет `$obj instanceof $name` с динамическим именем.
func (vm *VM) InstanceOfName(obj Slot, name string) bool {
	ci := vm.classInfo(obj)
	if ci == nil {
		return false
	}
	name = strings.TrimLeft(name, "\\")
	// Неявный Stringable (__toString, в т.ч. из трейта).
	if strings.EqualFold(name, "Stringable") && ci.lookupMethod("__toString") != nil {
		return true
	}
	target := vm.FindClass(name)
	if target == nil {
		return false
	}
	return ci.IsA(target)
}

// scopeDesc форматирует часть сообщения про область видимости.
func scopeDesc(scope ClassInfo) string {
	if scope == nil {
		return "global scope"
	}
	return "scope " + scope.Name()
}

// propAccessErr проверяет видимость объявленного свойства.
func (vm *VM) propAccessErr(pd *propDecl, decl ClassInfo, name string) error {
	if pd == nil || decl == nil {
		return nil
	}
	scope := vm.ctx.self
	switch pd.vis {
	case Private:
		if scope != decl {
			return NewThrowable("Error", "Cannot access private property "+classDisplayName(decl)+"::$"+name)
		}
	case Protected:
		if !canAccess(Protected, decl, scope) {
			return NewThrowable("Error", "Cannot access protected property "+classDisplayName(decl)+"::$"+name)
		}
	}
	return nil
}

// staticPropAccessErr проверяет видимость объявленного статического свойства
// (объявление ищется вверх по цепочке наследования, как в Zend).
func (vm *VM) staticPropAccessErr(cls ClassInfo, name string) error {
	rc, ok := cls.(*runtimeClass)
	if !ok {
		return nil
	}
	for k := rc; k != nil; k = k.parent {
		if pd, ok := k.ownProps[name]; ok && pd.static {
			return vm.propAccessErr(pd, k, name)
		}
	}
	return nil
}

// staticUninitErr возвращает ошибку обращения к незаинициализированному
// типизированному статическому свойству.
func staticUninitErr(cls ClassInfo, name string) error {
	return NewThrowable("Error", "Typed static property "+cls.Name()+"::$"+name+" must not be accessed before initialization")
}

// coercePropValue проверяет/приводит значение к объявленному типу свойства
// (типизированные свойства). Возвращает значение для записи.
//
// Если v — ссылка (reference held by property), проверка идёт по правилам Zend
// zend_verify_prop_assignable_by_ref: значение проверяется против целевого типа
// без приведения, а при успешной привязке (`=&`, refAdopt) на ячейку
// добавляется источник этого свойства. Для ссылки без источников значение
// приводится НА МЕСТЕ (слабо; zend_verify_scalar_type_hint).
func (vm *VM) coercePropValue(pd *propDecl, decl ClassInfo, name string, v Slot, strict bool, refAdopt bool) (Slot, error) {
	if pd == nil || !pd.hasType || strings.EqualFold(pd.typ, "mixed") {
		return v, nil
	}
	if v.typ == RefType {
		r := v.Ref()
		if r == nil {
			return v, nil
		}
		val := deref(v)
		if r.hasSources() {
			// Ссылка уже «held by» типизированного свойства. zend_verify_
			// prop_assignable_by_ref: значение обязано подходить целевому типу
			// БЕЗ приведения (общая ссылка не должна молча меняться).
			res := vm.typeAssignableResult(pd.typ, val, strict)
			if res > 0 {
				if refAdopt {
					r.addSource(guardFor(pd, decl, name))
				}
				return v, nil
			}
			if res < 0 {
				// Значение можно было бы привести, но ссылку держат другие
				// свойства: «Reference with value of type ... is not
				// compatible with property ...».
				if ok, _ := vm.coerceReturnModeEx(pd.typ, val, false, true); ok {
					return v, vm.refTypeErrorType(r.guard(), pd, decl, name, val)
				}
			}
			kind := valueTypeName(vm, val)
			return v, vm.propTypeError(kind, classDisplayName(decl), name, vm.displayTypeResolved(pd.typ), false)
		}
		if ok, out := vm.coerceReturnModeEx(pd.typ, val, strict, true); ok {
			// Значение приводится НА МЕСТЕ (Zend меняет ячейку ссылки), затем
			// свойство становится её источником.
			vm.deprecatePropFloatToInt(val, out)
			r.v = out
			if g := guardFor(pd, decl, name); g != nil {
				r.addSource(g)
			}
			return v, nil
		}
		kind := valueTypeName(vm, val)
		// Свежая ссылка, присваиваемая свойству через `=&` (refAdopt), не
		// считается «held by»: Zend использует обычную формулировку. Прямая
		// запись значения в ячейку-ссылку свойства (array_walk и т.п.) —
		// «reference held by property».
		return v, vm.propTypeError(kind, classDisplayName(decl), name, vm.displayTypeResolved(pd.typ), !refAdopt)
	}
	if ok, out := vm.coerceReturnMode(pd.typ, v, strict); ok {
		vm.deprecatePropFloatToInt(v, out)
		return out, nil
	}
	kind := valueTypeName(vm, v)
	return v, vm.propTypeError(kind, classDisplayName(decl), name, vm.displayTypeResolved(pd.typ), false)
}

// typeExactAccepts — тип содержит тип значения БЕЗ приведения: int не входит в
// float (Zend i_zend_verify_type_assignable_zval: ZEND_TYPE_CONTAINS_CODE).
func (vm *VM) typeExactAccepts(t string, s Slot) bool {
	if !vm.typeAccepts(t, s) {
		return false
	}
	if s.typ != IntType {
		return true
	}
	// int принимается «точно» только там, где в типе есть компонент int
	// (или mixed): для float Zend выполнит SSTH-приведение (result -1).
	return typeHasIntPart(t)
}

// typeHasIntPart — содержит ли объявленный тип компонент int.
func typeHasIntPart(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" || t == "mixed" {
		return true
	}
	if strings.HasPrefix(t, "?") {
		return typeHasIntPart(t[1:])
	}
	if strings.HasPrefix(t, "(") {
		t = strings.TrimSuffix(strings.TrimPrefix(t, "("), ")")
	}
	if i := topLevelSplit(t, '|'); i >= 0 {
		return typeHasIntPart(t[:i]) || typeHasIntPart(t[i+1:])
	}
	if i := topLevelSplit(t, '&'); i >= 0 {
		return typeHasIntPart(t[:i]) && typeHasIntPart(t[i+1:])
	}
	return t == "int"
}

// typeHasFloatPart — содержит ли объявленный тип компонент float (для strict
// SSTH: int -> float разрешён как -1).
func typeHasFloatPart(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" || t == "mixed" {
		return true
	}
	if strings.HasPrefix(t, "?") {
		return typeHasFloatPart(t[1:])
	}
	if strings.HasPrefix(t, "(") {
		t = strings.TrimSuffix(strings.TrimPrefix(t, "("), ")")
	}
	if i := topLevelSplit(t, '|'); i >= 0 {
		return typeHasFloatPart(t[:i]) || typeHasFloatPart(t[i+1:])
	}
	if i := topLevelSplit(t, '&'); i >= 0 {
		// Пересечение — только классовые типы.
		return false
	}
	return t == "float"
}

// typeAssignableResult — аналог i_zend_verify_type_assignable_zval:
// 1 — значение подходит без приведения; -1 — возможно слабое скалярное
// приведение; 0 — присваивание недопустимо.
func (vm *VM) typeAssignableResult(t string, v Slot, strict bool) int {
	if vm.typeExactAccepts(t, v) {
		return 1
	}
	if strict {
		if v.typ == IntType && typeHasFloatPart(t) {
			return -1
		}
		return 0
	}
	if v.typ == NullType {
		return 0
	}
	if ok, _ := vm.coerceReturnModeEx(t, v, false, true); ok {
		return -1
	}
	return 0
}

// verifyRefAssignable проверяет значение v (не ссылку) против ВСЕХ источников
// ссылки r и выбирает результат приведения (Zend
// zend_verify_ref_assignable_zval). Возвращает значение для записи в ячейку.
func (vm *VM) verifyRefAssignable(r *Ref, v Slot, strict bool) (Slot, error) {
	var first *propTypeGuard
	var coerced Slot
	coercedValid := false
	for _, src := range r.sources {
		res := vm.typeAssignableResult(src.typ, v, strict)
		if res == 0 {
			return v, vm.propTypeError(valueTypeName(vm, v), classDisplayName(src.decl), src.name, vm.displayTypeResolved(src.typ), true)
		}
		if res < 0 {
			if first == nil {
				first = src
				ok, out := vm.coerceReturnModeEx(src.typ, v, false, true)
				if !ok {
					return v, vm.propTypeError(valueTypeName(vm, v), classDisplayName(src.decl), src.name, vm.displayTypeResolved(src.typ), true)
				}
				coerced = out
				coercedValid = true
			} else if !coercedValid {
				// Предыдущее свойство принимало значение без приведения, это —
				// требует: результаты несовместимы.
				return v, vm.conflictingCoercionError(first, src, v)
			} else {
				ok, tmp := vm.coerceReturnModeEx(src.typ, v, false, true)
				if !ok {
					return v, vm.propTypeError(valueTypeName(vm, v), classDisplayName(src.decl), src.name, vm.displayTypeResolved(src.typ), true)
				}
				if !identical(coerced, tmp) {
					return v, vm.conflictingCoercionError(first, src, v)
				}
			}
		} else {
			if first == nil {
				first = src
			} else if coercedValid {
				// Предыдущее свойство требовало приведения, это — нет.
				return v, vm.conflictingCoercionError(first, src, v)
			}
		}
	}
	if coercedValid {
		return coerced, nil
	}
	return v, nil
}

// verifyRefArrayAssignable проверяет, что все источники ссылки допускают
// массив (Zend zend_verify_ref_array_assignable): при авто-вивификации
// массива через ссылку на null/false/undef иначе — auto-init ошибка.
func (vm *VM) verifyRefArrayAssignable(r *Ref) error {
	for _, src := range r.sources {
		if !typeAllowsArray(src.typ) {
			return NewThrowable("TypeError", "Cannot auto-initialize an array inside a reference held by property "+
				classDisplayName(src.decl)+"::$"+src.name+" of type "+vm.displayTypeResolved(src.typ))
		}
	}
	return nil
}

// typeAllowsArray сообщает, входит ли array в объявленный тип.
func typeAllowsArray(t string) bool {
	t = strings.TrimSpace(t)
	if t == "" || t == "mixed" || t == "iterable" {
		return true
	}
	if strings.HasPrefix(t, "?") {
		return typeAllowsArray(t[1:])
	}
	if strings.HasPrefix(t, "(") {
		t = strings.TrimSuffix(strings.TrimPrefix(t, "("), ")")
	}
	if i := topLevelSplit(t, '|'); i >= 0 {
		return typeAllowsArray(t[:i]) || typeAllowsArray(t[i+1:])
	}
	if i := topLevelSplit(t, '&'); i >= 0 {
		return typeAllowsArray(t[:i]) && typeAllowsArray(t[i+1:])
	}
	return t == "array"
}

// autoInitPropError — TypeError «Cannot auto-initialize an array inside
// property ...» (Zend zend_throw_auto_init_in_prop_error).
func (vm *VM) autoInitPropError(pd *propDecl, decl ClassInfo, name string) *Throwable {
	return NewThrowable("TypeError", "Cannot auto-initialize an array inside property "+
		classDisplayName(decl)+"::$"+name+" of type "+vm.displayTypeResolved(pd.typ))
}

// dimWritePropCheck — проверки Zend ZEND_FETCH_DIM_WRITE для базы-свойства:
// если значение авто-вивифицируется в массив (null/false/undef), а тип свойства
// не допускает array — auto-init TypeError. Для ячейки-ссылки с источниками
// проверяются все источники (zend_verify_ref_array_assignable). hasVal=false —
// свойство не инициализировано.
func (vm *VM) dimWritePropCheck(pd *propDecl, decl ClassInfo, name string, v Slot, hasVal bool) error {
	if hasVal && v.typ == RefType {
		r := v.Ref()
		if r == nil {
			return nil
		}
		if !promotesToArray(deref(v)) {
			return nil
		}
		if pd != nil && pd.hasType && !typeAllowsArray(pd.typ) {
			return vm.autoInitPropError(pd, decl, name)
		}
		if r.hasSources() {
			return vm.verifyRefArrayAssignable(r)
		}
		return nil
	}
	if !hasVal || promotesToArray(v) {
		if pd != nil && pd.hasType && !typeAllowsArray(pd.typ) {
			return vm.autoInitPropError(pd, decl, name)
		}
	}
	return nil
}

// dimWriteStaticCheck — как dimWritePropCheck, но для статического свойства.
func (vm *VM) dimWriteStaticCheck(cls ClassInfo, name string, v Slot, hasVal bool) error {
	rc, ok := cls.(*runtimeClass)
	if !ok {
		return nil
	}
	pd, dc := rc.propDeclOf(name)
	if hasVal && v.typ == RefType {
		r := v.Ref()
		if r == nil {
			return nil
		}
		if !promotesToArray(deref(v)) {
			return nil
		}
		if pd != nil && pd.hasType && !typeAllowsArray(pd.typ) {
			return vm.autoInitPropError(pd, dc, name)
		}
		if r.hasSources() {
			return vm.verifyRefArrayAssignable(r)
		}
		return nil
	}
	if !hasVal || promotesToArray(v) {
		if pd != nil && pd.hasType && !typeAllowsArray(pd.typ) {
			return vm.autoInitPropError(pd, dc, name)
		}
	}
	return nil
}

// promotesToArray — значение авто-вивифицируется в массив (null/false).
func promotesToArray(v Slot) bool {
	switch v.typ {
	case NullType:
		return true
	case BoolType:
		return !v.ToBool()
	}
	return false
}

// refTypeErrorType — TypeError «Reference with value of type ... held by
// property ... is not compatible with property ...» (zend_throw_ref_type_error_type).
func (vm *VM) refTypeErrorType(src *propTypeGuard, pd *propDecl, decl ClassInfo, name string, val Slot) *Throwable {
	srcDecl, srcName, srcTyp := "", "", ""
	if src != nil {
		srcDecl = classDisplayName(src.decl)
		srcName = src.name
		srcTyp = vm.displayTypeResolved(src.typ)
	}
	return NewThrowable("TypeError", "Reference with value of type "+valueTypeName(vm, val)+
		" held by property "+srcDecl+"::$"+srcName+" of type "+srcTyp+
		" is not compatible with property "+classDisplayName(decl)+"::$"+name+
		" of type "+vm.displayTypeResolved(pd.typ))
}

// conflictingCoercionError — TypeError «Cannot assign ... to reference held by
// property ... and property ..., as this would result in an inconsistent type
// conversion» (zend_throw_conflicting_coercion_error).
func (vm *VM) conflictingCoercionError(p1, p2 *propTypeGuard, v Slot) *Throwable {
	return NewThrowable("TypeError", "Cannot assign "+valueTypeName(vm, v)+
		" to reference held by property "+classDisplayName(p1.decl)+"::$"+p1.name+" of type "+vm.displayTypeResolved(p1.typ)+
		" and property "+classDisplayName(p2.decl)+"::$"+p2.name+" of type "+vm.displayTypeResolved(p2.typ)+
		", as this would result in an inconsistent type conversion")
}

// deprecatePropFloatToInt эмитит Deprecated о неявном float->int при записи в
// типизированное свойство (PHP 8.1+).
func (vm *VM) deprecatePropFloatToInt(in, out Slot) {
	if in.typ == RefType {
		in = deref(in)
	}
	if out.typ == IntType {
		vm.floatToIntWarn(in)
	}
}

// writeRef записывает значение в ячейку ссылки, применяя guard типизированного
// свойства (если он есть).
func (vm *VM) writeRef(r *Ref, val Slot, strict bool) error {
	if r == nil {
		return nil
	}
	if !r.hasSources() {
		// Новое значение устанавливается ДО списания старого: если списание
		// запускает вложенный __destruct (destroyDroppedNow), проверка
		// достижимости должна видеть уже обновлённый граф (иначе объект,
		// снятый этой же записью, считался бы достижимым через ячейку).
		old := r.v
		r.v = val
		vm.noteStored(val)
		vm.noteDropped(old)
		vm.noteRefArrayCycle(val, r)
		if old != val {
			vm.nestedDropped(old)
			vm.droppedObjUrgent(old)
		}
		return nil
	}
	val2 := val
	if val.typ == RefType {
		val2 = deref(val)
	}
	out, verr := vm.verifyRefAssignable(r, val2, strict)
	if verr != nil {
		return verr
	}
	vm.deprecatePropFloatToInt(val2, out)
	old := r.v
	r.v = out
	vm.noteStored(out)
	vm.noteDropped(old)
	if old != out {
		vm.nestedDropped(old)
		vm.droppedObjUrgent(old)
	}
	return nil
}

// detachRefGuard снимает guard типизированного свойства pd/decl/name с прежней
// ячейки-ссылки при перепривязке свойства к новой ячейке (`$p =& $other`).
// Ссылка, оставшаяся в переменной, после этого больше не проверяется типом
// этого свойства (Zend ZEND_REF_DEL_TYPE_SOURCE).
func (vm *VM) detachRefGuard(oldSlot, newSlot Slot, decl ClassInfo, name string) {
	if oldSlot.typ != RefType {
		return
	}
	old := oldSlot.Ref()
	if old == nil {
		return
	}
	if newSlot.typ == RefType && newSlot.Ref() == old {
		return
	}
	old.delSource(decl, name)
}

// detachPropRefGuard снимает guard типизированного свойства с ячейки-ссылки,
// лежащей в свойстве (вызывается при unset свойства). pname — имя без мангла,
// key — ключ хранения.
func (vm *VM) detachPropRefGuard(recv Slot, pname, key string) {
	ci := vm.classInfo(recv)
	if ci == nil {
		return
	}
	if s, ok := vm.getProp(recv, key); ok {
		if sl := valueSlot(s); sl.typ == RefType {
			if r := sl.Ref(); r != nil {
				if pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, pname); ok && pd != nil && pd.hasType {
					r.delSource(decl, pname)
				}
			}
		}
	}
}

// writeRefPropValue выполняет прямую запись в свойство, чья ячейка уже является
// ссылкой. Если свойство само типизировано, Zend использует «plain»-формулировку
// ошибки («property», без «reference held by»), сохраняя ячейку-ссылку. Если
// свойство не типизировано (ссылка ведёт на типизированное свойство), проверка
// идёт через guard ссылки.
func (vm *VM) writeRefPropValue(ci ClassInfo, pname string, r *Ref, val Slot, strict bool) (Slot, error) {
	if pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname); declared && pd != nil && pd.hasType {
		out, verr := vm.coercePropValue(pd, decl, pname, deref(val), strict, false)
		if verr != nil {
			return Slot{}, verr
		}
		// Zend zend_assign_to_typed_prop: после проверки по объявленному типу
		// самого свойства запись идёт zend_assign_to_variable_ex, который для
		// ячейки-ссылки с источниками применяет zend_assign_to_typed_ref —
		// значение проверяется ещё и против ВСЕХ источников ссылки.
		if r.hasSources() {
			out2, verr2 := vm.verifyRefAssignable(r, out, strict)
			if verr2 != nil {
				return Slot{}, verr2
			}
			out = out2
		}
		old := r.v
		r.v = out
		vm.noteStored(out)
		vm.noteDropped(old)
		if old != out {
			vm.nestedDropped(old)
			vm.droppedObjUrgent(old)
		}
		return out, nil
	}
	if werr := vm.writeRef(r, val, strict); werr != nil {
		return Slot{}, werr
	}
	return r.v, nil
}

// SetRefChecked записывает значение в ref-ячейку, применяя guard
// типизированного свойства (аналог обычной записи в переменную/свойство).
// Используется встроенными (settype), которые пишут через by-ref аргумент.
func (vm *VM) SetRefChecked(r *Ref, val Slot) error {
	return vm.writeRef(r, val, vm.chunk != nil && vm.chunk.Strict)
}

// SetRefCheckedStrict — строгая запись в ref-ячейку (ZEND_CATCH): без приведения
// значения к типам источников.
func (vm *VM) SetRefCheckedStrict(r *Ref, val Slot) error {
	return vm.writeRef(r, val, true)
}

// propSetExisting записывает значение в уже существующее свойство (не
// объявленное типизированным). Если свойство — ссылка, запись идёт сквозь неё.
// handled=false — свойства нет.
func (vm *VM) propSetExisting(o Slot, pname string, val Slot) (bool, error) {
	if v, ok := vm.getProp(o, pname); ok {
		s := valueSlot(v)
		if s.typ == RefType {
			if r := s.Ref(); r != nil {
				return true, vm.writeRef(r, val, vm.chunk != nil && vm.chunk.Strict)
			}
		}
		vm.setProp(o, pname, val)
		return true, nil
	}
	return false, nil
}

// propRefCell возвращает ячейку-ссылку свойства recv->pname, создавая её (и
// сохраняя в объекте) при необходимости. Для типизированного свойства ячейка
// получает guard.
// propRefOverloaded сообщает, что свойство читается/пишется через __get
// (реального свойства нет). Второй результат — __get объявлен с возвратом по
// ссылке (тогда модификация проходит через возвращённую ячейку).
// magicAccess: недоступное из текущей области объявленное свойство тоже
// считается overloaded (нужно write-контексту: magic_get_set, bug37667); для
// ссылочного чтения (OpPropRef) такой случай обязан давать ошибку доступа.
func (vm *VM) propRefOverloaded(ci ClassInfo, o Slot, pname string, magicAccess bool) (bool, bool) {
	m := ci.lookupMethod("__get")
	if m == nil {
		return false, false
	}
	pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, pname)
	key := vm.propKey(pd, decl, pname)
	inaccessible := false
	if declared {
		if ob := vm.objectOf(o); ob != nil && ob.IsUnset(key) {
			declared = false
		} else if magicAccess && vm.propAccessErr(pd, decl, pname) != nil {
			// Недоступное из текущей области свойство читается через __get
			// (Zend magic_get_set), даже если запись в хранилище есть.
			declared = false
			inaccessible = true
		}
	}
	if inaccessible {
		return true, m.ByRefReturn
	}
	if declared {
		return false, m.ByRefReturn
	}
	if _, ok := vm.getProp(o, key); ok {
		return false, m.ByRefReturn
	}
	return true, m.ByRefReturn
}

// magicGetValue вызывает __get и проверяет результат (общая часть для
// ссылочных выборок свойства).
func (vm *VM) magicGetValue(ci ClassInfo, o Slot, pname string) (Slot, error) {
	res, ok, err := vm.callMagicMethod(ci, o, "__get", []Slot{MakeString(pname)})
	if err != nil {
		return Slot{}, err
	}
	if !ok {
		return MakeNull(), nil
	}
	res, gerr := vm.checkMagicGet(ci, pname, res)
	if gerr != nil {
		return Slot{}, gerr
	}
	return res, nil
}

func (vm *VM) propRefCell(recv Slot, pname string) (Slot, error) {
	ci := vm.classInfo(recv)
	if ci == nil {
		return MakeRef(&Ref{v: MakeNull()}), nil
	}
	if ci.IsEnum() {
		return Slot{}, vm.enumWriteError(ci, recv, pname)
	}
	if overloaded, byRef := vm.propRefOverloaded(ci, recv, pname, false); overloaded {
		res, err := vm.magicGetValue(ci, recv, pname)
		if err != nil {
			return Slot{}, err
		}
		if byRef {
			if res.typ == RefType {
				return res, nil
			}
			return MakeRef(&Ref{v: deref(res)}), nil
		}
		// Косвенная модификация свойства из __get без ссылки: Notice и
		// отделённая ячейка (запись через неё объект не затрагивает).
		vm.Notice("Indirect modification of overloaded property " + classDisplayName(ci) + "::$" + pname + " has no effect")
		return MakeRef(&Ref{v: deref(res)}), nil
	}
	if pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, pname); ok && pd != nil {
		if err := vm.propAccessErr(pd, decl, pname); err != nil {
			// Недоступное объявленное свойство + __get: Zend не выдаёт
			// реальную ячейку, а берёт значение __get (Zend
			// fetch_property_address).
			if cell, _, mok, merr := vm.magicRefCell(ci, recv, pname); mok {
				if merr != nil {
					return Slot{}, merr
				}
				return cell, nil
			}
			return Slot{}, err
		}
		key := vm.propKey(pd, decl, pname)
		if pd.readonly {
			if _, found := vm.getProp(recv, key); found {
				return Slot{}, NewThrowable("Error", "Cannot modify readonly property "+classDisplayName(decl)+"::$"+pname)
			}
			return Slot{}, NewThrowable("Error", "Cannot indirectly modify readonly property "+decl.Name()+"::$"+pname)
		}
		if v, found := vm.getProp(recv, key); found {
			s := valueSlot(v)
			if s.typ == RefType {
				if s.Ref().hasSources() {
					vm.markTypedRefHolder(recv)
				}
				return s, nil
			}
			cell := MakeRef(&Ref{v: s})
			if g := guardFor(pd, decl, pname); g != nil {
				cell.Ref().addSource(g)
				vm.markTypedRefHolder(recv)
			}
			vm.setProp(recv, key, cell)
			return cell, nil
		}
		if pd.hasType {
			if typeAllowsNull(pd.typ) {
				// ?T при взятии по ссылке инициализируется null (Zend).
				cell := MakeRef(&Ref{v: MakeNull()})
				if g := guardFor(pd, decl, pname); g != nil {
					cell.Ref().addSource(g)
					vm.markTypedRefHolder(recv)
				}
				vm.setProp(recv, key, cell)
				return cell, nil
			}
			// Свойство снято через unset(): по ссылке Zend уходит в &__get()
			// (по значению — тоже, но там результат копируется). Если геттер
			// возвращает ссылку, её значение приводится к типу свойства «на
			// месте» (Zend: &$obj->val меняет сам источник ссылки), сама
			// ячейка свойства остаётся unset.
			if ob := vm.objectOf(recv); ob != nil && ob.IsUnset(key) {
				res, ok, merr := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)})
				if merr != nil {
					return Slot{}, merr
				}
				if ok {
					res, gerr := vm.checkMagicGet(ci, pname, res)
					if gerr != nil {
						return Slot{}, gerr
					}
					if res.IsRef() {
						return res, nil
					}
					// Геттер вернул значение (не ссылку): запись через цель
					// цикла ни на что не влияет (Zend: notice), но значение
					// доступно по ссылке.
					vm.Notice("Indirect modification of overloaded property " + classDisplayName(ci) + "::$" + pname + " has no effect")
					return MakeRef(&Ref{v: res}), nil
				}
			}
			return Slot{}, NewThrowable("Error", "Cannot access uninitialized non-nullable property "+classDisplayName(decl)+"::$"+pname+" by reference")
		}
	}
	if v, ok := vm.getProp(recv, pname); ok {
		s := valueSlot(v)
		if s.typ == RefType {
			return s, nil
		}
		cell := MakeRef(&Ref{v: s})
		vm.setProp(recv, pname, cell)
		return cell, nil
	}
	// Необъявленное свойство с __get: Zend не создаёт динамическое свойство,
	// а берёт значение __get.
	if cell, _, mok, merr := vm.magicRefCell(ci, recv, pname); mok {
		if merr != nil {
			return Slot{}, merr
		}
		return cell, nil
	}
	// Создание нового динамического свойства по ссылке: те же ограничения,
	// что и у обычной записи (WeakReference и т.п.).
	if err := checkDynamicProp(ci, pname); err != nil {
		return Slot{}, err
	}
	cell := MakeRef(&Ref{v: MakeNull()})
	vm.setProp(recv, pname, cell)
	return cell, nil
}

// magicRefCell берёт ячейку свойства через __get при недоступном/необъявленном
// свойстве (BP_VAR_W/REF). By-ref __get возвращает реальную ячейку (real=true);
// by-value — значение, а Zend предупреждает «Indirect modification of overloaded
// property ... has no effect» (zend_fetch_property_address).
func (vm *VM) magicRefCell(ci ClassInfo, recv Slot, pname string) (cell Slot, real bool, ok bool, err error) {
	g, mok, merr := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)})
	if !mok {
		return Slot{}, false, false, nil
	}
	if merr != nil {
		return Slot{}, false, true, merr
	}
	if g.typ == RefType {
		return g, true, true, nil
	}
	vm.Notice("Indirect modification of overloaded property " + ci.Name() + "::$" + pname + " has no effect")
	return MakeRef(&Ref{v: valueSlot(g)}), false, true, nil
}

// markTypedRefHolder отмечает объект как держащий типизированную ссылку.
func (vm *VM) markTypedRefHolder(recv Slot) {
	if ob := vm.objectOf(recv); ob != nil {
		ob.MarkTypedRef()
	}
}

// checkMagicGet проверяет/приводит результат __get() к типу неинициализированного
// типизированного свойства (PHP: "Value of type ... returned from C::__get() ...").
func (vm *VM) checkMagicGet(ci ClassInfo, name string, res Slot) (Slot, error) {
	pd, _, ok := vm.scopedProp(vm.ctx.self, ci, name)
	if !ok || pd == nil || !pd.hasType {
		return res, nil
	}
	strict := vm.chunk != nil && vm.chunk.Strict
	val := deref(res)
	if okc, out := vm.coerceReturnMode(pd.typ, val, strict); okc {
		if res.IsRef() {
			if r := res.Ref(); r != nil {
				r.v = out
			}
			return res, nil
		}
		return out, nil
	}
	kind := valueTypeName(vm, val)
	return res, NewThrowable("TypeError", "Value of type "+kind+" returned from "+
		classDisplayName(ci)+"::__get() must be compatible with unset property "+
		classDisplayName(ci)+"::$"+name+" of type "+vm.displayTypeResolved(pd.typ))
}

// magicGetInaccessible: если объявленное свойство недоступно из текущей
// области видимости и у класса есть __get, вызывает его (Zend-семантика).
// Возвращает (res, handled, ok, err): handled=false — свойство доступно/не
// объявлено, обработчик продолжает обычный путь.
func (vm *VM) magicGetInaccessible(ci ClassInfo, recv Slot, pname string) (Slot, bool, bool, error) {
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, pname)
	if !ok {
		return Slot{}, false, false, nil
	}
	if vm.propAccessErr(pd, decl, pname) == nil {
		return Slot{}, false, false, nil
	}
	res, mok, err := vm.callMagicMethod(ci, recv, "__get", []Slot{MakeString(pname)})
	return res, true, mok, err
}

// IncDecTypedProp выполняет ++/-- над свойством объекта с Zend-семантикой
// переполнения типизированных int-свойств. cur — уже прочитанное значение
// свойства (может быть ссылкой с guard). При переполнении int и типе, не
// допускающем float, поднимается TypeError "past its maximal/minimal value";
// иначе результат становится float. Возвращает новое значение.
func (vm *VM) IncDecTypedProp(o Slot, name string, cur Slot, dec bool) (Slot, error) {
	ci := vm.classInfo(o)
	if ci == nil {
		return Slot{}, errNotObject
	}
	pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, name)
	old := deref(cur)
	dir, lim := "increment", "maximal"
	if dec {
		dir, lim = "decrement", "minimal"
	}
	var newVal Slot
	overflow := false
	nv, ovf, cerr := vm.incDecCompute(old, dec)
	if cerr != nil {
		return Slot{}, cerr
	}
	newVal, overflow = nv, ovf
	// Метаданные «держателя» ссылки: guarded-ссылка важнее собственного
	// объявления свойства (динамическое свойство, ссылающееся на типизированное).
	holder, holderName, holderType := "", name, ""
	isRef := false
	if cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if g := r.guard(); g != nil {
				isRef = true
				holder = classDisplayName(g.decl)
				holderName = g.name
				holderType = vm.displayTypeResolved(g.typ)
			}
		}
	}
	if holderType == "" && declared && pd != nil && pd.hasType {
		holder = classDisplayName(decl)
		holderName = name
		holderType = vm.displayTypeResolved(pd.typ)
	}
	if overflow {
		allowFloat := false
		if holderType != "" {
			// Строгая проверка: float допустим только если объявленный тип
			// действительно включает float (не приводим float->int).
			if ok, _ := vm.coerceReturnMode(holderType, MakeFloat(1), true); ok {
				allowFloat = true
			}
		}
		if !allowFloat {
			if holderType != "" {
				kind := "property"
				if isRef {
					kind = "a reference held by property"
				}
				return Slot{}, NewThrowable("TypeError",
					"Cannot "+dir+" "+kind+" "+holder+"::$"+holderName+" of type "+
						holderType+" past its "+lim+" value")
			}
		}
		if dec {
			newVal = MakeFloat(float64(math.MinInt64))
		} else {
			newVal = MakeFloat(float64(math.MaxInt64))
		}
	}
	strict := vm.argStrict()
	// Запись результата — как в OpPropSet: для объявленного свойства с
	// проверкой типа, для ссылки сквозь её (с guard), для динамического —
	// __set.
	if cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if werr := vm.writeRef(r, newVal, strict); werr != nil {
				return Slot{}, werr
			}
			return r.v, nil
		}
		return newVal, nil
	}
	if declared {
		if err := vm.checkPropWrite(ci, o, name); err != nil {
			return Slot{}, err
		}
		val, verr := vm.coercePropValue(pd, decl, name, newVal, strict, false)
		if verr != nil {
			return Slot{}, verr
		}
		key := vm.propKey(pd, decl, name)
		if ob := vm.objectOf(o); ob != nil {
			ob.ClearUnset(key)
		}
		vm.setProp(o, key, val)
		return val, nil
	}
	if handled, herr := vm.propSetExisting(o, name, newVal); handled {
		if herr != nil {
			return Slot{}, herr
		}
		return newVal, nil
	}
	if _, ok, merr := vm.callMagicMethod(ci, o, "__set", []Slot{MakeString(name), newVal}); ok {
		return newVal, merr
	}
	if err := checkDynamicProp(ci, name); err != nil {
		return Slot{}, err
	}
	vm.setProp(o, name, newVal)
	return newVal, nil
}

// IncDecRef выполняет ++/-- над значением, прочитанным из ссылки (возможно, с
// guard типизированного свойства). Если ссылка guarded и переполнение int не
// допускается типом guard, поднимает TypeError. При наличии ссылки записывает
// новое значение в её ячейку. Возвращает новое значение.
func (vm *VM) IncDecRef(cur Slot, dec bool) (Slot, error) {
	newVal, overflow, err := vm.incDecCompute(deref(cur), dec)
	if err != nil {
		return Slot{}, err
	}
	if overflow {
		var gtyp, gdecl, gname string
		if cur.typ == RefType {
			if r := cur.Ref(); r != nil {
				if g := r.guard(); g != nil {
					gtyp = vm.displayTypeResolved(g.typ)
					gdecl = classDisplayName(g.decl)
					gname = g.name
				}
			}
		}
		allowFloat := false
		if gtyp != "" {
			if ok, _ := vm.coerceReturnMode(gtyp, MakeFloat(1), true); ok {
				allowFloat = true
			}
		}
		if gtyp != "" && !allowFloat {
			dir, lim := "increment", "maximal"
			if dec {
				dir, lim = "decrement", "minimal"
			}
			return Slot{}, NewThrowable("TypeError",
				"Cannot "+dir+" a reference held by property "+gdecl+"::$"+gname+" of type "+
					gtyp+" past its "+lim+" value")
		}
		newVal = overflowFloat(dec)
	}
	if cur.typ == RefType {
		if r := cur.Ref(); r != nil {
			if werr := vm.writeRef(r, newVal, vm.argStrict()); werr != nil {
				return Slot{}, werr
			}
			return r.v, nil
		}
	}
	return newVal, nil
}

// incDecCompute вычисляет old±1, сигналя переполнение int (результат тогда не
// задан — вызывающий решает, стать float или ошибкой).
func (vm *VM) incDecCompute(old Slot, dec bool) (Slot, bool, error) {
	switch old.typ {
	case IntType:
		v := old.ToInt()
		if dec {
			if v == math.MinInt64 {
				return Slot{}, true, nil
			}
			return MakeInt(v - 1), false, nil
		}
		if v == math.MaxInt64 {
			return Slot{}, true, nil
		}
		return MakeInt(v + 1), false, nil
	case FloatType:
		if dec {
			return MakeFloat(old.ToFloat() - 1), false, nil
		}
		return MakeFloat(old.ToFloat() + 1), false, nil
	case BoolType:
		// PHP 8.3: ++/-- на bool не имеет эффекта (Warning, значение не меняется).
		if dec {
			vm.Warn("Decrement on type bool has no effect, this will change in the next major version of PHP")
		} else {
			vm.Warn("Increment on type bool has no effect, this will change in the next major version of PHP")
		}
		return old, false, nil
	case ObjectType:
		verb := "increment"
		if dec {
			verb = "decrement"
		}
		name := "object"
		if ci := vm.classInfo(old); ci != nil {
			name = ci.Name()
		}
		return Slot{}, false, NewThrowable("TypeError", "Cannot "+verb+" "+name)
	case ArrayType:
		verb := "increment"
		if dec {
			verb = "decrement"
		}
		return Slot{}, false, NewThrowable("TypeError", "Cannot "+verb+" array")
	case ResourceType:
		verb := "increment"
		if dec {
			verb = "decrement"
		}
		return Slot{}, false, NewThrowable("TypeError", "Cannot "+verb+" resource")
	case NullType:
		// PHP 8.3: --null не имеет эффекта (Warning), ++null даёт 1.
		if dec {
			vm.Warn("Decrement on type null has no effect, this will change in the next major version of PHP")
			if vm.ctx.HasThrown() {
				return old, false, nil
			}
			return old, false, nil
		}
	case StringType:
		s := old.StringVal()
		if !IsNumericString(s) {
			if len(s) == 0 {
				if dec {
					vm.Deprecated("Decrement on empty string is deprecated as non-numeric")
					if vm.ctx.HasThrown() {
						return old, false, nil
					}
					return MakeInt(-1), false, nil
				}
				vm.Deprecated("Increment on non-alphanumeric string is deprecated")
				if vm.ctx.HasThrown() {
					return old, false, nil
				}
				return MakeString("1"), false, nil
			}
			if dec {
				vm.Deprecated("Decrement on non-numeric string has no effect and is deprecated")
				if vm.ctx.HasThrown() {
					return old, false, nil
				}
				return old, false, nil
			}
			if !isAlnumASCII(s) {
				vm.Deprecated("Increment on non-alphanumeric string is deprecated")
				if vm.ctx.HasThrown() {
					// Обработчик ошибок превратил deprecation в исключение —
					// операция не применяется.
					return old, false, nil
				}
			}
			return MakeString(incrementString(s)), false, nil
		}
	}
	var nv Slot
	var err error
	if dec {
		nv, err = vm.subSlow(old, MakeInt(1))
	} else {
		nv, err = vm.addSlow(old, MakeInt(1))
	}
	return nv, false, err
}

func overflowFloat(dec bool) Slot {
	if dec {
		return MakeFloat(float64(math.MinInt64))
	}
	return MakeFloat(float64(math.MaxInt64))
}

// IncDecValue вычисляет ++/-- над значением по семантике PHP (числа и строки).
func (vm *VM) IncDecValue(cur Slot, dec bool) (Slot, error) {
	nv, overflow, err := vm.incDecCompute(deref(cur), dec)
	if err != nil {
		return Slot{}, err
	}
	if overflow {
		nv = overflowFloat(dec)
	}
	return nv, nil
}

// isAlnumASCII сообщает, что строка состоит только из ASCII [0-9A-Za-z].
func isAlnumASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z') {
			return false
		}
	}
	return true
}

// incrementString повторяет Zend increment_string: инкремент «по алфавиту» с
// переносом для 9/z/Z и добавлением символа в начало при полном переносе.
func incrementString(s string) string {
	b := []byte(s)
	carry := true
	exhausted := true
	for pos := len(b); pos > 0; {
		p := pos - 1
		c := b[p]
		switch {
		case c == '9':
			b[p] = '0'
			pos--
		case c == 'z':
			b[p] = 'a'
			pos--
		case c == 'Z':
			b[p] = 'A'
			pos--
		case (c >= 'a' && c < 'z') || (c >= 'A' && c < 'Z') || (c >= '0' && c < '9'):
			b[p]++
			carry = false
			pos = 0
		default:
			exhausted = false
			pos = 0
		}
	}
	if carry && exhausted && len(b) > 0 {
		var pre byte
		switch b[0] {
		case '0':
			pre = '1'
		case 'A':
			pre = 'A'
		default:
			pre = 'a'
		}
		b = append([]byte{pre}, b...)
	}
	return string(b)
}

// coerceStaticProp проверяет/приводит значение к типу статического свойства
// (сообщение как у обычного типизированного свойства).
func (vm *VM) coerceStaticProp(cls ClassInfo, name string, v Slot) (Slot, error) {
	rc, ok := cls.(*runtimeClass)
	if !ok {
		return v, nil
	}
	pd, dc := rc.propDeclOf(name)
	if pd == nil || !pd.hasType {
		return v, nil
	}
	// Для статического свойства Zend использует «plain»-сообщение (без
	// «reference held by»), поэтому проверяем развёрнутое значение.
	return vm.coercePropValue(pd, dc, name, deref(v), vm.chunk != nil && vm.chunk.Strict, false)
}

// coerceStaticPropRef проверяет/приводит ссылку, присваиваемую статическому
// свойству через `=&`. Для типизированного свойства на ссылку вешается guard;
// если ссылка уже «held by» типизированного свойства — проверяется совместимость.
func (vm *VM) coerceStaticPropRef(cls ClassInfo, name string, v Slot) (Slot, error) {
	rc, ok := cls.(*runtimeClass)
	if !ok {
		return v, nil
	}
	pd, dc := rc.propDeclOf(name)
	if pd == nil || !pd.hasType {
		return v, nil
	}
	// Привязка ссылки приводит скаляры так же, как обычное присваивание
	// (weak/strict — по режиму объявления).
	return vm.coercePropValue(pd, dc, name, v, vm.chunk != nil && vm.chunk.Strict, true)
}

// staticRefCell возвращает ячейку-ссылку статического свойства <cls>::name,
// создавая её при необходимости и навешивая guard для типизированного
// свойства. Неинициализированное nullable-свойство инициализируется null;
// неинициализированное не-nullable даёт ошибку (как в Zend).
func (vm *VM) staticRefCell(cls ClassInfo, name string) (Slot, error) {
	rc, ok := cls.(*runtimeClass)
	if !ok {
		return MakeRef(&Ref{v: MakeNull()}), nil
	}
	s := rc.lookupStatic(name)
	if s == nil {
		return Slot{}, NewThrowable("Error", "Access to undeclared static property "+cls.Name()+"::$"+name)
	}
	if s.typ == RefType {
		return *s, nil
	}
	pd, dc := rc.propDeclOf(name)
	if rc.staticIsUninit(name) {
		if pd != nil && pd.hasType && !typeAllowsNull(pd.typ) {
			return Slot{}, NewThrowable("Error", "Cannot access uninitialized non-nullable property "+cls.Name()+"::$"+name+" by reference")
		}
		*s = MakeNull()
		rc.clearStaticUninit(name)
	}
	var guard *propTypeGuard
	if pd != nil && pd.hasType && !strings.EqualFold(pd.typ, "mixed") {
		guard = &propTypeGuard{typ: pd.typ, decl: dc, name: name}
	}
	cell := MakeRef(&Ref{v: *s})
	if guard != nil {
		cell.Ref().addSource(guard)
	}
	*s = cell
	return cell, nil
}

// checkPropWrite проверяет readonly-семантику записи свойства name.
func (vm *VM) checkPropWrite(ci ClassInfo, o Slot, name string) error {
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, name)
	if !ok {
		return nil
	}
	if err := vm.propAccessErr(pd, decl, name); err != nil {
		return err
	}
	if !pd.readonly {
		return nil
	}
	key := vm.propKey(pd, decl, name)
	_, initialized := vm.getProp(o, key)
	if initialized {
		if ob := vm.objectOf(o); ob != nil && ob.ConsumeReadonlyReinit(key) {
			return nil
		}
		return NewThrowable("Error", "Cannot modify readonly property "+classDisplayName(decl)+"::$"+name)
	}
	if vm.ctx.self == decl {
		return nil
	}
	return NewThrowable("Error", "Cannot initialize readonly property "+classDisplayName(decl)+"::$"+name+" from "+scopeDesc(vm.ctx.self))
}

// checkPropRead проверяет видимость и инициализацию типизированного свойства.
// Вызывается до getProp: объявленное свойство может присутствовать в props,
// но быть недоступным из текущей области. Если свойство снято через unset,
// проверки пропускаются (PHP-семантика «unset state» → __get).
func (vm *VM) checkPropRead(ci ClassInfo, o Slot, name string) error {
	key := propKeyForScope(vm.ctx.self, ci, name)
	if ob := vm.objectOf(o); ob != nil && ob.IsUnset(key) {
		return nil
	}
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, name)
	if !ok {
		return nil
	}
	if err := vm.propAccessErr(pd, decl, name); err != nil {
		return err
	}
	if _, initialized := vm.getProp(o, key); !initialized && (pd.readonly || pd.hasType) {
		return NewThrowable("Error", "Typed property "+classDisplayName(decl)+"::$"+name+" must not be accessed before initialization")
	}
	return nil
}

// checkPropReadVal — как checkPropRead, но заодно возвращает значение
// объявленного свойства, чтобы обработчик чтения не делал второй getProp
// (устраняет дублирующий map-lookup на горячем пути). found=false — свойство
// не объявлено или снято через unset (тогда обработчик идёт динамическим путём).
// uninitPropErr возвращает ошибку доступа к неинициализированному
// типизированному свойству (когда свойство было unset, но остаётся объявленным).
func (vm *VM) uninitPropErr(ci ClassInfo, pname string) error {
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, pname)
	if !ok || pd == nil || !pd.hasType {
		return nil
	}
	return NewThrowable("Error", "Typed property "+classDisplayName(decl)+"::$"+pname+" must not be accessed before initialization")
}

// checkPropReadVal проверяет/читает свойство для var_dump и диагностик.
func (vm *VM) checkPropReadVal(ci ClassInfo, o Slot, name string) (Slot, bool, error) {
	// Резолвим область один раз: ключ хранения выводится из (pd, decl), а не
	// отдельным проходом propKeyForScope (горячий путь $this->x).
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, name)
	key := name
	if ok {
		key = vm.propKey(pd, decl, name)
	}
	if ob := vm.objectOf(o); ob != nil && ob.IsUnset(key) {
		return Slot{}, false, nil
	}
	if !ok {
		return Slot{}, false, nil
	}
	if err := vm.propAccessErr(pd, decl, name); err != nil {
		return Slot{}, false, err
	}
	v, initialized := vm.getProp(o, key)
	if !initialized {
		if pd.readonly || pd.hasType {
			return Slot{}, false, NewThrowable("Error", "Typed property "+classDisplayName(decl)+"::$"+name+" must not be accessed before initialization")
		}
		return MakeNull(), true, nil
	}
	var s Slot
	SlotOf(&s, v)
	return s, true, nil
}

// ReadProperty читает свойство объекта с полной Zend-семантикой (видимость,
// __get) — для встроенных функций (array_column и т.п.). found=false, если
// свойства нет и магический доступ не сработал.
func (vm *VM) ReadProperty(o Slot, name string) (Slot, bool, error) {
	ci := vm.classInfo(o)
	if ci == nil {
		return Slot{}, false, nil
	}
	if gres, handled, mok, gerr := vm.magicGetInaccessible(ci, o, name); handled && mok {
		if gerr != nil {
			return Slot{}, false, gerr
		}
		return gres, true, nil
	}
	if v, found, verr := vm.checkPropReadVal(ci, o, name); verr != nil {
		return Slot{}, false, verr
	} else if found {
		return v, true, nil
	}
	if v, ok := vm.getProp(o, name); ok {
		var s Slot
		SlotOf(&s, v)
		return s, true, nil
	}
	if res, ok, err := vm.callMagicMethod(ci, o, "__get", []Slot{MakeString(name)}); ok {
		if err != nil {
			return Slot{}, false, err
		}
		res, gerr := vm.checkMagicGet(ci, name, res)
		if gerr != nil {
			return Slot{}, false, gerr
		}
		return res, true, nil
	}
	if uerr := vm.uninitPropErr(ci, name); uerr != nil {
		return Slot{}, false, uerr
	}
	return Slot{}, false, nil
}

// SetPropertyChecked записывает значение в свойство объекта с проверкой
// объявленного типа (в т.ч. для by-ref переданного значения из array_walk).
// Возвращает TypeError при нарушении типа — как Zend.
func (vm *VM) SetPropertyChecked(o Slot, name string, v Slot, strict bool) error {
	ci := vm.classInfo(o)
	if ci == nil {
		return errNotObject
	}
	if pd, decl, declared := vm.scopedProp(vm.ctx.self, ci, name); declared {
		// Видимость не проверяем: array_walk пишет свойства напрямую (как Zend),
		// включая private/protected. Проверяем только объявленный тип.
		val, verr := vm.coercePropValue(pd, decl, name, v, strict, false)
		if verr != nil {
			return verr
		}
		if ob := vm.objectOf(o); ob != nil {
			ob.ClearUnset(name)
		}
		vm.setProp(o, name, derefSlotVal(val))
		return nil
	}
	if handled, herr := vm.propSetExisting(o, name, derefSlotVal(v)); handled {
		return herr
	}
	vm.setProp(o, name, derefSlotVal(v))
	return nil
}

// derefSlotVal разыменовывает Ref-ячейку слота (для записи по значению).
func derefSlotVal(s Slot) Slot {
	if s.typ == RefType {
		return deref(s)
	}
	return s
}

// ObjectPropExists повторяет has_property: issetMode=false — режим EXISTS
// (существование объявленного/динамического свойства без __isset),
// issetMode=true — режим ISSET (с вызовом __isset и проверкой null).
func (vm *VM) ObjectPropExists(o Slot, name string, issetMode bool) bool {
	ci := vm.classInfo(o)
	if ci == nil {
		return false
	}
	if pd, _, ok := vm.scopedProp(vm.ctx.self, ci, name); ok {
		if pd != nil && !issetMode {
			return true
		}
		if v, initialized := vm.getProp(o, name); initialized {
			var s Slot
			SlotOf(&s, v)
			return deref(s).typ != NullType
		}
	} else if v, ok := vm.getProp(o, name); ok {
		if !issetMode {
			return true
		}
		var s Slot
		SlotOf(&s, v)
		return deref(s).typ != NullType
	}
	if issetMode {
		if res, ok, err := vm.callMagicMethod(ci, o, "__isset", []Slot{MakeString(name)}); ok && err == nil {
			return deref(res).ToBool()
		}
	}
	return false
}

// checkPropReadRW проверяет чтение свойства как базы для записи/косвенной
// модификации (`$x->p[] = ...`, `$x->p->q = ...`). Неинициализированное
// readonly-свойство даёт «Cannot indirectly modify readonly property ...».
func (vm *VM) checkPropReadRW(ci ClassInfo, o Slot, name string) error {
	if ob := vm.objectOf(o); ob != nil && ob.IsUnset(name) {
		return nil
	}
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, name)
	if !ok {
		return nil
	}
	if err := vm.propAccessErr(pd, decl, name); err != nil {
		return err
	}
	if _, initialized := vm.getProp(o, name); !initialized {
		if pd.readonly {
			return NewThrowable("Error", "Cannot indirectly modify readonly property "+classDisplayName(decl)+"::$"+name)
		}
		if pd.hasType {
			return NewThrowable("Error", "Typed property "+classDisplayName(decl)+"::$"+name+" must not be accessed before initialization")
		}
	}
	return nil
}

// checkPropUnset проверяет readonly-семантику unset свойства name.
func (vm *VM) checkPropUnset(ci ClassInfo, o Slot, name string) error {
	pd, decl, ok := vm.scopedProp(vm.ctx.self, ci, name)
	if !ok {
		return nil
	}
	if err := vm.propAccessErr(pd, decl, name); err != nil {
		return err
	}
	if !pd.readonly {
		return nil
	}
	if _, initialized := vm.getProp(o, name); initialized {
		return NewThrowable("Error", "Cannot unset readonly property "+classDisplayName(decl)+"::$"+name)
	}
	if vm.ctx.self == decl {
		return nil
	}
	return NewThrowable("Error", "Cannot unset readonly property "+classDisplayName(decl)+"::$"+name+" from "+scopeDesc(vm.ctx.self))
}

// checkDynamicProp сообщает ошибку создания динамического свойства в readonly
// классе (для необъявленных свойств).
func checkDynamicProp(ci ClassInfo, name string) error {
	if rc, ok := asRuntimeClass(ci); ok && rc != nil && rc.NoDynamicProps() {
		return NewThrowable("Error", "Cannot create dynamic property "+classDisplayName(ci)+"::$"+name)
	}
	if isReadonlyClass(ci) || (ci != nil && ci.Name() == "Generator") {
		return NewThrowable("Error", "Cannot create dynamic property "+classDisplayName(ci)+"::$"+name)
	}
	return nil
}

// classAllowsDynamicProps сообщает, что класс (или предок) помечен
// #[AllowDynamicProperties] либо является stdClass.
func classAllowsDynamicProps(ci ClassInfo) bool {
	if ci == nil {
		return false
	}
	if ci.Name() == "stdClass" {
		return true
	}
	for c := ci; c != nil; c = c.Parent() {
		for _, at := range classAttrs(c) {
			if at.Name == "AllowDynamicProperties" {
				return true
			}
		}
	}
	return false
}

// classIsUserDefined сообщает, что класс объявлен в userland (не встроенный).
func classIsUserDefined(ci ClassInfo) bool {
	if rc := asRC(ci); rc != nil {
		return rc.defFile != "" && !rc.internal
	}
	return false
}

// MaybeDeprecateDynamicProp печатает deprecation PHP 8.2+ при записи
// необъявленного свойства (используется unserialize, который пишет свойства
// в обход обычных assignment-проверок).
func (vm *VM) MaybeDeprecateDynamicProp(ci ClassInfo, o *Object, name string) {
	if ci == nil || o == nil {
		return
	}
	if ci.Name() == "stdClass" || ci.Name() == "__PHP_Incomplete_Class" {
		return
	}
	if !classIsUserDefined(ci) {
		// Встроенные классы не проверяем: у части из них (Exception и т.п.)
		// свойства моделируются как «динамические» внутри VM.
		return
	}
	if ci.HasProperty(name) {
		return
	}
	if _, exists := o.props[name]; exists {
		return
	}
	if classAllowsDynamicProps(ci) {
		return
	}
	vm.Deprecated("Creation of dynamic property " + classDisplayName(ci) + "::$" + name + " is deprecated")
}

// inactiveClassName — имя класса-заполнителя для ненайденных при unserialize.
const inactiveClassName = "__PHP_Incomplete_Class"

// incompleteObjName возвращает имя исходного класса incomplete-объекта.
func incompleteObjName(o *Object) (string, bool) {
	if o == nil || !o.incomplete {
		return "", false
	}
	if v, ok := o.GetProp("__PHP_Incomplete_Class_Name"); ok {
		n := v.ToString()
		if n != "" {
			return n, true
		}
	}
	return "unknown", true
}

// currentFunctionName — имя текущей функции для префикса диагностик
// ("main" на верхнем уровне).
// CurrentFunctionName — имя текущей (самой внутренней) функции кадра для
// диагностик, порождаемых из встроенных функций ("fwrite(): ...").
func (vm *VM) CurrentFunctionName() string { return vm.currentFunctionName() }

func (vm *VM) currentFunctionName() string {
	if n := len(vm.callStack); n > 0 {
		if f := vm.callStack[n-1].callee; f != nil && f.Name != "" {
			return f.Name
		}
	}
	return "main"
}

// incompleteClassDefTail — общий хвост сообщений про incomplete-объект.
func incompleteClassDefTail(class string) string {
	return " Please ensure that the class definition \"" + class + "\" of the object you are trying to operate" +
		" on was loaded _before_ unserialize() gets called or provide an autoloader to load the class definition"
}

// incompleteReadWarn печатает warning при чтении свойства incomplete-объекта.
func (vm *VM) incompleteReadWarn(o *Object, class string) {
	vm.Warn(vm.currentFunctionName() + "(): The script tried to access a property on an incomplete object." +
		incompleteClassDefTail(class))
}

// incompleteModifyErr — ошибка записи свойства incomplete-объекта.
func incompleteModifyErr(class string) error {
	return NewThrowable("Error", "The script tried to modify a property on an incomplete object."+
		incompleteClassDefTail(class))
}

// incompleteMethodErr — ошибка вызова метода incomplete-объекта.
func incompleteMethodErr(class string) error {
	return NewThrowable("Error", "The script tried to call a method on an incomplete object."+
		incompleteClassDefTail(class))
}

// enumWriteError формирует ошибку записи свойства enum-case.
func (vm *VM) enumWriteError(ci ClassInfo, o Slot, pname string) error {
	if _, ok := vm.getProp(o, pname); ok {
		return NewThrowable("Error", "Cannot modify readonly property "+classDisplayName(ci)+"::$"+pname)
	}
	return NewThrowable("Error", "Cannot create dynamic property "+classDisplayName(ci)+"::$"+pname)
}

// enumUnsetError формирует ошибку unset свойства enum-case.
func (vm *VM) enumUnsetError(ci ClassInfo, o Slot, pname string) error {
	if _, ok := vm.getProp(o, pname); ok {
		return NewThrowable("Error", "Cannot unset readonly property "+classDisplayName(ci)+"::$"+pname)
	}
	return NewThrowable("Error", "Cannot unset property "+classDisplayName(ci)+"::$"+pname)
}

// EnumCaseDupErr возвращает сообщение о дублирующемся backing-значении, если
// name — кейс перечисления cls, для которого зафиксирован дубликат.
// ConstPropName приводит динамическое имя свойства в константном выражении
// к строке (zend_ast_evaluate: try_convert_to_string). Ошибка конверсии
// (объект без __toString) — Error.
func (vm *VM) ConstPropName(name Slot) (Slot, error) {
	s, err := vm.stringify(name)
	if err != nil {
		return Slot{}, err
	}
	return MakeString(s), nil
}

// ConstPropCheck проверяет допустимость чтения свойства в константном
// выражении: объект обязан быть экземпляром enum (zend_ast_evaluate).
// Не-объекты проверяются обычным путём чтения свойства (warning + NULL).
func (vm *VM) ConstPropCheck(recv Slot) error {
	s := deref(recv)
	if s.typ != ObjectType {
		return nil
	}
	o := vm.objectOf(s)
	if o == nil {
		return nil
	}
	if ci := o.Class(); ci != nil && ci.IsEnum() {
		return nil
	}
	return NewThrowable("Error", "Fetching properties on non-enums in constant expressions is not allowed")
}

func (vm *VM) EnumCaseDupErr(cls ClassInfo, name string) (string, string) {
	rc, ok := asRuntimeClass(cls)
	if !ok || rc == nil || rc.enumCaseErrMsg == "" {
		return "", ""
	}
	if _, isCase := rc.enumByName[name]; !isCase {
		return "", ""
	}
	return rc.enumCaseErrType, rc.enumCaseErrMsg
}

// EnumCaseByName возвращает слот кейса перечисления по имени.
func (vm *VM) EnumCaseByName(cls ClassInfo, name string) (Slot, bool) {
	rc, ok := asRuntimeClass(cls)
	if !ok || rc == nil {
		return Slot{}, false
	}
	s, ok := rc.enumByName[name]
	return s, ok
}

// EnumCaseInfo возвращает имя класса-перечисления, имя кейса и базовый тип
// ("" для чистого enum). ok=false, если слот — не enum-case.
func (vm *VM) EnumCaseInfo(s Slot) (cls, name, backing string, ok bool) {
	s = deref(s)
	if s.typ != ObjectType {
		return "", "", "", false
	}
	ci := vm.classInfo(s)
	if ci == nil || !ci.IsEnum() {
		return "", "", "", false
	}
	if o := vm.objectOf(s); o != nil {
		if v, ok2 := o.GetProp("name"); ok2 {
			name = v.ToString()
		}
	}
	return ci.Name(), name, ci.EnumBacking(), true
}

// EnumDump форматирует enum-case как PHP var_dump: `enum(Class::Case)`.// Возвращает ("", false), если слот — не enum-case.
func (vm *VM) EnumDump(s Slot) (string, bool) {
	cls, name, _, ok := vm.EnumCaseInfo(s)
	if !ok {
		return "", false
	}
	return "enum(" + cls + "::" + name + ")", true
}

// isA: c — это target, его наследник, или класс, реализующий target-интерфейс.
func (c *runtimeClass) isA(target ClassInfo) bool {
	if target == nil {
		return false
	}
	if ClassInfo(c) == target {
		return true
	}
	if c.ancestors != nil {
		return c.ancestors[target]
	}
	for k := c; k != nil; k = k.parent {
		if ClassInfo(k) == target {
			return true
		}
		if k.implementsIface(target) {
			return true
		}
	}
	return false
}

func (c *runtimeClass) implementsIface(target ClassInfo) bool {
	for _, i := range c.ifaces {
		if i == target || i.isA(target) {
			return true
		}
	}
	return false
}

// canAccess проверяет видимость члена: private — только из объявившего класса,
// protected — из иерархии объявившего, public — отовсюду.
func canAccess(vis Visibility, decl, caller ClassInfo) bool {
	switch vis {
	case Private:
		return decl != nil && decl == caller
	case Protected:
		return decl != nil && caller != nil && (caller.isA(decl) || decl.isA(caller))
	default:
		return true
	}
}

// canAccessMethod — видимость метода: для protected дополнительно учитывается
// корневой прототип (zend_get_function_root_class, GH-14009): доступ разрешён
// области, связанной с классом первого объявления переопределяемого метода.
func canAccessMethod(vis Visibility, decl, proto, caller ClassInfo) bool {
	if canAccess(vis, decl, caller) {
		return true
	}
	if vis != Protected || proto == nil || caller == nil {
		return false
	}
	return caller.isA(proto) || proto.isA(caller)
}

// methodProto возвращает корневой прототип метода (nil, если метод не
// переопределяет унаследованный). Плоская таблица уже слинкована к моменту
// проверки доступа; до link() прототипов нет.
func (c *runtimeClass) methodProto(name string) ClassInfo {
	if c == nil || c.allMethods == nil || name == "" {
		return nil
	}
	if e, ok := c.allMethods[name]; ok {
		return e.proto
	}
	if ln := strings.ToLower(name); ln != name {
		if e, ok := c.allMethods[ln]; ok {
			return e.proto
		}
	}
	for k := c; k != nil; k = k.parent {
		for mname := range k.methods {
			if strings.EqualFold(mname, name) {
				if e, ok := c.allMethods[mname]; ok {
					return e.proto
				}
			}
		}
	}
	return nil
}

// scopeMethod реализует диспетчеризацию private-методов по области компиляции
// (compile-time scope), а не по runtime-классу получателя. Если scope, в
// котором скомпилирован вызов, объявляет собственный private-метод name,
// вызывается именно он — даже если у объекта (подкласса) есть свой private
// метод с тем же именем.
func scopeMethod(scope ClassInfo, name string) (*Function, ClassInfo, Visibility, bool) {
	rc, ok := scope.(*runtimeClass)
	if !ok || rc == nil {
		return nil, nil, Public, false
	}
	m, decl, vis := rc.methodInfo(name)
	if m != nil && vis == Private && decl == scope {
		return m, decl, vis, true
	}
	return nil, nil, Public, false
}
