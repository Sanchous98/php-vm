package vm

import (
	"strconv"
	"testing"
)

var htSinkInt int64

func htKeys(n int) []string {
	k := make([]string, n)
	for i := range k {
		k[i] = "k" + strconv.Itoa(i)
	}
	return k
}

// 150k уникальных вставок в mixed-массив (путь 03_arr_assoc).
func BenchmarkHtBuild(b *testing.B) {
	const n = 150000
	keys := htKeys(n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := &Array{packed: false}
		for j := 0; j < n; j++ {
			a.Set(stringKey(keys[j]), MakeInt(int64(j)))
		}
		htSinkInt = int64(a.Len())
	}
}

// То же в Go-map (как было до S1): map[string]int32 + append entries.
func BenchmarkMapBuild(b *testing.B) {
	const n = 150000
	keys := htKeys(n)
	type kv struct {
		k string
		v int64
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m := make(map[string]int32, 0)
		entries := make([]kv, 0, 0)
		for j := 0; j < n; j++ {
			if _, ok := m[keys[j]]; !ok {
				m[keys[j]] = int32(len(entries))
				entries = append(entries, kv{keys[j], int64(j)})
			}
		}
		htSinkInt = int64(len(m) + len(entries))
	}
}

// 50k чтений по существующим ключам.
func BenchmarkHtRead(b *testing.B) {
	const n = 150000
	keys := htKeys(n)
	a := &Array{packed: false}
	for j := 0; j < n; j++ {
		a.Set(stringKey(keys[j]), MakeInt(int64(j)))
	}
	b.ResetTimer()
	s := int64(0)
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j += 3 {
			v, _ := a.Get(stringKey(keys[j]))
			s += v.Int()
		}
	}
	htSinkInt = s
}

func BenchmarkMapRead(b *testing.B) {
	const n = 150000
	keys := htKeys(n)
	m := make(map[string]int32, n)
	vals := make([]int64, n)
	for j := 0; j < n; j++ {
		m[keys[j]] = int32(j)
		vals[j] = int64(j)
	}
	b.ResetTimer()
	s := int64(0)
	for i := 0; i < b.N; i++ {
		for j := 0; j < n; j += 3 {
			s += vals[m[keys[j]]]
		}
	}
	htSinkInt = s
}

// COW-копия 150k mixed-массива (было: переобход map).
func BenchmarkHtUnshare(b *testing.B) {
	const n = 150000
	a := &Array{packed: false}
	for j := 0; j < n; j++ {
		a.Set(stringKey(htKeys0(j)), MakeInt(int64(j)))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a.MarkShared()
		c := a.Unshare()
		htSinkInt = int64(c.Len())
	}
}

func htKeys0(i int) string { return "k" + strconv.Itoa(i) }

func BenchmarkHtBuildInt(b *testing.B) {
	const n = 150000
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := &Array{packed: false}
		for j := 0; j < n; j++ {
			a.SetInt(int64(j*2), MakeInt(int64(j)))
		}
		htSinkInt = int64(a.Len())
	}
}
