package php

import (
	"strings"
	"testing"
)

// agent_regress_round21_test.go — регрессии раунда 21: длинные K-цепочки
// (переполнение uint8-регистров), deref send-значения генератора, дамп
// замыканий (захваты/this/*RECURSION*), сигнатуры интерфейсов в рефлексии,
// SensitiveParameterValue::value, канонизация ${1} в $GLOBALS, динамический
// global, отсутствие __destruct после брошенного конструктора, разбор
// new/instanceof с выражением-классом, спред в деструктуризации/литералах и
// константных выражениях, порядок notice у by-ref return, число-ключи строк.
// Ожидания сверены с живым php 8.3.

func r21Check(t *testing.T, name, src, want string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil {
		if _, ok := err.(*fatalError); !ok {
			// фаталы компиляции компилируются в чанк и печатаются в выводе
			t.Fatalf("%s: run error: %v", name, err)
		}
	}
	if got != want {
		t.Errorf("%s:\n--- got ---\n%q\n--- want ---\n%q", name, got, want)
	}
}

func TestRegress21(t *testing.T) {
	chain := strings.Repeat(" + 1", 300)
	r21Check(t, "long_k_chain", `<?php
$a = 1;
$foo = $a`+chain+` + $a;
var_dump($foo);
`, "int(302)\n")

	r21Check(t, "yield_send_var", `<?php
function f3() { yield 1; $a = [yield "k" => "z"]; var_dump($a); }
$g = f3(); $g->rewind(); $g->send(5); $v = 6; $g->send($v);
`, "array(1) {\n  [0]=>\n  int(6)\n}\n")

	r21Check(t, "closure_capture_this", `<?php
class foo { public function bar() { $test = "one"; return function() use($test) {}; } }
var_dump((new foo)->bar());
`, "object(Closure)#2 (2) {\n  [\"static\"]=>\n  array(1) {\n    [\"test\"]=>\n    string(3) \"one\"\n  }\n  [\"this\"]=>\n  object(foo)#1 (0) {\n  }\n}\n")

	r21Check(t, "closure_recursion", `<?php
$a = function () use(&$a) {};
var_dump($a);
`, "object(Closure)#1 (1) {\n  [\"static\"]=>\n  array(1) {\n    [\"a\"]=>\n    *RECURSION*\n  }\n}\n")

	r21Check(t, "sensitive_value_reflection", `<?php
$v = new SensitiveParameterValue('secret');
$r = new ReflectionClass($v);
var_dump($r->getProperty('value')->getValue($v));
`, "string(6) \"secret\"\n")

	r21Check(t, "enum_name_property", `<?php
enum Foo { case Bar; }
var_dump((new ReflectionClass(Foo::class))->getProperties());
`, "array(1) {\n  [0]=>\n  object(ReflectionProperty)#2 (2) {\n    [\"name\"]=>\n    string(4) \"name\"\n    [\"class\"]=>\n    string(3) \"Foo\"\n  }\n}\n")

	r21Check(t, "interface_sig_reflection", `<?php
interface One { public function a(object $obj): object; }
class Two implements One { public function a(object $obj): object {} }
$m = (new ReflectionClass(One::class))->getMethod('a');
var_dump($m->getReturnType()->getName(), $m->getParameters()[0]->getType()->getName());
`, "string(6) \"object\"\nstring(6) \"object\"\n")

	r21Check(t, "globals_numeric_key", `<?php
${1} = 42;
var_dump($GLOBALS[1]);
`, "int(42)\n")

	r21Check(t, "global_dynamic_side_effect", `<?php
function sf($arg) { echo "called\n"; return $arg; }
function test() { global ${sf("a")}; var_dump($a); }
$a = 42; test();
`, "called\nint(42)\n")

	r21Check(t, "ctor_failure_no_dtor", `<?php
class T { function __construct() { echo "ctor\n"; throw new Exception("x"); }
          function __destruct() { echo "dtor\n"; } }
try { new T; } catch (Exception $e) { echo "caught\n"; }
`, "ctor\ncaught\n")

	r21Check(t, "new_paren_expr", `<?php
$c = 'class';
var_dump(new ('std'.$c) instanceof stdClass);
`, "bool(true)\n")

	r21Check(t, "new_static_prop_class", `<?php
class K { static $s = 'stdClass'; }
$k = 'K';
var_dump(new $k::$s);
`, "object(stdClass)#1 (0) {\n}\n")

	r21Check(t, "spread_in_destructuring", `<?php
[$a, ...$b] = [1, 2, 3];
`, "\nFatal error: Spread operator is not supported in assignments in diff.php on line 2\n")

	r21Check(t, "spread_literal_fatal", `<?php
var_dump([...42]);
`, "\nFatal error: Only arrays and Traversables can be unpacked in diff.php on line 2\n")

	r21Check(t, "const_spread_nonarray", `<?php
const C = [...new ArrayObject()];
`, "\nFatal error: Uncaught Error: Only arrays can be unpacked in constant expression in diff.php:2\nStack trace:\n#0 {main}\n  thrown in diff.php on line 2\n")

	r21Check(t, "const_object_offsetget", `<?php
enum Foo implements ArrayAccess {
    case Bar;
    public function offsetGet($key): mixed { return 42; }
    public function offsetExists($key): bool {}
    public function offsetSet($key, $value): void {}
    public function offsetUnset($key): void {}
}
class X { const F = Foo::Bar[0]; }
`, "\nFatal error: Uncaught Error: Cannot use [] on objects in constant expression in diff.php:9\nStack trace:\n#0 diff.php(11): [constant expression]()\n#1 {main}\n  thrown in diff.php on line 9\n")

	r21Check(t, "byref_ret_notice_after_type", `<?php
function &foo(array &$in) : array { return null; }
$array = [1, 2, 3];
try { foo($array); } catch (TypeError $e) { echo "T\n"; }
`, "T\n")

	r21Check(t, "string_offset_float_no_deprecated", `<?php
ini_set('error_reporting', E_ALL & ~E_WARNING);
$string = 'php';
var_dump($string[1.5]);
`, "string(1) \"h\"\n")

	r21Check(t, "use_function_dup_import", `<?php
namespace { use function foo\bar; use function foo\BAR; }
`, "\nFatal error: Cannot use function foo\\BAR as BAR because the name is already in use in diff.php on line 2\n")

	r21Check(t, "global_comma_error", `<?php
global $$foo->bar;
`, "\nParse error: syntax error, unexpected token \"->\", expecting \",\" or \";\" in diff.php on line 2\n")
}
