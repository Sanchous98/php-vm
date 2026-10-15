package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// fatal_tail_agent_test.go — регрессии волны «лишние фаталы» (w_err):
//
//   - резолв имён в пространствах имён: квалифицированные имена ищутся
//     только в таблице импортов классов; импорты function/const действуют
//     лишь для неквалифицированных имён; псевдоним const регистрозависим;
//   - вызываемые значения: [Closure, "__invoke"], Closure::fromCallable от
//     замыкания, строковое "Class::method" в динамическом вызове,
//     first-class callable $f->__invoke(...);
//   - foreach с ключом-свойством/элементом;
//   - запись в перегруженное свойство (__get) по ссылке/через [] —
//     «Indirect modification ... has no effect» вместо Error;
//   - мелкие SPL/сессионные/ресурсные функции, которые Zend имеет, а VM нет.
//
// Сравнение с живым php 8.3 — байт в байт.

func ftRunLive(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", f).CombinedOutput()
	res := strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
	return strings.ReplaceAll(res, dir, "")
}

func ftCheck(t *testing.T, name, src string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil && !vm.IsExitSignal(err) {
		t.Fatalf("%s: run error: %v", name, err)
	}
	want := ftRunLive(t, src)
	if got != want {
		t.Errorf("%s:\n--- src ---\n%s\n--- got ---\n%q\n--- want ---\n%q", name, src, got, want)
	}
}

func TestFatalTailVsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"ns_qualified_alias", `<?php
namespace A\B { function f() { echo "A\\B\\f\n"; } const K = "A\\B\\K"; }
namespace N {
    use A\B as C;
    C\f();
    echo C\K, "\n";
    try { C\strlen("x"); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
}
`},
		{"ns_func_alias_only_unqualified", `<?php
namespace A\B { function g() { echo "A\\B\\g\n"; } }
namespace N {
    use function A\B\g as h;
    h();
    try { h\g(); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
}
`},
		{"ns_const_alias_case", `<?php
namespace A\B { const K = "K"; }
namespace N {
    use const A\B\K as L;
    echo L, "\n";
    try { echo l, "\n"; } catch (\Error $e) { echo $e->getMessage(), "\n"; }
}
`},
		{"ns_qualified_closure", `<?php
namespace test\ns1;
function foo() { echo __FUNCTION__, "\n"; }
use test\ns1 as ns2;
foo();
ns2\foo();
`},
		{"array_callable_closure_invoke", `<?php
$a = [function () { echo "123\n"; }, '__invoke'];
$a();
$b = Closure::fromCallable($a);
$b();
$c = function (int $x) { echo $x, "\n"; };
call_user_func([$c, '__invoke'], 7);
try { [$c, 'foo'](); } catch (\Error $e) { echo $e->getMessage(), "\n"; }
`},
		{"string_callable_static", `<?php
class T { public static function m($x = 0) { echo "T::m($x)\n"; } }
$cb = 'T::m';
$cb(1);
call_user_func('T::m', 2);
`},
		{"fcc_closure_invoke", `<?php
$closure = function () { return "OK"; };
$foo = $closure->__invoke(...);
echo $foo(), "\n";
`},
		{"foreach_key_property", `<?php
class C { public $d; }
$c = new C();
$arr = array (1 => 'a', 2 => 'b', 3 => 'c');
foreach($arr as $c->d => $x) { echo "{$c->d} => {$x}\n"; }
`},
		{"foreach_key_index", `<?php
class P { public $var = []; function test() {
    $cont = ["mykey" => "myvalue"];
    foreach ($cont as $this->var['key'] => $this->var['value']) var_dump($this->var['value']);
}}
(new P)->test();
`},
		{"magic_ref_undeclared", `<?php
class D { function __get($n){ return [1,2]; } function __set($n,$v){ echo "set($n)\n"; } }
$d = new D;
$x = &$d->undef;
var_dump($x);
$d->undef = 5;
`},
		{"magic_ref_byref_get", `<?php
class foo implements ArrayAccess {
    private $bar;
    public function __construct() { $this->bar = new bar(); }
    public function & __get($key) { $bar = $this->bar; return $bar; }
    public function offsetGet($key): mixed { $bar = $this->bar; return $bar; }
    public function offsetSet($key, $val): void {}
    public function offsetUnset($key): void {}
    public function offsetExists($key): bool { return false; }
}
class bar { public $onBaz = []; }
$foo = new foo();
$foo->bar->onBaz[] = function() {};
var_dump(count($foo->bar->onBaz));
`},
		{"session_unset", `<?php
session_start();
$_SESSION['x'] = "1\n";
echo $_SESSION['x'];
session_unset();
var_dump(isset($_SESSION['x']));
`},
		{"get_resources", `<?php
fclose(fopen("php://temp","w+"));
$count = count(get_resources());
fclose(fopen("php://temp","w+"));
var_dump(count(get_resources()) == $count);
var_dump(is_array(get_resources("stream")));
`},
		{"spl_observer_subject", `<?php
class Foo implements \SplObserver { function update(\SplSubject $x): void { echo "ok\n"; } }
class Bar implements \SplSubject {
    function attach(\SplObserver $x): void { echo "ok\n"; }
    function notify(): void {}
    function detach(\SplObserver $x): void {}
}
$foo = new Foo(); $bar = new Bar();
$bar->attach($foo);
$foo->update($bar);
`},
		{"reflection_doc_comment", `<?php
class TestClass { function x() {} }
$rm = new ReflectionMethod('TestClass', 'x');
var_dump($rm->getDocComment());
trait myTrait { public function run() {} }
class myClass { use myTrait { MyTrait::run as private; } }
var_dump((new ReflectionClass('myClass'))->getTraitAliases());
`},
		{"magic_call_static_fallback", `<?php
class Foo {
    static function __callStatic($name, $arguments) { echo "callStatic($name)\n"; }
    protected function get($key) { echo "BUG!!!\n"; }
}
class Bar { static function __callStatic($n, $a) { echo Foo::get('x'); } }
Bar::someUndefinedStaticFunction();
`},
		{"magic_call_instance_fallback", `<?php
abstract class BaseClass {
    public function __call($name, $arguments) { return $this->$name(); }
    private function foobar() { return "okey\n"; }
}
class SubClass extends BaseClass {}
$test = new SubClass();
echo $test->foobar();
try { (new BaseClass2)->foobar(); } catch (\Error $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
class BaseClass2 { private function foobar() {} }
`},
		{"overloaded_prop_dim_write", `<?php
class Test {
    protected $property = array('foo' => 'bar');
    function __get($name) { return $this->property; }
}
$obj = new Test;
var_dump($obj->property['foo']);
$obj->property[] = 1;
var_dump($obj);
`},
		{"byref_get_dim_write", `<?php
class foo implements ArrayAccess {
    private $bar;
    public function __construct() { $this->bar = new bar(); }
    public function & __get($key) { $bar = $this->bar; return $bar; }
    public function offsetGet($key): mixed { $bar = $this->bar; return $bar; }
    public function offsetSet($key, $val): void {}
    public function offsetUnset($key): void {}
    public function offsetExists($key): bool { return false; }
}
class bar { public $onBaz = []; }
$foo = new foo();
$foo->bar->onBaz[] = function() {};
var_dump(count($foo->bar->onBaz));
`},
		{"overloaded_undeclared_dim_write", `<?php
class E { private $d = []; function __get($n){ return [1,2]; } }
$e = new E;
$e->undef[] = 3;
$e->undef[] = 4;
var_dump($e->undef);
var_dump($e);
`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) { ftCheck(t, c.name, c.src) })
	}
}
