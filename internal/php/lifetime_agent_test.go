package php

import (
	"strings"
	"testing"
)

// lifetime_agent_test.go — регрессионные тесты детерминированного времени
// жизни объектов (internal/vm/lifetime.go): __destruct при потере последней
// ссылки, переиспользование PHP-хендлов (#N), слабые ссылки и gc_collect_cycles.

func runLifetime(t *testing.T, src string) string {
	t.Helper()
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v (out=%q)", err, out)
	}
	return out
}

// TestLifetimeDestructLocalUnset — unset() локальной переменной вызывает
// __destruct до следующего оператора (и до конца функции).
func TestLifetimeDestructLocalUnset(t *testing.T) {
	got := runLifetime(t, `<?php
class A { function __destruct() { echo "D\n"; } }
function f() { $o = new A; unset($o); echo "in\n"; }
f();
echo "after\n";`)
	if want := "D\nin\nafter\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeDestructGlobalOverwrite — перезапись переменной уничтожает
// объект, только когда на него не осталось других ссылок (статика держит).
func TestLifetimeDestructGlobalOverwrite(t *testing.T) {
	got := runLifetime(t, `<?php
class A { public static $keep; function __destruct() { echo "D\n"; } }
$o = new A;
A::$keep = $o;
$o = null;
echo "alive\n";
A::$keep = null;
echo "end\n";`)
	if want := "alive\nD\nend\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeHandleReuse — PHP-хендл уничтоженного объекта переиспользуется
// следующим созданным (Zend берёт номер из free-list).
func TestLifetimeHandleReuse(t *testing.T) {
	got := runLifetime(t, `<?php
class X {}
$a = new X;
var_dump($a);
unset($a);
$b = new X;
var_dump($b);`)
	if strings.Count(got, "object(X)#1") != 2 {
		t.Fatalf("хендл не переиспользован: %q", got)
	}
}

// TestLifetimeDestructVisibility — вызов недоступного __destruct при unset
// поднимает Error сразу, а не откладывается до shutdown.
func TestLifetimeDestructVisibility(t *testing.T) {
	out, err := runFile(`<?php
class A { private function __destruct() {} }
$a = new A;
unset($a);
echo "unreachable\n";`)
	if err == nil {
		t.Fatalf("ожидалась непойманная Error, вывод %q", out)
	}
	if !strings.Contains(out, "Call to private A::__destruct() from global scope") {
		t.Fatalf("неверная диагностика: %q", out)
	}
}

// TestLifetimeWeakMapKeyRemoved — запись WeakMap исчезает, когда ключ-объект
// теряет последнюю ссылку.
func TestLifetimeWeakMapKeyRemoved(t *testing.T) {
	got := runLifetime(t, `<?php
$map = new WeakMap;
$obj = new stdClass;
$map[$obj] = 1;
echo count($map), "\n";
unset($obj);
echo count($map), "\n";`)
	if want := "1\n0\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeWeakMapDiscardedTemp — временный ключ-объект
// (`$map[new stdClass] = 1`) уничтожается в конце оператора.
func TestLifetimeWeakMapDiscardedTemp(t *testing.T) {
	got := runLifetime(t, `<?php
$map = new WeakMap;
$map[new stdClass] = 1;
echo count($map), "\n";`)
	if want := "0\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeWeakRefCleared — WeakReference::get() возвращает null после
// уничтожения цели.
func TestLifetimeWeakRefCleared(t *testing.T) {
	got := runLifetime(t, `<?php
$o = new stdClass;
$wr = WeakReference::create($o);
var_dump($wr->get() !== null);
unset($o);
var_dump($wr->get());`)
	if want := "bool(true)\nNULL\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeGeneratorClosedOnUnset — уничтожение незавершённого генератора
// прогоняет finally.
func TestLifetimeGeneratorClosedOnUnset(t *testing.T) {
	got := runLifetime(t, `<?php
function gen() {
    try { yield 1; } finally { echo "finally\n"; }
}
$g = gen();
$g->current();
unset($g);
echo "end\n";`)
	if want := "finally\nend\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeGCCounts — gc_collect_cycles считает собранные объекты и
// массивы циклического мусора.
func TestLifetimeGCCounts(t *testing.T) {
	got := runLifetime(t, `<?php
$a = new stdClass();
$a->a = $a;
unset($a);
var_dump(gc_collect_cycles());

$b = array();
$b[] =& $b;
unset($b);
var_dump(gc_collect_cycles());`)
	if want := "int(1)\nint(1)\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestLifetimeDestructOnOverwriteStmt — деструктор старого значения
// вызывается при присваивании, до следующего вывода.
func TestLifetimeDestructOnOverwriteStmt(t *testing.T) {
	got := runLifetime(t, `<?php
class A { function __destruct() { echo "D\n"; } }
$o = new A;
$o = new A;
echo "end\n";`)
	if want := "D\nend\nD\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
