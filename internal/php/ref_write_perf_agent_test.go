package php

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"phpvm/internal/vm"
)

// Тесты перф-правки записи через by-ref переменную (OpRefGetRaw/OpRefSetRaw/
// OpIndexRaw + in-place array_push): COW-семантика и «одна переменная»
// сохраняются, копирование всего массива на каждую запись убрано.
//
// Эталоны сняты с живого php 8.3 (php -r).

// runRefPHP выполняет код без лимита шагов (большие циклы в тестах).
func runRefPHP(t *testing.T, src string) string {
	t.Helper()
	c, err := Compile(src)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(0)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	if err := m.Run(); err != nil {
		if throwableOf(err) == nil {
			t.Fatalf("run: %v", err)
		}
	}
	return buf.String()
}

func refCheck(t *testing.T, src, want string) {
	t.Helper()
	got := runRefPHP(t, src)
	if got != want {
		t.Errorf("вывод VM:\n%s\nожидалось (php):\n%s", got, want)
	}
}

// TestRefWriteCOW — копирование при присваивании сохраняется: запись через
// $a не должна менять $b.
func TestRefWriteCOW(t *testing.T) {
	refCheck(t, `<?php
$a = [1, 2]; $b = $a; $a[] = 3; $a[0] = 9;
echo $b[0], $b[1], count($b), "\n";
echo $a[0], $a[2], "\n";
`, "122\n93\n")

	// Тот же сценарий, когда $a заведомо ref-переменная (мёртвый sort).
	refCheck(t, `<?php
$a = [1, 2]; $b = $a;
if (false) { sort($a); }
$a[] = 3; $a[0] = 9;
echo $b[0], $b[1], count($b), "\n";
echo $a[0], $a[2], "\n";
`, "122\n93\n")

	// Копия вложенного массива.
	refCheck(t, `<?php
$a = [[1]]; $b = $a[0];
if (false) { sort($a); }
$a[0][] = 2;
echo count($b), " ", $b[0], " ", $a[0][0], $a[0][1], "\n";
`, "1 1 12\n")
}

// TestRefWriteAlias — ссылки видят запись в любую из сторон.
func TestRefWriteAlias(t *testing.T) {
	refCheck(t, `<?php
$a = []; $r =& $a; $a[] = 1; $a[5] = 2; $r[] = 3;
echo count($r), " ", $r[0], $r[5], $r[6], "\n";
echo $a[0], $a[5], $a[6], "\n";
`, "3 123\n123\n")

	refCheck(t, `<?php
$a = []; $x =& $a; $x[] = 1; sort($a); $a[] = 2;
echo count($x), " ", $x[0], $x[1], "\n";
`, "2 12\n")

	refCheck(t, `<?php
$a = [1]; $r =& $a; $a += [5]; $a[0] = 7; $a[] = 2;
echo implode(",", $r), "\n";
`, "7,2\n")
}

// TestRefWriteByRefParam — запись через by-ref параметр меняет аргумент.
func TestRefWriteByRefParam(t *testing.T) {
	refCheck(t, `<?php
function f(&$x) { $x[] = 1; $x[0] = 9; }
$a = [0]; f($a); f($a);
echo count($a), " ", $a[0], $a[1], $a[2], "\n";
`, "3 911\n")

	refCheck(t, `<?php
function f(&$x) { for ($i = 0; $i < 3; $i++) { array_push($x, $i); } }
$a = []; f($a);
echo implode(",", $a), "\n";
`, "0,1,2\n")
}

// TestRefWriteNested — вложенные массивы через ref.
func TestRefWriteNested(t *testing.T) {
	refCheck(t, `<?php
$a = []; $r =& $a;
$a[0][] = 1; $a[0][2] = 3; $a[1][0] = 4;
echo count($a), " ", count($a[0]), " ", $a[0][0], $a[0][2], $a[1][0], "\n";
`, "2 2 134\n")

	refCheck(t, `<?php
function f(&$x) { $x["k"][] = "v"; $x["k"][] = "w"; }
$a = []; f($a);
echo count($a["k"]), " ", implode(",", $a["k"]), "\n";
`, "2 v,w\n")
}

// TestRefWriteArrayPush — array_push по ref-переменной; COW сохраняется.
func TestRefWriteArrayPush(t *testing.T) {
	refCheck(t, `<?php
$a = [1]; $b = $a; array_push($a, 2, 3);
echo implode(",", $b), "|", implode(",", $a), "\n";
$r =& $a; array_push($a, 4);
echo implode(",", $r), "\n";
sort($a); $a[] = 0;
echo implode(",", $a), "\n";
`, "1|1,2,3\n1,2,3,4\n1,2,3,4,0\n")
}

// TestRefWriteUnsetIncDec — unset/op=/++ через ref-базу.
func TestRefWriteUnsetIncDec(t *testing.T) {
	refCheck(t, `<?php
$a = [1, 2, 3]; $r =& $a;
unset($a[1]); $a[5] = 5; $a[5] += 2; $a[5]++;
$a[0] .= "x";
echo count($r), " ", $r[0], " ", $r[5], "\n";
`, "3 1x 8\n")
}

// TestRefWriteDeadByRefFast — маркировка by-ref из мёртвого кода не должна
// превращать сборку массива в O(n^2).
func TestRefWriteDeadByRefFast(t *testing.T) {
	src := `<?php
$a = [];
if (false) { sort($a); }
for ($i = 0; $i < 30000; $i++) { $a[] = $i; }
echo count($a), " ", $a[0], " ", $a[29999], "\n";
`
	start := time.Now()
	got := runRefPHP(t, src)
	el := time.Since(start)
	if got != "30000 0 29999\n" {
		t.Fatalf("вывод: %q", got)
	}
	if el > 3*time.Second {
		t.Fatalf("30k append заняли %v (ожидалось < 3s)", el)
	}
}

// TestRefWriteLargeAppendSort — 300k append + sort за пару секунд.
func TestRefWriteLargeAppendSort(t *testing.T) {
	src := `<?php
$a = [];
for ($i = 0; $i < 300000; $i++) { $a[] = ($i * 7919) % 1000000; }
sort($a);
echo $a[0], " ", $a[299999], "\n";
`
	start := time.Now()
	got := runRefPHP(t, src)
	el := time.Since(start)
	if got != "0 999960\n" {
		t.Fatalf("вывод: %q", got)
	}
	if el > 5*time.Second {
		t.Fatalf("300k append+sort заняли %v (ожидалось < 5s)", el)
	}
}

// TestRefWriteArrayPushFast — array_push в цикле без копирования массива.
func TestRefWriteArrayPushFast(t *testing.T) {
	src := `<?php
$a = [];
for ($i = 0; $i < 30000; $i++) { array_push($a, $i); }
echo count($a), " ", $a[29999], "\n";
`
	start := time.Now()
	got := runRefPHP(t, src)
	el := time.Since(start)
	if got != "30000 29999\n" {
		t.Fatalf("вывод: %q", got)
	}
	if el > 3*time.Second {
		t.Fatalf("30k array_push заняли %v (ожидалось < 3s)", el)
	}
}

// TestRefWriteSortThenAppendInPlace — после sort($a) ячейка снова владеет
// уникальным массивом: последующие $a[]= не копируют его.
func TestRefWriteSortThenAppendInPlace(t *testing.T) {
	src := `<?php
$a = range(0, 29999);
sort($a);
for ($i = 0; $i < 30000; $i++) { $a[] = $i; }
echo count($a), " ", $a[0], " ", $a[59999], "\n";
`
	start := time.Now()
	got := runRefPHP(t, src)
	el := time.Since(start)
	if got != "60000 0 29999\n" {
		t.Fatalf("вывод: %q", got)
	}
	if el > 3*time.Second {
		t.Fatalf("range+sort+30k append заняли %v (ожидалось < 3s)", el)
	}
}

// TestRefWriteGlobalRef — ref-переменная в глобальной таблице (global/$_).
func TestRefWriteGlobalRef(t *testing.T) {
	refCheck(t, `<?php
function f() { global $g; $g[] = 1; if (false) { sort($g); } }
$g = [0]; $r =& $g; f(); $g[] = 2; $r[] = 3;
echo implode(",", $g), "\n";
`, "0,1,2,3\n")
}

// TestRefWriteInterleavedRead — чтение через $a[..] между записями не должно
// ломать COW (копия допустима, корректность обязательна).
func TestRefWriteInterleavedRead(t *testing.T) {
	refCheck(t, `<?php
$a = [1, 2]; $b = $a;
if (false) { sort($a); }
for ($i = 0; $i < 3; $i++) { $a[] = $a[0]; }
echo implode(",", $a), "|", implode(",", $b), "\n";
`, "1,2,1,1,1|1,2\n")
}

// TestRefWriteSelfAppend — значение вычисляется до append.
func TestRefWriteSelfAppend(t *testing.T) {
	refCheck(t, `<?php
$a = [1];
if (false) { sort($a); }
$a[] = $a;
echo count($a), " ", $a[0], " ", count($a[1]), " ", $a[1][0], "\n";
`, "2 1 1 1\n")
}

// TestRefWriteListAssign — list()/[$a[k]]= через ref-базу.
func TestRefWriteListAssign(t *testing.T) {
	refCheck(t, `<?php
$a = []; $r =& $a;
[$a[0], $a[1]] = [7, 8];
list($a[2], $a[3]) = [9, 10];
echo implode(",", $r), "\n";
`, "7,8,9,10\n")
}

// TestRefWriteForeachPreserved — foreach по массиву не ломается записями
// через ту же ref-переменную.
func TestRefWriteForeachPreserved(t *testing.T) {
	refCheck(t, `<?php
$a = [1, 2, 3]; $r =& $a;
$s = 0;
foreach ($a as $v) { $s += $v; }
echo $s, "\n";
`, "6\n")

	// foreach по копии не видит последующие записи.
	refCheck(t, `<?php
$a = [1, 2]; $b = $a;
if (false) { sort($a); }
foreach ($b as $v) { $a[] = $v; }
echo implode(",", $b), "|", implode(",", $a), "\n";
`, "1,2|1,2,1,2\n")
}

// TestRefWriteNoShareLeak — запись через ref не должна помечать массив
// разделённым: строка с $b=$a до цикла остаётся копией.
func TestRefWriteNoShareLeak(t *testing.T) {
	out := runRefPHP(t, `<?php
$a = [0, 1, 2]; $b = $a;
if (false) { sort($a); }
for ($i = 0; $i < 10000; $i++) { $a[] = $i; }
$b[] = 99;
echo count($a), " ", count($b), " ", $b[3], "\n";
`)
	if strings.TrimSpace(out) != "10003 4 99" {
		t.Fatalf("вывод: %q", out)
	}
}
