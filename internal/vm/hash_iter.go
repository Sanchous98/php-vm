//go:build goexperiment.rangefunc

package vm

func (ht *HashTable) iterate() func(yield func(Value, Value) bool) {
	return func(yield func(Value, Value) bool) {
		for key, i := range ht.keys {
			if !yield(key, ht.values[i]) {
				return
			}
		}
	}
}
