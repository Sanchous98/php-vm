package php

import (
	"strings"
	"testing"
)

// Регрессионные тесты раунда: refcount-free семантика свойств/offset'ов,
// порядок операндов, магические методы, WeakReference/WeakMap, сравнение
// Stringable-объектов.

// TestAgentStringOffsetAssignResult: результат присваивания в строковый offset —
// фактически записанный символ, а не исходное значение (bug22592).
func TestAgentStringOffsetAssignResult(t *testing.T) {
	src := `<?php
$r = "*-*"; $a = $r[0] = $r[1] = "xy"; var_dump($a, $r);
$s = "xyz"; $q = ($s[3] = "Q"); var_dump($q, $s);
$t = "abc"; $u = &$t; $t[0] = "W"; var_dump($t, $u);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "\nWarning: Only the first byte will be assigned to the string offset in php on line 2\n" +
		"string(1) \"x\"\nstring(3) \"xx*\"\n" +
		"string(1) \"Q\"\nstring(4) \"xyzQ\"\n" +
		"string(3) \"Wbc\"\nstring(3) \"Wbc\"\n"
	if got != want {
		t.Fatalf("string offset result: got %q want %q", got, want)
	}
}

// TestAgentStringOffsetEmptyError: пустая строка в offset — Error.
func TestAgentStringOffsetEmptyError(t *testing.T) {
	src := `<?php
$s = "abc";
try { $s[0] = ""; } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump($s);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Cannot assign an empty string to a string offset") ||
		!strings.Contains(got, "string(3) \"abc\"") {
		t.Fatalf("empty offset: %q", got)
	}
}

// TestAgentGlobalStringOffset: запись в строковый offset глобальной переменной
// (быстрый путь GLOBAL_INDEX_SET + манглирование через ${expr}).
func TestAgentGlobalStringOffset(t *testing.T) {
	src := `<?php
function f() { return "name"; }
${f()} = "x";
$p = "abc"; $p[0] = "Z"; var_dump($p);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, `string(3) "Zbc"`) {
		t.Fatalf("global string offset: %q", got)
	}
}

// TestAgentIssetStringChain: isset/?? по цепочке на строках не бросают
// TypeError, а дают false/значение по умолчанию.
func TestAgentIssetStringChain(t *testing.T) {
	src := `<?php
$arr = ['x' => "foobar"];
var_dump(isset($arr['x']['foo']['bar']));
var_dump($arr['x']['foo'] ?? "def");
$s = "foobar";
var_dump(isset($s["foo"]["bar"]), isset($s[0][0]));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "bool(false)\nstring(3) \"def\"\nbool(false)\nbool(true)\n"
	if got != want {
		t.Fatalf("isset chain: got %q want %q", got, want)
	}
}

// TestAgentGlobalOperandOrder: левый операнд-глобал читается в момент
// выполнения операции (после побочных эффектов правого), включая `.=`.
func TestAgentGlobalOperandOrder(t *testing.T) {
	src := `<?php
function foo() { global $a; $a = "good"; return $a; }
$a = "bad"; echo $a . foo(), "\n";
$g = "a";
function fb() { global $g; $g .= "Z"; return "Z"; }
$g .= fb(); echo $g, "\n";
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "goodgood\naZZ\n"; got != want {
		t.Fatalf("operand order: got %q want %q", got, want)
	}
}

// TestAgentPrivateShadow: private-свойства хранятся по объявившему классу и
// видны только из своего scope.
func TestAgentPrivateShadow(t *testing.T) {
	src := `<?php
class B { private $c = "A's c"; function show() { echo $this->c, "\n"; } }
class D extends B { private $c = "B's c"; function show2() { echo $this->c, "\n"; } }
$b = new B; $b->show();
$d = new D; $d->show(); $d->show2();
var_dump(isset($d->c));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "A's c\nA's c\nB's c\n") || !strings.Contains(got, "bool(false)") {
		t.Fatalf("private shadow: %q", got)
	}
}

// TestAgentClassVarsVisibility: get_class_vars/get_object_vars учитывают
// видимость из scope; get_mangled_object_vars возвращает манглированные имена.
func TestAgentClassVarsVisibility(t *testing.T) {
	src := `<?php
class Test {
    public static int $int1;
    public int $int3;
    private $p1 = 'sadf';
    function gv($o) { return get_object_vars($o); }
}
var_dump(get_class_vars(Test::class));
var_dump((new Test)->gv(new Test));
var_dump(get_mangled_object_vars(new Test));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, `["int3"]=>`) || !strings.Contains(got, `["int1"]=>`) {
		t.Fatalf("get_class_vars: %q", got)
	}
	if !strings.Contains(got, `["p1"]=>`) {
		t.Fatalf("get_object_vars scope: %q", got)
	}
	if !strings.Contains(got, "\"\x00Test\x00p1\"") {
		t.Fatalf("get_mangled_object_vars: %q", got)
	}
}

// TestAgentMagicDeclChecks: диагностики объявления магических методов
// (статичность, by-ref, типы параметров, регистр имени).
func TestAgentMagicDeclChecks(t *testing.T) {
	cases := []struct {
		src  string
		want string
	}{
		{"<?php class A { static function __unset($a) {} }", "Method A::__unset() cannot be static"},
		{"<?php class A { static function __call($a, $b) {} }", "Method A::__call() cannot be static"},
		{"<?php interface I { function __callstatic($a, $b); }", "Method I::__callstatic() must be static"},
		{"<?php class A { function __set(&$a, $b) {} }", "Method A::__set() cannot take arguments by reference"},
		{"<?php class A { function __call(int $name, array $args) {} }", "Parameter #1 ($name) must be of type string when declared"},
		{"<?php class A { function __call(string $n, int $args) {} }", "Parameter #2 ($args) must be of type array when declared"},
		{"<?php class A { private function __get($n) {} }", "The magic method A::__get() must have public visibility"},
	}
	for _, c := range cases {
		got, err := runFile(c.src)
		if err != nil {
			t.Fatalf("run %q: %v", c.src, err)
		}
		if !strings.Contains(got, c.want) {
			t.Fatalf("magic decl %q: got %q want substring %q", c.src, got, c.want)
		}
	}
}

// TestAgentWeakReference: create/get, запрет прямого создания, запрет
// сериализации.
func TestAgentWeakReference(t *testing.T) {
	src := `<?php
$o = new stdClass;
$r = WeakReference::create($o);
var_dump($r instanceof WeakReference, $r->get() === $o);
try { serialize($r); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
try { new WeakReference; } catch (Error $e) { echo $e->getMessage(), "\n"; }
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "bool(true)\nbool(true)\n" +
		"Serialization of 'WeakReference' is not allowed\n" +
		"Direct instantiation of WeakReference is not allowed, use WeakReference::create instead\n"
	if got != want {
		t.Fatalf("weakref: got %q want %q", got, want)
	}
}

// TestAgentWeakMapErrors: WeakMap key/append/not-contained диагностики и
// var_dump пар.
func TestAgentWeakMapErrors(t *testing.T) {
	src := `<?php
$m = new WeakMap;
try { $m[1] = 2; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { $m[] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { $m[][1] = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { var_dump($m[new stdClass]); } catch (Error $e) { echo $e->getMessage(), "\n"; }
$o = new stdClass; $m[$o] = 42;
var_dump($m);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	wantParts := []string{
		"WeakMap key must be an object",
		"Cannot append to WeakMap",
		"Cannot append to WeakMap",
		"not contained in WeakMap",
		`["key"]=>`,
		`["value"]=>`,
		"int(42)",
	}
	for _, w := range wantParts {
		if !strings.Contains(got, w) {
			t.Fatalf("weakmap: missing %q in %q", w, got)
		}
	}
}

// TestAgentStringableCompare: сравнение строки с Stringable-объектом вызывает
// __toString (и работает в обе стороны и для ==).
func TestAgentStringableCompare(t *testing.T) {
	src := `<?php
class T { function __toString() { return ''; } }
$o = new T;
var_dump('test' > $o, $o > 'test', $o == '', $o == 'x');
$s = new stdClass;
var_dump('test' > $s, $s > 'test');
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "bool(true)\nbool(false)\nbool(true)\nbool(false)\nbool(false)\nbool(true)\n"
	if got != want {
		t.Fatalf("stringable compare: got %q want %q", got, want)
	}
}

// TestAgentExceptionConsumesHandle: внутренняя ошибка (TypeError) занимает
// PHP-хендл объекта, как в Zend.
func TestAgentExceptionConsumesHandle(t *testing.T) {
	src := `<?php
class A {}
$o = new A;
try { var_dump((string)$o); } catch (Error $e) {}
$b = new stdClass; var_dump($b);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "object(stdClass)#3") {
		t.Fatalf("exception handle: %q", got)
	}
}

// TestAgentGetClassVarsUninit: неинициализированные типизированные свойства
// попадают в get_class_vars как NULL.
func TestAgentGetClassVarsUninit(t *testing.T) {
	src := `<?php
class T { public static int $s1; public int $i1 = 5; public int $i2; }
var_dump(get_class_vars(T::class));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, `["i2"]=>`) || !strings.Contains(got, `["s1"]=>`) ||
		strings.Count(got, "NULL") < 2 {
		t.Fatalf("get_class_vars uninit: %q", got)
	}
}
