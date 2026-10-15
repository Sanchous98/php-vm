package php

import (
	"strings"
	"testing"
)

// Регрессионные тесты диагностик (Warning/Notice/Deprecated) по корневым
// тестам Zend/tests: тексты и порядок сверены с php 8.3.6.
//
// Все тесты используют runFileAt из phpt_dir_test.go: вывод VM сравнивается с
// ожидаемым (включая префикс "\n" у диагностик CLI).

func mustRun(t *testing.T, src, file string) string {
	t.Helper()
	out, err := runFileAt(src, file)
	if err != nil {
		if throwableOf(err) == nil {
			t.Fatalf("run error: %v", err)
		}
	}
	return out
}

func wantOutput(t *testing.T, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("output mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// Object -> int/float: явный каст даёт Warning и 1, сравнение — Notice.
func TestDiagTailObjectNumConvert(t *testing.T) {
	src := `<?php
class Foo { public $bar = "bat"; }
$o = new Foo;
var_dump((int)$o);
var_dump((float)$o);
var_dump($o == 1);
var_dump($o == 1.0);
`
	got := mustRun(t, src, "obj.php")
	want := "\nWarning: Object of class Foo could not be converted to int in obj.php on line 4\n" +
		"int(1)\n" +
		"\nWarning: Object of class Foo could not be converted to float in obj.php on line 5\n" +
		"float(1)\n" +
		"\nNotice: Object of class Foo could not be converted to int in obj.php on line 6\n" +
		"bool(true)\n" +
		"\nNotice: Object of class Foo could not be converted to float in obj.php on line 7\n" +
		"bool(true)\n"
	wantOutput(t, got, want)
}

// Array -> string/float в касте.
func TestDiagTailArrayConvert(t *testing.T) {
	src := `<?php
var_dump((string)array(1,2));
var_dump((float)array(1,2));
`
	got := mustRun(t, src, "arr.php")
	want := "\nWarning: Array to string conversion in arr.php on line 2\n" +
		"string(5) \"Array\"\n" +
		"float(1)\n"
	wantOutput(t, got, want)
}

// foreach по переменной, ставшей null в теле (by-ref обход): Warning.
func TestDiagTailForeachNullMidLoop(t *testing.T) {
	src := `<?php
$a = [42];
foreach ($a as &$c) {
    unset($a[0]);
    $a = null;
}
`
	got := mustRun(t, src, "fnull.php")
	want := "\nWarning: foreach() argument must be of type array|object, null given in fnull.php on line 3\n"
	wantOutput(t, got, want)
}

// Notice «Only variable references should be returned by reference»:
// `return;` в by-ref функции и неявный возврат; Deprecated для void+&.
func TestDiagTailByRefReturnNotice(t *testing.T) {
	src := `<?php
function &a() { return; }
$r =& a();
function &b() {}
$s =& b();
function &c(): void {}
$t =& c();
`
	got := mustRun(t, src, "br.php")
	want := "\nDeprecated: Returning by reference from a void function is deprecated in br.php on line 6\n" +
		"\nNotice: Only variable references should be returned by reference in br.php on line 2\n" +
		"\nNotice: Only variable references should be returned by reference in br.php on line 4\n" +
		"\nNotice: Only variable references should be returned by reference in br.php on line 6\n"
	wantOutput(t, got, want)
}

// `fn&() => ...` (стрелочная функция с возвратом по ссылке) — Notice.
func TestDiagTailArrowByRef(t *testing.T) {
	src := `<?php
$f = fn&() => true;
var_dump($f());
`
	got := mustRun(t, src, "fn.php")
	want := "\nNotice: Only variable references should be returned by reference in fn.php on line 2\n" +
		"bool(true)\n"
	wantOutput(t, got, want)
}

// Closure::bindTo(null) для замыкания, использующего $this (в т.ч. isset($this)),
// и Closure::call() по встроенному классу.
func TestDiagTailClosureBind(t *testing.T) {
	src := `<?php
class A {
    private static $priv = 7;
    function getClosure() { return function() { var_dump(isset(A::$priv)); var_dump(isset($this)); }; }
}
$a = new A();
$scoped = $a->getClosure();
$d = $scoped->bindTo(null, null);
var_dump($d);
$bar = function () { return $this->x; };
$std = new stdClass;
var_dump($bar->call($std));
`
	got := mustRun(t, src, "cb.php")
	want := "\nWarning: Cannot unbind $this of closure using $this in cb.php on line 8\n" +
		"NULL\n" +
		"\nWarning: Cannot bind closure to scope of internal class stdClass in cb.php on line 12\n" +
		"NULL\n"
	wantOutput(t, got, want)
}

// «Undefined variable» при чтении до записи: прямая, цикл, ++, составное
// присваивание; isset/?? и by-ref аргументы не диагностируются.
func TestDiagTailUndefReadBeforeWrite(t *testing.T) {
	src := `<?php
function f1() { $c = $c + 1; var_dump($c); }
f1();
function f2() { $i++; var_dump($i); }
f2();
function f3() { $j += 2; var_dump($j); }
f3();
function f4() { while (true) { var_dump($a + 1); $a = 1; break; } }
f4();
function f5() { var_dump($u ?? 1); echo isset($v) ? "y" : "n", "\n"; }
f5();
`
	got := mustRun(t, src, "uv.php")
	want := "\nWarning: Undefined variable $c in uv.php on line 2\n" +
		"int(1)\n" +
		"\nWarning: Undefined variable $i in uv.php on line 4\n" +
		"int(1)\n" +
		"\nWarning: Undefined variable $j in uv.php on line 6\n" +
		"int(2)\n" +
		"\nWarning: Undefined variable $a in uv.php on line 8\n" +
		"int(1)\n" +
		"int(1)\n" +
		"n\n"
	wantOutput(t, got, want)
}

// extract(): ключ "this" всегда Error; переменные, определённые extract(),
// не диагностируются, неопределённые — да.
func TestDiagTailExtract(t *testing.T) {
	src := `<?php
function f() {
    try { extract(["this" => 42, "a" => 24]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
    var_dump($a);
}
f();
function g() {
    extract(["x" => 5]);
    echo $x, "\n";
    var_dump($y);
}
g();
`
	got := mustRun(t, src, "ex.php")
	want := "Cannot re-assign $this\n" +
		"\nWarning: Undefined variable $a in ex.php on line 4\n" +
		"NULL\n" +
		"5\n" +
		"\nWarning: Undefined variable $y in ex.php on line 10\n" +
		"NULL\n"
	wantOutput(t, got, want)
}

// unset($GLOBALS['x']) и unset($$name) удаляют переменную (чтение после —
// «Undefined variable»).
func TestDiagTailGlobalsUnset(t *testing.T) {
	src := `<?php
$x = "ok\n";
echo $x;
unset($GLOBALS["x"]);
echo $x;
$key = "asdf";
foreach (get_defined_vars() as $key => $value) { unset($$key); }
var_dump($key);
`
	got := mustRun(t, src, "gu.php")
	want := "ok\n" +
		"\nWarning: Undefined variable $x in gu.php on line 5\n" +
		"\nWarning: Undefined variable $key in gu.php on line 8\n" +
		"NULL\n"
	wantOutput(t, got, want)
}

// Косвенная модификация overloaded-свойства: Notice + запись без эффекта;
// `=&` в overloaded-свойство — Notice и Error.
func TestDiagTailOverloadedProp(t *testing.T) {
	src := `<?php
class A {
    public $arr = array();
    function __get($n) { return $this->arr; }
}
$a = new A;
$a->x[] = 1;
var_dump($a->x);
$b = "v";
try { $a->y =& $b; } catch (Error $e) { echo $e->getMessage(), "\n"; }
`
	got := mustRun(t, src, "ov.php")
	want := "\nNotice: Indirect modification of overloaded property A::$x has no effect in ov.php on line 7\n" +
		"array(0) {\n}\n" +
		"\nNotice: Indirect modification of overloaded property A::$y has no effect in ov.php on line 10\n" +
		"Cannot assign by reference to overloaded object\n"
	wantOutput(t, got, want)
}

// `use A;` — Warning о некомпаундном имени (в т.ч. в eval через обработчик).
func TestDiagTailUseNonCompound(t *testing.T) {
	src := `<?php
use A;
use \B;
`
	got := mustRun(t, src, "use.php")
	want := "\nWarning: The use statement with non-compound name 'A' has no effect in use.php on line 2\n" +
		"\nWarning: The use statement with non-compound name 'B' has no effect in use.php on line 3\n"
	wantOutput(t, got, want)

	ev := `<?php
set_error_handler(function ($c, $m, $f, $l) { echo $m, "\n"; });
eval('namespace {use Exception;}');
`
	got2 := mustRun(t, ev, "ev.php")
	wantOutput(t, got2, "The use statement with non-compound name 'Exception' has no effect\n")
}

// continue в switch: Warning «equivalent to break» (+ подсказка уровня).
func TestDiagTailContinueSwitch(t *testing.T) {
	src := `<?php
function t() {
    switch ($foo) {
        case 0:
            continue;
    }
    while ($foo) {
        switch ($bar) {
            case 0:
                continue;
        }
    }
}
`
	got := mustRun(t, src, "cs.php")
	want := "\nWarning: \"continue\" targeting switch is equivalent to \"break\" in cs.php on line 5\n" +
		"\nWarning: \"continue\" targeting switch is equivalent to \"break\". Did you mean to use \"continue 2\"? in cs.php on line 10\n"
	wantOutput(t, got, want)
}

// require() для отсутствующего потока: Warning + Error с include_path.
func TestDiagTailRequireOpenFail(t *testing.T) {
	src := `<?php
require '://@';
`
	got := mustRun(t, src, "req.php")
	if !strings.HasPrefix(got, "\nWarning: require(://@): Failed to open stream: No such file or directory in req.php on line 2\n") {
		t.Errorf("warning missing: %q", got)
	}
	if !strings.Contains(got, "Fatal error: Uncaught Error: Failed opening required '://@' (include_path='") {
		t.Errorf("fatal text mismatch: %q", got)
	}
}

// Illegal string offset при by-ref обходе строки.
func TestDiagTailIllegalStringOffsetByRef(t *testing.T) {
	src := `<?php
$a = 'aaa';
foreach ($a['2bbb'] as &$value) {}
`
	got := mustRun(t, src, "so.php")
	want := "\nWarning: Illegal string offset \"2bbb\" in so.php on line 3\n" +
		"\nFatal error: Uncaught Error: Cannot create references to/from string offsets in so.php:3\n" +
		"Stack trace:\n#0 {main}\n  thrown in so.php on line 3\n"
	wantOutput(t, got, want)
}
