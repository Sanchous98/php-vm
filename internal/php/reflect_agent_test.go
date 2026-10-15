package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestReflectAgentVsPHP — дифференциальные кейсы функций рефлексии классов и
// объектов. Сравнение вывода с живым php 8.3.
//
// Замечание: компилятор этого интерпретатора не регистрирует методы с пустым
// телом (считает их абстрактными/интерфейсными), поэтому в кейсах у методов
// непустые тела.
func TestReflectAgentVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- get_class_methods: базовая ---
		`<?php class C { public function a(){return 1;} public function b(){return 2;} } var_dump(get_class_methods('C'));`,
		`<?php class C { public function a(){return 1;} public function b(){return 2;} } var_dump(get_class_methods(new C));`,
		`<?php class C { public function a(){return 1;} } var_dump(method_exists('C','a'), method_exists('C','x'));`,
		`<?php class C { public function a(){return 1;} } $c=new C; var_dump(method_exists($c,'a'));`,

		// --- get_class_methods: scope (public/protected/private) ---
		`<?php
		class X { public function a(){return 1;} private function b(){return 2;} protected function c(){return 3;} }
		class Y extends X { private function bb(){return 4;} static public function test(){ var_dump(get_class_methods('X')); var_dump(get_class_methods('Y')); } }
		var_dump(get_class_methods('X'));
		var_dump(get_class_methods('Y'));
		Y::test();`,

		// --- get_class_methods: изнутри класса видны protected ---
		`<?php
		class X { public function a(){return 1;} protected function c(){return 3;} private function b(){return 2;} }
		class Y extends X { static function t(){ var_dump(get_class_methods('X')); } }
		Y::t();`,

		// --- get_object_vars: только public, порядок объявления ---
		`<?php class C { public $a=1; public $b=2; public $c=3; } var_dump(get_object_vars(new C));`,
		`<?php class P { public $p=1; } class C extends P { public $c=2; } var_dump(get_object_vars(new C));`,
		`<?php class C { public $a=1; } $o=new C; $o->dyn=9; var_dump(get_object_vars($o));`,
		`<?php $o=new stdClass; $o->x=1; $o->y=2; var_dump(get_object_vars($o));`,

		// --- get_class_vars ---
		`<?php class A { public $a=1; public $b=2; } var_dump(get_class_vars('A'));`,
		`<?php class A { public $a=1; } class B extends A { public $b=2; static public $s=3; } var_dump(get_class_vars('B'));`,
		`<?php class A { public $a=1; public static $A=2; public $b=3; } var_dump(get_class_vars('A'));`,

		// --- class_exists / interface_exists / trait_exists / enum_exists ---
		`<?php class A{} interface I{} trait T{} var_dump(class_exists('A'),class_exists('I'),class_exists('nope'),class_exists('a'));`,
		`<?php interface I{} var_dump(interface_exists('I'), interface_exists('C'));`,
		`<?php trait T {} class C{ use T; } var_dump(trait_exists('T'), trait_exists('C'));`,
		`<?php var_dump(enum_exists('Nope'));`,

		// --- property_exists ---
		`<?php class C { public $x; static protected $y; } var_dump(property_exists('C','x'), property_exists('C','y'), property_exists('C','z'));`,
		`<?php class C { public $x; } $o=new C; $o->d=1; var_dump(property_exists($o,'d'));`,
		`<?php class P { public $a; } class C extends P {} var_dump(property_exists('C','a'));`,

		// --- is_a / is_subclass_of ---
		`<?php interface I{} class A{} class B extends A implements I{} $b=new B;
		var_dump(is_a($b,'A'), is_a($b,'I'), is_a($b,'nope'));`,
		`<?php class A{} var_dump(is_a('A','A',true), is_a('A','A'));`,
		`<?php interface I{} class A{} class B extends A implements I{}
		var_dump(is_subclass_of('B','A'), is_subclass_of('B','I'), is_subclass_of('A','A'));`,
		`<?php class A{} class B extends A{} $b=new B; var_dump(is_subclass_of($b,'A'), is_subclass_of($b,'B'));`,

		// --- get_parent_class ---
		`<?php class A{} class B extends A{} var_dump(get_parent_class('B'), get_parent_class('A'));`,
		`<?php class A{} class B extends A{} $b=new B; var_dump(get_parent_class($b));`,
		`<?php var_dump(get_parent_class('stdClass'));`,

		// --- get_called_class (late static binding) ---
		`<?php
		class TestClass { public static function getClassName(){ return get_called_class(); } }
		class ChildClass extends TestClass {}
		echo TestClass::getClassName(), "\n", ChildClass::getClassName(), "\n";`,
		`<?php
		class A { public static function test($x=null){ return get_called_class(); } }
		class B extends A {} class C extends A {} class D extends A {}
		echo A::test(B::test(C::test(D::test()))), "\n";`,
		`<?php try { var_dump(get_called_class()); } catch (Error $e) { echo $e->getMessage(), "\n"; }`,

		// --- class_implements / class_parents / class_uses ---
		`<?php interface I1{} interface I2 extends I1{} interface I3{} class P implements I3{} class C extends P implements I2{} var_dump(class_implements('C'));`,
		`<?php class A{} class B extends A{} class C extends B{} var_dump(class_parents('C'));`,
		`<?php trait T1{} trait T2{} class C { use T1,T2; } var_dump(class_uses('C'));`,
		`<?php class C{} var_dump(class_implements('C'), class_parents('C'), class_uses('C'));`,

		// --- get_declared_* (через in_array, т.к. список классов разный) ---
		`<?php class FooBar{} var_dump(in_array('FooBar', get_declared_classes()));`,
		`<?php interface IFooBar{} class FooBar{} var_dump(in_array('IFooBar', get_declared_interfaces()), in_array('FooBar', get_declared_interfaces()));`,
		`<?php trait TFooBar{} class C { use TFooBar; } var_dump(in_array('TFooBar', get_declared_traits()));`,

		// --- дополнительные кейсы ---
		`<?php $c = function(){}; var_dump(method_exists($c, '__invoke'));`,
		`<?php $o = new stdClass; $o->{1234} = "Numeric"; $p = get_object_vars($o); var_dump(array_key_exists(1234, $p));`,
		`<?php interface I { const X = 1; } class C implements I {} var_dump(defined('C::X'));`,
		`<?php class C { private const X = 1; } var_dump(defined('C::X'));`,
	}

	for _, src0 := range cases {
		src := src0
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Logf("php error for %q: %v", src, werr)
			continue
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q: run error: %v", src, err)
			continue
		}
		if normNoDep(got) != normNoDep(string(want)) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}
