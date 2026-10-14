package php

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// runTDPhpt запускает один .phpt из Zend/tests/type_declarations и возвращает
// нормализованный вывод и результат сравнения с EXPECT/EXPECTF.
func runTDPhpt(t *testing.T, name string) (got string, ok bool) {
	t.Helper()
	f := filepath.Join("/home/dev/phpsrc/Zend/tests/type_declarations", name)
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatalf("read %s: %v", f, err)
	}
	p := parsePhpt(string(data))
	out, rerr := runPhptAt(p, f)
	if throwableOf(rerr) != nil {
		rerr = nil
	}
	if rerr != nil {
		t.Logf("%s: run error: %v", name, rerr)
		return out, false
	}
	g := stripStartup(strings.TrimSpace(out))
	if p.HasExpect {
		return g, g == stripStartup(strings.TrimSpace(p.Expect))
	}
	re := expectfToRegex(stripStartup(strings.TrimSpace(p.Expectf)))
	return g, re.MatchString(g)
}

// TestTypeDeclPhptRegression фиксирует набор .phpt из type_declarations,
// починенных в ходе работы над типизированными свойствами/константами.
func TestTypeDeclPhptRegression(t *testing.T) {
	names := []string{
		"typed_properties_006.phpt",
		"typed_properties_007.phpt",
		"typed_properties_008.phpt",
		"typed_properties_013.phpt",
		"typed_properties_014.phpt",
		"typed_properties_017.phpt",
		"typed_properties_026.phpt",
		"typed_properties_030.phpt",
		"typed_properties_035.phpt",
		"typed_properties_040.phpt",
		"typed_properties_047.phpt",
		"typed_properties_050.phpt",
		"typed_properties_051.phpt",
		"typed_properties_052.phpt",
		"typed_properties_056.phpt",
		"typed_properties_058.phpt",
		"typed_properties_069.phpt",
		"typed_properties_072.phpt",
		"typed_properties_080.phpt",
		"typed_properties_085.phpt",
		"typed_properties_100.phpt",
		"typed_class_constants_inheritance_error3.phpt",
		"typed_class_constants_inheritance_error4.phpt",
		"typed_class_constants_inheritance_error5.phpt",
		"scalar_reserved7.phpt",
		"return_separation.phpt",
		"nullable_typed_return_without_value.phpt",
	}
	for _, n := range names {
		if _, err := os.Stat(filepath.Join("/home/dev/phpsrc/Zend/tests/type_declarations", n)); err != nil {
			continue
		}
		got, ok := runTDPhpt(t, n)
		if !ok {
			t.Errorf("%s: mismatch got=%q", n, got)
		}
	}
}

// TestArithNumericString проверяет PHP-семантику арифметики с числовыми
// строками: целочисленный numeric-префикс даёт int, иначе float; null -> int 0.
func TestArithNumericString(t *testing.T) {
	got, err := runFile(`<?php
var_dump("100" + 5);
var_dump("100.5" + 5);
var_dump("1e2" + 5);
var_dump(null + 5);
var_dump("12abc" + 5);
var_dump("2" + "3");`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	// PHP 8.3: ведуще-числовая строка ("12abc") даёт число, но с Warning
	// "A non-numeric value encountered".
	want := "int(105)\nfloat(105.5)\nfloat(105)\nint(5)\n\nWarning: A non-numeric value encountered in php on line 6\nint(17)\nint(5)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestInterpSingleExprString проверяет, что интерполяция из одной переменной
// без литералов даёт строку ("$x" -> string), а не исходное значение.
func TestInterpSingleExprString(t *testing.T) {
	got, err := runFile(`<?php
$b = 1; $a = "$b"; var_dump($a);
$c = "$a"; var_dump($c);`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "string(1) \"1\"\nstring(1) \"1\"\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestUnsetRefDetaches проверяет, что unset ссылочной переменной разрывает
// alias и не обнуляет исходное значение.
func TestUnsetRefDetaches(t *testing.T) {
	got, err := runFile(`<?php
function f(){ $a = []; $b = &$a; unset($b); var_dump($a); } f();
function g(){ $x = 42; $y = &$x; unset($y); var_dump($x); } g();`)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "array(0) {\n}\nint(42)\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestDiagTD — отладочный прогон одного файла (TD_NAME=...).
func TestDiagTD(t *testing.T) {
	name := os.Getenv("TD_NAME")
	if name == "" {
		t.Skip("TD_NAME не задан")
	}
	f := filepath.Join("/home/dev/phpsrc/Zend/tests/type_declarations", name)
	data, _ := os.ReadFile(f)
	p := parsePhpt(string(data))
	want := p.Expect
	if p.HasExpectf {
		want = p.Expectf
	}
	got, ok := runTDPhpt(t, name)
	t.Logf("=== %s ok=%v\nGOT : %q\nWANT: %q", name, ok, got, want)
}
