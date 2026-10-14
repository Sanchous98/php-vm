package php

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// TestLangAgentAllFails печатает полный список провалов PHPT_DIR (по умолчанию
// tests/lang), без усечения samples до 60. Диагностический инструмент.
func TestLangAgentAllFails(t *testing.T) {
	root := os.Getenv("PHPT_DIR")
	if root == "" {
		t.Skip("PHPT_DIR не задан")
	}
	var files []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".phpt") {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	pass, fail, skip := 0, 0, 0
	n := 0
	for _, f := range files {
		data, _ := os.ReadFile(f)
		p := parsePhpt(string(data))
		if p.HasSkipIf || p.HasExpectRe || p.HasFileExt || p.File == "" || (!p.HasExpect && !p.HasExpectf) {
			skip++
			continue
		}
		got, err := runPhptAt(p, f)
		if tt := throwableOf(err); tt != nil {
			err = nil
		}
		ok := err == nil
		if ok {
			g := stripStartup(strings.TrimSpace(strings.ReplaceAll(got, "\r\n", "\n")))
			if p.HasExpect {
				ok = g == stripStartup(strings.TrimSpace(p.Expect))
			}
			if ok && p.HasExpectf {
				ok = expectfMatch(stripStartup(strings.TrimSpace(p.Expectf)), g)
			}
		}
		if ok {
			pass++
			continue
		}
		fail++
		n++
		why := "mismatch"
		if err != nil {
			why = "run: " + err.Error()
		}
		fmt.Printf("FAIL %s :: %s\n", filepath.Base(f), clip(why))
	}
	fmt.Printf("ИТОГО: %d / %d, пропущено %d\n", pass, pass+fail, skip)
	_ = strconv.Itoa
}

// TestAgentMatch проверяет базовую семантику выражения match.
func TestAgentMatch(t *testing.T) {
	src := `<?php
function f($x) { return match($x) { 1, 2 => 'a', '1' => 'b', default => 'c' }; }
echo f(1), f(2), f('1'), f(3), "\n";
echo match(true) { $q = 1 => 'x', default => 'y' }, "\n";
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "aabc\ny\n"; got != want {
		t.Fatalf("match: got %q want %q", got, want)
	}
}

// TestAgentMatchUnhandled проверяет UnhandledMatchError и его сообщение.
func TestAgentMatchUnhandled(t *testing.T) {
	src := `<?php
class Beep {}
function t($v) { try { match($v) {}; } catch (UnhandledMatchError $e) { echo $e->getMessage(), "\n"; } }
t(null); t(1); t(5.0); t("foo"); t(true); t([1]); t(new Beep());
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "Unhandled match case NULL\n" +
		"Unhandled match case 1\n" +
		"Unhandled match case 5.0\n" +
		"Unhandled match case 'foo'\n" +
		"Unhandled match case true\n" +
		"Unhandled match case of type array\n" +
		"Unhandled match case of type Beep\n"
	if got != want {
		t.Fatalf("unhandled match: got %q want %q", got, want)
	}
}

// TestAgentArrayUnpack проверяет распаковку массивов.
func TestAgentArrayUnpack(t *testing.T) {
	src := `<?php
$a = [1, 2, "k" => 3];
$b = [4, 5];
var_dump([...$a, ...$b]);
var_dump(["x" => 0, ...["x" => 9]]);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, `int(3)`) || !strings.Contains(got, `int(5)`) || !strings.Contains(got, `["x"]=>`) {
		t.Fatalf("array unpack: unexpected output %q", got)
	}
}

// TestAgentArgUnpack проверяет распаковку аргументов вызова.
func TestAgentArgUnpack(t *testing.T) {
	src := `<?php
function f(...$args) { var_dump($args); }
$f = function(...$args) { var_dump($args); };
f(...[1, 2, 3, 4]);
$f(...[5, 6]);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Count(got, "int(") != 6 {
		t.Fatalf("arg unpack: unexpected output %q", got)
	}
}

// TestAgentVariadic проверяет вариативные функции и замыкания.
func TestAgentVariadic(t *testing.T) {
	src := `<?php
function f($a, ...$rest) { var_dump($a, $rest); }
f(1);
f(1, 2, 3);
$g = fn(...$x) => $x;
var_dump($g(7, 8));
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, `array(0)`) || !strings.Contains(got, `int(3)`) {
		t.Fatalf("variadic: unexpected output %q", got)
	}
}

// TestAgentNullsafe проверяет базовое короткое замыкание `?->`.
func TestAgentNullsafe(t *testing.T) {
	src := `<?php
class Foo { public $bar = 'bar'; function qux() { return 'qux'; } }
$n = null;
$f = new Foo();
var_dump($n?->bar);
var_dump($n?->qux());
var_dump($n?->bar?->baz);
var_dump($f?->bar);
var_dump($f?->qux());
$f->bar = null;
var_dump($f?->bar?->baz);
`
	got, err := runFile(src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "NULL\nNULL\nNULL\nstring(3) \"bar\"\nstring(3) \"qux\"\nNULL\n"
	if got != want {
		t.Fatalf("nullsafe: got %q want %q", got, want)
	}
}
