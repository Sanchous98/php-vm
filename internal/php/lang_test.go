package php

import (
	"os/exec"
	"strconv"
	"strings"
	"testing"
)

// TestLangVsPHP прогоняет программы через наши компилятор+VM и живой php.
func TestLangVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// массивы
		`<?php $a=[1,2,3]; echo $a[0]; echo $a[2];`,
		`<?php $a=["x"=>1,"y"=>2]; echo $a["x"]; echo $a["y"];`,
		`<?php $a=[]; $a[]=5; $a[]=6; echo $a[0]+$a[1];`,
		`<?php $a=[1,[2,3]]; echo $a[1][0]; echo $a[1][1];`,
		`<?php $a=[1,2,3]; echo count($a);`,
		`<?php echo count([1,2,3,4]);`,
		// foreach
		`<?php foreach([1,2,3] as $v){ echo $v; }`,
		`<?php $s=0; foreach([1,2,3,4] as $v){ $s+=$v; } echo $s;`,
		`<?php foreach(["a"=>1,"b"=>2] as $k=>$v){ echo $k; echo "="; echo $v; echo ";"; }`,
		// for
		`<?php for($i=0;$i<3;$i++){ echo $i; }`,
		`<?php $s=0; for($i=1;$i<=10;$i++){ $s=$s+$i; } echo $s;`,
		`<?php for($i=0;$i<10;$i++){ if($i==3){ continue; } if($i==6){ break; } echo $i; }`,
		// while / do-while
		`<?php $i=0; while($i<3){ echo $i; $i++; }`,
		`<?php $i=0; do { echo $i; $i++; } while($i<3);`,
		// switch
		`<?php $x=2; switch($x){ case 1: echo "a"; break; case 2: echo "b"; break; default: echo "c"; }`,
		`<?php $x=9; switch($x){ case 1: echo "a"; break; default: echo "c"; }`,
		// ternary / coalesce / logical
		`<?php echo 1<2 ? "y" : "n";`,
		`<?php $a=NULL; echo $a ?? "def";`,
		`<?php $a=5; echo $a ?? "def";`,
		`<?php echo (1<2) && (3>2) ? "t" : "f";`,
		`<?php echo (1>2) || (3>2) ? "t" : "f";`,
		`<?php echo true && false;`,
		`<?php echo true or false;`,
		// in/decrement и составное присваивание
		`<?php $a=5; $a+=3; $a*=2; echo $a;`,
		`<?php $a=1; echo $a++; echo $a; echo ++$a;`,
		`<?php $a=[1,2]; $a[0]+=10; echo $a[0];`,
		`<?php $a="x"; $a.="y"; echo $a;`,
		// var_dump
		`<?php var_dump(1);`,
		`<?php var_dump("hi");`,
		`<?php var_dump(true); var_dump(false); var_dump(NULL);`,
		`<?php var_dump(1.5);`,
		`<?php var_dump([1,2,3]);`,
		`<?php var_dump(["a"=>1,"b"=>2]);`,
		// функции с массивами
		`<?php function sum($a){ $s=0; foreach($a as $v){ $s+=$v; } return $s; } echo sum([1,2,3,4]);`,
		`<?php function fib($n){ if($n<2){ return $n; } return fib($n-1)+fib($n-2); } echo fib(10);`,
		// встроенные
		`<?php echo strlen("hello");`,
		`<?php echo strtoupper("abc");`,
		`<?php echo str_repeat("ab",3);`,
		`<?php echo implode(",",[1,2,3]);`,
		`<?php echo count(array_keys(["a"=>1,"b"=>2]));`,
		`<?php echo array_sum([1,2,3,4]);`,
		`<?php echo max(3,7,2); echo min(3,7,2);`,
		`<?php echo abs(-5);`,
		`<?php echo intdiv(17,5);`,
		`<?php echo sprintf("%05d",42);`,
		`<?php echo substr("hello world",0,5);`,
		`<?php echo str_replace("a","b","banana");`,
		`<?php print_r([1,2]);`,
		// is_*
		`<?php var_dump(is_int(1)); var_dump(is_string("x"));`,
		`<?php var_dump(is_array([1])); var_dump(is_null(NULL));`,
		// интерполяция строк
		`<?php $a=5; echo "x=$a y=6";`,
		`<?php $a=[10,20]; echo "v={$a[0]}";`,
		`<?php $a=["k"=>7]; echo "v={$a["k"]}";`,
		`<?php $a="Z"; echo "pre{$a}post";`,
		`<?php $i=3; echo "$i: done";`,
		`<?php $a=1; $b=2; echo "$a+$b=".($a+$b);`,
		// строка как массив символов + доступ
		`<?php echo "hello"[1];`,
		// новые встроенные
		`<?php define("FOO", 42); echo FOO;`,
		`<?php define("BAR", "x"); var_dump(defined("BAR"));`,
		`<?php echo number_format(1234567.891, 2, ".", ",");`,
		`<?php echo substr("abcdef", -2);`,
		`<?php echo str_pad("5", 3, "0");`,
		`<?php echo implode("-", array_keys(["a"=>1,"b"=>2]));`,
		`<?php echo array_sum(array_values(["a"=>1,"b"=>2,"c"=>3]));`,
		`<?php echo in_array("b", ["a","b","c"]) ? "y" : "n";`,
		`<?php echo count(range(1,5));`,
		`<?php echo array_reverse([1,2,3])[0];`,
		`<?php echo version_compare("8.3.0", "8.2.0", ">") ? "y" : "n";`,
		`<?php var_dump(gettype(1.5)); var_dump(gettype([1]));`,
		`<?php echo str_contains("hello world", "world") ? "y" : "n";`,
		// константы
		`<?php echo PHP_EOL === "\n" ? "y" : "n";`,
		`<?php echo PHP_INT_MAX > 0 ? "y" : "n";`,
		// isset/empty
		`<?php $a=1; var_dump(isset($a)); var_dump(empty($a)); var_dump(empty($b));`,
		// ООП: класс, свойства, методы
		`<?php class A { public $x = 1; public function get() { return $this->x; } } $a = new A(); echo $a->get(); $a->x = 5; echo $a->get();`,
		`<?php class B { public $x, $y; public function set($a,$b){ $this->x=$a; $this->y=$b; } public function sum(){ return $this->x+$this->y; } } $b = new B(); $b->set(3,4); echo $b->sum();`,
		// конструктор
		`<?php class P { public $x,$y; public function __construct($x,$y){ $this->x=$x; $this->y=$y; } public function sum(){ return $this->x+$this->y; } } $p = new P(3,4); echo $p->sum();`,
		// наследование + parent::__construct
		`<?php class Base { public $v; public function __construct($v){ $this->v=$v; } } class Child extends Base { public function __construct($v){ parent::__construct($v+1); } } $c = new Child(10); echo $c->v;`,
		// static-метод/static-свойство
		`<?php class Counter { public static $count = 0; public static function inc(){ self::$count = self::$count + 1; return self::$count; } } echo Counter::inc(); echo Counter::inc(); echo Counter::$count;`,
		// константа класса
		`<?php class K { const PI = 3.14; const NAME = "x"; } echo K::PI; echo K::NAME;`,
		// instanceof + наследование
		`<?php class Animal {} class Dog extends Animal {} $d = new Dog(); var_dump($d instanceof Dog); var_dump($d instanceof Animal);`,
		// интерфейс + instanceof
		`<?php interface I {} class Impl implements I {} $o = new Impl(); var_dump($o instanceof I);`,
		// get_class
		`<?php class E {} $e = new E(); echo get_class($e);`,
		// массив-дефолт свойства + метод
		`<?php class M { public $items = [1,2,3]; public function total(){ $s=0; foreach($this->items as $v){ $s+=$v; } return $s; } } echo (new M())->total();`,
		// ++ свойства, мутация массива-свойства
		`<?php class A { public $x = 1; public function inc(){ $this->x++; return $this->x; } } $a=new A(); echo $a->inc();`,
		`<?php class A { public $a=[1,2]; public function m(){ $this->a[0]=9; return $this->a[0]; } } $x=new A(); echo $x->m(); echo $x->a[0];`,
		// static:: (late static binding) и static-свойство
		`<?php class A { public static $x = 1; public static function set($v){ static::$x=$v; return static::$x; } } echo A::set(9); echo A::$x;`,
		// parent::method()
		`<?php class B { public function hi(){ return "B"; } } class C extends B { public function hi(){ return "C+".parent::hi(); } } echo (new C())->hi();`,
		// цепочка вызовов и new self
		`<?php class A { public function me(){ return $this; } public function x(){ return 7; } } $a=new A(); echo $a->me()->x();`,
		`<?php class A { public $x=2; public function m(){ return new self(); } } $a=new A(); echo $a->m()->x;`,
		// трейты и константы интерфейса
		`<?php trait T { public function t(){ return 3; } } class A { use T; } echo (new A())->t();`,
		`<?php interface I { const C = 5; } echo I::C;`,
		// protected-свойство доступно из метода
		`<?php class A { protected $x=1; public function get(){ return $this->x; } } echo (new A())->get();`,
		// static-метод с несколькими аргументами
		`<?php class Math2 { public static function add($a,$b){ return $a+$b; } } echo Math2::add(3,4);`,
		// исключения: throw + catch + getMessage
		`<?php try { throw new Exception("boom"); } catch (Exception $e) { echo $e->getMessage(); }`,
		// finally выполняется при return
		`<?php function f(){ try { return 1; } finally { echo "f"; } } echo f();`,
		// вложенные try (finally внутреннего + внешний catch)
		`<?php try { try { throw new Exception("a"); } finally { echo "inner"; } } catch (Exception $e) { echo "caught:".$e->getMessage(); }`,
		// multicatch
		`<?php try { throw new RuntimeException("x"); } catch (TypeError | RuntimeException $e) { echo "got"; }`,
		// catch без переменной
		`<?php try { throw new Exception("z"); } catch (Exception) { echo "ok"; }`,
		// пользовательский наследник Exception + get_class/getCode
		`<?php class MyEx extends Exception {} try { throw new MyEx("m", 7); } catch (Exception $e) { echo get_class($e).":".$e->getMessage().":".$e->getCode(); }`,
		// finally при break внутри try
		`<?php for($i=0;$i<3;$i++){ try { break; } finally { echo "F"; } } echo "done";`,
		// исключение из функции ловится в вызывающем кадре
		`<?php function g(){ throw new Exception("e"); } try { g(); } catch (Exception $e) { echo $e->getMessage(); }`,
		// повторный throw из catch летит во внешний try
		`<?php try { try { throw new Exception("a"); } catch (Exception $e) { throw new Exception("b"); } } catch (Exception $e) { echo $e->getMessage(); }`,
		// пустой finally — тоже finally
		`<?php try { echo "a"; } finally {} echo "b";`,
		// return на верхнем уровне с finally
		`<?php try { echo "try"; return; } finally { echo "fin"; } echo "after";`,
		// exception из try/finally ловится внешним catch
		`<?php try { try { throw new Exception("x"); } finally { echo "F"; } } catch (Exception $e) { echo "C".$e->getMessage(); }`,
		// ссылки: alias-переменные
		`<?php $a=1; $b=&$a; $b=2; echo $a; echo $b;`,
		`<?php $a=1; $b=&$a; $a=5; echo $b;`,
		`<?php $count=0; $c=&$count; for($i=0;$i<3;$i++){ $c++; } echo $count;`,
		// ссылки: by-ref параметры
		`<?php function f(&$x){ $x++; } $a=1; f($a); echo $a;`,
		`<?php function addOne(&$x){ $x=$x+1; } $a=10; addOne($a); addOne($a); echo $a;`,
		`<?php function swap(&$x,&$y){ $t=$x; $x=$y; $y=$t; } $a=1; $b=2; swap($a,$b); echo $a; echo $b;`,
		// ссылки: by-ref возврат
		`<?php function &g(&$a){ return $a; } $a=1; $b =& g($a); $b=7; echo $a; echo $b;`,
		`<?php function &ref(&$x){ return $x; } $a=3; $r =& ref($a); $r=9; echo $a;`,
		// ссылки: by-ref foreach
		`<?php $arr=[1,2,3]; foreach($arr as &$v){ $v=$v*2; } echo $arr[0]; echo $arr[1]; echo $arr[2];`,
		`<?php $arr=["a"=>1,"b"=>2]; foreach($arr as $k=>&$v){ $v=$v+10; } echo $arr["a"]; echo $arr["b"];`,
		// приведения типов
		`<?php var_dump((int)"12abc");`,
		`<?php var_dump((int)"abc");`,
		`<?php var_dump((int)true); var_dump((int)false);`,
		`<?php var_dump((int)1.9); var_dump((int)-1.9);`,
		`<?php var_dump((float)"1.5"); var_dump((double)"2.25");`,
		`<?php var_dump((string)1.5); var_dump((string)true); var_dump((string)false);`,
		`<?php var_dump((bool)""); var_dump((bool)"0"); var_dump((bool)"x");`,
		`<?php var_dump((bool)0); var_dump((bool)0.0); var_dump((bool)[]); var_dump((bool)[1]);`,
		`<?php var_dump((array)5); var_dump((array)null);`,
		`<?php $s="hi"; var_dump((array)$s);`,
		`<?php var_dump((unset)5);`,
		`<?php var_dump((int)"1e3"); var_dump((float)"1e3");`,
		`<?php echo (int)"12abc" + (string)1.5;`,
		`<?php $o=(object)["a"=>1,"b"=>2]; var_dump($o->a); var_dump($o->b);`,
		`<?php $o=(object)5; var_dump($o->scalar);`,
		`<?php $a=(array)[1,2,3]; var_dump(count($a));`,
		`<?php var_dump((int)(string)3.7);`,
		// heredoc / nowdoc
		"<?php $a=5; echo <<<EOT\nx=$a y\nEOT;",
		"<?php echo <<<EOT\nline1\nline2\nEOT;",
		"<?php $arr=[1,2]; echo <<<EOT\nv={$arr[1]}\nEOT;",
		"<?php echo <<<'EOT'\nno $x interp \\n here\nEOT;",
		"<?php echo <<<\"EOT\"\nquoted $x\nEOT;",
		"<?php $name=\"World\"; echo <<<EOT\nHello $name!\nEOT;",
		"<?php echo <<<EOT\n\ttab\\tend\nEOT;",
		"<?php $a=1; $b=2; echo <<<EOT\n{$a}+{$b}=3\nEOT;",
		"<?php echo <<<EOT\n  indented\n  EOT;",
		// магические константы
		`<?php echo __LINE__;`,
		`<?php $x = __LINE__; echo $x;`,
		`<?php function foo(){ return __FUNCTION__; } echo foo();`,
		`<?php echo "[" . __FUNCTION__ . "]";`,
		`<?php class C { public function m(){ return __CLASS__; } } $c=new C(); echo $c->m();`,
		`<?php class C { public function m(){ return __METHOD__; } } $c=new C(); echo $c->m();`,
		`<?php function foo(){ return __METHOD__; } echo foo();`,
		`<?php echo __NAMESPACE__ . "end";`,
		// деструктуризация
		`<?php list($a,$b) = [1,2]; echo $a; echo $b;`,
		`<?php [$a,$b] = [3,4]; echo $a; echo $b;`,
		`<?php list(,$b) = [1,2]; echo $b;`,
		`<?php [$a,,$c] = [5,6,7]; echo $a; echo $c;`,
		`<?php error_reporting(0); list($a,$b) = ["x"=>1,"y"=>2]; var_dump($a);`,
		`<?php list("x"=>$a,"y"=>$b) = ["x"=>10,"y"=>20]; echo $a; echo $b;`,
		`<?php error_reporting(0); list($a,$b) = [1]; var_dump($b);`,
		`<?php [$a,$b] = [1,2]; [$a,$b] = [$b,$a]; echo $a; echo $b;`,
		`<?php $arr = [1,2,3]; list($x,$y) = $arr; echo $x+$y;`,
		`<?php list($a, list($b,$c)) = [1,[2,3]]; echo $a; echo $b; echo $c;`,
		// unicode-escape \u{...} и сохранение неизвестных escape-последовательностей
		`<?php var_dump("\u{61}");`,
		`<?php var_dump("\u{FF}"); var_dump("\u{ff}");`,
		`<?php var_dump("\u{2603}");`,
		`<?php var_dump("\u{1F602}");`,
		`<?php var_dump("\u");`,
		`<?php var_dump("\u202e");`,
		`<?php var_dump("\q");`,
		"<?php echo <<<EOT\n\\u{41}\\u{42}\\nEOT;",
		// динамические имена
		`<?php $f="strlen"; echo $f("hello");`,
		`<?php function add($a,$b){return $a+$b;} $f="add"; echo $f(2,3);`,
		`<?php $f="strtoupper"; echo $f("abc");`,
		`<?php $f="abs"; echo $f(-7)+1;`,
		`<?php class A { public $x=5; } $c="A"; $o=new $c(); echo $o->x;`,
		`<?php class A { public $x=5; function __construct($v){$this->x=$v;} } $c="A"; $o=new $c(9); echo $o->x;`,
		// целочисленные литералы с основанием и разделителями
		`<?php var_dump(0x7FFFFFFF);`,
		`<?php var_dump(0x7FFFFFFFFFFFFFFF);`,
		`<?php var_dump(0xFFFFFFFFFFFFFFFF);`,
		`<?php var_dump(0b1010);`,
		`<?php var_dump(0o17); var_dump(017);`,
		`<?php var_dump(0xff); var_dump(0XfF);`,
		`<?php var_dump(1_000_000);`,
		`<?php var_dump(1_000.5); var_dump(01.5); var_dump(010.5);`,
		`<?php var_dump(1e3, 1_0e2);`,
		`<?php echo 0xFF + 1;`,
		// new с выражением имени класса и case с ';'
		`<?php class Test { function hi(){ return "hi"; } } class F { public $name = "Test"; function create(){ return new $this->name; } } echo (new F)->create()->hi();`,
		`<?php $s="*"; switch($s){ case '*'; echo "OK"; break; default; echo "no"; }`,
		`<?php var_dump(1.7e319); var_dump(-1.7e319);`,
		// замыкания
		`<?php $f = function($x){ return $x+1; }; echo $f(2);`,
		`<?php $a=10; $f = function() use ($a){ return $a; }; echo $f();`,
		`<?php $x=1; $f = function() use (&$x){ $x++; }; $f(); $f(); echo $x;`,
		`<?php $f = fn($x) => $x*2; echo $f(21);`,
		`<?php $a=5; $f = fn($x) => $x+$a; echo $f(10);`,
		`<?php $n=3; $f = fn() => $n*$n; echo $f();`,
		`<?php $f = function($a,$b){ return $a*$b; }; echo $f(6,7);`,
		`<?php $f = function($x) use ($x) { return $x; }; echo $f(1);`,
		`<?php class C { public $v=7; public function m(){ return function(){ return $this->v; }; } } $c=new C(); $f=$c->m(); echo $f();`,
		`<?php $mul = function($a){ return function($b) use ($a){ return $a*$b; }; }; $f=$mul(6); echo $f(7);`,
		`<?php $f = function(){ return 42; }; echo $f();`,
		`<?php $f = function($x) { return fn($y) => $x+$y; }; $g=$f(10); echo $g(5);`,
		`<?php $f = fn($x) => $x*2; echo $f($f(3));`,
		`<?php $f = static fn($x) => $x*2; echo $f(3);`,
		`<?php $f=function(&$x){ $x++; }; $a=5; $f($a); echo $a;`,
		`<?php $f=function($a,$b=10){ return $a+$b; }; echo $f(1); echo $f(1,2);`,
		`<?php class Z { public $v=3; function m(){ return fn() => $this->v; } } echo (new Z)->m()();`,
		`<?php class Z { public $v=4; function m(){ return function(){ return $this->v; }; } } echo (new Z)->m()();`,
		// COW массивов и append-в-цикле (проверка отсутствия O(n^2))
		`<?php $a=[1,2]; $b=$a; $b[]=3; var_dump($a); var_dump($b);`,
		`<?php $a=[]; for($i=0;$i<2000;$i++){ $a[]=$i; } echo count($a);`,
		`<?php $a=[1,2,3]; $a[1]=9; var_dump($a);`,
		`<?php $a=[1,2,3]; $a[]=4; $a[]=5; var_dump($a);`,
		`<?php $a=["x"=>1]; $a["y"]=2; var_dump($a);`,
	}
	for _, src := range cases {
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			continue // тест-программа некорректна для php — пропускаем
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q:\n  run error: %v", src, err)
			continue
		}
		if got != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}

// TestVMBugFixes — регрессии: ловимость ValueError/ArithmeticError из
// builtin'ов, вызов с числом аргументов больше регистрового файла и
// отсутствие зависаний range() на границах int64.
func TestVMBugFixes(t *testing.T) {
	cases := []struct{ src, want string }{
		{`<?php try { str_repeat("x", -1); } catch (ValueError $e) { echo "value:" . $e->getMessage(); }`,
			"value:str_repeat(): Argument #2 ($times) must be greater than or equal to 0"},
		{`<?php try { range(1, 5, 0); } catch (ValueError $e) { echo "step"; }`, "step"},
		{`<?php try { echo 1 / 0; } catch (DivisionByZeroError $e) { echo "div"; }`, "div"},
		{`<?php try { echo 1 / 0; } catch (ArithmeticError $e) { echo "arith"; }`, "arith"},
		{`<?php try { range(0, INF); } catch (ValueError $e) { echo $e->getMessage(); }`,
			"range(): Argument #2 ($end) must be a finite number, INF provided"},
		{`<?php try { var_dump(range(0, PHP_INT_MAX)); } catch (ValueError $e) { echo $e->getMessage(); }`,
			"The supplied range exceeds the maximum array size: start=0 end=9223372036854775807 step=1"},
	}
	for _, c := range cases {
		got, err := runCompiled(t, c.src)
		if err != nil {
			t.Errorf("%s: run error: %v", c.src, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s\n  vm  = %q\n  want= %q", c.src, got, c.want)
		}
	}

	// Вызов с числом аргументов больше регистрового файла (256).
	args := make([]string, 400)
	for i := range args {
		args[i] = strconv.Itoa(i)
	}
	src := "<?php echo max(" + strings.Join(args, ",") + "); echo \"|\"; echo min(" + strings.Join(args, ",") + ");"
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("big call: %v", err)
	}
	if got != "399|0" {
		t.Errorf("big call: got %q want %q", got, "399|0")
	}

	// range не должен зависать на границе int64 (шаг 1 меньше ULP float).
	got, err = runCompiled(t, `<?php echo count(range(PHP_INT_MIN + 513, PHP_INT_MIN));`)
	if err != nil {
		t.Fatalf("range int64: %v", err)
	}
	if got != "514" {
		t.Errorf("range int64: got %q want 514", got)
	}
}
