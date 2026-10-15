package php

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// root_tail_a_agent_test.go — агент «root Zend tail, первая половина».
//
// Хелперы (запускаются только с переменными окружения):
//   PHPT_FILE=<файл>  TestRootTailAOne   — полный got/want + diff с живым php.
//   PHPT_DIR + PHPT_LIST + ROOT_TAIL_OUT TestRootTailABatch — только провалы
//   из списка, с диффами (и diff с живым php при ROOT_TAIL_PHP=1).
//
// Регрессионные проверки — TestRootTailARegress* ниже (ожидания сверены с php 8.3).

// TestRootTailAOne прогоняет один .phpt и печатает полный got/want и отличия от
// вывода живого php.
func TestRootTailAOne(t *testing.T) {
	f := os.Getenv("PHPT_FILE")
	if f == "" {
		t.Skip("PHPT_FILE не задан")
	}
	data, err := os.ReadFile(f)
	if err != nil {
		t.Fatal(err)
	}
	p := parsePhpt(string(data))
	if p.File == "" {
		t.Fatal("нет FILE section")
	}
	got, rerr := runPhptAt(p, f)
	g := stripStartup(strings.TrimSpace(got))
	want := p.Expect
	if p.HasExpectf {
		want = p.Expectf
	}
	want = stripStartup(strings.TrimSpace(want))
	fmt.Printf("\n===== FILE: %s =====\n", f)
	if rerr != nil {
		if th := throwableOf(rerr); th == nil {
			fmt.Printf("RUN ERROR: %v\n", rerr)
		} else {
			fmt.Printf("THROW: %s\n", th.Message)
		}
	}
	fmt.Printf("----- GOT -----\n%s\n----- WANT -----\n%s\n", firstDiff(g, want), firstDiff(want, g))
	if real, rerr := runRealPHP(p); rerr == nil {
		fmt.Printf("----- vsPHP -----\n%s\n", firstDiff(strings.TrimSpace(real), g))
	} else {
		fmt.Printf("----- vsPHP: ошибка: %v -----\n", rerr)
	}
	fmt.Printf("----- END -----\n")
}

// TestRootTailABatch прогоняет файлы из PHPT_LIST и пишет полные диффы провалов.
func TestRootTailABatch(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	lp := os.Getenv("PHPT_LIST")
	if root == "" || lp == "" {
		t.Skip("PHPT_DIR/PHPT_LIST не заданы")
	}
	outPath := os.Getenv("ROOT_TAIL_OUT")
	if outPath == "" {
		outPath = filepath.Join(t.TempDir(), "roottail.txt")
	}
	data, err := os.ReadFile(lp)
	if err != nil {
		t.Fatal(err)
	}
	var pats []string
	for _, ln := range strings.Split(string(data), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			pats = append(pats, ln)
		}
	}
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, e error) error {
		if e == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			for _, pat := range pats {
				if strings.Contains(p, pat) {
					files = append(files, p)
					break
				}
			}
		}
		return nil
	})
	sort.Strings(files)
	fh, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	w := bufio.NewWriter(fh)
	defer w.Flush()
	pass, fail, skip := 0, 0, 0
	for _, f := range files {
		data, _ := os.ReadFile(f)
		p := parsePhpt(string(data))
		if p.HasSkipIf || p.HasExpectRe || p.HasFileExt || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			skip++
			continue
		}
		if hasUnsupportedExt(p.Extensions) {
			skip++
			continue
		}
		got, rerr := runPhptAt(p, f)
		g := stripStartup(strings.TrimSpace(got))
		ok := rerr == nil
		if th := throwableOf(rerr); th != nil {
			ok = true
		}
		want := p.Expect
		if p.HasExpectf {
			want = p.Expectf
		}
		want = stripStartup(strings.TrimSpace(want))
		if ok {
			if p.HasExpect {
				ok = g == want
			}
			if ok && p.HasExpectf {
				ok = expectfToRegex(want).MatchString(g)
			}
		}
		if ok {
			pass++
			continue
		}
		fail++
		fmt.Fprintf(w, "\n===== FAIL %s =====\n", f)
		if rerr != nil {
			if th := throwableOf(rerr); th != nil {
				fmt.Fprintf(w, "THROW: %s\n", th.Message)
			} else {
				fmt.Fprintf(w, "RUNERR: %v\n", rerr)
			}
		}
		fmt.Fprintf(w, "--- GOT ---\n%s\n--- WANT ---\n%s\n", firstDiff(g, want), firstDiff(want, g))
		if os.Getenv("ROOT_TAIL_PHP") == "1" {
			if real, rerr := runRealPHP(p); rerr == nil {
				fmt.Fprintf(w, "--- vsPHP ---\n%s\n", firstDiff(strings.TrimSpace(real), g))
			}
		}
	}
	fmt.Fprintf(w, "\nTOTAL pass=%d fail=%d skip=%d\n", pass, fail, skip)
	t.Logf("roottail pass=%d fail=%d skip=%d out=%s", pass, fail, skip, outPath)
}

// ---------------------------------------------------------------------------
// Регрессии агента «root Zend tail, первая половина» (ожидания сверены с php 8.3).

func tailExact(t *testing.T, name, src, want string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	if got != want {
		t.Errorf("%s (err=%v):\n got=%q\nwant=%q", name, err, got, want)
	}
}

func tailHas(t *testing.T, name, src string, subs ...string) {
	t.Helper()
	got, err := runOurPHP(t, src)
	for _, sub := range subs {
		if !strings.Contains(got, sub) {
			t.Errorf("%s (err=%v): вывод %q не содержит %q", name, err, got, sub)
		}
	}
}

func TestRootTailARegressCompileDiags(t *testing.T) {
	// Операнд break/continue проверяется на компиляции (Zend).
	tailHas(t, "break_zero", `<?php function f(){ break 0; }`,
		"'break' operator accepts only positive integers")
	tailHas(t, "break_var", `<?php function f(){ break $x; }`,
		"'break' operator with non-integer operand is no longer supported")
	tailHas(t, "continue_zero", `<?php function f(){ continue 0; }`,
		"'continue' operator accepts only positive integers")
	tailHas(t, "continue_minus", `<?php function f(){ continue -1; }`,
		"'continue' operator with non-integer operand is no longer supported")
	tailHas(t, "continue_str_dash", `<?php while(0){ break -1; }`,
		"'break' operator with non-integer operand is no longer supported")
	// `break -1`/`+2` — не литерал (унарная операция) → non-integer; `"2"` и
	// `2.0` — литералы не-int → "positive integers".
	tailHas(t, "break_float", `<?php while(0){ break 2.0; }`,
		"'break' operator accepts only positive integers")
	tailHas(t, "break_str", `<?php while(0){ break "2"; }`,
		"'break' operator accepts only positive integers")
	// Фигурные скобки у offset'а — ошибка компиляции (PHP 8).
	tailHas(t, "curly_offset", `<?php $a = 'xy'; echo $a{0};`,
		"Array and string offset access syntax with curly braces is no longer supported")
	tailHas(t, "curly_offset_const", `<?php const FOO = "BAR"{0};`,
		"Array and string offset access syntax with curly braces is no longer supported")
	tailHas(t, "curly_offset_interp", `<?php "{$g{'h'}}";`,
		"Array and string offset access syntax with curly braces is no longer supported")
	// catch-переменная не может быть $this.
	tailHas(t, "catch_this", `<?php try { throw new Exception(); } catch (Exception $this) {}`,
		"Cannot re-assign $this")
	// Литеральное не-строковое имя метода/класса.
	tailHas(t, "method_name_lit", `<?php class C{} (new C)->{0}();`,
		"Method name must be a string")
	tailHas(t, "static_method_name_lit", `<?php class C{} C::{0}();`,
		"Method name must be a string")
	tailHas(t, "illegal_class_name", `<?php echo (2)::class;`,
		"Illegal class name")
	tailHas(t, "illegal_class_name_ref", `<?php class C{} (-0)::${"prop"};`,
		"Illegal class name")
	// Класс не может реализовать Iterator и IteratorAggregate одновременно.
	tailHas(t, "both_iterators", `<?php abstract class A implements Iterator, IteratorAggregate {}`,
		"cannot implement both Iterator and IteratorAggregate at the same time")
	// Ошибка alias в трейте при методе, определённом в двух трейтах.
	tailHas(t, "trait_alias_ambig", `<?php
trait T1 { public function func() {} }
trait T2 { public function func() {} }
class Bar { use T1, T2 { func as f1; } }`,
		"An alias was defined for method func(), which exists in both T1 and T2")
	// use несоставного имени в глобальном пространстве имён — Warning.
	tailHas(t, "use_no_effect", `<?php use Exception;`,
		"The use statement with non-compound name 'Exception' has no effect")
}

func TestRootTailARegressRuntime(t *testing.T) {
	// define(array) — TypeError (не приведение к "Array").
	tailExact(t, "define_array",
		`<?php try { define([1], 1); } catch (TypeError $e) { echo $e->getMessage(); }`,
		"define(): Argument #1 ($constant_name) must be of type string, array given")
	// get_class() без аргумента вне класса — Error без deprecation.
	tailExact(t, "get_class_no_scope",
		`<?php try { get_class(); } catch (Error $e) { echo $e->getMessage(); }`,
		"get_class() without arguments must be called from within a class")
	// Статический вызов с динамическим именем через $$var.
	tailExact(t, "static_dyn_dollar",
		`<?php class C { static function a(){ echo "ok"; } } $b = 'a'; C::$$b();`,
		"ok")
	// `->$$var` — имя члена по значению ${$var}.
	tailExact(t, "member_dyn_dollar",
		`<?php class C { public $y = 7; } $c = 'y'; $d = 'c'; $o = new C; var_dump($o->$$d);`,
		"int(7)\n")
	// Объект как имя класса в `new $obj` даёт класс объекта (не Error).
	tailExact(t, "new_object_class",
		`<?php $x = new stdClass; echo get_class(new $x), "\n";`,
		"stdClass\n")
	tailExact(t, "new_scalar_class",
		`<?php $x = 5; try { new $x; } catch (Error $e) { echo $e->getMessage(); }`,
		"Class name must be a valid object or a string")
	// (int)/(float) объекта: Warning + 1; (float)/(string) массива.
	tailHas(t, "cast_int_obj",
		`<?php class T {} var_dump((int)new T);`,
		"Object of class T could not be converted to int", "int(1)")
	tailHas(t, "cast_float_obj",
		`<?php class T {} var_dump((float)new T);`,
		"Object of class T could not be converted to float", "float(1)")
	tailExact(t, "cast_float_array", `<?php var_dump((float)[1,2]); var_dump((float)[]);`,
		"float(1)\nfloat(0)\n")
	tailHas(t, "cast_string_array", `<?php var_dump((string)[1]);`,
		"Array to string conversion", "string(5) \"Array\"")
	// `f($arr[])` для by-ref параметра: элемент создаётся и передаётся ссылкой.
	tailExact(t, "append_byref_arg",
		`<?php function t(&$v){ $v = 5; } t($arr[]); var_dump($arr);`,
		"array(1) {\n  [0]=>\n  int(5)\n}\n")
	// Ошибка LHS `$arr[] =&` имеет приоритет над Notice о RHS.
	tailExact(t, "append_ref_lhs_error",
		`<?php function val(){ return 42; } $var = 24; $arr = [PHP_INT_MAX => "foo"];
try { var_dump($arr[] =& val()); } catch (Error $e) { echo $e->getMessage(), "\n"; }
var_dump(count($arr));`,
		"Cannot add element to the array as the next element is already occupied\nint(1)\n")
	// Свойство-ссылка при чтении фиксируется в момент чтения.
	tailExact(t, "ref_read_freeze",
		`<?php $x = new stdClass; $x->a = 1; $r =& $x->a; unset($r);
var_dump($x->a + ($x->a = 2));`,
		"int(3)\n")
	// Замыкание, привязанное к объекту без scope: кадр Closure->{closure}().
	tailHas(t, "closure_trace_class",
		`<?php class B { private $x = 1; }
$f = function(){ return $this->x; };
$f = Closure::bind($f, new B, null);
try { $f(); } catch (Error $e) { echo $e->getTraceAsString(); }`,
		"Closure->{closure}()")
	// `[&$this]` не создаёт ссылку: элемент передаётся по значению.
	tailHas(t, "this_ref_array",
		`<?php function ref(&$r) { var_dump($r); }
new class { function __construct() { $args = [&$this]; call_user_func_array('ref', $args); } };`,
		"ref(): Argument #1 ($r) must be passed by reference, value given")
}
