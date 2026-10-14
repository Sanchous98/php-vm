package php

import (
	"strings"
	"testing"
)

// runR19 запускает фрагмент и требует отсутствия ошибок выполнения.
func runR19(t *testing.T, src string) string {
	t.Helper()
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	return got
}

// TestR19TypedRefMultiSource — запись через ссылку проверяется по ВСЕМ
// типизированным свойствам, которые её держат (Zend type sources).
func TestR19TypedRefMultiSource(t *testing.T) {
	got := runR19(t, `<?php
class Test { public X|Y|Z|int $x; }
class Test2 { public int|string $x; public float|string $y; }
class Test3 { public ?iterable $it = []; public ?array $a; public ?Traversable $t; }
$a = new Test3;
$ref = &$a->it;
$a->a = &$ref;
try { $ref = new ArrayIterator(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
unset($a->a);
$ref = null;
$a->t = &$ref;
try { $ref = []; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
`)
	want := "Cannot assign ArrayIterator to reference held by property Test3::$a of type ?array\n" +
		"Cannot assign array to reference held by property Test3::$t of type ?Traversable\n"
	if got != want {
		t.Fatalf("multi-source refs: got %q want %q", got, want)
	}
}

// TestR19TypedRefConflictingCoercion — разные приведения у источников ссылки
// дают conflicting coercion error с обоими свойствами в тексте.
func TestR19TypedRefConflictingCoercion(t *testing.T) {
	got := runR19(t, `<?php
class Test {
    public int|string $x;
    public float|string $y;
}
$test = new Test;
$r = "foobar";
$test->x =& $r;
$test->y =& $r;
$v = 42;
try { $r = $v; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($r);
`)
	want := "Cannot assign int to reference held by property Test::$x of type string|int and property Test::$y of type string|float, as this would result in an inconsistent type conversion\n" +
		"string(6) \"foobar\"\n"
	if got != want {
		t.Fatalf("conflicting coercion: got %q want %q", got, want)
	}
}

// TestR19StaticRefAliasAndCoerce — присваивание ссылки типизированному
// статическому свойству приводит значение ячейки НА МЕСТЕ и разделяет alias.
func TestR19StaticRefAliasAndCoerce(t *testing.T) {
	got := runR19(t, `<?php
class Foo { public static int $i; public static string $s; }
function &nonNumeric() { static $a = "x"; return $a; }
function &five() { static $b = 5; return $b; }
Foo::$s = &five();
var_dump(Foo::$s, five());
$x = 0;
Foo::$i = &$x;
$x = "7";
var_dump(Foo::$i, $x);
try { Foo::$i = &nonNumeric(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
`)
	want := "string(1) \"5\"\nstring(1) \"5\"\nint(7)\nint(7)\n" +
		"Cannot assign string to property Foo::$i of type int\n"
	if got != want {
		t.Fatalf("static ref adopt: got %q want %q", got, want)
	}
}

// TestR19StaticVarRebindKeepsTable — `static $v = &$x` отвязывает CV от
// статической таблицы на текущий вызов, значение таблицы пишется обычным
// присваиванием значения (Bug #20175).
func TestR19StaticVarRebindKeepsTable(t *testing.T) {
	got := runR19(t, `<?php
$count = 0;
function myglob() { global $count; echo "myglob()\n"; return 'v:' . ++$count; }
function mystat() {
    static $value;
    echo "stat()\n";
    if (!isset($value)) { $value = &myglob(); }
    return $value;
}
print mystat()."\n";
print mystat()."\n";
`)
	want := "stat()\nmyglob()\n\nNotice: Only variables should be assigned by reference in php on line 7\nv:1\nstat()\nv:1\n"
	if got != want {
		t.Fatalf("static var rebind: got %q want %q", got, want)
	}
}

// TestR19CatchBindStrict — привязка переменной catch к ячейке-ссылке
// типизированного свойства строгая: Stringable-объект не приводится (ZEND_CATCH).
func TestR19CatchBindStrict(t *testing.T) {
	got := runR19(t, `<?php
class Test { public string $s = "str"; }
$test = new Test;
$ref = &$test->s;
try {
    try { throw new Exception("ex"); } catch (Exception $ref) { echo "Unreachable\n"; }
} catch (TypeError $e) {
    echo $e->getMessage(), "\n";
}
var_dump($test->s);
`)
	want := "Cannot assign Exception to reference held by property Test::$s of type string\n" +
		"string(3) \"str\"\n"
	if got != want {
		t.Fatalf("catch strict bind: got %q want %q", got, want)
	}
}

// TestR19CloneTypedRef — клон наследует источники типизированных ссылок:
// уничтожение оригинала не снимает защиту клона (typed_properties_081).
func TestR19CloneTypedRef(t *testing.T) {
	got := runR19(t, `<?php
class Test { public int $x = 42; }
$test = new Test;
$x = &$test->x;
$test2 = clone $test;
unset($test);
try { $x = "foo"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($test2->x);
`)
	want := "Cannot assign string to reference held by property Test::$x of type int\nint(42)\n"
	if got != want {
		t.Fatalf("clone typed ref: got %q want %q", got, want)
	}
}

// TestR19AutoInitArrayDim — авто-вивификация массива в типизированном
// свойстве без array даёт auto-init TypeError (typed_properties_083).
func TestR19AutoInitArrayDim(t *testing.T) {
	got := runR19(t, `<?php
class Foo { public ?string $p; public static ?string $s; }
$a = new Foo;
try { $a->p[] = "x"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
$a->p = null;
try { $a->p[] = "x"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($a->p);
$ref = &$a->p;
try { $ref[] = "y"; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { Foo::$s["k"][] = 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
`)
	want := "Cannot auto-initialize an array inside property Foo::$p of type ?string\n" +
		"Cannot auto-initialize an array inside property Foo::$p of type ?string\n" +
		"NULL\n" +
		"Cannot auto-initialize an array inside a reference held by property Foo::$p of type ?string\n" +
		"Cannot auto-initialize an array inside property Foo::$s of type ?string\n"
	if got != want {
		t.Fatalf("auto-init dim: got %q want %q", got, want)
	}
}

// TestR19ClosureReturnType — тип возврата замыкания проверяется (в т.ч.
// static без scope: TypeError при вызове).
func TestR19ClosureReturnType(t *testing.T) {
	got := runR19(t, `<?php
$f = function(): static { return new stdClass; };
try { $f(); } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
$g = fn(): int => "abc";
try { $g(); } catch (Throwable $e) { echo "g: ", $e->getMessage(), "\n"; }
class A { function m() { $h = function(): static { return $this; }; echo $h() instanceof A ? "A\n" : "no\n"; } }
(new A)->m();
`)
	want := "TypeError: {closure}(): Return value must be of type static, stdClass returned\n" +
		"g: {closure}(): Return value must be of type int, string returned\n" +
		"A\n"
	if got != want {
		t.Fatalf("closure return type: got %q want %q", got, want)
	}
}

// TestR19ConstDefaultCheck — дефолт параметра из константы проверяется при
// вызове как ошибка аргумента (scalar_constant_defaults).
func TestR19ConstDefaultCheck(t *testing.T) {
	got := runR19(t, `<?php
const NULL_VAL = null;
const STR_VAL = "test";
function f(int $a = NULL_VAL) { return $a; }
function g(int $a = STR_VAL) { return $a; }
try { f(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
try { g(); } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
function h(?int $a = NULL_VAL) { return $a; }
var_dump(h());
`)
	want := "f(): Argument #1 ($a) must be of type int, null given, called in php on line 6\n" +
		"g(): Argument #1 ($a) must be of type int, string given, called in php on line 7\n" +
		"NULL\n"
	if got != want {
		t.Fatalf("const default: got %q want %q", got, want)
	}
}

// TestR19RefGetRawArrayCheck — запись массива через ссылку с источником без
// array запрещена до создания массива.
func TestR19RefGetRawArrayCheck(t *testing.T) {
	got := runR19(t, `<?php
class A { public ?array $a = []; public ?string $s; }
$o = new A;
$ref = &$o->s;
try { $ref[] = 1; } catch (TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($o->s);
$refA = &$o->a;
$refA[] = 3;
var_dump($refA);
`)
	want := "Cannot auto-initialize an array inside a reference held by property A::$s of type ?string\n" +
		"NULL\n" +
		"array(1) {\n  [0]=>\n  int(3)\n}\n"
	if got != want {
		t.Fatalf("refgetraw array check: got %q want %q", got, want)
	}
}

// TestR19AssertMultilineText — многострочный текст assert() нормализует
// отступы (typed_class_constants_ast_print).
func TestR19AssertMultilineText(t *testing.T) {
	got := runR19(t, `<?php
try {
    assert(false && new class {
        public const int X = 1;
    });
} catch (AssertionError $e) {
    echo $e->getMessage(), "\n";
}
`)
	if !strings.Contains(got, "    public const int X = 1;") || !strings.Contains(got, "\n})") {
		t.Fatalf("assert text: unexpected %q", got)
	}
}
