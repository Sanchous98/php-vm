package vm

import (
	"strconv"
	"testing"
	"unsafe"
)

func TestIdxSizes(t *testing.T) {
	var kv KV
	t.Logf("sizeof(KV)=%d  sizeof(int)=%d  sizeof(int32)=%d  sizeof(uint32)=%d  sizeof(*KV)=%d",
		unsafe.Sizeof(kv), unsafe.Sizeof(int(0)), unsafe.Sizeof(int32(0)), unsafe.Sizeof(uint32(0)), unsafe.Sizeof(&kv))
}

func idxKeys() []ArrayKey { return oneMapKeys() }

func BenchmarkIdxInt(b *testing.B) {
	keys := idxKeys()
	m := make(map[string]int, oneMapN)
	entries := make([]KV, oneMapN)
	for i, k := range keys {
		m[k.s] = i
		entries[i] = KV{key: k, live: true}
	}
	idx := benchIdxs(oneMapN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = entries[m[keys[idx[i&(len(idx)-1)]].s]].val
	}
}

func BenchmarkIdxInt32(b *testing.B) {
	keys := idxKeys()
	m := make(map[string]int32, oneMapN)
	entries := make([]KV, oneMapN)
	for i, k := range keys {
		m[k.s] = int32(i)
		entries[i] = KV{key: k, live: true}
	}
	idx := benchIdxs(oneMapN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = entries[m[keys[idx[i&(len(idx)-1)]].s]].val
	}
}

func BenchmarkPtrKV(b *testing.B) {
	keys := idxKeys()
	m := make(map[string]*KV, oneMapN)
	entries := make([]KV, oneMapN)
	for i, k := range keys {
		entries[i] = KV{key: k, live: true}
		m[k.s] = &entries[i]
	}
	idx := benchIdxs(oneMapN)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = m[keys[idx[i&(len(idx)-1)]].s].val
	}
}

// Демонстрация: указатель на элемент растущего слайса «повисает» после append
// (append переаллоцирует бэкенг). Это тот самый data-evacuation, но для слайса.
func TestPtrIntoGrowingSliceDangles(t *testing.T) {
	a := make([]KV, 0, 4)
	m := map[string]*KV{}
	for i := 0; i < 100; i++ {
		k := stringKey("k" + strconv.Itoa(i))
		a = append(a, KV{key: k, val: MakeInt(int64(i)), live: true})
		m[k.s] = &a[len(a)-1]
	}
	m["k0"].val = MakeInt(999) // правим «через карту»
	if a[0].val.Int() == 999 {
		t.Fatal("ожидали, что указатель повиснет (append переаллоцировал бэкенг)")
	}
	t.Logf("подтверждено: a[0]=%d, а m[k0].val=999 — указатель ведёт в старый массив",
		a[0].val.Int())
}
