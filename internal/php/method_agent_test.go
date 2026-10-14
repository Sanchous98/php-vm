package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runMethodPHP компилирует и исполняет src, сверяя вывод с живым php 8.3.
// Дифф-тесты для пустых методов, сигнатур (interface/abstract), переопределений
// и private-shadow (диспетчеризация private по области компиляции).
func runMethodPHP(t *testing.T, src string) string {
	t.Helper()
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	dir := t.TempDir()
	f := filepath.Join(dir, "case.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	want, werr := exec.Command(phppath, f).Output()
	if werr != nil {
		t.Fatalf("php %q: %v\n%s", src, werr, want)
	}
	got, err := runFileAt(src, f)
	if err != nil {
		t.Fatalf("vm %q: run: %v", src, err)
	}
	if got != string(want) {
		t.Errorf("method %q:\n vm  = %q\n php = %q", src, got, string(want))
	}
	return got
}

// TestEmptyMethodVsPHP — `function m(){}` — конкретный метод с пустым телом:
// регистрируется, возвращает null, виден в method_exists/get_class_methods.
func TestEmptyMethodVsPHP(t *testing.T) {
	cases := []string{
		`<?php class C { function m(){} } $c=new C; var_dump($c->m());`,
		`<?php class C { function m(){} } var_dump(method_exists('C','m'));`,
		`<?php class C { function m(){} } var_dump(get_class_methods('C'));`,
		`<?php class C { function m(){} function n(){ return 1; } } $c=new C; var_dump($c->m(), $c->n());`,
		`<?php class C { public function m(){} protected function p(){} private function q(){} } var_dump(get_class_methods('C'));`,
		`<?php class C { static function s(){} } var_dump(C::s());`,
		`<?php class C { function m($a){ return null; } } $c=new C; var_dump($c->m(1));`,
	}
	for _, src := range cases {
		runMethodPHP(t, src)
	}
}

// TestOverrideEmptyVsPHP — переопределение пустого метода непустым и наоборот.
func TestOverrideEmptyVsPHP(t *testing.T) {
	cases := []string{
		// Пустой в родителе, непустой в потомке → вызывается потомок.
		`<?php class A { function m(){} } class B extends A { function m(){ return "B"; } } $b=new B; echo $b->m();`,
		// Непустой в родителе, пустой в потомке → вызывается пустой (null).
		`<?php class A { function m(){ return "A"; } } class B extends A { function m(){} } $b=new B; var_dump($b->m());`,
		// Пустой унаследован несколькими поколениями.
		`<?php class A { function m(){} } class B extends A {} class C extends B {} $c=new C; var_dump($c->m());`,
		// Пустой → непустой → пустой.
		`<?php class A { function m(){} } class B extends A { function m(){ return 1; } } class C extends B { function m(){} } $c=new C; var_dump($c->m());`,
		// Разные ветви: один подкласс переопределяет, другой нет.
		`<?php class A { function m(){} } class B extends A { function m(){ return "B"; } } class C extends A {} var_dump((new C)->m()); echo (new B)->m();`,
	}
	for _, src := range cases {
		runMethodPHP(t, src)
	}
}

// TestSignaturesVsPHP — сигнатуры (метод интерфейса / abstract function):
// контракты, видимые рефлексии, но не вызываемые как реализация.
func TestSignaturesVsPHP(t *testing.T) {
	cases := []string{
		// Интерфейс + реализующий класс.
		`<?php interface I { function f(); } class C implements I { function f(){ return "ok"; } } echo (new C)->f();`,
		`<?php interface I { function f(); } var_dump(method_exists('I','f'));`,
		`<?php interface I { function f(); function g(); } var_dump(get_class_methods('I'));`,
		// Абстрактный класс + конкретный потомок.
		`<?php abstract class Base { abstract function f(); } class Sub extends Base { function f(){ return "sub"; } } echo (new Sub)->f();`,
		// Интерфейс виден в class_implements, реализация наследуется.
		`<?php interface I { function f(); } class P { function f(){ return "p"; } } class C extends P implements I {} echo (new C)->f();`,
		// Абстрактный метод в родителе, реализация в потомке, вызов через потомка.
		`<?php abstract class Base { abstract function f(); function call(){ return $this->f(); } } class Sub extends Base { function f(){ return "x"; } } echo (new Sub)->call();`,
		// Пустой метод удовлетворяет сигнатуру интерфейса.
		`<?php interface I { function f(); } class C implements I { function f(){} } var_dump((new C)->f());`,
		// Абстрактный метод трейта реализуется в классе.
		`<?php trait T { abstract function f(); } class C { use T; function f(){ return "t"; } } echo (new C)->f();`,
	}
	for _, src := range cases {
		runMethodPHP(t, src)
	}
}

// TestPrivateShadowVsPHP — private-метод затенён в подклассе: PHP зовёт private
// метода класса объявления (по области компиляции), а не по runtime-классу.
func TestPrivateShadowVsPHP(t *testing.T) {
	cases := []string{
		// Вызов из метода, объявленного в A.
		`<?php class A { private function p(){ return "A::p"; } function call(){ return $this->p(); } } class B extends A { private function p(){ return "B::p"; } } echo (new B)->call();`,
		// Тот же объект базового класса.
		`<?php class A { private function p(){ return "A"; } function call(){ return $this->p(); } } $a=new A; echo $a->call();`,
		// Вызов private на другом объекте из области A.
		`<?php class A { private function p(){ return "A"; } function call($o){ return $o->p(); } } class B extends A { private function p(){ return "B"; } } echo (new A)->call(new B);`,
		// Без затенения: private виден своему классу.
		`<?php class A { private function p(){ return "secret"; } function call(){ return $this->p(); } } echo (new A)->call();`,
		// private static через self:: (scope-диспетчеризация).
		`<?php class C { private static function s(){ return "C::s"; } static function run(){ return self::s(); } } echo C::run();`,
		// private на другом экземпляре того же класса.
		`<?php class E { private function p(){ return "E"; } function call($o){ return $o->p(); } } echo (new E)->call(new E);`,
	}
	for _, src := range cases {
		runMethodPHP(t, src)
	}
}
