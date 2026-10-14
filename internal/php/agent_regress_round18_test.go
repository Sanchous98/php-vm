package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// agent_regress_round18_test — регрессионные тесты раунда 18:
//  1. совместимость сигнатур (by-ref возврат, печать дефолтов, трейты,
//     встроенные интерфейсы/классы, предок из eval);
//  2. declare(ticks=N) и register_tick_function;
//  3. protected-доступ по корневому прототипу (GH-14009);
//  4. readonly-свойства (косвенная модификация, __set/__unset после unset,
//     привязка ссылки, байнд в __clone-стиле);
//  5. IS-режим цепочек isset/??/unset через свойства.

func regressContains(t *testing.T, out, want string) {
	t.Helper()
	if !strings.Contains(out, want) {
		t.Fatalf("вывод %q не содержит %q", out, want)
	}
}

// --- 1. Сигнатуры ---

// by-ref возврат: потомок обязан сохранить `&` (objects_005).
func TestR18CompatByRefReturn(t *testing.T) {
	out, err := runFile(`<?php
class test { function &foo() {} }
class test2 extends test { function &foo() {} }
class test3 extends test { function foo() {} }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of test3::foo() must be compatible with & test::foo()")
}

// Неявная nullable от `= null` и сброшенный дефолт перед обязательным
// параметром (bug71428.3): `?int $a`, без `= null`.
func TestR18CompatImplicitNullableDefault(t *testing.T) {
	out, err := runFile(`<?php
class B { function m(int $a = null, $n) {} } class C extends B { function m(string $a, $n) {} }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of C::m(string $a, $n) must be compatible with B::m(?int $a, $n)")
}

// Дефолт self::class резолвится в строку с именем класса, а динамический
// class-const — в <expression>.
func TestR18CompatDefaultPrinting(t *testing.T) {
	out, err := runFile(`<?php
abstract class Foo { function bar($a = self::class) {} }
trait T { public function bar() {} }
class B extends Foo { use T; }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Foo::bar($a = 'Foo')")

	out, err = runFile(`<?php
class A { public function test(int $x) {} }
class B extends A { public function test(string $x = Foo::{C}) {} }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "B::test(string $x = <expression>) must be compatible with A::test(int $x)")
}

// Абстрактный private-метод трейта разрешён и проверяется у реализации
// (traits/abstract_method_3), self в подписи трейта резолвится в потребителя.
func TestR18CompatTraitAbstractPrivate(t *testing.T) {
	out, err := runFile(`<?php
trait T { abstract private function neededByTheTrait(int $a, string $b); }
class C { use T; private function neededByTheTrait(array $a, object $b) {} }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of C::neededByTheTrait(array $a, object $b) must be compatible with T::neededByTheTrait(int $a, string $b)")

	out, err = runFile(`<?php
trait T { abstract private function method(self $x): self; }
class C { use T; private function method(int $x): int { } }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of C::method(int $x): int must be compatible with T::method(C $x): C")

	// Абстрактный private в обычном классе — по-прежнему фатал.
	out, err = runFile(`<?php
abstract class C { abstract private function f(); }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Abstract function C::f() cannot be declared private")
}

// Метод родителя, примешанный трейтом, печатается по классу-потребителю
// (bug62358); сигнатура трейта-потомка — против унаследованного метода
// родителя, а унаследованный метод проверяется против сигнатуры трейта
// (gh14009_002).
func TestR18CompatTraitInheritance(t *testing.T) {
	out, err := runFile(`<?php
trait T { public function foo() { echo "from T"; } }
interface I { public function foo(); }
abstract class A implements I { use T; }
class B extends A { public function foo($var) {} }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of B::foo($var) must be compatible with A::foo()")

	out, err = runFile(`<?php
class P { protected function common() {} }
class A extends P {}
trait T { private abstract function common(int $param); }
class B extends P { use T; }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of P::common() must be compatible with T::common(int $param)")
}

// Файл/строка диагностики трейтового метода — из файла трейта (bug81192).
func TestR18CompatTraitFileLine(t *testing.T) {
	dir := t.TempDir()
	inc := filepath.Join(dir, "trait.inc")
	if err := os.WriteFile(inc, []byte("<?php\n\ntrait T {\n    public function foo(): string {\n        return \"oops\";\n    }\n}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	main := filepath.Join(dir, "main.php")
	src := "<?php\nrequire " + quotePHP(inc) + ";\nclass A {\n    public function foo(): int { return 2; }\n}\nclass B extends A {\n    use T;\n}\n"
	out, err := runFileAt(src, main)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of T::foo(): string must be compatible with A::foo(): int in "+inc+" on line 4")
}

func quotePHP(s string) string { return "'" + strings.ReplaceAll(s, "'", "\\'") + "'" }

// Встроенные интерфейсы и классы: реализация обязана совпасть по параметрам
// (argument_restriction_005, internal_declaration_error_*).
func TestR18CompatNativeAncestors(t *testing.T) {
	out, err := runFile(`<?php
class Sub implements ArrayAccess { public function offsetSet(): void {} }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of Sub::offsetSet(): void must be compatible with ArrayAccess::offsetSet(mixed $offset, mixed $value): void")

	out, err = runFile(`<?php
class MyDateTimeZone extends DateTimeZone
{
    public function getTransitions(): array|false
    {
    }
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "DateTimeZone::getTransitions(int $timestampBegin = PHP_INT_MIN, int $timestampEnd = PHP_INT_MAX): array|false")

	out, err = runFile(`<?php
interface MyDateTimeInterface extends DateTimeInterface
{
    public function diff(): DateInterval;
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of MyDateTimeInterface::diff(): DateInterval must be compatible with DateTimeInterface::diff(DateTimeInterface $targetObject, bool $absolute = false): DateInterval")

	out, err = runFile(`<?php
class MyDateTime extends DateTime
{
    public function setTime(int $hour, int $minute, int $second = 0, bool $microsecond = false): DateTime
    {
    }
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "must be compatible with DateTime::setTime(int $hour, int $minute, int $second = 0, int $microsecond = 0): DateTime")
}

// Предок, объявленный вне юнита (eval): проверка берёт сигнатуру из таблицы
// VM (gh9407).
func TestR18CompatEvalRuntimeAncestor(t *testing.T) {
	out, err := runFile(`<?php
class A {
    public function duplicate(): static {}
}
class C {
    public static function generate() {
        eval('class B extends A { public function duplicate(): A {} }');
    }
}
C::generate();
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Declaration of B::duplicate(): A must be compatible with A::duplicate(): static")
	regressContains(t, out, ": eval()'d code on line ")
}

// Типы переданных аргументов проверяются раньше нехватки остальных
// (argument_count_incorrect_userland_strict).
func TestR18CompatArgTypeBeforeArity(t *testing.T) {
	out, err := runFile(`<?php
declare(strict_types=1);
function bat(int $foo, string $bar) { }
try { bat("123"); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
try { bat(123); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "TypeError: bat(): Argument #1 ($foo) must be of type int, string given")
	regressContains(t, out, "ArgumentCountError")
}

// Двойные запятые в вызовах: «unexpected \",\", expecting \")\"».
func TestR18CompatMultiComma(t *testing.T) {
	out, err := runFile("<?php\nfoo($foo,,$bar);\n")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, `syntax error, unexpected token ",", expecting ")"`)
}

// --- 2. Ticks ---

// Блочная форма: tick после каждого оператора в блоке (declare_005).
func TestR18TicksBlock(t *testing.T) {
	out, err := runFile(`<?php
register_tick_function(function () { echo "tick\n"; });
function foo() { }
declare(ticks=1) {
$statement;
foo();
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "tick\ntick\n" {
		t.Fatalf("got %q", out)
	}
}

// Аргументы register_tick_function передаются каждому тику (declare_007).
func TestR18TicksCallbackArgs(t *testing.T) {
	out, err := runFile(`<?php
register_tick_function(function (stdClass $object, array $array) {
    echo "tick: ", get_class($object), " ", count($array), "\n";
}, new \stdClass(), [1, 2, 3]);
declare(ticks=1) {
$x;
$y;
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "tick: stdClass 3\ntick: stdClass 3\n" {
		t.Fatalf("got %q", out)
	}
}

// Файловая форма: тик исполняется после оператора с незарегистрированной
// функцией (bug66094), unregister изнутри тика — ошибка (bug41037).
func TestR18TicksFileAndUnregister(t *testing.T) {
	out, err := runFile(`<?php
declare(ticks=1);
register_tick_function($closure = function () { echo "Tick!\n"; });
unregister_tick_function($closure);
echo "done";
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "Tick!\ndone" {
		t.Fatalf("got %q", out)
	}

	out, err = runFile(`<?php
function a() {
    echo "hello\n";
    try {
        unregister_tick_function('a');
    } catch (Error $exception) {
        echo $exception->getMessage() . "\n";
    }
}
declare (ticks=1) {
    register_tick_function('a');
    echo "Done\n";
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "hello\nRegistered tick function cannot be unregistered while it is being executed\nDone\n" +
		"hello\nRegistered tick function cannot be unregistered while it is being executed\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

// Не-литеральное значение declare(ticks) — фатал (declare_006).
func TestR18TicksNonLiteral(t *testing.T) {
	out, err := runFile("<?php\ndeclare(ticks = UNKNOWN_CONST) {\n  echo 'Done';\n}\n")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "declare(ticks) value must be a literal")
}

// --- 3. protected/прототип (GH-14009) ---

func TestR18ProtectedPrototype(t *testing.T) {
	out, err := runFile(`<?php
class P {
    protected function common() {
        throw new Exception('Unreachable');
    }
}
class A extends P {
    public function test(P $sibling) {
        $sibling->common();
    }
}
class B extends P {
    protected function common() {
        echo __METHOD__, "\n";
    }
}
trait T {
    protected function common() {
        echo __METHOD__, "\n";
    }
}
class C extends P {
    use T;
}
$a = new A();
$a->test(new B());
$a->test(new C());
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "B::common\nT::common\n" {
		t.Fatalf("got %q", out)
	}
}

func TestR18ProtectedPrototypeInheritedTrait(t *testing.T) {
	out, err := runFile(`<?php
class A {
    protected function test() {
        throw new Exception('Unreachable');
    }
}
class B extends A {
    protected function test() {
        echo __METHOD__, "\n";
    }
}
trait T {
    protected abstract function test();
}
class C extends B {
    use T;
}
class D extends A {
    public static function callTest($sibling) {
        $sibling->test();
    }
}
D::callTest(new C());
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if out != "B::test\n" {
		t.Fatalf("got %q", out)
	}
}

// --- 4. readonly ---

// Привязка ссылки к readonly-свойству (gh7942) и запись ссылки; сообщения
// для инициализированного и пустого свойства различаются
// (readonly_modification).
func TestR18ReadonlyReferenceBinding(t *testing.T) {
	out, err := runFile(`<?php
class Foo {
    public readonly int $bar;
    public function __construct(int &$bar) {
        $this->bar = &$bar;
    }
}
try {
    $i = 42;
    new Foo($i);
} catch (Error $e) {
    echo $e->getMessage(), PHP_EOL;
}
class T2 { public readonly int $p; public function __construct(){ $this->p = 1; } }
$t = new T2; $r = 5;
try { $t->p =& $r; } catch (Error $e) { echo $e->getMessage(), PHP_EOL; }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Cannot indirectly modify readonly property Foo::$bar\nCannot modify readonly property T2::$p\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

// Вложенные цепочки записи: инициализированное readonly-свойство-объект
// модифицируемо, пустое — «Cannot indirectly modify», прямой контейнер
// инициализированного — «Cannot modify» (variation_nested, variation).
func TestR18ReadonlyNestedChains(t *testing.T) {
	out, err := runFile(`<?php
class Inner {
    public int $prop = 1;
    public array $array = [];
}
class Test {
    public readonly Inner $prop;
    public readonly int $scalar;
    public function init() {
        $this->prop = new Inner();
        $this->scalar = 1;
    }
}
function w($test) { $test->prop->prop = 0; echo 'done'; }
function rw($test) { $test->prop->prop += 1; echo 'done'; }
function im($test) { $test->prop->array[] = 1; echo 'done'; }
function is_($test) { echo (int) isset($test->prop->prop); }
function us($test) { unset($test->prop->prop); echo 'done'; }
function dd($test) { $test->scalar[] = 1; }
foreach ([true, false] as $init) {
    foreach (['w', 'rw', 'im', 'is_', 'us'] as $op) {
        $test = new Test();
        if ($init) { $test->init(); }
        echo 'Init: ' . ((int) $init) . ', op: ' . $op . ": ";
        try { $op($test); } catch (Error $e) { echo $e->getMessage(); }
        echo "\n";
    }
}
try { dd($test = new Test); } catch (Error $e) { echo $e->getMessage(), "\n"; }
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Init: 1, op: w: done\n")
	regressContains(t, out, "Init: 1, op: rw: done\n")
	regressContains(t, out, "Init: 1, op: im: done\n")
	regressContains(t, out, "Init: 0, op: w: Cannot indirectly modify readonly property Test::$prop\n")
	regressContains(t, out, "Init: 0, op: rw: Typed property Test::$prop must not be accessed before initialization\n")
	regressContains(t, out, "Init: 0, op: im: Cannot indirectly modify readonly property Test::$prop\n")
	regressContains(t, out, "Init: 0, op: is_: 0\n")
	regressContains(t, out, "Cannot indirectly modify readonly property Test::$scalar\n")
}

// unset readonly-свойства внутри класса переводит его в magic-состояние:
// последующие isset/чтение/запись/unset идут через __isset/__get/__set/
// __unset (magic_get_set).
func TestR18ReadonlyMagicAfterUnset(t *testing.T) {
	out, err := runFile(`<?php
class Test {
    public readonly int $prop;
    public function unsetProp() {
        unset($this->prop);
    }
    public function __get($name) {
        echo __METHOD__, "($name)\n";
        return 1;
    }
    public function __set($name, $value) {
        echo __METHOD__, "($name, $value)\n";
    }
    public function __unset($name) {
        echo __METHOD__, "($name)\n";
    }
    public function __isset($name) {
        echo __METHOD__, "($name)\n";
        return true;
    }
}
$test = new Test;
$test->unsetProp();
var_dump(isset($test->prop));
var_dump($test->prop);
$test->prop = 2;
unset($test->prop);
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Test::__isset(prop)\nbool(true)\nTest::__get(prop)\nint(1)\nTest::__set(prop, 2)\nTest::__unset(prop)\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

// readonly-модификатор на методе в trait-адаптации — фатал
// (readonly_method_trait).
func TestR18ReadonlyMethodTraitAlias(t *testing.T) {
	out, err := runFile(`<?php
class Test {
    use T { foo as readonly; }
}
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	regressContains(t, out, "Cannot use the readonly modifier on a method")
}

// --- 5. IS-режим цепочек ---

// isset/?? по цепочке через неинициализированное типизированное свойство не
// диагностирует доступ; __isset вызывается перед __get.
func TestR18IssetSafeChain(t *testing.T) {
	out, err := runFile(`<?php
class B { public $c = 1; }
class A { public ?B $b; public ?array $arr; }
$a = new A;
var_dump(isset($a->b->c));
var_dump(isset($a->arr[0]));
var_dump($a->b->c ?? 'x');
class M { public function __get($n){ echo "get:$n\n"; return new stdClass; } public function __isset($n){ echo "isset:$n\n"; return true; } }
$m = new M;
var_dump(isset($m->p->q));
`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "bool(false)\nbool(false)\nstring(1) \"x\"\nisset:p\nget:p\nbool(false)\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}
