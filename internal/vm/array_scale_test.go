package vm

import (
	"math/rand"
	"strconv"
	"testing"
)

func packedArr(n int) *Array {
	a := newArray()
	for i := 0; i < n; i++ {
		a.Append(MakeInt(int64(i)))
	}
	return a
}

func mixedStrArr(n int) (*Array, []ArrayKey) {
	a := newArray()
	keys := make([]ArrayKey, n)
	for i := 0; i < n; i++ {
		keys[i] = stringKey("k" + strconv.Itoa(i))
		a.Set(keys[i], MakeInt(int64(i)))
	}
	return a, keys
}

func mixedIntArr(n int) (*Array, []ArrayKey) {
	a := newArray()
	keys := make([]ArrayKey, n)
	for i := 0; i < n; i++ {
		keys[i] = intKey(int64(i * 2))
		a.Set(keys[i], MakeInt(int64(i)))
	}
	return a, keys
}

// Случайный доступ: убирает последовательный prefetch, обнажает кэш.
// Ключ строится на месте (intKey), чтобы измерялась именно стоимость доступа
// к хранилищу массива, а не второй случайный поток по 32-байтным ArrayKey.
func benchGetRand(b *testing.B, a *Array, n int) {
	rng := rand.New(rand.NewSource(1))
	const W = 1 << 16
	idxs := make([]int, W)
	for i := range idxs {
		idxs[i] = rng.Intn(n)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot, _ = a.Get(intKey(int64(idxs[i&(W-1)])))
	}
}

func BenchmarkPacked1e3(b *testing.B) { benchGetRand(b, packedArr(1e3), 1e3) }
func BenchmarkPacked1e4(b *testing.B) { benchGetRand(b, packedArr(1e4), 1e4) }
func BenchmarkPacked1e5(b *testing.B) { benchGetRand(b, packedArr(1e5), 1e5) }
func BenchmarkPacked1e6(b *testing.B) { benchGetRand(b, packedArr(1e6), 1e6) }
func BenchmarkPacked1e7(b *testing.B) { benchGetRand(b, packedArr(1e7), 1e7) }

func benchGetRandKeys(b *testing.B, a *Array, keys []ArrayKey) {
	n := len(keys)
	rng := rand.New(rand.NewSource(1))
	const W = 1 << 16
	idxs := make([]int, W)
	for i := range idxs {
		idxs[i] = rng.Intn(n)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot, _ = a.Get(keys[idxs[i&(W-1)]])
	}
}

func BenchmarkMixedInt1e3(b *testing.B) { a, k := mixedIntArr(1e3); benchGetRandKeys(b, a, k) }
func BenchmarkMixedInt1e4(b *testing.B) { a, k := mixedIntArr(1e4); benchGetRandKeys(b, a, k) }
func BenchmarkMixedInt1e5(b *testing.B) { a, k := mixedIntArr(1e5); benchGetRandKeys(b, a, k) }
func BenchmarkMixedInt1e6(b *testing.B) { a, k := mixedIntArr(1e6); benchGetRandKeys(b, a, k) }
func BenchmarkMixedInt1e7(b *testing.B) { a, k := mixedIntArr(1e7); benchGetRandKeys(b, a, k) }

func BenchmarkMixedStr1e3(b *testing.B) { a, k := mixedStrArr(1e3); benchGetRandKeys(b, a, k) }
func BenchmarkMixedStr1e4(b *testing.B) { a, k := mixedStrArr(1e4); benchGetRandKeys(b, a, k) }
func BenchmarkMixedStr1e5(b *testing.B) { a, k := mixedStrArr(1e5); benchGetRandKeys(b, a, k) }
func BenchmarkMixedStr1e6(b *testing.B) { a, k := mixedStrArr(1e6); benchGetRandKeys(b, a, k) }

// sinkArr/sinkAny держат результат, чтобы компилятор не выбросил сборку.
var sinkArr *Array

// Построение packed-массива из n int-значений: B/op показывает память на
// элемент (с учётом промежуточных копий при росте хранилища).
func benchBuild(b *testing.B, n int) {
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		a := newArray()
		for j := 0; j < n; j++ {
			a.Append(MakeInt(int64(j))) //nolint:errcheck
		}
		sinkArr = a
	}
}

func BenchmarkBuild1e3(b *testing.B) { benchBuild(b, 1e3) }
func BenchmarkBuild1e4(b *testing.B) { benchBuild(b, 1e4) }
func BenchmarkBuild1e5(b *testing.B) { benchBuild(b, 1e5) }
func BenchmarkBuild1e6(b *testing.B) { benchBuild(b, 1e6) }

func intKeys(n int) []ArrayKey {
	k := make([]ArrayKey, n)
	for i := range k {
		k[i] = intKey(int64(i))
	}
	return k
}
