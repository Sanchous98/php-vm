package php

import (
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// runTypesFile запускает исходник; непойманное исключение (fatal) не считается
// ошибкой — его текст уже в выводе.
func runTypesFile(t *testing.T, src string) string {
	t.Helper()
	got, err := runFile(src)
	if err != nil {
		if _, ok := err.(*vm.Throwable); !ok {
			t.Fatalf("run: %v", err)
		}
	}
	return got
}

// wantContains проверяет, что вывод содержит подстроку.
func wantContains(t *testing.T, got, want string) {
	t.Helper()
	if !strings.Contains(got, want) {
		t.Fatalf("got %q, want contains %q", got, want)
	}
}

// TestTypesCanonicalUnionDisplay — канонический порядок типов в сообщениях
// Zend: раскрытие iterable, ?T -> T|null, порядок object/callable/array/
// string/int/float/bool/null и расстановка скобок вокруг DNF-пересечений.
func TestTypesCanonicalUnionDisplay(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "iterable_object_order",
			src:  `<?php function bar(): object|iterable|T|null { return null; }`,
			want: "Type Traversable|T|object|array|null contains both object and a class type, which is redundant",
		},
		{
			name: "iterable_object_order5",
			src:  `<?php function bar(): T|object|iterable|null { return null; }`,
			want: "Type T|Traversable|object|array|null contains both object and a class type, which is redundant",
		},
		{
			name: "nullable_iterable_runtime",
			src: `<?php
function f(?iterable $p) {}
try { f(1); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }`,
			want: "must be of type Traversable|array|null, int given",
		},
		{
			name: "nullable_scalar_keeps_question",
			src: `<?php
function f(?int $p) {}
try { f("x"); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }`,
			want: "must be of type ?int, string given",
		},
		{
			name: "implicit_nullable_intersection",
			src: `<?php
interface X {} interface Y {}
function foo(X&Y $foo = null) {}
try { foo(1); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }`,
			want: "must be of type (X&Y)|null, int given",
		},
		{
			name: "mixed_expansion_order",
			src: `<?php
class Foo { public function method(mixed $a) {} }
class Bar extends Foo { public function method(bool|int|float|string|array|object|null $a) {} }`,
			want: "Declaration of Bar::method(object|array|string|int|float|bool|null $a) must be compatible with Foo::method(mixed $a)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesVarianceAndCompat — ковариантность/контравариантность
// intersection↔union, object/static/Stringable, сообщение «Could not check
// compatibility ... because class X is not available».
func TestTypesVarianceAndCompat(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "commutative_intersection_ok",
			src: `<?php
interface A {} interface B {} interface X {}
class Foo { public X|(A&B) $prop; public function f(X|(A&B) $v): X|(A&B) {} }
class FooChild extends Foo { public (B&A)|X $prop; public function f((B&A)|X $v): (B&A)|X {} }
echo "ok";`,
			want: "ok",
		},
		{
			name: "object_supertype_of_intersection",
			src: `<?php
interface Y {}
class Test { function m1(): object {} }
class Test2 extends Test { function m1(): (X&Y)|Countable {} }
echo "ok";`,
			want: "ok",
		},
		{
			name: "static_covariant_return",
			src: `<?php
class A { public function t(): object { return new static; } }
class B extends A { public function t(): static { return new static; } }
echo "ok";`,
			want: "ok",
		},
		{
			name: "stringable_variance",
			src: `<?php
class Foo { public function t(): Stringable {} }
class Bar extends Foo { public function t(): Bar {} public function __toString() { return ""; } }
echo "ok";`,
			want: "ok",
		},
		{
			name: "intersection_to_union_covariance_error",
			src: `<?php
interface X {} interface Y {}
class TestOne implements X, Y {}
class TestTwo implements X {}
interface A { public function foo(): X&Y; }
interface B extends A { public function foo(): TestOne|TestTwo; }`,
			want: "Declaration of B::foo(): TestOne|TestTwo must be compatible with A::foo(): X&Y",
		},
		{
			name: "could_not_check_missing_class",
			src: `<?php
class Test { function method(): object {} }
class Test2 extends Test { function method(): X&Y {} }`,
			want: "Could not check compatibility between Test2::method(): X&Y and Test::method(): object, because class X is not available",
		},
		{
			name: "untyped_cannot_override_typed_mixed",
			src: `<?php
class Foo { public function method(): mixed {} }
class Bar extends Foo { public function method() {} }`,
			want: "Declaration of Bar::method() must be compatible with Foo::method(): mixed",
		},
		{
			name: "void_not_subtype_of_mixed",
			src: `<?php
class Foo { public function method(): mixed {} }
class Bar extends Foo { public function method(): void {} }`,
			want: "Declaration of Bar::method(): void must be compatible with Foo::method(): mixed",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesRedundancy — проверки избыточности union/DNF-типов.
func TestTypesRedundancy(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "bool_and_false",
			src:  `<?php function test(): bool|false {}`,
			want: "Duplicate type false is redundant",
		},
		{
			name: "bool_and_true",
			src:  `<?php function test(): bool|true {}`,
			want: "Duplicate type true is redundant",
		},
		{
			name: "less_restrictive_dnf",
			src: `<?php
interface A {} interface B {} interface C {}
function test(): (A&B)|(A&B&C) {}`,
			want: "Type A&B&C is redundant as it is more restrictive than type A&B",
		},
		{
			name: "duplicate_dnf_alias",
			src: `<?php
use A as B;
function foo(): (X&A)|(X&B) {}`,
			want: "Type X&A is redundant with type X&A",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesClassAlias — builtin class_alias и запрет зарезервированных имён.
func TestTypesClassAlias(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "reserved_error",
			src:  `<?php class foobar {} class_alias("foobar", "int");`,
			want: "Fatal error: Cannot use 'int' as class name as it is reserved",
		},
		{
			name: "no_duplicate_on_runtime_alias",
			src: `<?php
class A {}
class_alias('A', 'B');
function foo(): A&B {}
echo "done";`,
			want: "done",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesStrictDeclarationPlacement — declare(strict_types=1) обязан быть
// первым оператором и не может быть блочным.
func TestTypesStrictDeclarationPlacement(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "encoding_before_is_ok",
			src: `<?php
declare(encoding="UTF-8");
declare(strict_types=1);
namespace Foo;
var_dump(strlen("abc"));`,
			want: "int(3)",
		},
		{
			name: "after_statement",
			src: `<?php
namespace Foo;
declare(strict_types=1);`,
			want: "strict_types declaration must be the very first statement in the script",
		},
		{
			name: "block_mode",
			src: `<?php
declare(strict_types=1) {
    var_dump(strlen("abc"));
}`,
			want: "strict_types declaration must not use block mode",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesTypedPropertyAssign — проверка/приведение типа при записи в
// типизированное свойство.
func TestTypesTypedPropertyAssign(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "weak_int_from_string",
			src: `<?php
class Foo { public int $bar; }
$f = new Foo; $f->bar = "42";
var_dump($f->bar);`,
			want: "int(42)",
		},
		{
			name: "int_to_float",
			src: `<?php
class Foo { public float $bar; }
$f = new Foo; $f->bar = 5;
var_dump($f->bar);`,
			want: "float(5)",
		},
		{
			name: "bad_string_for_int",
			src: `<?php
class Foo { public int $bar; }
$f = new Foo;
try { $f->bar = "abc"; } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot assign string to property Foo::$bar of type int",
		},
		{
			name: "null_for_non_nullable",
			src: `<?php
class Foo { public int $bar; }
$f = new Foo;
try { $f->bar = null; } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot assign null to property Foo::$bar of type int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesTypedClassConstants — типизированные константы класса: проверка
// значения, расширение int->float, наследование и запрещённые типы.
func TestTypesTypedClassConstants(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "compile_value_error",
			src:  `<?php class A { public const string C = 1; }`,
			want: "Cannot use int as value for class constant A::C of type string",
		},
		{
			name: "int_to_float",
			src:  `<?php class A { public const float C = 3; } var_dump(A::C);`,
			want: "float(3)",
		},
		{
			name: "forbidden_callable",
			src:  `<?php class A { public const callable C = "f"; }`,
			want: "Class constant A::C cannot have type callable",
		},
		{
			name: "inheritance_ok",
			src: `<?php
class A { public const iterable C = []; }
class B extends A { public const array C = []; }
var_dump(B::C);`,
			want: "array(0)",
		},
		{
			name: "inheritance_error",
			src: `<?php
class A { public const int C = 1; }
class B extends A { public const string C = 'a'; }`,
			want: "Type of B::C must be compatible with A::C of type int",
		},
		{
			name: "runtime_value_error",
			src: `<?php
class A { public const int C = G; }
define("G", "c");
try { var_dump(A::C); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot assign string to class constant A::C of type int",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runTypesFile(t, tc.src)
			wantContains(t, got, tc.want)
		})
	}
}

// TestTypesUninitializedVarDump — var_dump показывает неинициализированные
// типизированные свойства как uninitialized(T), но не считает их в (N).
func TestTypesUninitializedVarDump(t *testing.T) {
	src := `<?php
class Foo {
    public int $a;
    public int $b = 1;
    public ?Foo $c;
}
var_dump(new Foo);`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "object(Foo)#1 (1) {\n" +
		"  [\"a\"]=>\n" +
		"  uninitialized(int)\n" +
		"  [\"b\"]=>\n" +
		"  int(1)\n" +
		"  [\"c\"]=>\n" +
		"  uninitialized(?Foo)\n" +
		"}\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
