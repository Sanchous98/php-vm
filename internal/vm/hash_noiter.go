//go:build !goexperiment.rangefunc

package vm

func (ht *HashTable) iterate() map[Value]Value {
	iter := make(map[Value]Value, len(ht.keys))
	for key, i := range ht.keys {
		iter[key] = ht.values[i]
	}
	return iter
}
