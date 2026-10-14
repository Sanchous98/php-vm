package php

import "testing"

// Резолв свойства (scope, obj, name) и ключ хранения private-свойства
// кэшируются в VM (scopedProp/propKey). Кэш не должен «протекать» между
// областями видимости: приватные тени базового/производного класса,
// private-свойства трейта в двух использующих классах (манглирование по
// использующему классу), чтение из замыкания и чередование в цикле.
func TestAgentPropScopeCache(t *testing.T) {
	src := `<?php
trait T { private $x = 'T'; function getT(){ return $this->x; } }
class A { private $x = 'A'; function getA(){ return $this->x; } }
class B extends A { private $x = 'B'; function getB(){ return $this->x; } }
class C { use T; }
class D { use T; }
$b = new B();
echo $b->getA(), $b->getB(), "\n";
$c = new C(); $d = new D();
echo $c->getT(), $d->getT(), "\n";
var_dump(array_keys((array)$c));
var_dump(array_keys((array)$d));
$f = function() use ($b) { return $b->getB(); };
echo $f(), "\n";
$s = "";
for ($i = 0; $i < 3; $i++) { $s .= $b->getA() . $b->getB(); }
echo $s, "\n";
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "AB\nTT\n" +
		"array(1) {\n  [0]=>\n  string(4) \"\x00C\x00x\"\n}\n" +
		"array(1) {\n  [0]=>\n  string(4) \"\x00D\x00x\"\n}\n" +
		"B\nABABAB\n"
	if got != want {
		t.Fatalf("prop scope cache:\n got %q\nwant %q", got, want)
	}
}
