package vm

import (
	"math/rand"
	"strconv"
	"testing"
)

const oneMapN = 1 << 17

func benchIdxs(n int) []int {
	rng := rand.New(rand.NewSource(1))
	const W = 1 << 16
	idx := make([]int, W)
	for i := range idx {
		idx[i] = rng.Intn(n)
	}
	return idx
}

func oneMapKeys() []ArrayKey {
	k := make([]ArrayKey, oneMapN)
	for i := range k {
		k[i] = stringKey("k" + strconv.Itoa(i))
	}
	return k
}

// Два map: map[string]int + map[int64]int (как сейчас).
func BenchmarkTwoMapRead(b *testing.B) {
	keys := oneMapKeys()
	m := make(map[string]int, oneMapN)
	entries := make([]Slot, oneMapN)
	for i, k := range keys {
		m[k.s] = i
		entries[i] = MakeInt(int64(i))
	}
	idx := benchIdxs(oneMapN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = entries[m[keys[idx[i&(len(idx)-1)]].s]]
	}
}

// Один map: map[ArrayKey]int.
func BenchmarkOneMapRead(b *testing.B) {
	keys := oneMapKeys()
	m := make(map[ArrayKey]int, oneMapN)
	entries := make([]Slot, oneMapN)
	for i, k := range keys {
		m[k] = i
		entries[i] = MakeInt(int64(i))
	}
	idx := benchIdxs(oneMapN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = entries[m[keys[idx[i&(len(idx)-1)]]]]
	}
}
