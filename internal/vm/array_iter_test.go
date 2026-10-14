package vm

import (
	"testing"
)

const iterN = 100000

// Итерация mixed через итератор All() — только по entries, без хеша.
func BenchmarkIterMixedStr(b *testing.B) {
	a, _ := mixedStrArr(iterN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for _, v := range a.All() {
			sum += v.Int()
		}
		sinkInt = sum
	}
}

// Итерация packed: последовательно по типизированному int-хранилищу.
func BenchmarkIterPacked(b *testing.B) {
	a := packedArr(iterN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for j := range a.ints {
			sum += a.ints[j]
		}
		sinkInt = sum
	}
}

// Эталоны: слайс и нативная map.
func BenchmarkIterSlice(b *testing.B) {
	s := make([]Slot, iterN)
	for i := range s {
		s[i] = MakeInt(int64(i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for j := range s {
			sum += s[j].Int()
		}
		sinkInt = sum
	}
}

func BenchmarkIterGoMap(b *testing.B) {
	m, _ := goMapStrArr(iterN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for _, v := range m {
			sum += int64(v)
		}
		sinkInt = sum
	}
}

type iterEntry struct {
	key  ArrayKey
	val  Slot
	live bool
}

// Итерация упорядоченного массива, где значение лежит рядом с ключом.
func BenchmarkIterEntries(b *testing.B) {
	e := make([]iterEntry, iterN)
	for i := range e {
		e[i] = iterEntry{key: stringKey("k"), val: MakeInt(int64(i)), live: true}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		var sum int64
		for j := range e {
			if e[j].live {
				sum += e[j].val.Int()
			}
		}
		sinkInt = sum
	}
}
