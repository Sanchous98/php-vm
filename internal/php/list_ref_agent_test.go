package php

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// list_ref_agent_test.go — регрессии ссылочной распаковки list()/[]:
// Zend FETCH_LIST_W + MAKE_REF (OpListFetchRef), распространение ссылок во
// вложенные списки, by-ref offsetGet у ArrayAccess, foreach со ссылочной
// деструктуризацией, Notice о невозможности взять ссылку.
// Сравнение с живым php 8.3 (полный вывод, включая диагностики).

// runLivePHPExitTolerant — как runLivePHP, но фатальные тесты (php выходит с
// кодом 255) тоже дают вывод.
func runLivePHPExitTolerant(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", f).CombinedOutput()
	return strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
}

// checkDiffFatal — сравнение вывода для сценариев, завершающихся фатальной
// ошибкой (наша VM возвращает fail-signal, php — код 255).
func checkDiffFatal(t *testing.T, name, src string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil && throwableOf(err) == nil && !vm.IsExitSignal(err) {
		t.Fatalf("%s: run error: %v", name, err)
	}
	want := runLivePHPExitTolerant(t, src)
	if got != want {
		t.Errorf("%s:\n--- src ---\n%s\n--- got ---\n%q\n--- want ---\n%q", name, src, got, want)
	}
}

func TestListRefAgentVsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"general", `<?php
$arr = array(1, array(2));
list(&$a, list(&$b)) = $arr;
var_dump($a, $b);
var_dump($arr);

$arr = array(1, array(2));
[&$a, [&$b]] = $arr;
var_dump($a, $b);
var_dump($arr);

$arr = array("one" => 1, "two" => array(2));
["one" => &$a, "two" => [&$b], "three" => &$c] = $arr;
var_dump($a, $b, $c);
var_dump($arr);`},
		{"nested_first_ref", `<?php
$arr = [[2], 1];
[[&$b], $x] = $arr;
var_dump($arr, $b, $x);

$arr = [[2, 3]];
[[$c, &$d]] = $arr;
var_dump($arr, $c, $d);`},
		{"from_functions", `<?php
function no_ref($a) { return $a; }
function no_ref_by_ref(&$a) { return $a; }
function &ref_return(&$a) { return $a; }
function &ref_return_global() { global $arr; return $arr; }

$a = [1, 2];
[&$var] = no_ref($a);
var_dump($var);
var_dump($a);

$a = [1, 2];
[&$var] = no_ref_by_ref($a);
var_dump($var);
var_dump($a);

$a = [1, 2];
[&$var] = ref_return($a);
var_dump($var);
var_dump($a);

[,&$var] = ref_return_global();
var_dump($var);
var_dump($arr);`},
		{"nested_unreferenceable", `<?php
function f() { return [[1]]; }
[[&$x]] = f();
var_dump($x);`},
		{"oddities", `<?php
$a = 1;
$b =& $a;
$arr = [&$a, &$b];
list(&$a, &$b) = $arr;
var_dump($a, $b, $arr);
$b++;
var_dump($a, $b, $arr);
unset($a, $b, $arr);

$arr = [1, 2];
list(&$a, $a) = $arr;
var_dump($a);
$a++;
var_dump($arr);
unset($a, $arr);

$a = [1, 2];
$ref =& $a;
list(&$a, &$b) = $a;
var_dump($a, $b);
$a++; $b++;
var_dump($ref);

$p = [1, 2];
list(&$p, &$q) = $p;
var_dump($p, $q);`},
		{"self_assign_plain", `<?php
$x = [5, 6];
list(&$x, &$y) = $x;
var_dump($x, $y);`},
		{"arrayaccess_noref", `<?php
class StorageNoRef implements ArrayAccess {
    private $s = [];
    function __construct(array $a) { $this->s = $a; }
    function offsetSet ($k, $v): void { $this->s[$k] = $v; }
    function offsetGet ($k): mixed { return $this->s[$k]; }
    function offsetExists ($k): bool { return isset($this->s[$k]); }
    function offsetUnset ($k): void { unset($this->s[$k]); }
}
$a = new StorageNoRef([1, 2]);
list(&$one, $two) = $a;
var_dump($a);

$a = new StorageNoRef([1, 2]);
list(,,list($var)) = $a;
var_dump($a);

$a = new StorageNoRef(['one' => 1, 'two' => 2]);
['one' => &$one, 'two' => $two] = $a;
var_dump($a);

$a = new StorageNoRef([[1]]);
list(,list(&$v)) = $a;
var_dump($a, $v);`},
		{"arrayaccess_ref", `<?php
class StorageRef implements ArrayAccess {
    private $s = [];
    function __construct(array $a) { $this->s = $a; }
    function offsetSet ($k, $v): void { $this->s[$k] = $v; }
    function &offsetGet ($k): mixed { return $this->s[$k]; }
    function offsetExists ($k): bool { return isset($this->s[$k]); }
    function offsetUnset ($k): void { unset($this->s[$k]); }
}
$a = new StorageRef([1, 2]);
list(&$one, $two) = $a;
var_dump($a);

$a = new StorageRef([1, 2]);
list(,,list($var)) = $a;
var_dump($a);

$a = new StorageRef([1, 2]);
list(,,list(&$var)) = $a;
var_dump($a);

$a = new StorageRef(['one' => 1, 'two' => 2]);
['one' => &$one, 'two' => $two] = $a;
var_dump($a);`},
		{"class_props", `<?php
class A {
    public $a = [['hello']];
    public $b = ['world'];
    public function getVar() { return $this->a; }
    public function &getVarRef() { return $this->a; }
}
class B { static $a = [['world']]; }

$a = new A();
[&$var] = $a->a;
[&$var_too] = $a->b;
var_dump($a->a);
var_dump($a->b);

$a = new A();
[&$var] = $a->getVar();
var_dump($a->a);

$a = new A();
[&$var] = $a->getVarRef();
var_dump($a->a);

[&$var] = B::$a;
var_dump(B::$a);`},
		{"vm_safety_keys", `<?php
$ary = [[0, 1]];
[[
    0 => &$a,
    ($ary["foo"] = 1) => &$b
]] = $ary;

var_dump($ary, $a, $b);
unset($ary, $a, $b);

$ary = [[0, 1]];
[
    0 => &$a,
    ($ary["foo"] = 1) => &$b
] = $ary[0];
var_dump($ary, $a, $b);`},
		{"autovivify_and_scalars", `<?php
$x = null;
[&$v] = $x;
var_dump($x, $v);

$s = "ab";
try { [&$vs] = $s; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

$i = 5;
try { [&$vi] = $i; } catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

$arr = [null, false, "str", 5];
$a1 = $arr; [&$v1] = $a1; var_dump($a1, $v1);
$a2 = $arr; [,&$v2] = $a2; var_dump($a2[1], $v2);`},
		{"foreach_ref_list", `<?php
$coords = array(array(1, 2), array(3, 4));
foreach ($coords as [&$x, $y]) {
    $x++;
    $y++;
}
var_dump($coords);

$a = [[1]];
$b = $a;
foreach ($a as [&$x]) { $x++; }
var_dump($a, $b);`},
		{"foreach_obj_ref_list", `<?php
class C { public $a=[1]; public $b=[2]; }
$c = new C;
foreach ($c as [&$x]) { var_dump($x); }
var_dump($c);`},
		{"prop_base", `<?php
$o = new stdClass;
[&$v] = $o->p;
var_dump($o, $v);

$u = null;
[&$w] = $u[0];
var_dump($u, $w);`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { checkDiff(t, c.name, c.src) })
	}
}

// TestListRefFatalVsPHP — компайл-фаталы ссылочной деструктуризации.
func TestListRefFatalVsPHP(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"const_rhs", `<?php
list(&$foo) = [42];`},
		{"const_rhs2", `<?php
const FOO = 10;
[&$f] = FOO;`},
		{"new_rhs", `<?php
class X {}
[&$f] = new X;`},
		{"mix_syntax", `<?php
$arr = [1, 2];
[&$a, list($b)] = $arr;`},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { checkDiffFatal(t, c.name, c.src) })
	}
}
