package vm

import (
	"math/rand"
	"strconv"
	"testing"
)

func goMapStrArr(n int) (map[string]int, []string) {
	m := make(map[string]int, n)
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = "k" + strconv.Itoa(i)
		m[keys[i]] = i
	}
	return m, keys
}

func benchGoMap(b *testing.B, m map[string]int, keys []string) {
	n := len(keys)
	rng := rand.New(rand.NewSource(1))
	const W = 1 << 16
	idxs := make([]int, W)
	for i := range idxs {
		idxs[i] = rng.Intn(n)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkInt = int64(m[keys[idxs[i&(W-1)]]])
	}
}

func BenchmarkGoMapStr1e3(b *testing.B) { m, k := goMapStrArr(1e3); benchGoMap(b, m, k) }
func BenchmarkGoMapStr1e4(b *testing.B) { m, k := goMapStrArr(1e4); benchGoMap(b, m, k) }
func BenchmarkGoMapStr1e5(b *testing.B) { m, k := goMapStrArr(1e5); benchGoMap(b, m, k) }
func BenchmarkGoMapStr1e6(b *testing.B) { m, k := goMapStrArr(1e6); benchGoMap(b, m, k) }
func BenchmarkGoMapStr1e7(b *testing.B) { m, k := goMapStrArr(1e7); benchGoMap(b, m, k) }

func goMapSlotArr(n int) (map[string]Slot, []string) {
	m := make(map[string]Slot, n)
	keys := make([]string, n)
	for i := 0; i < n; i++ {
		keys[i] = "k" + strconv.Itoa(i)
		m[keys[i]] = MakeInt(int64(i))
	}
	return m, keys
}

func benchGoMapSlot(b *testing.B, m map[string]Slot, keys []string) {
	n := len(keys)
	rng := rand.New(rand.NewSource(1))
	const W = 1 << 16
	idxs := make([]int, W)
	for i := range idxs {
		idxs[i] = rng.Intn(n)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = m[keys[idxs[i&(W-1)]]]
	}
}

func BenchmarkGoMapSlot1e3(b *testing.B) { m, k := goMapSlotArr(1e3); benchGoMapSlot(b, m, k) }
func BenchmarkGoMapSlot1e4(b *testing.B) { m, k := goMapSlotArr(1e4); benchGoMapSlot(b, m, k) }
func BenchmarkGoMapSlot1e5(b *testing.B) { m, k := goMapSlotArr(1e5); benchGoMapSlot(b, m, k) }
func BenchmarkGoMapSlot1e6(b *testing.B) { m, k := goMapSlotArr(1e6); benchGoMapSlot(b, m, k) }
func BenchmarkGoMapSlot1e7(b *testing.B) { m, k := goMapSlotArr(1e7); benchGoMapSlot(b, m, k) }
