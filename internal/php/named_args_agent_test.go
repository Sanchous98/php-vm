package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// named_args_agent_test.go — регрессии именованных аргументов и ссылочной
// распаковки аргументов (SEND_UNPACK): extra named в трейсах, биндинг имён у
// call_user_func*, аричность ZPP с именованными, &...$refs, by-ref spread по
// массивам и Traversable, а также `new` без аргументов у конструктора с
// необязательными параметрами (легаси-окно argc). Сравнение с живым php 8.3.
//
// php запускается с zend.exception_ignore_args=0 — как корпус php-src.

func runLivePHPArgs(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", "-d", "zend.exception_ignore_args=0", f).CombinedOutput()
	// php печатает полный путь; наша VM компилирует файл как "diff.php".
	// Длину в string(N) тоже приводим к короткому имени.
	full := dir + string(os.PathSeparator) + "diff.php"
	re := regexp.MustCompile(`string\(\d+\) "` + regexp.QuoteMeta(full) + `"`)
	res := re.ReplaceAllString(string(out), `string(8) "diff.php"`)
	return strings.ReplaceAll(res, dir+string(os.PathSeparator), "")
}

func checkDiffArgs(t *testing.T, name, src string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil && !vm.IsExitSignal(err) {
		t.Fatalf("%s: run error: %v", name, err)
	}
	want := runLivePHPArgs(t, src)
	if got != want {
		t.Errorf("%s:\n--- src ---\n%s\n--- got ---\n%q\n--- want ---\n%q", name, src, got, want)
	}
}

func TestNamedArgsAgentVsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"extra_named_in_traces", `<?php
function test($a, ...$rest) {
    var_dump(debug_backtrace());
    throw new Exception("Test");
}
try {
    test(1, 2, x: 3, y: 4);
} catch (Exception $e) {
    var_dump($e->getTrace());
    echo $e, "\n";
}`},
		{"call_user_func_named", `<?php
$test = function($a = 'a', $b = 'b', $c = 'c') { echo "a = $a, b = $b, c = $c\n"; };
$test_variadic = function(...$args) { var_dump($args); };
$test_ref = function(&$ref) { $ref++; };
call_user_func($test, 'A', c: 'C');
call_user_func($test, c: 'C', a: 'A');
call_user_func($test, c: 'C');
call_user_func($test_variadic, 'A', c: 'C');
call_user_func($test_ref, ref: null);
var_dump(call_user_func('call_user_func', $test, c: 'D'));
var_dump(call_user_func('array_slice', [1, 2, 3, 4, 'x' => 5], 3, preserve_keys: true));
$rf = new ReflectionFunction($test);
$rf->invoke('A', c: 'C');
$rf->invokeArgs(['A', 'c' => 'C']);
class Test {
    public function __construct($a = 'a', $b = 'b', $c = 'c') {
        if (func_num_args() != 0) { echo "ctor a = $a, b = $b, c = $c\n"; }
    }
    public function method($a = 'a', $b = 'b', $c = 'c') { echo "m a = $a, b = $b, c = $c\n"; }
}
$rm = new ReflectionMethod(Test::class, 'method');
$rm->invoke(new Test, 'A', c: 'C');
$rm->invokeArgs(new Test, ['A', 'c' => 'C']);
$rc = new ReflectionClass(Test::class);
$rc->newInstance('A', c: 'C');
$rc->newInstanceArgs(['A', 'c' => 'C']);`},
		{"multisort_named_priority", `<?php
try {
    call_user_func_array("array_multisort", ["" => 1]);
} catch (Throwable $e) { echo $e->getMessage(), "\n"; }
try {
    call_user_func_array("array_multisort", ["x" => [2, 1]]);
} catch (Throwable $e) { echo $e->getMessage(), "\n"; }
try {
    call_user_func_array("array_merge", ["" => [1]]);
} catch (Throwable $e) { echo $e->getMessage(), "\n"; }`},
		{"variadic_named_refs", `<?php
function test($a, string ...$extra) {
    var_dump($a, $extra, func_num_args(), func_get_args());
}
test(b: 'b', a: 'a', c: 'c', extra: 'extra');

function test2(&...$refs) {
    foreach ($refs as &$ref) $ref++;
}
$x = 0; $y = 0;
test2(x: $x, y: $y);
var_dump($x, $y);`},
		{"unpack_named_and_refs", `<?php
function test($a, $b, $c) { echo "a = $a, b = $b, c = $c\n"; }
function test2($a = null, &$b = null) { $b++; }

test(...['a' => 'a', 'b' => 'b', 'c' => 'c']);
test(...['a', 'b' => 'b', 'c' => 'c']);
try { test(...['a', 'b' => 'b', 'c']); } catch (Error $e) { echo $e->getMessage(), "\n"; }
try { test(...['a', 'a' => 'a']); } catch (Error $e) { echo $e->getMessage(), "\n"; }

$ary = ['b' => 0];
$ary2 = $ary;
test2(...$ary);
var_dump($ary, $ary2);

test(...new ArrayIterator(['a' => 'a', 'b' => 'b', 'c' => 'c']));
try { test(...new ArrayIterator(['a', 'b' => 'b', 'c'])); } catch (Error $e) { echo $e->getMessage(), "\n"; }`},
		{"spread_by_ref", `<?php
function test1(&...$args) {
    foreach ($args as &$arg) { $arg++; }
}
test1(...[1, 2, 3]);
$array = [1, 2, 3];
test1(...$array);
var_dump($array);

$array1 = [1, 2]; $array2 = [3, 4];
test1(...$array1, ...$array2);
var_dump($array1, $array2);

function test2($val1, &$ref1, $val2, &$ref2) { $ref1++; $ref2++; }
$array = [0, 0, 0, 0];
test2(...$array);
var_dump($array);

$a = $b = $c = $d = 0;
$array = [0, 0, 0, 0];
test2($a, ...$array);
var_dump($a, $array);
test2($a, $b, $c, $d, ...$array);
var_dump($a, $b, $c, $d, $array);`},
		{"spread_traversable_by_ref", `<?php
function test($val1, $val2, $val3, &$ref) { $ref = 42; }
function gen($array) { foreach ($array as $el) { yield $el; } }
$b = 0;
test(1, 2, 3, $b, ...gen([4, 5, 6]));
var_dump($b);
test(...gen([1, 2, 3, 4]));
test(1, 2, ...gen([3, 4]));`},
		{"new_optional_ctor_args", `<?php
class T {
    function __construct($a = 'A', $b = 'B') { var_dump($a, $b, func_num_args()); }
}
new T;
$q = [1, 2, 3];
new T;
new T('x');
new T(b: 'y');
var_dump($q);`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { checkDiffArgs(t, c.name, c.src) })
	}
}
