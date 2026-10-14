package vm

import (
	"sort"
	"strings"
)

// typeRank задаёт канонический порядок вариантов union-типа в диагностиках
// Zend. Классы/интерфейсы (и self/static/parent) имеют ранг 0 и сохраняют
// порядок объявления; затем идут callable, object, array, string, int, float,
// bool, false, true, null.
func typeRank(atom string) int {
	switch strings.ToLower(strings.TrimPrefix(atom, "\\")) {
	case "callable":
		return 1
	case "object":
		return 2
	case "array":
		return 3
	case "string":
		return 4
	case "int":
		return 5
	case "float":
		return 6
	case "bool":
		return 7
	case "false":
		return 8
	case "true":
		return 9
	case "null":
		return 10
	case "mixed":
		return -2
	case "void", "never":
		return -1
	}
	// Неизвестное имя трактуем как класс (ранг 0).
	return 0
}

// typeMember — один вариант union-типа: либо одиночный атом, либо пересечение.
type typeMember struct {
	atoms []string
	rank  int
	order int
}

// TypeString рендерит объявление типа в каноническом виде Zend: раскрывает
// `?T` -> `T|null` и `iterable` -> `Traversable|array`, канонически
// упорядочивает варианты union и расставляет скобки вокруг пересечений в DNF.
func TypeString(t string) string {
	if t == "" {
		return ""
	}
	// standalone mixed/void/never не трогаем
	if t == "mixed" || t == "void" || t == "never" {
		return t
	}
	if strings.HasPrefix(t, "?") {
		inner := strings.TrimSpace(t[1:])
		// `?iterable` раскрывается полностью, `?T` для простого типа
		// сохраняет синтаксическую форму (как Zend).
		if inner != "iterable" && topLevelSplit(inner, '|') < 0 && topLevelSplit(inner, '&') < 0 {
			return "?" + strings.TrimPrefix(inner, "\\")
		}
		t = inner + "|null"
	}
	raw := splitTopLevelAll(t, '|')
	var members []typeMember
	for _, alt := range raw {
		alt = strings.TrimSpace(alt)
		if alt == "" {
			continue
		}
		if alt == "iterable" {
			members = append(members, typeMember{atoms: []string{"Traversable"}, rank: 0, order: len(members)})
			members = append(members, typeMember{atoms: []string{"array"}, rank: 3, order: len(members)})
			continue
		}
		body := alt
		if strings.HasPrefix(body, "(") && strings.HasSuffix(body, ")") {
			body = body[1 : len(body)-1]
		}
		var atoms []string
		for _, a := range splitTopLevelAll(body, '&') {
			a = strings.TrimSpace(a)
			if a != "" {
				atoms = append(atoms, strings.TrimPrefix(a, "\\"))
			}
		}
		rank := 0
		for i, a := range atoms {
			r := typeRank(a)
			if i == 0 || r < rank {
				rank = r
			}
		}
		members = append(members, typeMember{atoms: atoms, rank: rank, order: len(members)})
	}
	if len(members) == 0 {
		return t
	}
	stableSortMembers(members)
	isUnion := len(members) > 1
	var b strings.Builder
	for i, m := range members {
		if i > 0 {
			b.WriteByte('|')
		}
		if len(m.atoms) > 1 && isUnion {
			b.WriteByte('(')
			b.WriteString(strings.Join(m.atoms, "&"))
			b.WriteByte(')')
		} else {
			b.WriteString(strings.Join(m.atoms, "&"))
		}
	}
	return b.String()
}

func stableSortMembers(m []typeMember) {
	sort.SliceStable(m, func(i, j int) bool { return m[i].rank < m[j].rank })
}
