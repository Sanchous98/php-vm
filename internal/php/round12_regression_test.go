package php

import (
	"strings"
	"testing"
)

// round12_regression_test.go — регрессии раунда 12: WeakMap/GC-семантика,
// деструкторы в точке throw, цепочка previous в Exception::__toString,
// class_alias с автозагрузкой, final/неоднозначные константы, составное
// присваивание readonly/typed-свойств, кадр обработчика ошибок.
func TestRound12WeakMapGC(t *testing.T) {
	// gh10043-002: значение, ссылающееся на ключ через массив, не удерживает
	// ключ — после потери внешних ссылок запись собирается.
	src := `<?php
$map = new WeakMap;
$obj = new stdClass;
$map[$obj] = [$obj];
$obj = null;
gc_collect_cycles();
var_dump($map);`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "WeakMap)#1 (0)") {
		t.Errorf("WeakMap не очистился: %q", got)
	}

	// gh10043-005: внешняя ссылка на массив ($value) удерживает ключ.
	src = `<?php
$map = new WeakMap;
$obj = new stdClass;
$value = [$obj];
$map[$obj] = $value;
$obj = null;
gc_collect_cycles();
var_dump($map);
$value = null;
gc_collect_cycles();
var_dump($map);`
	got, err = runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Count(got, "WeakMap)#1 (0)") != 1 || !strings.Contains(got, "WeakMap)#1 (1)") {
		t.Errorf("gh10043-005: got %q", got)
	}
}

func TestRound12DestructorAtThrow(t *testing.T) {
	// gh20183: временный объект уничтожается в точке отказавшего опкода, и
	// его деструктор выбрасывает исключение до обработки исходной ошибки.
	src := `<?php
class A { function __destruct(){ echo "dtor\n"; } }
try { B::$b = new A; } catch (Error $e) { echo "caught: ", $e->getMessage(), "\n"; }
echo "after\n";`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "dtor\ncaught: Class \"B\" not found\nafter\n"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}

	// bug73338: исключение деструктора временного значения заменяет исходное
	// и ловится тем же catch.
	src = `<?php
try { var_dump(new stdClass, call_user_func("fail")); } catch (Error $e) { echo "caught\n"; }
echo "done\n";`
	got, err = runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "caught\ndone\n" {
		t.Errorf("got %q", got)
	}
}

func TestRound12ExceptionStringChain(t *testing.T) {
	// PHP 8.3: __toString печатает цепочку previous от самой глубокой к
	// текущему исключению с разделителем "\n\nNext ".
	src := `<?php
$e = new Exception("primary", 0, new Exception("prev"));
echo $e, "\n";`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.HasPrefix(got, "Exception: prev") ||
		!strings.Contains(got, "\n\nNext Exception: primary") {
		t.Errorf("порядок цепочки: %q", got)
	}
}

func TestRound12ClassAliasAutoload(t *testing.T) {
	src := `<?php
spl_autoload_register(function ($a) { class foo {} });
class_alias('foo', 'bar', true);
var_dump(new bar);
$c = new class extends stdClass {};
class_alias('foo', 'baz');
var_dump(class_exists('baz', false));`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "object(foo)") || !strings.Contains(got, "bool(true)") {
		t.Errorf("class_alias: %q", got)
	}
}

func TestRound12FinalConstants(t *testing.T) {
	cases := []struct{ src, want string }{
		{`<?php class Foo { final const A = 1; } class Bar extends Foo { const A = 2; }`,
			"Bar::A cannot override final constant Foo::A"},
		{`<?php interface I1 { const C = 1; } interface I2 { const C = 1; } class C implements I1, I2 {}`,
			"Class C inherits both I1::C and I2::C, which is ambiguous"},
		{`<?php class C { const C = 1; } interface I { const C = 1; } class C2 extends C implements I {}`,
			"Class C2 inherits both C::C and I::C, which is ambiguous"},
		{`<?php class Foo { private const A = 1; }`,
			"Private constant Foo::A cannot be final as it is not visible to other classes"},
	}
	// Последний кейс должен быть с final: проверяем отдельной строкой.
	cases[3].src = `<?php class Foo { final private const A = 1; }`
	for _, c := range cases {
		got, err := runCompiled(t, c.src)
		if err != nil && !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v", c.src, err)
			continue
		}
		if err == nil && !strings.Contains(got, c.want) {
			t.Errorf("%s:\n got %q\n want содержит %q", c.src, got, c.want)
		}
	}
}

func TestRound12CompoundReadonlyTyped(t *testing.T) {
	// variation.phpt: += по неинициализированному readonly — ошибка доступа
	// типизированного свойства, а не "indirectly modify".
	src := `<?php
class Test { public readonly int $prop; }
$t = new Test;
try { $t->prop += 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != "Typed property Test::$prop must not be accessed before initialization\n" {
		t.Errorf("readonly rw: %q", got)
	}
}

func TestRound12ErrorHandlerFrame(t *testing.T) {
	// Кадр обработчика set_error_handler в трейсе привязан к точке ошибки.
	src := `<?php
function eh($n, $m, $f, $l) { debug_print_backtrace(); }
set_error_handler('eh');
function gen() { $a = $b; }
gen();`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "eh(2, 'Undefined varia") ||
		!strings.Contains(got, "gen()") ||
		strings.Contains(got, "[internal function]: eh") {
		t.Errorf("трейс обработчика: %q", got)
	}
}
