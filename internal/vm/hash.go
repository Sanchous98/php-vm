package vm

import (
	"maps"
	"slices"
)

type HashTable struct {
	keys   map[Value]int
	values []Value
}

func NewList(init []Value) (ht HashTable) {
	// TODO: don't use keys in list
	ht.keys = make(map[Value]int, len(init))

	for i := range init {
		ht.keys[Int(i)] = i
	}

	ht.values = init
	return
}
func NewHash(init map[Value]Value) (ht HashTable) {
	ht.keys = make(map[Value]int, len(init))
	ht.values = make([]Value, 0, len(init))

	for k, v := range init {
		ht.keys[k] = len(ht.values)
		ht.values = append(ht.values, v)
	}

	return
}

func (ht *HashTable) access(key Value) (*Value, bool) {
	if k, ok := ht.keys[key]; ok {
		return &ht.values[k], true
	}

	return nil, false
}
func (ht *HashTable) identical(y HashTable) bool {
	if ht.isList() {
		return slices.Equal(ht.values, y.values)
	}

	return slices.Equal(ht.values, y.values) && maps.Equal(ht.keys, y.keys)
}
func (ht *HashTable) assign(key Value) *Value {
	i, ok := ht.keys[key]

	if !ok {
		i = len(ht.values)
		ht.keys[key] = i
		ht.values = append(ht.values, Null{})
	}

	return &ht.values[i]
}
func (ht *HashTable) delete(key Value) {
	if i, ok := ht.keys[key]; ok {
		delete(ht.keys, key)
		ht.values = slices.Delete(ht.values, i, i+1)
	}
}
func (ht *HashTable) clone() HashTable {
	return HashTable{maps.Clone(ht.keys), slices.Clone(ht.values)}
}
func (ht *HashTable) add(v HashTable) {
	newKeys := maps.Clone(v.keys)
	maps.DeleteFunc(newKeys, func(k Value, _ int) (ok bool) {
		_, ok = ht.keys[k]
		return
	})

	ht.values = slices.Grow(ht.values, len(newKeys))

	for k, i := range newKeys {
		newV := v.values[i]
		ht.keys[k] = len(ht.values)
		ht.values = append(ht.values, newV)
	}
}

func (ht *HashTable) isList() bool { return ht.keys == nil }
func (ht *HashTable) toMap() {
	if !ht.isList() {
		return
	}

	ht.keys = make(map[Value]int, len(ht.values))
	for k := range ht.values {
		ht.keys[Int(k)] = k
	}
}
func (ht *HashTable) getKeys(f func(x, y Value) int) []Value {
	keys := make([]Value, 0, len(ht.keys))

	for k := range ht.keys {
		keys = append(keys, k)
	}

	slices.SortFunc(keys, f)
	return keys
}

func hashCompare(ctx Context, x, y HashTable) Int {
	xCount, yCount := len(x.values), len(y.values)

	if sign := intSign(xCount - yCount); sign != 0 {
		return Int(sign)
	}

	for key, i := range x.keys {
		if v, ok := x.access(key); !ok {
			return +1
		} else if c := x.values[i].(Comparable).Compare(ctx, *v); c != 0 {
			return c
		}
	}

	return 0
}
