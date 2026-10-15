package php

import (
	"strings"
	"testing"
)

// gc_agent_test.go — регрессии сборщика циклов (gc-счётчики, weakrefs-соседние
// сценарии): модель счёта Zend, поведение деструкторов и gc_status().
//
// Гейты: Zend/tests/gc-набор (gc_0*.phpt, bug70805*, bug72543*) и
// Zend/tests/weakrefs.

func runGC(t *testing.T, src string) string {
	t.Helper()
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// TestGCAgentSelfRefArray: собранный само-ссылочный массив = 1.
func TestGCAgentSelfRefArray(t *testing.T) {
	got := runGC(t, `<?php
$a = array();
$a[] =& $a;
unset($a);
var_dump(gc_collect_cycles());
`)
	if want := "int(1)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentObjArrayCycle: объект + вложенный массив в цикле = 2 (gc_006).
func TestGCAgentObjArrayCycle(t *testing.T) {
	got := runGC(t, `<?php
$a = new stdClass();
$a->a = array();
$a->a[0] =& $a;
unset($a);
var_dump(gc_collect_cycles());
`)
	if want := "int(2)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentDtorCycleNested: деструктор, снимающий ссылки на соседей по
// циклу, не даёт считать их собранными (gc_017: 1, а не 3), но деструкторы
// всех трёх узлов выполняются.
func TestGCAgentDtorCycleNested(t *testing.T) {
	got := runGC(t, `<?php
class Node {
    public $name; public $children; public $parent;
    function __construct($n) { $this->name = $n; $this->parent = null; }
    function insert($x) { $x->parent = $this; $this->children[] = $x; }
    function __destruct() { echo $this->name; unset($this->name); unset($this->children); unset($this->parent); }
}
$a = new Node('A'); $b = new Node('B'); $c = new Node('C');
$a->insert($b); $a->insert($c);
unset($a); unset($b); unset($c);
echo "|", gc_collect_cycles(), "|\n";
`)
	if want := "|ABC1|\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentMutualDtor: два объекта с деструкторами, разрывающими цикл = 1
// (gc_028/gc_029).
func TestGCAgentMutualDtor(t *testing.T) {
	got := runGC(t, `<?php
class Foo { public $bar; function __destruct() { if ($this->bar !== null) unset($this->bar); } }
class Bar { public $foo; function __destruct() { if ($this->foo !== null) unset($this->foo); } }
$foo = new Foo(); $bar = new Bar();
$foo->bar = $bar; $bar->foo = $foo;
unset($foo); unset($bar);
var_dump(gc_collect_cycles());
`)
	if want := "int(1)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentRefReadSnapshot: чтение свойства/элемента снимает значение в
// момент чтения — запись через ту же ссылку его не меняет (bug72543_3/4/5).
func TestGCAgentRefReadSnapshot(t *testing.T) {
	got := runGC(t, `<?php
$o = new stdClass; $o->a = 1;
$r =& $o->a;
unset($r);
var_dump($o->a + ($o->a = 2));
$a = [1];
$r =& $a[0];
unset($r);
var_dump($a[0] + ($a[0] = 2));
`)
	if want := "int(3)\nint(3)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentIncDecRefKeepsCell: ++/-- по элементу-ссылке и по ссылке на
// типизированное свойство работает через ячейку (guard переполнения,
// запись в свойство-цель): bug64417, typed_properties_064.
func TestGCAgentIncDecRefKeepsCell(t *testing.T) {
	got := runGC(t, `<?php
$a = [1];
$r =& $a[0];
$a[0]++;
echo "a:", $r, "\n";
$o = new class { public int $foo = 1; public $_; };
$o->_ = &$o->foo;
$o->_--;
echo "b:", $o->foo, "\n";
`)
	if want := "a:2\nb:0\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentAssignThroughSameCell: само-цикл, снятый присваиванием через ту
// же ячейку ссылки, не считается собранным — собирается только вложенный
// само-ссылочный массив (gc_020).
func TestGCAgentAssignThroughSameCell(t *testing.T) {
	got := runGC(t, `<?php
$a = array();
$a[0] =& $a;
$a[1] = array();
$a[1][0] =& $a[1];
$a = 1;
var_dump(gc_collect_cycles());
`)
	if want := "int(1)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentResurrected: деструктор, воскрешающий объект в $GLOBALS, не
// попадает в счёт, а объект остаётся рабочим (gc_041).
func TestGCAgentResurrected(t *testing.T) {
	got := runGC(t, `<?php
class ryat {
    var $ryat; var $chtg; var $nested;
    function __destruct() { $GLOBALS['x'] = $this; }
}
$o = new ryat;
$o->nested = [];
$o->nested[] =& $o->nested;
$o->ryat = $o;
$x =& $o->chtg;
unset($o);
var_dump(gc_collect_cycles());
var_dump($x->ryat->ryat === $x->ryat);
`)
	if want := "int(0)\nbool(true)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentDtorExceptionPropagates: исключение из __destruct внутри
// gc_collect_cycles поднимается в вызывающий код (error-протокол) и, если
// деструкторов несколько, сцепляется цепочкой previous (gc_030).
func TestGCAgentDtorExceptionPropagates(t *testing.T) {
	got := runGC(t, `<?php
class foo { public $foo; public function __destruct() { throw new Exception("foobar"); } }
$f1 = new foo; $f2 = new foo;
$f1->foo = $f2; $f2->foo = $f1;
unset($f1, $f2);
try {
    gc_collect_cycles();
} catch (Exception $e) {
    echo "caught:", $e->getMessage(), "\n";
    $p = $e->getPrevious();
    echo "prev:", $p === null ? "none" : $p->getMessage(), "\n";
}
`)
	if !strings.Contains(got, "caught:foobar\n") || !strings.Contains(got, "prev:foobar\n") {
		t.Fatalf("got %q", got)
	}
}

// TestGCAgentStatusCounters: статистика gc_status() ведётся помимо VM и
// сбрасывается между Init (runs/collected — Zend GC_G(gc_runs)/GC_G(collected)).
func TestGCAgentStatusCounters(t *testing.T) {
	got := runGC(t, `<?php
$s = gc_status();
echo $s["runs"], "|", $s["collected"], "|", $s["threshold"], "|", $s["buffer_size"], "\n";
$a = array();
$a[] =& $a;
unset($a);
var_dump(gc_collect_cycles());
$s = gc_status();
echo $s["runs"], "|", $s["collected"], "\n";
`)
	if want := "0|0|10001|16384\nint(1)\n1|1\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentArrayObjectStorage: storage ArrayObject — отдельный белый узел,
// цикл объект+storage = 2 (gc_024).
func TestGCAgentArrayObjectStorage(t *testing.T) {
	got := runGC(t, `<?php
$a = new ArrayObject();
$a[0] = $a;
unset($a);
var_dump(gc_collect_cycles());
`)
	if want := "int(2)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentUnserializeCycle: циклический массив из unserialize во временном
// значении считается собранным (gc_022).
func TestGCAgentUnserializeCycle(t *testing.T) {
	got := runGC(t, `<?php
$a = array(array());
$a[0][0] =& $a[0];
$s = array(1) + unserialize(serialize($a[0]));
var_dump(gc_collect_cycles());
`)
	if want := "int(1)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentMBParseStr: mb_parse_str возвращает true и заполняет by-ref
// аргумент; замена аргумента снимает цикл и он собирается (gc_039).
func TestGCAgentMBParseStr(t *testing.T) {
	got := runGC(t, `<?php
$out = new stdClass;
$out->x = $out;
var_dump(mb_parse_str("a=b", $out));
var_dump($out);
var_dump(gc_collect_cycles());
`)
	if want := "bool(true)\narray(1) {\n  [\"a\"]=>\n  string(1) \"b\"\n}\nint(1)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestGCAgentCallbackFilterIterator: фильтрующий итератор SPL (gc_046).
func TestGCAgentCallbackFilterIterator(t *testing.T) {
	got := runGC(t, `<?php
$it = new ArrayIterator([1, 2, 3, 4]);
$f = new CallbackFilterIterator($it, fn($v) => $v % 2 === 0);
foreach ($f as $k => $v) { echo "$k:$v "; }
echo "\n";
`)
	if want := "1:2 3:4 \n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
