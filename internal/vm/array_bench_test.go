package vm

import "testing"

var sinkSlot Slot
var sinkInt int64

// Чтение из packed-списка: прямой индекс в values (без хеша).
func BenchmarkArrayGetPacked(b *testing.B) {
	a := newArray()
	for i := 0; i < 1000; i++ {
		a.Set(intKey(int64(i)), MakeInt(int64(i)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot, _ = a.Get(intKey(int64(i % 1000)))
	}
}

// Чтение из mixed-массива (строковые ключи): карта key->позиция.
func BenchmarkArrayGetMixed(b *testing.B) {
	a := newArray()
	a.Set(stringKey("alpha"), MakeInt(1))
	a.Set(stringKey("beta"), MakeInt(2))
	a.Set(stringKey("gamma"), MakeInt(3))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot, _ = a.Get(stringKey("beta"))
	}
}

// Запись в packed (append) и чтение — модель $a[] = x; $a[$i].
func BenchmarkArrayAppend(b *testing.B) {
	a := newArray()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.Append(MakeInt(int64(i)))
		if a.storeLen() > 100000 {
			a.ints = a.ints[:0]
		}
	}
}
