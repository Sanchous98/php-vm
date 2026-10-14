package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// runDynPHP сравнивает вывод интерпретатора с живым php 8.3 для заданного
// исходника. Динамические имена и магические методы.
func runDynPHP(t *testing.T, src string) string {
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
		t.Errorf("dyn %q:\n vm  = %q\n php = %q", src, got, string(want))
	}
	return got
}

// TestVariableVariables — переменные переменных: $$x, ${expr}, $$$x.
func TestVariableVariables(t *testing.T) {
	cases := []string{
		`$a="x"; $$a=5; echo $x;`,
		`${"y"}=7; echo $y;`,
		`$a="b"; $$a="c"; $$$a=9; echo $b, $c;`,
		`$n="z"; echo isset($$n) ? "1" : "0"; $$n=3; echo $z;`,
		`$a="x"; $$a=1; unset($$a); echo isset($x) ? "y" : "n";`,
		`$a="x"; $$a=1; $$a++; echo $x;`,
		`$k="foo"; $$k = "bar"; echo ${$k};`,
		`$a="x"; $$a=5; echo $x + 1;`,
	}
	for _, src := range cases {
		runDynPHP(t, src)
	}
}

// TestDynamicProperties — $obj->$p, $obj->{expr}, динамическое статическое.
func TestDynamicProperties(t *testing.T) {
	cases := []string{
		`$o=new stdClass; $p="q"; $o->$p=3; echo $o->q;`,
		`$o=new stdClass; $o->{"p"}=4; echo $o->p;`,
		`$o=new stdClass; $n="a"; $o->{$n."b"}=5; echo $o->ab;`,
		`$o=new stdClass; $p="x"; $o->$p=1; echo $o->$p;`,
		`class A { public static $s=10; } $p="s"; A::$$p=20; echo A::$s;`,
		`class A { public static $s=10; } $p="s"; echo A::$$p;`,
	}
	for _, src := range cases {
		runDynPHP(t, src)
	}
}

// TestDynamicMethodNames — $obj->$m(...) и Class::$m(...).
func TestDynamicMethodNames(t *testing.T) {
	cases := []string{
		`class A { function hi($x){ return "hi".$x; } } $o=new A; $m="hi"; echo $o->$m("!");`,
		`class A { function one(){ return 1; } function two(){ return 2; } } $o=new A; $m="two"; echo $o->$m();`,
		`class A { static function s($x){ return "s".$x; } } $m="s"; echo A::$m("!");`,
		`class A { function f(){ return "f"; } } $o=new A; $n="f"; echo $o->{$n}();`,
	}
	for _, src := range cases {
		runDynPHP(t, src)
	}
}

// TestMagicMethods — __get/__set/__isset/__unset/__call.
func TestMagicMethods(t *testing.T) {
	cases := []string{
		`class A { private $d=[]; function __get($n){ return "get:".$n; } } $a=new A; echo $a->foo;`,
		`class A { public $log=""; function __set($n,$v){ $this->log .= "$n=$v;"; } } $a=new A; $a->x=5; echo $a->log;`,
		`class A { function __isset($n){ return $n==="known"; } } $a=new A; var_dump(isset($a->known)); var_dump(isset($a->other));`,
		`class A { public $u=""; function __unset($n){ $this->u = "unset:".$n; } } $a=new A; unset($a->q); echo $a->u;`,
		`class A { function __call($n,$args){ return $n."(".implode(",",$args).")"; } } $a=new A; echo $a->foo(1,2,3);`,
		`class A { function __call($n,$args){ return count($args); } } $a=new A; echo $a->x(); echo $a->y(9);`,
		`class B { function __get($n){ return "B:".$n; } } class C extends B {} $c=new C; echo $c->z;`,
		`class A { function __set($n,$v){ echo "set $n=$v\n"; } } $a=new A; $a->p=7;`,
		`class A { private $x=1; function __get($n){ return $this->x; } } $a=new A; echo $a->y;`,
	}
	for _, src := range cases {
		runDynPHP(t, src)
	}
}

// TestMagicToStringInvoke — __toString при конкатенации/echo и __invoke.
func TestMagicToStringInvoke(t *testing.T) {
	cases := []string{
		`class A { function __toString(){ return "A!"; } } echo new A;`,
		`class A { function __toString(){ return "A"; } } $a=new A; echo "x".$a."y";`,
		`class A { function __invoke($x){ return $x*2; } } $a=new A; echo $a(21);`,
		`class A { function __invoke(){ return "inv"; } } $a=new A; echo $a();`,
	}
	for _, src := range cases {
		runDynPHP(t, src)
	}
}

// TestMagicExtra — __callStatic, динамические __isset/__unset, комбинации.
func TestMagicExtra(t *testing.T) {
	cases := []string{
		`class A { static function __callStatic($n,$a){ return "S:".$n."(".implode(",",$a).")"; } } echo A::foo(1,2);`,
		`class A { function __isset($n){ return $n==="x"; } function __unset($n){ echo "u:$n\n"; } } $p="x"; $a=new A; var_dump(isset($a->$p)); unset($a->$p);`,
		`class A { private $vals=[]; function __set($n,$v){ $this->vals[$n]=$v; } function __get($n){ return $this->vals[$n]; } } $a=new A; $a->foo=42; echo $a->foo;`,
		`$o=new stdClass; $p="a"; $o->$p=1; $p="b"; $o->$p=2; echo $o->a+$o->b;`,
		`class A { function m($a,$b){ return $a-$b; } } $o=new A; $f="m"; echo $o->$f(10,3);`,
		`$a="x"; $$a=5; $b="x"; echo $$b;`,
		`class A { static $s=1; } $k="s"; A::$$k += 5; echo A::$s;`,
	}
	for _, src := range cases {
		runDynPHP(t, src)
	}
}
