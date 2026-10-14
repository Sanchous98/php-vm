package php

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// Дифференциальные тесты типизированных packed-массивов против живого php:
// вывод (включая предупреждения) должен совпадать байт в байт.

var phpBin string

func livePHP(t *testing.T) string {
	t.Helper()
	if phpBin != "" {
		return phpBin
	}
	p, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php не найден в PATH")
	}
	phpBin = p
	return p
}

func runOurPHP(t *testing.T, src string) (string, error) {
	t.Helper()
	c, err := CompileFile(src, "diff.php")
	if err != nil {
		return "", err
	}
	var m vm.VM
	m.Init(c.Main, c.Funcs, c.Classes)
	m.SetStepLimit(0)
	var buf bytes.Buffer
	m.SetOutput(&buf)
	err = m.Run()
	return buf.String(), err
}

func runLivePHP(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	f := filepath.Join(dir, "diff.php")
	if err := os.WriteFile(f, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", f).CombinedOutput()
	if err != nil {
		t.Fatalf("php: %v\n%s", err, out)
	}
	// В диагностиках php печатает полный путь; наша VM компилирует файл как
	// "diff.php" — убираем префикс каталога, чтобы сравнивались только суффиксы.
	return strings.ReplaceAll(string(out), dir+string(os.PathSeparator), "")
}

// checkDiff сверяет вывод нашей VM и php. Нормализуем только хвостовые переводы
// строк и возможный перевод строки после последнего вывода.
func checkDiff(t *testing.T, name, src string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if err != nil {
		if th := throwableOf(err); th == nil {
			t.Fatalf("%s: run error: %v", name, err)
		}
	}
	want := runLivePHP(t, src)
	if got != want {
		t.Errorf("%s:\n--- src ---\n%s\n--- got ---\n%q\n--- want ---\n%q", name, src, got, want)
	}
}

func TestTypedArrayDiff(t *testing.T) {
	livePHP(t)
	cases := []struct{ name, src string }{
		{"build_sum", `<?php
$a = [];
for ($i = 0; $i < 1000; $i++) { $a[] = $i * 3 - 7; }
$s = 0;
foreach ($a as $v) { $s += $v; }
echo $s + array_sum($a), "\n";`},

		{"set_by_index", `<?php
$a = array_fill(0, 100, 0);
for ($i = 0; $i < 100; $i++) { $a[$i] = $i; }
var_export($a);
echo "\n";`},

		{"int_overflow_sum", `<?php
$a = [PHP_INT_MAX, 1];
var_dump(array_sum($a));
var_dump(array_sum([PHP_INT_MAX, PHP_INT_MAX]));
var_dump(array_product([PHP_INT_MAX, 2]));
var_dump(array_sum([]));
var_dump(array_product([]));`},

		{"float_arith", `<?php
$a = [1.5, 2.25];
$a[] = 3.0;
$s = 0.0;
foreach ($a as $v) { $s += $v; }
var_dump($s, array_sum($a), array_product($a), count($a), $a[1]);
echo implode(",", $a), "\n";`},

		{"int_float_mix", `<?php
$a = [];
$a[] = 1;
$a[] = 2.5;
$a[] = 3;
var_dump($a);
echo array_sum($a), "\n";
echo gettype($a[0]), " ", gettype($a[1]), " ", gettype($a[2]), "\n";`},

		{"float_then_int", `<?php
$a = [1.0];
$a[] = 2;
var_dump($a);`},

		{"string_array", `<?php
$a = ["b", "a", "c"];
$a[] = "aa";
var_dump($a);
echo implode("-", $a), "\n";
var_dump(array_search("a", $a), array_search("a", $a, true), in_array("c", $a));
sort($a);
var_dump($a);
rsort($a);
var_dump($a);`},

		{"string_sort_flags", `<?php
$a = ["10", "9", "1", "abc", "2"];
$b = $a;
sort($b, SORT_STRING);
var_dump($b);
sort($b, SORT_NUMERIC);
var_dump($b);
sort($b, SORT_REGULAR);
var_dump($b);`},

		{"assoc_string_keys", `<?php
$m = [];
for ($i = 0; $i < 50; $i++) { $m["k" . ($i % 7)] = $i; }
$s = 0;
foreach ($m as $k => $v) { $s += $v + strlen($k); }
echo $s, " ", count($m), "\n";
var_dump(array_keys($m));`},

		{"negative_keys", `<?php
$a = [];
$a[-1] = "neg";
$a[] = "auto";
$a[-2] = "neg2";
var_dump($a);
var_dump(array_keys($a));
echo json_encode($a), "\n";`},

		{"numeric_string_keys", `<?php
$a = [];
$a["10"] = 1;
$a["1e2"] = 2;
$a["01"] = 3;
$a["-5"] = 4;
var_dump($a);
var_dump(array_keys($a), array_values($a));`},

		{"cow_copy", `<?php
$a = [1, 2, 3];
$b = $a;
$b[0] = 100;
$b[] = 4;
var_dump($a, $b);`},

		{"cow_nested", `<?php
$a = [[1, 2], [3, 4]];
$b = $a;
$b[0][0] = 9;
var_dump($a[0][0], $b[0][0]);
$c = $a;
$c[0] = [7];
var_dump($a[0], $c[0]);`},

		{"cow_string_list", `<?php
$a = ["x", "y"];
$b = $a;
$b[0] = "z";
var_dump($a, $b);`},

		{"ref_cell", `<?php
$a = [1, 2, 3];
$b = 10;
$a[0] = &$b;
$b = 20;
var_dump($a);
$r = &$a[1];
$r = 99;
var_dump($a);
unset($r);
var_dump($a);`},

		{"ref_foreach", `<?php
$a = [1, 2, 3];
foreach ($a as &$v) { $v *= 2; }
unset($v);
var_dump($a);
foreach ($a as $k => &$v) { $v += $k; }
var_dump($a);`},

		{"unset_holes", `<?php
$a = [10, 20, 30, 40];
unset($a[1]);
unset($a[3]);
var_dump($a, count($a), array_keys($a), array_values($a));
$a[] = 50;
var_dump($a);
$a[7] = 70;
var_dump($a);`},

		{"unset_all", `<?php
$a = [1, 2];
unset($a[0]);
unset($a[1]);
var_dump($a, count($a), empty($a));
$a[] = 5;
var_dump($a);`},

		{"dump_export", `<?php
$a = [1, 2, 3];
var_dump($a);
var_export($a);
echo "\n";
echo json_encode($a), "\n";
echo serialize($a), "\n";
print_r($a);
$f = [1.5, -0.0, 2.0];
var_dump($f);
echo json_encode($f), "\n";
echo serialize($f), "\n";
var_export($f);
echo "\n";`},

		{"str_dump_export", `<?php
$a = ["x", "hello world", ""];
var_dump($a);
var_export($a);
echo "\n";
echo json_encode($a), "\n";
echo serialize($a), "\n";
echo implode("|", $a), "\n";`},

		{"count_empty", `<?php
$a = [];
var_dump(empty($a), count($a));
$a[] = 0;
var_dump(empty($a), count($a), $a);
echo count($a, COUNT_RECURSIVE), "\n";`},

		{"splice", `<?php
$a = [1, 2, 3, 4, 5];
$r = array_splice($a, 1, 2, [9, 8, 7]);
var_dump($a, $r);
$b = ["a", "b", "c"];
$r2 = array_splice($b, -2);
var_dump($b, $r2);`},

		{"reverse", `<?php
$a = [1, 2, 3, 4];
var_dump(array_reverse($a), array_reverse($a, true));
$s = ["a", "b"];
var_dump(array_reverse($s));`},

		{"sort_int", `<?php
$a = [5, 3, 9, 1, 3];
var_dump(sort($a), $a);
$b = [5, 3, 9];
var_dump(rsort($b), $b);
$c = ["x" => 3, "y" => 1, "z" => 2];
var_dump(asort($c), $c);
var_dump(ksort($c), $c);
$d = [4, 2, 8];
var_dump(usort($d, function ($x, $y) { return $y <=> $x; }), $d);`},

		{"sort_mixed_types", `<?php
$a = [3, "2", 1.5, true, null];
sort($a);
var_dump($a);
$b = ["3", "20", "100", "abc"];
sort($b);
var_dump($b);`},

		{"unique_column", `<?php
$a = [1, 2, 2, 3, 1];
var_dump(array_unique($a));
$b = ["a", "b", "a"];
var_dump(array_unique($b));
$rows = [["id" => 1, "n" => "x"], ["id" => 2, "n" => "y"], ["id" => 1, "n" => "z"]];
var_dump(array_column($rows, "n", "id"));
var_dump(array_column($rows, "n"));`},

		{"list_destructuring", `<?php
$a = [1, 2, 3];
[$x, $y, $z] = $a;
var_dump($x, $y, $z);
["k" => $v] = ["k" => "val"];
var_dump($v);
list($p, , $q) = $a;
var_dump($p, $q);
[$first, $second] = array_reverse($a);
var_dump($first, $second);`},

		{"push_multi", `<?php
$a = [1, 2];
var_dump(array_push($a, 3, 4, 5), $a);
$b = ["x"];
var_dump(array_push($b, "y"), $b);`},

		{"shift_unshift_pop", `<?php
$a = [1, 2, 3];
var_dump(array_shift($a), $a);
var_dump(array_unshift($a, 0, -1), $a);
var_dump(array_pop($a), $a);
$b = ["a", "b"];
var_dump(array_shift($b), $b);
var_dump(array_pop($a), $a, array_pop($a), $a);
var_dump(array_shift($a), $a);`},

		{"search_strict", `<?php
$a = [0, 1, 2, 3];
var_dump(array_search("1", $a), array_search("1", $a, true), in_array("1", $a), in_array("1", $a, true));
var_dump(array_search(1.0, $a), array_search(1.0, $a, true));
var_dump(array_search(true, $a), array_search(true, $a, true));
$b = ["a", "b"];
var_dump(array_search("b", $b), in_array("c", $b), array_search("z", $b));`},

		{"sum_product_mixed", `<?php
var_dump(array_sum([1, 2, 3]), array_product([1, 2, 3]));
var_dump(array_sum([1.5, 2]), array_product([1.5, 2]));
var_dump(array_sum(["1", "2.5", "abc"]), array_product(["2", "3"]));
var_dump(array_sum([true, false, null]));
$a = [1, 2, 3];
var_dump(array_sum($a), array_product($a));`},

		{"map_null_callback", `<?php
$a = [1, 2, 3];
$b = ["x", "y", "z"];
var_dump(array_map(null, $a, $b));
var_dump(array_map(null, $a));
var_dump(array_map(function ($x) { return $x * 2; }, $a));
var_dump(array_map("strval", $a));
$c = [1, 2, 3, 4];
var_dump(array_filter($c, function ($x) { return $x % 2 == 0; }));
var_dump(array_filter($c));
var_dump(array_filter([0, "", null, "0", 1, false, 2.5]));`},

		{"merge_slice", `<?php
$a = [1, 2, 3];
var_dump(array_merge($a, [4, 5]));
var_dump(array_merge($a, ["k" => 9]));
var_dump(array_slice($a, 1), array_slice($a, -2), array_slice($a, 1, 1, true));
var_dump(array_merge([], [1], []));
$s = ["a", "b", "c"];
var_dump(array_slice($s, 1), array_merge($s, ["d"]));`},

		{"fill_range_flip", `<?php
var_dump(array_fill(0, 5, 7));
var_dump(array_fill(3, 3, "x"));
var_dump(range(1, 5), range(5, 1), range(0, 10, 3), range(1.5, 3, 0.5));
var_dump(array_flip(["a", "b", "c"]));
var_dump(array_flip([1, 2]));`},

		{"key_exists_isset", `<?php
$a = [1, null, 3];
var_dump(isset($a[0]), isset($a[1]), isset($a[2]), array_key_exists(1, $a));
var_dump(isset($a[3]), array_key_exists(3, $a));
$b = ["k" => "v"];
var_dump(isset($b["k"]), array_key_exists("k", $b), isset($b["z"]));
echo implode(",", array_keys($a)), "\n";`},

		{"walk", `<?php
$a = [1, 2, 3];
array_walk($a, function (&$v, $k) { $v = $v * 10 + $k; });
var_dump($a);
$b = ["x" => 1];
array_walk($b, function ($v, $k) { echo "$k=$v;"; });
echo "\n";`},

		{"objects_in_array", `<?php
class P { public $v; function __construct($v) { $this->v = $v; } }
$objs = [];
for ($i = 0; $i < 5; $i++) { $objs[] = new P($i); }
$s = 0;
foreach ($objs as $o) { $s += $o->v; }
echo $s, " ", count($objs), "\n";
$objs2 = $objs;
$objs2[0] = new P(100);
var_dump($objs[0]->v, $objs2[0]->v);`},

		{"count_values", `<?php
var_dump(array_count_values([1, 2, 2, 3, 1, 1]));
var_dump(array_count_values(["a", "b", "a"]));
var_dump(array_sum(array_count_values([1, 1, 2])));`},

		{"reduce_max_min", `<?php
$a = [4, 8, 2, 9];
var_dump(max($a), min($a), max(1, 5, 3), min([2, 1]));
var_dump(array_reduce($a, function ($c, $x) { return $c + $x; }, 0));
var_dump(array_sum(array_map(function ($x) { return $x + 1; }, $a)));`},

		{"append_loop_strings", `<?php
$a = [];
for ($i = 0; $i < 10; $i++) { $a[] = "s" . $i; }
echo implode(",", $a), "\n";
var_dump($a);`},

		{"nested_typed", `<?php
$a = [];
for ($i = 0; $i < 3; $i++) { $a[] = range($i, $i + 2); }
var_dump($a);
var_dump(array_sum(array_merge(...$a)));
echo json_encode($a), "\n";`},

		{"foreach_modify_value", `<?php
$a = [1, 2, 3];
foreach ($a as $k => $v) { $a[$k] = $v * 100; }
var_dump($a);
$b = [1, 2, 3];
foreach ($b as $k => $v) { $b[] = $v; if (count($b) > 6) break; }
var_dump($b);`},

		{"append_after_copy_loop", `<?php
$a = [];
for ($i = 0; $i < 5; $i++) { $a[] = $i; }
$b = $a;
$a[] = 100;
var_dump($a, $b);`},

		{"current_next_prev", `<?php
$a = [10, 20, 30];
var_dump(current($a), next($a), current($a), key($a));
var_dump(prev($a), end($a), current($a), key($a));
var_dump(reset($a), current($a));
var_dump(next($a), next($a), next($a), current($a), key($a));
$s = ["a", "b"];
var_dump(current($s), end($s), current($s));`},

		{"array_diff_intersect", `<?php
$a = [1, 2, 3, 4];
var_dump(array_diff($a, [2, 4]), array_intersect($a, [1, 3, 9]));
var_dump(array_sum(array_diff($a, [1])));`},

		{"splice_typed_grow", `<?php
$a = [1, 2, 3];
for ($i = 0; $i < 5; $i++) { array_splice($a, 1, 0, [$i]); }
var_dump($a);
var_dump(array_sum($a));`},

		{"isset_nested", `<?php
$a = [[1, 2], [3]];
var_dump(isset($a[0][1]), isset($a[1][1]), isset($a[2][0]));
$a[0][1] = null;
var_dump(isset($a[0][1]), array_key_exists(1, $a[0]));`},

		{"compact_typed", `<?php
$a = [1, 2];
$copy = $a;
$copy[0] = 5;
echo array_sum($a), " ", array_sum($copy), "\n";
$r = &$a[1];
$r = 50;
var_dump($a, $copy);`},

		{"sort_after_pop_append", `<?php
$a = [3, 1, 2];
array_pop($a);
sort($a);
$a[] = 9;
var_dump($a, array_keys($a));
$b = [5, 4];
unset($b[1]);
$b[] = 6;
sort($b);
var_dump($b);`},

		{"huge_int_key", `<?php
$a = [1];
$a[PHP_INT_MAX] = 2;
var_dump($a, array_keys($a));
echo count($a), "\n";
$b = [1, 2];
$b[(string)PHP_INT_MAX] = 3;
var_dump($b);`},

		{"nest_recursive_count", `<?php
$a = [[1, 2], [3, [4, 5]]];
var_dump(count($a), count($a, COUNT_RECURSIVE));
$b = [1, 2, 3];
var_dump(count($b), count($b, COUNT_RECURSIVE));`},

		{"serialize_roundtrip", `<?php
$a = [1, 2, 3];
$s = serialize($a);
var_dump($s, unserialize($s));
$f = [1.5, 2.5];
var_dump(serialize($f), unserialize(serialize($f)));
$st = ["a", "b"];
var_dump(serialize($st), unserialize(serialize($st)));`},

		{"map_type_change", `<?php
$a = range(1, 4);
$b = array_map(function ($x) { return "v" . $x; }, $a);
var_dump($b);
$c = array_map(function ($x) { return $x % 2 ? $x : "even"; }, $a);
var_dump($c, array_sum($a));
$d = array_map(function ($x) { return $x * 1.5; }, $a);
var_dump($d);`},

		{"in_array_float_strict", `<?php
$a = [1.5, 2.5, 3.5];
var_dump(in_array(2.5, $a), in_array(2.5, $a, true), in_array(2, $a), in_array(2, $a, true));
var_dump(array_search(1.5, $a), array_search("1.5", $a), array_search("1.5", $a, true));
$b = [1, 2];
var_dump(in_array(2.0, $b), in_array(2.0, $b, true), array_search(2.0, $b));`},

		{"array_sum_big_overflow", `<?php
$a = [];
for ($i = 0; $i < 100; $i++) { $a[] = PHP_INT_MAX; }
var_dump(array_sum($a) > 0, is_float(array_sum($a)));
$b = [];
for ($i = 0; $i < 100; $i++) { $b[] = 1000000; }
var_dump(array_sum($b), array_product([1000, 1000, 1000]));
$c = [1, 2, -3];
var_dump(array_sum($c), array_product($c));`},

		{"reverse_unique_int", `<?php
$a = [3, 1, 3, 2, 1];
var_dump(array_reverse($a), array_unique($a), array_reverse(array_unique($a)));
var_dump(array_sum(array_unique($a)));
$b = array_values(array_unique($a));
var_dump($b, array_keys($b));`},

		{"push_pop_mix_typed", `<?php
$a = [];
for ($i = 0; $i < 20; $i++) { array_push($a, $i); }
while (count($a) > 10) { array_pop($a); }
var_dump($a);
array_unshift($a, -1, -2);
var_dump($a[0], $a[1], array_sum($a));
echo implode(",", $a), "\n";`},

		{"merge_slice_typed_big", `<?php
$a = range(0, 999);
$b = array_slice($a, 100, 50);
var_dump(count($b), $b[0], $b[49], array_sum($b));
$c = array_merge($b, $b);
var_dump(count($c), $c[0], $c[50], $c[99], array_sum($c));
$d = array_filter($a, function ($x) { return $x % 100 == 0; });
var_dump(count($d), array_sum($d));`},

		{"shared_mutations", `<?php
$a = [3, 1, 2];
$b = $a;
sort($a);
var_dump($a, $b);
$b[] = 9;
var_dump($a, $b);
array_push($b, 10, 11);
var_dump($b, count($a));
$c = $a;
unset($c[0]);
var_dump($a, $c);
$d = $a;
array_pop($d);
var_dump($a, $d);
$e = $a;
$e[0] = 100;
var_dump($a, $e);`},

		{"shared_string_mutations", `<?php
$a = ["b", "a"];
$b = $a;
sort($a);
var_dump($a, $b);
$b[0] = "z";
var_dump($a, $b);
$c = $a;
array_unshift($c, "first");
var_dump($a, $c);
$d = array_reverse($a);
var_dump($a, $d);`},

		{"ref_to_array_and_copy", `<?php
$a = [1, 2, 3];
$r = &$a;
$r[] = 4;
var_dump($a, $r);
$b = $a;
$b[0] = 9;
var_dump($a, $b);
$c = &$a[1];
$c = 20;
var_dump($a);
unset($c);
$a[] = 5;
var_dump($a);`},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) { checkDiff(t, c.name, c.src) })
	}
}
