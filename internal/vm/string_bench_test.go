package vm

import (
	"strings"
	"testing"
)

// Бенчмарки строковых слотов. Значение строки хранится прямо в слоте
// (ptr — данные, num — длина), поэтому MakeString не должен аллоцировать
// ничего, кроме самих данных строки (когда они вычисляются на месте).

var sinkString string

const benchAlphabet = "the quick brown fox jumps over the lazy dog 0123456789"

// Конкатенация: s = s . "x" (как OpConcatApp — накопление в буфере).
func BenchmarkStringConcat(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		s := appendStringSlot(MakeNull(), "", "abc")
		for j := 0; j < 64; j++ {
			s = appendStringSlot(s, s.StringVal(), "x")
		}
		sinkString = s.StringVal()
	}
}

// Цепочка из 5 частей (как OpConcatN): одна аллокация на результат.
func BenchmarkStringConcatChain(b *testing.B) {
	parts := []string{"user_", "42", "_", "84", "@example.com"}
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkSlot = concatNResult(parts, total)
	}
}

// substr: вырезание подстроки (как builtin substr по готовой строке).
func BenchmarkStringSubstr(b *testing.B) {
	src := MakeString("abcdefghijklmnopqrstuvwxyz0123456789")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		v := src.StringVal()
		sinkSlot = MakeString(v[3:20])
	}
}

// str_repeat: многократное повторение строки.
func BenchmarkStringRepeat(b *testing.B) {
	s := MakeString("ab")
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		sinkSlot = MakeString(strings.Repeat(s.StringVal(), 12))
	}
}

// Обход массива строк: построение массива из MakeString и чтение значений
// (foreach ($a as $v) { $n += strlen($v); }).
func BenchmarkStringArrayWalk(b *testing.B) {
	src := make([]string, 256)
	for i := range src {
		src[i] = benchAlphabet
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		a := newArray()
		for _, s := range src {
			a.Append(MakeString(s))
		}
		n := 0
		for _, v := range a.Values() {
			n += len(v.StringVal())
		}
		sinkInt = int64(n)
	}
}

// Изолированно: упаковка/чтение строкового слота (без данных — строка
// литеральная, поэтому аллокация — только сам слот).
func BenchmarkStringSlotRoundtrip(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		sinkSlot = MakeString(benchAlphabet)
		sinkString = sinkSlot.StringVal()
	}
}
