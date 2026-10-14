package php

import (
	"strings"
	"testing"
)

// Файл тестов рантайм-объявления классов/интерфейсов/трейтов/enum'ов вне
// верхнего уровня (OpDeclareClass): объявления в телах функций/замыканий,
// под условиями, с отложенным родителем и автозагрузкой.

// TestLazyClassDeclareOrder — объявление внутри функции выполняется в точке
// выполнения: родитель должен быть объявлен ранее.
func TestLazyClassDeclareOrder(t *testing.T) {
	src := `<?php
function f() {
  class B {}
  class A extends B {}
  var_dump(get_class(new A));
}
f();
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "string(1) \"A\"\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassForwardParentFatal — родитель, объявленный позже (и без
// автозагрузки), приводит к перехватываемой ошибке в точке объявления.
func TestLazyClassForwardParentFatal(t *testing.T) {
	src := `<?php
function f() {
  class A extends B {}
  class B {}
}
try { f(); } catch (Error $e) { echo $e->getMessage(), "\n"; }
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "Class \"B\" not found\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassConditional — объявление под условием не видно до выполнения.
func TestLazyClassConditional(t *testing.T) {
	src := `<?php
var_dump(class_exists('A', false));
if (true) { class A {} }
var_dump(class_exists('A', false));
var_dump(new A instanceof A);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "bool(false)\nbool(true)\nbool(true)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassIfaceTraitEnum — вложенные интерфейс, трейт и enum.
func TestLazyClassIfaceTraitEnum(t *testing.T) {
	src := `<?php
function f() {
  interface I { public function m(); }
  trait T { public function m() { return "T"; } }
  class C implements I { use T; }
  enum E: string { case A = 'a'; }
  echo (new C)->m(), "\n";
  var_dump(E::A->value);
  var_dump((new C) instanceof I);
}
f();
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "T\nstring(1) \"a\"\nbool(true)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassRedeclare — повторное выполнение объявления — фатал Zend.
func TestLazyClassRedeclare(t *testing.T) {
	src := `<?php
function f() { class A {} }
f();
f();
`
	got, _ := runFile(src)
	if !strings.Contains(got, "Cannot declare class A, because the name is already in use") {
		t.Fatalf("unexpected output %q", got)
	}
}

// TestLazyClassMissingParentCatchable — расширение неизвестного класса
// бросает Error, которое можно перехватить (bug75765).
func TestLazyClassMissingParentCatchable(t *testing.T) {
	src := `<?php
try {
    class A extends B {}
} catch (Error $e) {
    var_dump(class_exists('A', false));
    var_dump(class_exists('B', false));
    echo $e->getMessage(), "\n";
}
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "bool(false)\nbool(false)\nClass \"B\" not found\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassAutoloadChain — автозагрузка разрешает цепочку родителей.
func TestLazyClassAutoloadChain(t *testing.T) {
	src := `<?php
spl_autoload_register(function ($class) {
    if ($class === 'A') { class A { public function m(): B {} } }
    else if ($class === 'B') { class B extends A {} }
});
$a = new A;
var_dump(get_class($a));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "string(1) \"A\"\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassAliasPropInvariance — property_types_early_bind: class_alias
// учитывается при проверке инвариантности типов свойств.
func TestLazyClassAliasPropInvariance(t *testing.T) {
	src := `<?php
class X {}
class_alias('X', 'Y');
class A { public X $prop; }
class B extends A { public Y $prop; }
echo "DONE\n";
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "DONE\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestLazyClassEnumExistsAutoload — enum_exists вызывает автозагрузку и
// видит enum, объявленный в автозагрузчике.
func TestLazyClassEnumExistsAutoload(t *testing.T) {
	src := `<?php
spl_autoload_register(function ($class) {
    if ($class === 'Quux') { enum Quux {} }
});
var_dump(enum_exists('Quux'));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "bool(true)\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
