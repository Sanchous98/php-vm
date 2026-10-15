package php

// Вторая партия изолированных замеров (см. stl_isolated_bench_test.go):
// json, массивы, preg, строки — реализация vs идиоматичный Go.
//
// Запуск: go test -run '^$' -bench 'Iso2_' -benchmem -count=3 ./internal/php/

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// ---------- json_encode ----------

func BenchmarkIso2_JsonEncode_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "json_encode")
	arr := vm.NewArray()
	for i := 0; i < 100; i++ {
		arr.Set(vm.MakeStrKey("k"+strconv.Itoa(i)), vm.MakeString("value "+strconv.Itoa(i)))
	}
	args := []vm.Slot{vm.MakeArray(arr)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_JsonEncode_Go(b *testing.B) {
	mm := make(map[string]string, 100)
	for i := 0; i < 100; i++ {
		mm["k"+strconv.Itoa(i)] = "value " + strconv.Itoa(i)
	}
	b.ReportAllocs()
	var out []byte
	for i := 0; i < b.N; i++ {
		out, _ = json.Marshal(mm)
	}
	_ = out
}

// ---------- json_decode ----------

func BenchmarkIso2_JsonDecode_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "json_decode")
	mm := make(map[string]string, 200)
	for i := 0; i < 200; i++ {
		mm["k"+strconv.Itoa(i)] = "value " + strconv.Itoa(i)
	}
	raw, _ := json.Marshal(mm)
	args := []vm.Slot{vm.MakeString(string(raw))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_JsonDecode_Go(b *testing.B) {
	mm := make(map[string]string, 200)
	for i := 0; i < 200; i++ {
		mm["k"+strconv.Itoa(i)] = "value " + strconv.Itoa(i)
	}
	raw, _ := json.Marshal(mm)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var out map[string]string
		_ = json.Unmarshal(raw, &out)
	}
}

// ---------- array_unique ----------

func BenchmarkIso2_ArrayUnique_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "array_unique")
	arr := vm.NewArray()
	for i := 0; i < 50000; i++ {
		_, _ = arr.Append(vm.MakeInt(int64(i % 5000)))
	}
	args := []vm.Slot{vm.MakeArray(arr)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_ArrayUnique_Go(b *testing.B) {
	src := make([]int, 50000)
	for i := range src {
		src[i] = i % 5000
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		seen := make(map[int]bool, len(src))
		out := make([]int, 0, len(src))
		for _, v := range src {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
		_ = out
	}
}

// ---------- array_slice ----------

func BenchmarkIso2_ArraySlice_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "array_slice")
	arr := vm.NewArray()
	for i := 0; i < 100000; i++ {
		_, _ = arr.Append(vm.MakeInt(int64(i)))
	}
	args := []vm.Slot{vm.MakeArray(arr), vm.MakeInt(1000), vm.MakeInt(50000)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_ArraySlice_Go(b *testing.B) {
	src := make([]int, 100000)
	for i := range src {
		src[i] = i
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out := make([]int, 50000)
		copy(out, src[1000:51000])
		_ = out
	}
}

// ---------- array_merge ----------

func BenchmarkIso2_ArrayMerge_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "array_merge")
	a, c := vm.NewArray(), vm.NewArray()
	for i := 0; i < 50000; i++ {
		_, _ = a.Append(vm.MakeInt(int64(i)))
		_, _ = c.Append(vm.MakeInt(int64(i)))
	}
	args := []vm.Slot{vm.MakeArray(a), vm.MakeArray(c)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_ArrayMerge_Go(b *testing.B) {
	a := make([]int, 50000)
	c := make([]int, 50000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		out := make([]int, 0, len(a)+len(c))
		out = append(out, a...)
		out = append(out, c...)
		_ = out
	}
}

// ---------- in_array ----------

func BenchmarkIso2_InArray_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "in_array")
	arr := vm.NewArray()
	for i := 0; i < 5000; i++ {
		_, _ = arr.Append(vm.MakeInt(int64(i * 2)))
	}
	args := []vm.Slot{vm.MakeInt(9999), vm.MakeArray(arr)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_InArray_Go(b *testing.B) {
	src := make([]int, 5000)
	for i := range src {
		src[i] = i * 2
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		found := false
		for _, v := range src {
			if v == 9999 {
				found = true
				break
			}
		}
		_ = found
	}
}

// ---------- strrev ----------

func BenchmarkIso2_Strrev_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "strrev")
	args := []vm.Slot{vm.MakeString(strings.Repeat("abcdefghij", 1000))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_Strrev_Go(b *testing.B) {
	in := strings.Repeat("abcdefghij", 1000)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		bs := []byte(in)
		for l, r := 0, len(bs)-1; l < r; l, r = l+1, r-1 {
			bs[l], bs[r] = bs[r], bs[l]
		}
		_ = string(bs)
	}
}

// ---------- array_sum ----------

func BenchmarkIso2_ArraySum_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "array_sum")
	arr := vm.NewArray()
	for i := 0; i < 100000; i++ {
		_, _ = arr.Append(vm.MakeInt(int64(i)))
	}
	args := []vm.Slot{vm.MakeArray(arr)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_ArraySum_Go(b *testing.B) {
	src := make([]int64, 100000)
	for i := range src {
		src[i] = int64(i)
	}
	b.ReportAllocs()
	var s int64
	for i := 0; i < b.N; i++ {
		s = 0
		for _, v := range src {
			s += v
		}
	}
	_ = s
}

// ---------- preg_match (простой шаблон) ----------

func BenchmarkIso2_Preg_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "preg_match")
	args := []vm.Slot{vm.MakeString("~[a-z]+~"), vm.MakeString(strings.Repeat("123 abc def ", 100))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso2_Preg_Go(b *testing.B) {
	re := regexp.MustCompile(`[a-z]+`)
	in := strings.Repeat("123 abc def ", 100)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = re.FindStringIndex(in)
	}
}
