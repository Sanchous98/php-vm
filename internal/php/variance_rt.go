package php

import (
	"strings"

	"phpvm/internal/vm"
)

// Отложенные рантайм-проверки совместимости сигнатур (Zend variance
// obligations). Компилятор строит для классов, объявляемых в рантайме
// (OpDeclareClass), план проверок; VM выполняет его в момент объявления, когда
// предки разрешены (в т.ч. автозагрузкой), а недостающие классы автозагружены.
// См. Zend/zend_inheritance.c: perform_delayable_implementation_check,
// load_delayed_classes, resolve_delayed_variance_obligations.

// rtVarCheck — одна проверка: собственный метод класса против унаследованного
// (родителя или интерфейса).
type rtVarCheck struct {
	cd *ClassDecl
	m  *ClassMember
	pd *ClassDecl
	pm *ClassMember
}

// rtVarPlan — план проверок класса.
type rtVarPlan struct {
	file   string
	rtIdx  int // индекс класса в vm.classes (для __php_variance_check)
	checks []rtVarCheck
}

func init() { vm.VarianceHook = phpVarianceHook }

// phpVarianceHook выполняет план проверок. Возвращает имена классов, которых
// нет в таблице классов VM (их нужно автозагрузить и повторить проверку), а
// также сигнатуры первой нерешённой проверки для диагностики. Если проверка
// доказала несовместимость — возвращает фатальную ошибку.
func phpVarianceHook(v *vm.VM, plan any) (vm.VarianceProbe, error) {
	p, _ := plan.(*rtVarPlan)
	var probe vm.VarianceProbe
	if p == nil {
		return probe, nil
	}
	probe.File = p.file
	if probe.File == "" {
		probe.File = "php"
	}
	lookup := func(name string) vm.ClassInfo { return v.LookupClassVarianceUse(name) }
	isA := func(child, parent vm.ClassInfo) bool { return v.UnlinkedIsA(child, parent) }
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		name = strings.TrimLeft(name, "\\")
		if name == "" {
			return
		}
		key := strings.ToLower(name)
		if seen[key] {
			return
		}
		seen[key] = true
		names = append(names, name)
	}
	for _, chk := range p.checks {
		c := &compiler{curLine: chk.m.Line, rtLookup: lookup, rtIsA: isA}
		status, _ := c.implCheckCompat(chk.cd, chk.m, chk.pd, chk.pm)
		switch status {
		case subYes:
			// всё хорошо
		case subUnknown:
			c.collectMissingTypeNames(chk, lookup, add)
			if probe.ChildSig == "" {
				probe.ChildSig = methodSigStringSub(chk.cd.Name, chk.m, chk.cd.Name, chk.cd.Parent)
				probe.ParentSig = methodSigStringSub(chk.pd.Name, chk.pm, chk.pd.Name, chk.pd.Parent)
				probe.Line = chk.m.Line
			}
		default:
			childSig := methodSigStringSub(chk.cd.Name, chk.m, chk.cd.Name, chk.cd.Parent)
			parentSig := methodSigStringSub(chk.pd.Name, chk.pm, chk.pd.Name, chk.pd.Parent)
			return probe, &vm.VarianceFatal{
				Msg:  "Declaration of " + childSig + " must be compatible with " + parentSig,
				File: probe.File,
				Line: chk.m.Line,
			}
		}
	}
	probe.Unresolved = names
	return probe, nil
}

// collectMissingTypeNames собирает имена классов из сигнатур проверки, которых
// нет в таблице классов. Порядок повторяет Zend: для параметров сначала типы
// родителя (proto), затем потомка (fe); для возврата — потомка, затем родителя
// (zend_do_perform_implementation_check + register_unresolved_classes).
func (c *compiler) collectMissingTypeNames(chk rtVarCheck, lookup func(string) vm.ClassInfo, add func(string)) {
	cd, m, pd, pm := chk.cd, chk.m, chk.pd, chk.pm
	n := len(m.Params)
	if len(pm.Params) > n {
		n = len(pm.Params)
	}
	for i := 0; i < n; i++ {
		if i < len(pm.Params) {
			c.addMissingTypeNames(substRelativeAtoms(pm.Params[i].Type, pd.Name, pd.Parent), lookup, add)
		}
		if i < len(m.Params) {
			c.addMissingTypeNames(substRelativeAtoms(m.Params[i].Type, cd.Name, cd.Parent), lookup, add)
		}
	}
	c.addMissingTypeNames(substRelativeAtoms(m.RetType, cd.Name, cd.Parent), lookup, add)
	c.addMissingTypeNames(substRelativeAtoms(pm.RetType, pd.Name, pd.Parent), lookup, add)
}

func (c *compiler) addMissingTypeNames(t string, lookup func(string) vm.ClassInfo, add func(string)) {
	if t == "" {
		return
	}
	for _, alt := range normalizeTypeAlts(t) {
		for _, atom := range alt {
			name := strings.TrimSpace(atom)
			if name == "" {
				continue
			}
			lc := strings.ToLower(name)
			if builtinTypeNames[lc] || lc == "self" || lc == "parent" || lc == "static" {
				continue
			}
			if lookup(name) == nil {
				add(name)
			}
		}
	}
}
