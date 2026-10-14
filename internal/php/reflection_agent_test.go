package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestReflectionClassesVsPHP — дифференциальные кейсы встроенных классов
// Reflection (ReflectionClass/Method/Property/Function/Parameter/Constant/
// NamedType/Attribute/Enum). Сравнение вывода с живым php 8.3.
func TestReflectionClassesVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- ReflectionClass: имя/неймспейс ---
		`<?php namespace A\B; class C {} $r=new \ReflectionClass(C::class);
		var_dump($r->getName(), $r->getShortName(), $r->getNamespaceName(), $r->isUserDefined(), $r->isInternal());`,
		// --- флаги класса ---
		`<?php interface I{} trait T{} abstract class A{} final class F{} enum E { case X; }
		foreach(['I','T','A','F','E'] as $n){ $r=new ReflectionClass($n);
		var_dump($r->isInterface(), $r->isTrait(), $r->isAbstract(), $r->isFinal(), $r->isEnum(), $r->isInstantiable()); }`,
		// --- getType типизированного свойства ---
		`<?php class Foo { public int $bar, $qux; } $r=new ReflectionClass(Foo::class);
		$p=$r->getProperty('qux'); var_dump($p->getName(), $p->getType()->getName(), $p->hasType());`,
		// --- union/nullable тип ---
		`<?php class Foo { public int|string|null $x; public ?int $y; } $r=new ReflectionClass(Foo::class);
		var_dump((string)$r->getProperty('x')->getType(), (string)$r->getProperty('y')->getType());
		var_dump($r->getProperty('x')->getType()->allowsNull());`,
		// --- getModifiers класса ---
		`<?php abstract class A{} final class F{} $r=new ReflectionClass('A'); $f=new ReflectionClass('F');
		var_dump($r->getModifiers() & ReflectionClass::IS_EXPLICIT_ABSTRACT, $f->getModifiers() & ReflectionClass::IS_FINAL);`,
		// --- методы ---
		`<?php class P { public function pa(){} protected function pb(){} static function ps(){} }
		class C extends P { private function cc(){} final public function f(){} }
		$r=new ReflectionClass(C::class);
		$m=$r->getMethod('pa'); var_dump($m->getName(), $m->getDeclaringClass()->getName(), $m->isPublic());
		$m=$r->getMethod('cc'); var_dump($m->isPrivate(), $m->isPublic());
		$m=$r->getMethod('f'); var_dump($m->isFinal());
		$m=$r->getMethod('ps'); var_dump($m->isStatic());`,
		// --- hasMethod/getMethod отсутствующего ---
		`<?php class C { function x(){} } $r=new ReflectionClass(C::class);
		var_dump($r->hasMethod('x'), $r->hasMethod('y'));
		try { $r->getMethod('y'); } catch (ReflectionException $e) { echo $e->getMessage(), "\n"; }`,
		// --- свойства ---
		`<?php class C { public $a=1; protected static $s=2; public readonly int $r; function __construct(){ $this->r=5; } }
		$r=new ReflectionClass(C::class);
		var_dump($r->hasProperty('a'), $r->hasProperty('s'), $r->hasProperty('z'));
		$p=$r->getProperty('s'); var_dump($p->getName(), $p->isStatic(), $p->isPublic());
		$p=$r->getProperty('a'); var_dump($p->isPublic());
		$p=$r->getProperty('r'); var_dump($p->isReadOnly(), $p->getType()->getName());`,
		// --- константы ---
		`<?php class C { const A=1; private const B=2; } interface I { const X=9; }
		class D extends C implements I {}
		$r=new ReflectionClass(D::class);
		var_dump($r->hasConstant('A'), $r->getConstant('A'), $r->getConstant('X'));
		$rc=$r->getReflectionConstant('A'); var_dump($rc->getName(), $rc->getValue(), $rc->getDeclaringClass()->getName(), $rc->isPublic());`,
		// --- родитель/интерфейсы ---
		`<?php interface I1{} interface I2 extends I1{} class P{} class C extends P implements I2{}
		$r=new ReflectionClass(C::class);
		var_dump($r->getParentClass()->getName(), $r->getInterfaceNames());
		var_dump($r->implementsInterface('I1'), $r->implementsInterface('I2'), $r->isSubclassOf('P'), $r->isSubclassOf('C'));
		try { $r->implementsInterface('P'); } catch (ReflectionException $e) { echo $e->getMessage(), "\n"; }`,
		// --- newInstance ---
		`<?php class C { public $v; function __construct($x='def'){ $this->v=$x; } }
		$r=new ReflectionClass(C::class);
		var_dump($r->newInstance()->v, $r->newInstance(7)->v, $r->newInstanceArgs([9])->v);
		$r2=new ReflectionClass(C::class); $o=$r2->newInstanceWithoutConstructor(); var_dump($o->v);`,
		// --- getDefaultProperties ---
		`<?php class C { public $a=1; public int $b; static public $s=3; } var_dump((new ReflectionClass(C::class))->getDefaultProperties());`,
		// --- ReflectionObject ---
		`<?php class C { public $x=1; } $o=new C; $r=new ReflectionObject($o);
		var_dump($r->getName(), $r->getProperty('x')->getValue($o));`,
		// --- ReflectionMethod invoke ---
		`<?php class C { function add($a,$b){ return $a+$b; } static function s(){ return 'S'; } }
		$m=new ReflectionMethod(C::class,'add'); var_dump($m->invoke(new C, 2, 3), $m->invokeArgs(new C,[4,5]));
		$m=new ReflectionMethod(C::class,'s'); var_dump($m->invoke(null));`,
		// --- ReflectionFunction ---
		`<?php function f(int $a, string $b='x', ...$rest): bool { return true; }
		$r=new ReflectionFunction('f');
		var_dump($r->getName(), $r->hasReturnType(), (string)$r->getReturnType());
		$ps=$r->getParameters();
		var_dump(count($ps), $ps[0]->getName(), $ps[0]->getPosition(), $ps[0]->isOptional());
		var_dump($ps[1]->isOptional(), $ps[1]->isDefaultValueAvailable());
		var_dump($ps[0]->getType()->getName(), $ps[0]->getType()->isBuiltin());`,
		// --- ReflectionParameter byref ---
		`<?php function g(&$x){} $p=(new ReflectionFunction('g'))->getParameters()[0];
		var_dump($p->isPassedByReference(), $p->isOptional());`,
		// --- ReflectionFunction::invoke ---
		`<?php function add($a,$b){ return $a+$b; } $r=new ReflectionFunction('add');
		var_dump($r->invoke(1,2), $r->invokeArgs([3,4]), $r->isClosure());`,
		// --- замыкание ---
		`<?php $c=function($x){ return $x*2; }; $r=new ReflectionFunction($c);
		var_dump($r->isClosure(), $r->invoke(5));`,
		// --- ReflectionClassConstant ---
		`<?php class C { public const A=10; protected const B=20; } $r=new ReflectionClass('C');
		$c=$r->getReflectionConstant('A'); var_dump($c->getValue(), $c->isPublic(), $c->getModifiers());
		$c=$r->getReflectionConstant('B'); var_dump($c->isProtected(), $c->getValue());`,
		// --- ReflectionNamedType builtin ---
		`<?php class Foo { public int $a; public Bar $b; } $r=new ReflectionClass(Foo::class);
		var_dump($r->getProperty('a')->getType()->isBuiltin(), (string)$r->getProperty('a')->getType());
		var_dump($r->getProperty('b')->getType()->isBuiltin(), (string)$r->getProperty('b')->getType());`,
		// --- enum reflection ---
		`<?php enum Suit: string { case Hearts='H'; case Spades='S'; }
		$r=new ReflectionEnum(Suit::class);
		var_dump($r->isEnum(), $r->isBacked(), (string)$r->getBackingType());
		foreach($r->getCases() as $c){ var_dump($c->getName()); var_dump($c->getValue()); var_dump((string)$c); }`,
		// --- pure enum ---
		`<?php enum E { case A; case B; } $r=new ReflectionEnum(E::class);
		var_dump($r->isBacked(), $r->hasCase('A'), (string)$r->getCase('B')->getName());`,
		// --- getAttributes ---
		`<?php #[Attribute] class MyAttr { public function __construct(public $v=1){} }
		#[MyAttr(42)] class C {}
		$r=new ReflectionClass(C::class);
		foreach($r->getAttributes() as $a){ var_dump($a->getName(), $a->getArguments()); }`,
		// --- getAttributes filter ---
		`<?php #[Attribute] class A1 {} #[Attribute] class A2 {}
		#[A1] #[A2] function f(){} $r=new ReflectionFunction('f');
		var_dump(count($r->getAttributes()), count($r->getAttributes('A2')));`,
		// --- file/line ---
		`<?php class C { function m(){} } $r=new ReflectionClass('C');
		var_dump($r->getStartLine() > 0, $r->getMethod('m')->getStartLine() > 0);`,
		// --- getTraitNames ---
		`<?php trait T1{} trait T2{} class C { use T1, T2; } var_dump((new ReflectionClass('C'))->getTraitNames());`,
	}
	for _, src0 := range cases {
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src0), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			t.Logf("php error for %q: %v", src0, werr)
			continue
		}
		got, err := runCompiled(t, src0)
		if err != nil {
			t.Errorf("%q: run error: %v", src0, err)
			continue
		}
		if got != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src0, got, string(want))
		}
	}
}
