package php

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// runVsPHP выполняет исходник нашей VM и живым php, сверяя вывод. Если php
// считает программу некорректной — тест пропускается.
func runVsPHP(t *testing.T, src string) {
	t.Helper()
	phppath, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not found in PATH")
	}
	code := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(src), "<?php"))
	// Окружение как в run-tests.php: display_errors/E_ALL (иначе php CLI
	// с display_errors=Off не печатает предупреждения в stdout).
	want, werr := exec.Command(phppath,
		"-d", "display_errors=1", "-d", "error_reporting=32767",
		"-d", "html_errors=0", "-r", code).Output()
	if werr != nil {
		t.Skipf("php error on %q: %v", src, werr)
	}
	got, err := runCompiled(t, src)
	if err != nil {
		t.Errorf("%q: run error: %v", src, err)
		return
	}
	// php -r называет файл "Command line code", наша VM — "php".
	wantS := strings.ReplaceAll(string(want), "Command line code", "php")
	if got != wantS {
		t.Errorf("%q:\n  vm  = %q\n  php = %q", src, got, wantS)
	}
}

// TestSpaceshipVsPHP — оператор <=>.
func TestSpaceshipVsPHP(t *testing.T) {
	cases := []string{
		`<?php var_dump(1<=>2, 2<=>1, 1<=>1);`,
		`<?php var_dump("a"<=>"b", "b"<=>"a", "a"<=>"a");`,
		`<?php var_dump(1.5<=>1, 1<=>1.5, 1.5<=>1.5);`,
		`<?php var_dump(1<=>2.5, 2.5<=>1);`,
		`<?php $a=3; $b=7; echo $a<=>$b; echo $b<=>$a; echo $a<=>$a;`,
		`<?php echo (1<=>2) + (2<=>1) + (1<=>1);`,
		`<?php var_dump("10"<=>9, "abc"<=>0);`,
		`<?php var_dump(NAN<=>1, 1<=>NAN, NAN<=>NAN);`,
		`<?php function cmp($a,$b){ return $a<=>$b; } echo cmp(2,5); echo cmp(5,2); echo cmp(5,5);`,
	}
	for _, src := range cases {
		runVsPHP(t, src)
	}
}

// TestDeclareVsPHP — declare(...) в вариантах.
func TestDeclareVsPHP(t *testing.T) {
	cases := []string{
		`<?php declare(strict_types=1); echo "ok";`,
		`<?php declare(ticks=1); echo "t";`,
		`<?php declare(ticks=1) { echo "b"; }`,
		`<?php declare(ticks=1) { echo 1; echo 2; } echo 3;`,
		`<?php declare(encoding='UTF-8'); echo "e";`,
		`<?php declare(ticks=1): echo "a"; echo "b"; enddeclare; echo "c";`,
		`<?php declare(strict_types=1); function f($x){ return $x*2; } echo f(21);`,
	}
	for _, src := range cases {
		runVsPHP(t, src)
	}
}

// TestGotoVsPHP — goto/метки.
func TestGotoVsPHP(t *testing.T) {
	cases := []string{
		`<?php goto end; echo "skip"; end: echo "done";`,
		`<?php $i=0; start: echo $i; $i++; if($i<3){ goto start; } echo "x";`,
		`<?php goto a; echo "-"; a: echo "A"; goto b; echo "-"; b: echo "B";`,
		`<?php function f(){ goto skip; echo "no"; skip: return "yes"; } echo f();`,
		`<?php $n=0; loop: $n++; if($n<5){ goto loop; } echo $n;`,
		`<?php if(true){ goto out; } echo "no"; out: echo "yes";`,
	}
	for _, src := range cases {
		runVsPHP(t, src)
	}
}

// TestParserConstructsVsPHP — кейсы, ранее дававшие ошибки парсера:
// бинарные строки, именованные аргументы, promoted-свойства, деструктуризация
// в foreach, анонимные классы.
func TestParserConstructsVsPHP(t *testing.T) {
	cases := []string{
		`<?php var_dump(b"hello", b'world');`,
		`<?php $x=5; var_dump(b"v=$x");`,
		`<?php function f($a, $b=10, $c=20){ return $a+$b+$c; } echo f(1, c: 3);`,
		`<?php function f($a=0, $b=0){ return $a.$b; } echo f(b: "B", a: "A");`,
		`<?php class P { public function __construct(public int $x, private string $s) {} public function get(){ return $this->x.$this->s; } } $p = new P(2, "z"); echo $p->get();`,
		`<?php $a = [[1,2],[3,4]]; foreach($a as [$x,$y]){ echo $x+$y; }`,
		`<?php $a = ["p"=>[1,2],"q"=>[3,4]]; foreach($a as $k => [$x,$y]){ echo $k.$x.$y; }`,
		`<?php $o = new class { public function hi(){ return "hi"; } }; echo $o->hi();`,
		`<?php $o = new class(3) { public $v; public function __construct($v){ $this->v=$v; } }; echo $o->v;`,
		`<?php class Base { public function up(){ return "up"; } } $o = new class extends Base { public $v=9; }; echo $o->up(); echo $o->v;`,
		`<?php $o = new class(2) { public function __construct(public int $n){} }; echo $o->n;`,
		`<?php $a = [1,2,3]; $b = [0]; foreach($a as $b[0]){ echo $b[0]; }`,
		`<?php $obj = new stdClass; $a = [1,2]; foreach($a as $obj->v){ echo $obj->v; }`,
		`<?php echo 1, 2, 3;`,
		`<?php print "x"; print "y";`,
		`<?php @$undefined; echo "ok";`,
		`<?php $x = @$undefined + 1; echo $x;`,
		`<?php $a=1; if ($a): echo "t"; else: echo "f"; endif;`,
		`<?php $a=0; if ($a): echo "t"; elseif ($a===0): echo "z"; else: echo "f"; endif;`,
		`<?php $a=0; while ($a<3): echo $a; $a++; endwhile;`,
		`<?php $a=0; for(;$a<2;): echo $a; $a++; endfor;`,
	}
	for _, src := range cases {
		runVsPHP(t, src)
	}
}

// TestExitVsPHP — exit/die с аргументом и без.
func TestExitVsPHP(t *testing.T) {
	cases := []string{
		`<?php echo "a"; exit("bye"); echo "z";`,
		`<?php echo "a"; die("bye"); echo "z";`,
		`<?php echo "a"; exit; echo "z";`,
		`<?php echo "a"; exit(); echo "z";`,
		`<?php echo "a"; exit(0); echo "z";`,
		`<?php echo "a"; exit(3+4); echo "z";`,
		`<?php function f(){ exit("F"); } echo "a"; f(); echo "z";`,
		`<?php try { exit("x"); } catch (Exception $e) { echo "caught"; } echo "z";`,
	}
	for _, src := range cases {
		runVsPHP(t, src)
	}
}

// TestZendSuiteChunks — тот же прогон, что TestPHPTree, но по срезу файлов
// (PHPT_START/PHPT_COUNT), чтобы большие каталоги можно было прогнать частями.
func TestZendSuiteChunks(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	start, _ := strconv.Atoi(os.Getenv("PHPT_START"))
	count, _ := strconv.Atoi(os.Getenv("PHPT_COUNT"))
	var files []string
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	end := start + count
	if count == 0 || end > len(files) {
		end = len(files)
	}
	if start > len(files) {
		start = len(files)
	}
	chunk := files[start:end]
	pass, fail, skip := 0, 0, 0
	for _, f := range chunk {
		data, _ := os.ReadFile(f)
		p := parsePhpt(string(data))
		if p.HasSkipIf || p.HasExpectRe || p.HasFileExt || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			skip++
			continue
		}
		got, err := runPhptAt(p, f)
		if throwableOf(err) != nil {
			err = nil
		}
		ok := err == nil
		if ok {
			g := strings.TrimSpace(got)
			if p.HasExpect {
				ok = g == strings.TrimSpace(p.Expect)
			}
			if ok && p.HasExpectf {
				ok = expectfToRegex(strings.TrimSpace(p.Expectf)).MatchString(g)
			}
		}
		if ok {
			pass++
		} else {
			fail++
		}
	}
	t.Logf("CHUNK [%d:%d] ИТОГО: %d / %d, пропущено %d", start, end, pass, pass+fail, skip)
}

// TestScanParseErrors — диагностический прогон: ищет .phpt, которые не
// парсятся, и печатает ошибки. Запускается только при заданном PHPT_DIR.
// PHPT_GREP фильтрует подстроку ошибки.
func TestScanParseErrors(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	pattern := os.Getenv("PHPT_GREP")
	var n int
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".phpt") {
			return nil
		}
		data, _ := os.ReadFile(p)
		ph := parsePhpt(string(data))
		if ph.File == "" {
			return nil
		}
		if _, e := ParseMixed(ph.File); e != nil {
			if pattern == "" || strings.Contains(e.Error(), pattern) {
				t.Logf("%s: %v", p, e)
				n++
			}
		}
		return nil
	})
	t.Logf("parse-error count: %d", n)
}
