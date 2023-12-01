package vm

import "slices"

type htValue[V any] struct {
	v V
}

type HashTable[K comparable, V any] struct {
	internal map[K]*htValue[V]
}

func (h *HashTable[K, V]) access(key K) (*V, bool) {
	if v, ok := h.internal[key]; ok {
		return &(v.v), true
	}
	return nil, false
}

func (h *HashTable[K, V]) assign(key K) *V {
	if v, ok := h.internal[key]; ok {
		return &(v.v)
	}

	v := new(htValue[V])

	h.internal[key] = v
	return &(v.v)
}

func (h *HashTable[K, V]) delete(key K) { delete(h.internal, key) }

func (h *HashTable[K, V]) keys(cmp func(x, y K) int) []K {
	keys := make([]K, 0, len(h.internal))
	for k := range h.internal {
		keys = append(keys, k)
	}
	if cmp != nil {
		slices.SortFunc(keys, func(a, b K) int { return cmp(a, b) })
	}
	return keys
}
