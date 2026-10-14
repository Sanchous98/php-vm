package php

import (
	"strings"
	"testing"
)

// TestTypesAgentReadonly — readonly-свойства/классы (PHP 8.3): инициализация
// из класса объявления, запрет повторной записи/unset, readonly class.
func TestTypesAgentReadonly(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "prop_double_write",
			src: `<?php
class Test { public readonly int $prop; public function __construct(){ $this->prop = 1; } }
$t = new Test;
try { $t->prop = 2; } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump($t->prop);`,
			want: "Cannot modify readonly property Test::$prop\nint(1)",
		},
		{
			name: "init_from_global_scope",
			src: `<?php
class Test { public readonly int $prop; }
$t = new Test;
try { $t->prop = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot initialize readonly property Test::$prop from global scope",
		},
		{
			name: "unset_readonly",
			src: `<?php
class Test { public readonly int $prop; public function __construct(){ $this->prop = 1; } }
$t = new Test;
try { unset($t->prop); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot unset readonly property Test::$prop",
		},
		{
			name: "indirect_append",
			src: `<?php
class C { public readonly array $a; public function init(){ $this->a[] = 1; } }
try { (new C)->init(); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot indirectly modify readonly property C::$a",
		},
		{
			name: "readonly_class_dynamic",
			src: `<?php
readonly class Foo {}
$foo = new Foo();
try { $foo->bar = 1; } catch (Error $e) { echo $e->getMessage(), "\n"; }`,
			want: "Cannot create dynamic property Foo::$bar",
		},
		{
			name: "readonly_class_no_type",
			src:  `<?php readonly class Foo { public $bar; }`,
			want: "Fatal error: Readonly property Foo::$bar must have type",
		},
		{
			name: "readonly_static",
			src:  `<?php class Test { public static readonly int $prop; }`,
			want: "Fatal error: Static property Test::$prop cannot be readonly",
		},
		{
			name: "readonly_with_default",
			src:  `<?php class Test { public readonly int $prop = 1; }`,
			want: "Fatal error: Readonly property Test::$prop cannot have default value",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runFile(tc.src)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want contains %q", got, tc.want)
			}
		})
	}
}

// TestTypesAgentDefaultValue — валидация значений по умолчанию (PHP 8.3):
// несовместимые типы параметров/свойств.
func TestTypesAgentDefaultValue(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "param_bool_for_float",
			src:  `<?php function test(float $arg = true) {}`,
			want: "Cannot use bool as default value for parameter $arg of type float",
		},
		{
			name: "param_int_for_iterable",
			src:  `<?php function baz(iterable $iterable = 1) {}`,
			want: "Cannot use int as default value for parameter $iterable of type Traversable|array",
		},
		{
			name: "prop_string_for_int",
			src:  `<?php class Foo { public int $bar = "string"; }`,
			want: "Cannot use string as default value for property Foo::$bar of type int",
		},
		{
			name: "prop_null_for_intersection",
			src:  `<?php class Test { public X&Y $y = null; }`,
			want: "Cannot use null as default value for property Test::$y of type X&Y",
		},
		{
			name: "param_null_is_ok",
			src:  `<?php function f(int $i = null) { echo "ok"; } f();`,
			want: "ok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runFile(tc.src)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want contains %q", got, tc.want)
			}
		})
	}
}

// TestTypesAgentUnionRedundancy — избыточность в union/intersection-типах.
func TestTypesAgentUnionRedundancy(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "duplicate_union",
			src:  `<?php function test(): int|INT {}`,
			want: "Duplicate type int is redundant",
		},
		{
			name: "iterable_array_duplicate",
			src:  `<?php function test(): iterable|array {}`,
			want: "Duplicate type array is redundant",
		},
		{
			name: "object_and_class",
			src:  `<?php function test(): object|Test {}`,
			want: "Type Test|object contains both object and a class type, which is redundant",
		},
		{
			name: "intersection_duplicate",
			src:  `<?php function test(): Foo&A&FOO {}`,
			want: "Duplicate type FOO is redundant",
		},
		{
			name: "int_not_duplicate",
			src:  `<?php function f(int $a): int|float { return $a; } f(1); echo "ok";`,
			want: "ok",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := runFile(tc.src)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("got %q, want contains %q", got, tc.want)
			}
		})
	}
}

// TestTypesAgentTypedUninitialized — чтение неинициализированного typed
// свойства даёт ту же ошибку, что и PHP, а isset — false.
func TestTypesAgentTypedUninitialized(t *testing.T) {
	src := `<?php
class A { public int $p; }
$a = new A;
var_dump(isset($a->p));
try { echo $a->p; } catch (Error $e) { echo $e->getMessage(), "\n"; }`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "bool(false)\nTyped property A::$p must not be accessed before initialization\n"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
