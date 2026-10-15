package php

// Изолированные замеры: НАША реализация встроенной функции (прямой вызов
// нативной Go-функции, без интерпретатора) против идиоматичного Go-эквивалента.
// Отношение показывает, насколько сама реализация отличается от «просто Go».
//
// Запуск: go test -run '^$' -bench 'Iso_' -benchmem -count=3 ./internal/php/

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"io"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

func isoVM(b *testing.B) *vm.VM {
	b.Helper()
	c, err := Compile("<?php 1;")
	if err != nil {
		b.Fatal(err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetOutput(io.Discard)
	m.SetStepLimit(0)
	return &m
}

func isoNative(tb testing.TB, name string) *vm.NativeFunction {
	for _, f := range append(builtins(), internalFuncs()...) {
		if f.Name == name {
			if nf, ok := f.Executable.(*vm.NativeFunction); ok {
				return nf
			}
			tb.Fatalf("%s: не нативная функция", name)
		}
	}
	tb.Fatalf("нативная %s не найдена", name)
	return nil
}

func isoCall(b *testing.B, m *vm.VM, nf *vm.NativeFunction, args []vm.Slot) {
	ctx := vm.NewContextForBench(m, args)
	if _, err := nf.Call(m, ctx, args); err != nil {
		b.Fatal(err)
	}
}

// ---------- sprintf ----------

func BenchmarkIso_Sprintf_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "sprintf")
	args := []vm.Slot{vm.MakeString("%d-%s"), vm.MakeInt(0), vm.MakeString("x")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		args[1] = vm.MakeInt(int64(i))
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Sprintf_Go(b *testing.B) {
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		s = fmt.Sprintf("%d-%s", i, "x")
	}
	_ = s
}

// ---------- implode ----------

func BenchmarkIso_Implode_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "implode")
	arr := vm.NewArray()
	for i := 0; i < 20000; i++ {
		_, _ = arr.Append(vm.MakeInt(int64(i)))
	}
	args := []vm.Slot{vm.MakeString(","), vm.MakeArray(arr)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Implode_Go(b *testing.B) {
	nums := make([]int, 20000)
	for i := range nums {
		nums[i] = i
	}
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		parts := make([]string, 0, len(nums))
		for _, v := range nums {
			parts = append(parts, strconv.Itoa(v))
		}
		s = strings.Join(parts, ",")
	}
	_ = s
}

// ---------- str_replace ----------

func BenchmarkIso_StrReplace_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "str_replace")
	args := []vm.Slot{vm.MakeString("abc"), vm.MakeString("1234"), vm.MakeString(strings.Repeat("abc def xyz abc ", 20))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_StrReplace_Go(b *testing.B) {
	subject := strings.Repeat("abc def xyz abc ", 20)
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		s = strings.ReplaceAll(subject, "abc", "1234")
	}
	_ = s
}

// ---------- substr_count ----------

func BenchmarkIso_SubstrCount_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "substr_count")
	args := []vm.Slot{vm.MakeString(strings.Repeat("ab cd ab ef ", 150)), vm.MakeString("ab")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_SubstrCount_Go(b *testing.B) {
	hay := strings.Repeat("ab cd ab ef ", 150)
	b.ReportAllocs()
	var n int
	for i := 0; i < b.N; i++ {
		n = strings.Count(hay, "ab")
	}
	_ = n
}

// ---------- md5 / crc32 ----------

func BenchmarkIso_Md5_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "md5")
	args := []vm.Slot{vm.MakeString(strings.Repeat("The quick brown fox ", 200))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Md5_Go(b *testing.B) {
	data := []byte(strings.Repeat("The quick brown fox ", 200))
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		sum := md5.Sum(data)
		s = hex.EncodeToString(sum[:])
	}
	_ = s
}

func BenchmarkIso_Crc32_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "crc32")
	args := []vm.Slot{vm.MakeString(strings.Repeat("The quick brown fox ", 200))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Crc32_Go(b *testing.B) {
	data := []byte(strings.Repeat("The quick brown fox ", 200))
	b.ReportAllocs()
	var n uint32
	for i := 0; i < b.N; i++ {
		n = crc32.ChecksumIEEE(data)
	}
	_ = n
}

// ---------- urlencode ----------

func BenchmarkIso_Urlencode_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "urlencode")
	args := []vm.Slot{vm.MakeString(strings.Repeat("hello world a=b+c/d", 20))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Urlencode_Go(b *testing.B) {
	in := strings.Repeat("hello world a=b+c/d", 20)
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		s = url.QueryEscape(in)
	}
	_ = s
}

// ---------- base64_decode ----------

func BenchmarkIso_Base64Decode_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "base64_decode")
	enc := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("payload-", 100)))
	args := []vm.Slot{vm.MakeString(enc)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Base64Decode_Go(b *testing.B) {
	enc := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("payload-", 100)))
	b.ReportAllocs()
	var out []byte
	for i := 0; i < b.N; i++ {
		out, _ = base64.StdEncoding.DecodeString(enc)
	}
	_ = out
}

// ---------- strtr (массив пар) ----------

func BenchmarkIso_StrtrPairs_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "strtr")
	pairs := vm.NewArray()
	for i := 0; i < 60; i++ {
		pairs.Set(vm.MakeStrKey("k"+strconv.Itoa(i)+"_longer"), vm.MakeString("v"+strconv.Itoa(i)))
	}
	args := []vm.Slot{vm.MakeString(strings.Repeat("aaa k5_longer bbb k59_longer ccc ", 100)), vm.MakeArray(pairs)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_StrtrPairs_Go(b *testing.B) {
	subject := strings.Repeat("aaa k5_longer bbb k59_longer ccc ", 100)
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		oldnew := make([]string, 0, 120)
		for j := 0; j < 60; j++ {
			oldnew = append(oldnew, "k"+strconv.Itoa(j)+"_longer", "v"+strconv.Itoa(j))
		}
		s = strings.NewReplacer(oldnew...).Replace(subject)
	}
	_ = s
}

// ---------- trim ----------

func BenchmarkIso_Trim_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "trim")
	args := []vm.Slot{vm.MakeString(" \t\n  hello world\t \n "), vm.MakeString(" \t\n")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Trim_Go(b *testing.B) {
	in := " \t\n  hello world\t \n "
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		s = strings.Trim(in, " \t\n")
	}
	_ = s
}

// ---------- str_repeat ----------

func BenchmarkIso_StrRepeat_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "str_repeat")
	args := []vm.Slot{vm.MakeString("ab"), vm.MakeInt(500)}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_StrRepeat_Go(b *testing.B) {
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		s = strings.Repeat("ab", 500)
	}
	_ = s
}

// ---------- strtolower (ASCII) ----------

func BenchmarkIso_Strtolower_Native(b *testing.B) {
	m := isoVM(b)
	nf := isoNative(b, "strtolower")
	args := []vm.Slot{vm.MakeString(strings.Repeat("Hello World ABC DEF ", 40))}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		isoCall(b, m, nf, args)
	}
}

func BenchmarkIso_Strtolower_Go(b *testing.B) {
	in := strings.Repeat("Hello World ABC DEF ", 40)
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		s = strings.ToLower(in)
	}
	_ = s
}
