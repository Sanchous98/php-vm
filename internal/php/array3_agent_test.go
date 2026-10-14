package php

import (
	"strings"
	"testing"
)

// runArray3 компилирует и исполняет PHP-фрагмент (с открывающим тегом).
func runArray3(t *testing.T, src string) string {
	t.Helper()
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v (out=%q)", err, out)
	}
	return out
}

// TestCompactLocalSymtab — compact() в теле функции видит только локальные
// переменные (и объявленные global), но не main-переменные.
func TestCompactLocalSymtab(t *testing.T) {
	got := runArray3(t, `<?php
$a = 'main.a';
function f() {
    $b = 'f.b';
    $c = 'f.c';
    var_dump(compact('a', 'b', 'c'));
}
f();`)
	want := "\nWarning: compact(): Undefined variable $a in php on line 6\n" +
		"array(2) {\n  [\"b\"]=>\n  string(3) \"f.b\"\n  [\"c\"]=>\n  string(3) \"f.c\"\n}\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestCompactThis — compact('this') возвращает $this в методе, но не в main и
// не в статическом замыкании.
func TestCompactThis(t *testing.T) {
	got := runArray3(t, `<?php
class C {
    function test() { return compact('this'); }
}
var_dump((new C)->test());
var_dump(compact('this'));
var_dump((static function () { return compact('this'); })());`)
	if !strings.Contains(got, `object(C)#1 (0)`) {
		t.Fatalf("expected $this object, got %q", got)
	}
	if strings.Count(got, "array(0)") != 2 {
		t.Fatalf("expected two empty arrays, got %q", got)
	}
}

// TestExtractArgValidation — ValueError для невалидного типа извлечения,
// отсутствующего и невалидного префикса.
func TestExtractArgValidation(t *testing.T) {
	got := runArray3(t, `<?php
$arr = [1];
try { extract($arr, -1); } catch (\ValueError $e) { echo $e->getMessage(), "\n"; }
try { extract($arr, 7, "wddr"); } catch (\ValueError $e) { echo $e->getMessage(), "\n"; }
try { extract($arr, EXTR_PREFIX_IF_EXISTS); } catch (\ValueError $e) { echo $e->getMessage(), "\n"; }
try { extract($arr, EXTR_PREFIX_ALL, '85bogus'); } catch (\ValueError $e) { echo $e->getMessage(), "\n"; }`)
	want := "extract(): Argument #2 ($flags) must be a valid extract type\n" +
		"extract(): Argument #2 ($flags) must be a valid extract type\n" +
		"extract(): Argument #3 ($prefix) is required when using this extract type\n" +
		"extract(): Argument #3 ($prefix) must be a valid identifier\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestExtractPrefixAllEmptyKey — пустой строковый ключ отбрасывается при
// EXTR_PREFIX_ALL, но не при EXTR_PREFIX_INVALID.
func TestExtractPrefixAllEmptyKey(t *testing.T) {
	got := runArray3(t, `<?php
$a = ["" => 1, "4name" => 2];
var_dump(extract($a, EXTR_PREFIX_ALL, "bb"));
var_dump(extract($a, EXTR_PREFIX_INVALID, "cc"));`)
	want := "int(1)\nint(2)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestExtractWriteThroughRef — extract() пишет сквозь существующую ссылку.
func TestExtractWriteThroughRef(t *testing.T) {
	got := runArray3(t, `<?php
$var = 'original value';
$ref =& $var;
$hash = ['var' => 'new value'];
extract($hash);
var_dump($var === $ref);`)
	if got != "bool(true)\n" {
		t.Fatalf("got %q", got)
	}
}

// TestExtractTypedRefError — extract() в ссылку на типизированное свойство
// проверяет тип и бросает TypeError.
func TestExtractTypedRefError(t *testing.T) {
	got := runArray3(t, `<?php
class Test { public int $i = 0; }
$t = new Test;
$i =& $t->i;
try { extract(['i' => 'foo']); } catch (\TypeError $e) { echo $e->getMessage(), "\n"; }
var_dump($t->i);`)
	want := "Cannot assign string to reference held by property Test::$i of type int\nint(0)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestMergeRecursiveSkipsObjects — array_merge_recursive при совпадении
// строковых ключей отбрасывает объекты/замыкания (GH-16053).
func TestMergeRecursiveSkipsObjects(t *testing.T) {
	got := runArray3(t, `<?php
class test {}
$o = new test;
var_dump(array_merge_recursive(["string" => $o], ["string" => "hello"]));
var_dump(array_merge_recursive(["string" => "hello"], ["string" => $o]));`)
	want := "array(1) {\n  [\"string\"]=>\n  array(1) {\n    [0]=>\n    string(5) \"hello\"\n  }\n}\n" +
		"array(1) {\n  [\"string\"]=>\n  array(1) {\n    [0]=>\n    string(5) \"hello\"\n  }\n}\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestArrayMultisortNoByRefNotice — array_multisort не эмитит Notice для
// временного значения (результата вызова) в by-ref позиции.
func TestArrayMultisortNoByRefNotice(t *testing.T) {
	got := runArray3(t, `<?php
$data = [['aa' => 'bb'], ['aa' => 'bb']];
try {
    array_multisort(array_column($data, 'bb'), SORT_DESC, $data);
} catch (\ValueError $e) { echo $e->getMessage(), "\n"; }`)
	if got != "Array sizes are inconsistent\n" {
		t.Fatalf("got %q", got)
	}
}

// TestSortRegularObjectString — SORT_REGULAR сравнивает объект с __toString
// со строкой по строковому представлению ("Class A object" < "string").
func TestSortRegularObjectString(t *testing.T) {
	got := runArray3(t, `<?php
class A { function __toString() { return "Class A object"; } }
$a = new A;
$arr = ["string", $a];
sort($arr);
echo gettype($arr[0]), "\n";
echo gettype($arr[1]), "\n";`)
	if got != "object\nstring\n" {
		t.Fatalf("got %q", got)
	}
}

// TestAssignRefToCallResult — `=&` от вызова обычной функции даёт Notice и не
// создаёт alias с возвращённой ссылкой.
func TestAssignRefToCallResult(t *testing.T) {
	got := runArray3(t, `<?php
$a = 1;
$array = [&$a];
$b =& array_shift($array);
$b = 2;
echo "a = $a, b = $b\n";`)
	want := "\nNotice: Only variables should be assigned by reference in php on line 4\na = 1, b = 2\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}
