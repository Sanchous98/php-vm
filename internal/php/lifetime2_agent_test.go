package php

import (
	"strings"
	"testing"
)

// lifetime2_agent_test.go — регрессии времени жизни и семантики ссылок,
// добавленные вместе с FETCH_W-правками: ленивое основание W-цепочек,
// деструктор внутри выражения, собираемость циклов генератора/файбера,
// idempotent WeakReference::create.

// TestIndexWriteMovingBase — `$a[k1][k2] = v`: ключи и значение вычисляются
// до выборки основания, поэтому запись попадает в массив, который переменная
// содержит в момент записи (Zend: engine_assignExecutionOrder_002).
func TestIndexWriteMovingBase(t *testing.T) {
	got := runLifetime(t, `<?php
function f() { global $ee; $ee = array("new"); return 1; }
$ee = array("old");
$ee["k"][f()] = "v";
print_r($ee);`)
	want := "Array\n(\n    [0] => new\n    [k] => Array\n        (\n            [1] => v\n        )\n\n)\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestIndexWriteKeyOrder — порядок вычисления ключей цепочки: слева направо
// (внешние уровни выбираются позже), затем значение.
func TestIndexWriteKeyOrder(t *testing.T) {
	got := runLifetime(t, `<?php
function k() { global $n; return $n++; }
$n = 0; $a = array(array(0, 0), 0);
$a[k()][k()] = $n;
var_dump($a);`)
	// ключи 0 и 1, значение — уже 2 (порядок k(), k(), $n).
	want := "array(2) {\n  [0]=>\n  array(2) {\n    [0]=>\n    int(0)\n    [1]=>\n    int(2)\n  }\n  [1]=>\n  int(0)\n}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestListAssignmentWarns — деструктуризация массива с недостающими ключами
// печатает Warning "Undefined array key" (Zend FETCH_LIST_R), но строка/скаляр
// диагностик не порождает.
func TestListAssignmentWarns(t *testing.T) {
	got := runLifetime(t, `<?php
list($a, $b) = [1];
echo "b=", var_export($b, true), "\n";
list($c) = "x";
echo "c=", var_export($c, true), "\n";`)
	if !strings.Contains(got, "Warning: Undefined array key 1") {
		t.Fatalf("нет Warning про отсутствующий ключ: %q", got)
	}
	if strings.Contains(got, "Trying to access array offset") || strings.Contains(got, "Uninitialized string offset") {
		t.Fatalf("лишняя диагностика для не-массива: %q", got)
	}
	if !strings.Contains(got, "b=NULL") || !strings.Contains(got, "c=NULL") {
		t.Fatalf("got %q", got)
	}
}

// TestAssignRefDtorBeforeResult — `$a = &$tmp` с деструктором старого значения:
// деструктор запускается до чтения результата и может обнулить саму ячейку
// (Zend/tests/gh10168, assign_ref).
func TestAssignRefDtorBeforeResult(t *testing.T) {
	got := runLifetime(t, `<?php
class T { function __destruct() { $GLOBALS['a'] = null; } }
$a = new T;
$tmp = new T;
var_dump($a = &$tmp);`)
	if got != "NULL\n" {
		t.Fatalf("got %q, want NULL", got)
	}
}

// TestGeneratorSelfCycleCollected — сам-ссылка `use (&$gen)` делает цикл
// генератора собираемым только в gc_collect_cycles(), без удержания
// итератором (Zend/tests/generators/gh9750-001).
func TestGeneratorSelfCycleCollected(t *testing.T) {
	got := runLifetime(t, `<?php
class C { function __destruct() { echo "D\n"; } }
function f() {}
$gen = function ($c) use (&$gen) { f($gen, yield); };
$gen = $gen(new C());
foreach ($gen as $value) { break; }
$gen = null;
gc_collect_cycles();
echo "end\n";`)
	if got != "D\nend\n" {
		t.Fatalf("got %q, want D/end", got)
	}
}

// TestFiberLocalSurvivesSuspend — локальная переменная файбера держит объект
// до уничтожения файбера, а не до конца функции (gh9735).
func TestFiberLocalSurvivesSuspend(t *testing.T) {
	got := runLifetime(t, `<?php
class C { function __destruct() { echo "D\n"; } }
$f = new Fiber(function () { $c = new C(); Fiber::suspend(); });
$f->start();
gc_collect_cycles();
echo "suspended\n";
$f = null;
gc_collect_cycles();
echo "end\n";`)
	if got != "suspended\nD\nend\n" {
		t.Fatalf("got %q", got)
	}
}

// TestWeakRefCreateIdempotent — WeakReference::create на тот же объект
// возвращает ту же ссылку (zend_weakref_find).
func TestWeakRefCreateIdempotent(t *testing.T) {
	got := runLifetime(t, `<?php
$o = new stdClass;
$r1 = WeakReference::create($o);
$r2 = WeakReference::create($o);
var_dump($r1 === $r2);`)
	if got != "bool(true)\n" {
		t.Fatalf("got %q", got)
	}
}

// TestWeakMapValueDtorOnKeyUnset — удаление записи WeakMap (по уничтожению
// ключа) уничтожает значение в том же проходе.
func TestWeakMapValueDtorOnKeyUnset(t *testing.T) {
	got := runLifetime(t, `<?php
$m = new WeakMap;
$o = new stdClass;
$m[$o] = new class { function __destruct() { echo "D\n"; } };
unset($o);
echo "after\n";`)
	if got != "D\nafter\n" {
		t.Fatalf("got %q", got)
	}
}
