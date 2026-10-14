package php

import (
	"os/exec"
	"strings"
	"testing"
)

// TestFmtVsPHP — дифференциальные кейсы для var_dump / print_r / var_export
// (в т.ч. вложенные массивы) и float-формата. Сравнение побайтово с живым php 8.3.
func TestFmtVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		// --- var_dump: вложенность 2-3 уровня, смешанные ключи ---
		`<?php $a=[0=>1, 3=>["foo"=>"orange"]]; var_dump($a);`,
		`<?php $b=[1,[2,[3,["x"=>4]]],"s"=>[true,null,1.5]]; var_dump($b);`,
		`<?php var_dump([]);`,
		`<?php var_dump([[]]);`,
		`<?php var_dump(["a"=>[], "b"=>[1=>[]]]);`,
		`<?php var_dump(["a\"b"=>1]);`,
		`<?php var_dump(null, true, false, 1.5, "hi");`,
		`<?php var_dump(1.0, -0.0, 0.1, 1e20, 1e-5, 1e17);`,
		`<?php var_dump(INF, NAN);`,

		// --- print_r: вложенность, пустые массивы, blank line ---
		`<?php print_r([0=>1, 3=>["foo"=>"orange"]]);`,
		`<?php print_r([1,[2,[3,["x"=>4]]],"s"=>[true,null,1.5]]);`,
		`<?php print_r([1,2,3]);`,
		`<?php print_r([]);`,
		`<?php print_r(["a"=>[], "b"=>[1=>[]]]);`,
		`<?php $s = print_r([1,2], true); echo "[" . $s . "]";`,

		// --- var_export: вложенность, экранирование, NUL ---
		`<?php var_export([0=>1, 3=>["foo"=>"orange"]]);`,
		`<?php var_export([1,[2,[3,["x"=>4]]],"s"=>[true,null,1.5]]);`,
		`<?php var_export([]);`,
		`<?php var_export([[[]]]);`,
		`<?php var_export("a'b\\c");`,
		`<?php var_export(["a'b\\c"=>1]);`,
		// NUL строим через chr(0): VM пока не разворачивает "\0" в лексере.
		`<?php var_export("a" . chr(0) . "b");`,
		`<?php var_export([1.0, -0.0, INF, NAN, 1e20]);`,
	}
	for _, src := range cases {
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		want, werr := exec.Command(phppath, "-r", code).Output()
		if werr != nil {
			continue
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

// TestArrayFnsVsPHP — дифференциальные кейсы для чистых array-функций.
func TestArrayFnsVsPHP(t *testing.T) {
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	cases := []string{
		`<?php var_dump(array_change_key_case(["a"=>1,"B"=>2,"C"=>3], CASE_UPPER));`,
		`<?php var_dump(array_change_key_case([1=>"x","A"=>"y"], CASE_LOWER));`,
		`<?php var_dump(array_change_key_case(["a"=>1,"A"=>2]));`,
		`<?php var_dump(array_replace_recursive(["a"=>[1,2],"b"=>3], ["a"=>[9],"c"=>4]));`,
		`<?php var_dump(array_replace_recursive(["a"=>["x"=>1]], ["a"=>["y"=>2]]));`,
		`<?php var_dump(array_replace_recursive(["a"=>[1,2]], ["a"=>[9,8,7]]));`,
		`<?php $a=["a"=>[1]]; $b=array_replace_recursive($a, ["a"=>[9]]); var_dump($a);`,
		`<?php var_dump(array_count_values([1,1,"1","a",2]));`,
		`<?php var_dump(array_flip(["a"=>1,"b"=>"x"]));`,
		`<?php var_dump(array_key_first([]), array_key_last([]));`,
		`<?php var_dump(array_key_first(["a"=>1,"b"=>2]), array_key_last(["a"=>1,"b"=>2]));`,
		`<?php var_dump(array_fill_keys(["a","b"], 7));`,
		`<?php var_dump(array_fill_keys([null, true, false, 1.9, "1", "01"], 5));`,
		`<?php var_dump(array_combine([1.9, 2.0, "1"], ["a","b","c"]));`,
		`<?php var_dump(array_column([["id"=>1.9,"n"=>"a"],["id"=>2.0,"n"=>"b"]], "n", "id"));`,
		`<?php var_dump(array_key_exists("1", [1=>"x"]), array_key_exists(null, [""=>1]));`,
		`<?php var_dump(array_column([["id"=>1,"n"=>"a"],["id"=>2,"n"=>"b"]], "n", "id"));`,
		`<?php var_dump(array_pad([1,2], 5, 0));`,
		`<?php var_dump(array_unique([1,1,2,3]));`,
	}
	for _, src := range cases {
		code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
		// Сверяем с E_ALL: phpt-раннер выполняет тесты с полным выводом
		// диагностик, поэтому и здесь нужен тот же уровень.
		want, werr := exec.Command(phppath, "-d", "error_reporting=E_ALL", "-r", code).Output()
		if werr != nil {
			continue
		}
		got, err := runCompiled(t, src)
		if err != nil {
			t.Errorf("%q:\n  run error: %v", src, err)
			continue
		}
		// php печатает диагностики в stderr, поэтому их исключаем из
		// сравнения (Output() возвращает только stdout).
		if stripDiagLines(got) != string(want) {
			t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, string(want))
		}
	}
}

// stripDiagLines убирает строки диагностик (Warning/Notice/Deprecated), чтобы
// дифференциальное сравнение с php stdout не зависело от уровня диагностик.
func stripDiagLines(s string) string {
	var out []string
	removed := false
	for _, ln := range strings.Split(s, "\n") {
		if strings.HasPrefix(ln, "Warning: ") || strings.HasPrefix(ln, "Notice: ") ||
			strings.HasPrefix(ln, "Deprecated: ") || strings.HasPrefix(ln, "Fatal error: ") {
			removed = true
			continue
		}
		out = append(out, ln)
	}
	res := strings.Join(out, "\n")
	if removed {
		res = strings.TrimLeft(res, "\n")
	}
	return res
}
