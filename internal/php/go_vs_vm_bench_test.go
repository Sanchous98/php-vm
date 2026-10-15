package php

// Парные бенчмарки: «наш VM на PHP-скрипте» против «идиоматичный Go».
// Оба варианта делают ОДИН и тот же объём работы; отношение ns/op показывает,
// во сколько раз интерпретация PHP-строки дороже прямого Go-кода.
//
// Запуск: go test -run '^$' -bench 'GoVs' -count=3 ./internal/php/

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// ---------- sprintf ----------

func BenchmarkGoVsVM_Sprintf(b *testing.B) {
	benchPHP(b, `<?php $s=""; for($i=0;$i<20000;$i++){ $s = sprintf("%d-%s", $i, "x"); } echo strlen($s);`)
}

func BenchmarkGoVsGo_Sprintf(b *testing.B) {
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		for j := 0; j < 20000; j++ {
			s = fmt.Sprintf("%d-%s", j, "x")
		}
	}
	_ = s
}

// ---------- implode ----------

func BenchmarkGoVsVM_Implode(b *testing.B) {
	benchPHP(b, `<?php $a=[]; for($i=0;$i<20000;$i++){ $a[] = (string)$i; } echo strlen(implode(",", $a));`)
}

func BenchmarkGoVsGo_Implode(b *testing.B) {
	b.ReportAllocs()
	var out string
	for i := 0; i < b.N; i++ {
		a := make([]string, 0, 20000)
		for j := 0; j < 20000; j++ {
			a = append(a, strconv.Itoa(j))
		}
		out = strings.Join(a, ",")
	}
	_ = out
}

// ---------- strtr (массив) ----------

func BenchmarkGoVsVM_Strtr(b *testing.B) {
	benchPHP(b, `<?php
$pairs = [];
for ($i = 0; $i < 60; $i++) { $pairs["k".$i."_longer"] = "v".$i; }
$s = str_repeat("aaa k5_longer bbb k59_longer ccc ", 200);
for ($i = 0; $i < 2000; $i++) { $s = strtr($s, $pairs); }
echo strlen($s);`)
}

func BenchmarkGoVsGo_Strtr(b *testing.B) {
	b.ReportAllocs()
	var out string
	for i := 0; i < b.N; i++ {
		oldnew := make([]string, 0, 120)
		for j := 0; j < 60; j++ {
			oldnew = append(oldnew, "k"+strconv.Itoa(j)+"_longer", "v"+strconv.Itoa(j))
		}
		r := strings.NewReplacer(oldnew...)
		s := strings.Repeat("aaa k5_longer bbb k59_longer ccc ", 200)
		for j := 0; j < 2000; j++ {
			s = r.Replace(s)
		}
		out = s
	}
	_ = out
}

// ---------- usort ----------

func BenchmarkGoVsVM_Usort(b *testing.B) {
	benchPHP(b, `<?php
for ($k = 0; $k < 100; $k++) {
    $a = [];
    for ($i = 0; $i < 300; $i++) { $a[] = ($i * 7919 + $k) % 100000; }
    usort($a, function ($x, $y) { return $x <=> $y; });
}
echo "ok";`)
}

func BenchmarkGoVsGo_Usort(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		for k := 0; k < 100; k++ {
			a := make([]int, 0, 300)
			for j := 0; j < 300; j++ {
				a = append(a, (j*7919+k)%100000)
			}
			sort.SliceStable(a, func(x, y int) bool { return a[x] < a[y] })
		}
	}
}

// ---------- запись объявленного свойства ----------

func BenchmarkGoVsVM_PropWrite(b *testing.B) {
	benchPHP(b, `<?php class P { public $a = 0; public $b = 0; } $p = new P;
for ($i = 0; $i < 200000; $i++) { $p->a = $i; $p->b = $p->a + 1; }
echo $p->b;`)
}

func BenchmarkGoVsGo_PropWrite(b *testing.B) {
	b.ReportAllocs()
	type P struct{ a, b int64 }
	for i := 0; i < b.N; i++ {
		var p P
		for j := int64(0); j < 200000; j++ {
			p.a = j
			p.b = p.a + 1
		}
		_ = p
	}
}

// ---------- вызов метода ----------

func BenchmarkGoVsVM_MethodCall(b *testing.B) {
	benchPHP(b, `<?php class M { public $v = 0; public function add($x) { $this->v += $x; return $this; } }
$m = new M; for ($i = 0; $i < 200000; $i++) { $m->add(1); } echo $m->v;`)
}

func BenchmarkGoVsGo_MethodCall(b *testing.B) {
	b.ReportAllocs()
	type M struct{ v int64 }
	for i := 0; i < b.N; i++ {
		m := &M{}
		for j := int64(0); j < 200000; j++ {
			m.v += 1
		}
		_ = m
	}
}

// ---------- конкатенация строк в цикле ----------

func BenchmarkGoVsVM_StrBuilder(b *testing.B) {
	benchPHP(b, `<?php $s = ""; for ($i = 0; $i < 200000; $i++) { $s .= "abc"; } echo strlen($s);`)
}

func BenchmarkGoVsGo_StrBuilder(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var sb strings.Builder
		sb.Grow(200000 * 3)
		for j := 0; j < 200000; j++ {
			sb.WriteString("abc")
		}
		_ = sb.Len()
	}
}

// ---------- ассоциативный массив (строковые ключи) ----------

func BenchmarkGoVsVM_AssocSet(b *testing.B) {
	benchPHP(b, `<?php $m = []; for ($i = 0; $i < 50000; $i++) { $m["k".$i] = $i; } echo count($m);`)
}

func BenchmarkGoVsGo_AssocSet(b *testing.B) {
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		m := make(map[string]int, 50000)
		for j := 0; j < 50000; j++ {
			m["k"+strconv.Itoa(j)] = j
		}
		_ = len(m)
	}
}

// ---------- str_repeat ----------

func BenchmarkGoVsVM_StrRepeat(b *testing.B) {
	benchPHP(b, `<?php $s = ""; for ($i = 0; $i < 20000; $i++) { $s = str_repeat("ab", 20); } echo strlen($s);`)
}

func BenchmarkGoVsGo_StrRepeat(b *testing.B) {
	b.ReportAllocs()
	var s string
	for i := 0; i < b.N; i++ {
		for j := 0; j < 20000; j++ {
			s = strings.Repeat("ab", 20)
		}
	}
	_ = s
}
