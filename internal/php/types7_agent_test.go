package php

import "testing"

// TestTypes7StaticRefRebindGuard — при перепривязке `C::$p =& $other` ссылка,
// оставшаяся в переменной, больше не проверяется по типу прежнего свойства
// (Zend снимает guard при отвязке).
func TestTypes7StaticRefRebindGuard(t *testing.T) {
	src := `<?php
class C {
    public static int $x = 0;
    public static $y = 1;
}
$r =& C::$x;
try { $r = "foo"; } catch (TypeError $e) { echo "guard\n"; }
C::$x =& C::$y;
$r = "foo";
var_dump($r, C::$x);
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "guard\n" + `string(3) "foo"` + "\n" + "int(1)\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}

// TestTypes7ClosureDump — var_dump замыкания печатает object(Closure)#N.
func TestTypes7ClosureDump(t *testing.T) {
	src := `<?php
$c = function () {};
var_dump($c);
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "object(Closure)#1 (0) {\n}\n"
	if out != want {
		t.Fatalf("got %q want %q", out, want)
	}
}
