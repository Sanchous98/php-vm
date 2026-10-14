package php

import (
	"strings"
	"testing"
)

// round12b_agent_test.go — регрессии раунда 12b: именованные аргументы в
// динамических вызовах/методах/статические, диагностики битовых операций,
// EmptyIterator, `${expr}`-интерполяция.

func TestRound12bNamedArgsCompile(t *testing.T) {
	src := `<?php
class T {
    function m($a = 'a', $b = 'b') { echo "m:$a,$b\n"; }
    static function sm($a = 'a', $b = 'b') { echo "sm:$a,$b\n"; }
}
T::sm(b: 'B');
T::sm(b: 'B', a: 'A');
$t = new T;
$t->m(b: 'B');
$t->m(b: 'B', a: 'A');`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "sm:a,B\nsm:A,B\nm:a,B\nm:A,B\n"
	if got != want {
		t.Errorf("named args: got %q, want %q", got, want)
	}
}

func TestRound12bNamedArgsCall(t *testing.T) {
	// __call/__callStatic получают именованный массив как есть.
	src := `<?php
class Test {
    public function __call(string $method, array $args) {
        $this->{'_'.$method}(...$args);
    }
    public static function __callStatic(string $method, array $args) {
        (new static)->{'_'.$method}(...$args);
    }
    private function _method($a = 'a', $b = 'b') { echo "a: $a, b: $b\n"; }
}
$test = new Test;
$test->method(a: 'A', b: 'B');
$test->method(b: 'B');
Test::method(a: 'A', b: 'B');
Test::method(b: 'B');`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := "a: A, b: B\na: a, b: B\na: A, b: B\na: a, b: B\n"
	if got != want {
		t.Errorf("__call named: got %q, want %q", got, want)
	}
}

func TestRound12bNamedArgsCtorAndVariadic(t *testing.T) {
	src := `<?php
class Point { public $x, $y, $z; function __construct($x, $y, $z) { $this->x=$x; $this->y=$y; $this->z=$z; } }
$p = new Point(z: 3, x: 1, y: 2);
echo "$p->x$p->y$p->z\n";
function v($a, ...$rest) { var_dump($a, $rest, func_num_args(), func_get_args()); }
v(b: 'b', a: 'a', c: 'c');
try { new stdClass(x: 1); } catch (Error $e) { echo $e->getMessage(), "\n"; }`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"123", `string(1) "a"`, `["c"]`, "int(1)", "Unknown named parameter $x"} {
		if !strings.Contains(got, want) {
			t.Errorf("ctor/variadic named: %q не содержит %q", got, want)
		}
	}
}

func TestRound12bNamedArgsDuplicate(t *testing.T) {
	src := `<?php
function f($a, ...$rest) {}
try { f(...[1, 'b' => 2], b: 20); } catch (Error $e) { echo $e->getMessage(), "\n"; }`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Named parameter $b overwrites previous argument") {
		t.Errorf("duplicate: %q", got)
	}
	// Распаковка после именованного — фатальная ошибка компиляции.
	src = `<?php test(a: 42, ...[]);`
	got, err = runCompiled(t, src)
	msg := got
	if err != nil {
		msg += err.Error()
	}
	if !strings.Contains(msg, "Cannot use argument unpacking after named arguments") {
		t.Errorf("unpack after named: got=%q err=%v", got, err)
	}
}

func TestRound12bNonNumericBitwiseWarn(t *testing.T) {
	src := `<?php
$s = "45345some";
$s &= 33;
var_dump($s);`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "A non-numeric value encountered") || !strings.Contains(got, "int(33)") {
		t.Errorf("bitwise warn: %q", got)
	}
	// Оба операнда-строки — побайтовая операция, без предупреждений.
	src = `<?php $a = "ff"; $a &= "  "; var_dump($a);`
	got, err = runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(got, "non-numeric") || !strings.Contains(got, `string(2) "  "`) {
		t.Errorf("bitwise strings: %q", got)
	}
}

func TestRound12bEmptyIterator(t *testing.T) {
	src := `<?php
$i = new EmptyIterator;
var_dump($i->valid());
$i->rewind();
$i->next();
try { $i->current(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
try { $i->key(); } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump($i instanceof Iterator);`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{"bool(false)", "Accessing the value of an EmptyIterator", "Accessing the key of an EmptyIterator", "bool(true)"} {
		if !strings.Contains(got, want) {
			t.Errorf("EmptyIterator: %q не содержит %q", got, want)
		}
	}
}

func TestRound12bDollarBraceExpr(t *testing.T) {
	// `${"..."}` внутри строки — variable variables: отдельная диагностика.
	src := `<?php
$a = "foo";
${"x$a"} = "hi";
echo "${"x$a"}", "\n";`
	got, err := runCompiled(t, src)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(got, "Using ${expr} (variable variables) in strings is deprecated") {
		t.Errorf("${expr} deprecation: %q", got)
	}
	if !strings.HasSuffix(strings.TrimSpace(got), "hi") {
		t.Errorf("${expr} value: %q", got)
	}
}
