package php

import "testing"

// TestBuiltinStrictArgsGuard — strict_types=1 распространяется на аргументы
// ВСТРОЕННЫХ функций (Zend ZPP): несовпадение типа — TypeError без коэрции
// (кроме расширения int->float). Тексты сверены с php 8.3 (cli):
// имя параметра печатается для фиксированных параметров и опускается для
// вариативного хвоста. Тест защищает ветку c2_strict от регрессий при мержах.
func TestBuiltinStrictArgsGuard(t *testing.T) {
	src := `<?php
declare(strict_types=1);
$probe = function(string $label, callable $f) {
    try { $f(); echo "$label: no error\n"; }
    catch (Throwable $e) { echo "$label: ".get_class($e).": ".$e->getMessage()."\n"; }
};
$probe('array_merge', fn() => array_merge([1], 2));
$probe('array_slice_float', fn() => array_slice([1,2,3], 0, 2.0));
$probe('sprintf_var', fn() => sprintf('%d', 1, 2));
$probe('str_repeat_float', fn() => str_repeat('a', 2.0));
$probe('strlen_int', fn() => strlen(5));
$probe('array_fill_float', fn() => array_fill(0, 2.0, 'x'));
$probe('strpos_float', fn() => strpos('abc', 'b', 1.0));
$probe('trim_float', fn() => trim('a', 1.0));
$probe('array_key_exists_int', fn() => array_key_exists(1, [1=>2]));
$probe('substr_float', fn() => substr('abc', 1.0));
$probe('str_contains_int', fn() => str_contains(123, 'b'));
$probe('json_encode_ok', fn() => json_encode([1,2]));
`
	want := `array_merge: TypeError: array_merge(): Argument #2 must be of type array, int given
array_slice_float: TypeError: array_slice(): Argument #3 ($length) must be of type ?int, float given
sprintf_var: no error
str_repeat_float: TypeError: str_repeat(): Argument #2 ($times) must be of type int, float given
strlen_int: TypeError: strlen(): Argument #1 ($string) must be of type string, int given
array_fill_float: TypeError: array_fill(): Argument #2 ($count) must be of type int, float given
strpos_float: TypeError: strpos(): Argument #3 ($offset) must be of type int, float given
trim_float: TypeError: trim(): Argument #2 ($characters) must be of type string, float given
array_key_exists_int: no error
substr_float: TypeError: substr(): Argument #2 ($offset) must be of type int, float given
str_contains_int: TypeError: str_contains(): Argument #1 ($haystack) must be of type string, int given
json_encode_ok: no error
`
	got, err := runFileAt(src, "strict_builtin.php")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != want {
		t.Fatalf("strict builtin args mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestBuiltinWeakArgsNoStrict — в слабом режиме те же вызовы не должны давать
// TypeError: аргументы коэрцятся (Zend ZPP non-strict). Защищает от того,
// чтобы strict-проверка не срабатывала вне strict_types=1.
func TestBuiltinWeakArgsNoStrict(t *testing.T) {
	src := `<?php
$a = array_slice([1,2,3], 0, 2.0);
var_dump($a);
var_dump(strlen(5));
var_dump(str_repeat('a', 2.0));
`
	want := "array(2) {\n  [0]=>\n  int(1)\n  [1]=>\n  int(2)\n}\nint(1)\nstring(2) \"aa\"\n"
	got, err := runFileAt(src, "weak_builtin.php")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got != want {
		t.Fatalf("weak builtin args mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
