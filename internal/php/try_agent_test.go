package php

import (
	"strings"
	"testing"
)

// Регрессии семантики try/catch/finally, найденные по Zend/tests/try:
// раскрутка finally при goto/break/return/continue, освобождение временных
// регистров foreach при выходе из цикла (Zend FE_FREE/cleanup_live_vars),
// перекрытие исходного исключения исключением из finally и закрытие
// генератора с отложенными исключениями (bug71604).

func mustRunTry(t *testing.T, src string) string {
	t.Helper()
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v\nout=%q", err, out)
	}
	return out
}

// goto из try прогоняет finally (обратный и прямой переходы, вложенность).
func TestTryAgentGotoFinally(t *testing.T) {
	src := `<?php
label: try {
    goto label;
} finally {
    print "success";
    return;
}
`
	if got := mustRunTry(t, src); got != "success" {
		t.Fatalf("got %q, want %q", got, "success")
	}

	src2 := `<?php
function foo($ex = NULL) {
    try {
        try {
            goto label;
        } finally {
            var_dump("finally1");
            if ($ex) throw $ex;
        }
    } catch (Exception $e) {
       var_dump("caught");
       if ($ex) return "return1";
    } finally {
       var_dump("finally2");
    }
label:
   var_dump("label");
   return "return2";
}
var_dump(foo());
var_dump(foo(new Exception()));
`
	want2 := "string(8) \"finally1\"\nstring(8) \"finally2\"\nstring(5) \"label\"\nstring(7) \"return2\"\n" +
		"string(8) \"finally1\"\nstring(6) \"caught\"\nstring(8) \"finally2\"\nstring(7) \"return1\"\n"
	if got := mustRunTry(t, src2); got != want2 {
		t.Fatalf("got %q, want %q", got, want2)
	}
}

// goto внутрь тела try (обратный переход) не запускает finally, а метка перед
// try — запускает (Zend: zend_resolve_goto_label гасит FAST_CALL невышедших
// регионов).
func TestTryAgentGotoInsideTry(t *testing.T) {
	src := `<?php
function f() { try { goto x; x: echo "after\n"; echo "end-body\n"; } finally { echo "fin\n"; } echo "done\n"; }
f();
`
	if got := mustRunTry(t, src); got != "after\nend-body\nfin\ndone\n" {
		t.Fatalf("got %q", got)
	}
}

// break 2: освобождение итератора покинутого foreach происходит между
// finally внутреннего и внешнего try; исключение деструктора ловится внешним
// catch (Zend/tests/try/try_finally_017).
func TestTryAgentBreakDtorOrder(t *testing.T) {
	src := `<?php
class A {
    public $a = 1;
    public $b = 2;
    public function __destruct() { throw new Exception; }
}
function foo() {
    foreach ([0] as $_) {
        try {
            foreach (new A as $value) {
                try { break 2; }
                catch (Exception $e) { echo "catch1\n"; }
                finally { echo "finally1\n"; }
            }
        } catch (Exception $e) { echo "catch2\n"; }
        finally { echo "finally2\n"; }
    }
}
foo();
`
	if got := mustRunTry(t, src); got != "finally1\ncatch2\nfinally2\n" {
		t.Fatalf("got %q", got)
	}
}

// Порядок деструкторов и finally при goto из вложенных циклов
// (Zend/tests/try/try_finally_019).
func TestTryAgentGotoDtorOrder(t *testing.T) {
	src := `<?php
class A {
    public $n = 0;
    function __construct($n) { $this->n = $n; }
    function __destruct() { echo "destruct" . $this->n . "\n"; }
}
foreach ([new A(1)] as $a) {
    $a = null;
    try {
        foreach ([new A(2)] as $a) {
            $a = null;
            try {
                foreach ([new A(3)] as $a) {
                    $a = null;
                    goto out;
                }
            } finally { echo "finally1\n"; }
out: ;
        }
    } finally { echo "finally2\n"; }
}
`
	want := "destruct3\nfinally1\ndestruct2\nfinally2\ndestruct1\n"
	if got := mustRunTry(t, src); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// Исключение освобождает циклы тела до входа в catch/finally
// (Zend/tests/try/try_finally_020).
func TestTryAgentExceptionCleanupOrder(t *testing.T) {
	src := `<?php
class A {
    public $n = 0;
    function __construct($n) { $this->n = $n; }
    function __destruct() { echo "destruct" . $this->n . "\n"; }
}
foreach ([new A(1)] as $a) {
    $a = null;
    try {
        foreach ([new A(2)] as $a) {
            $a = null;
            try {
                foreach ([new A(3)] as $a) {
                    $a = null;
                    throw new Exception();
                }
            } finally { echo "finally1\n"; }
        }
    } catch (Exception $e) { echo "catch\n"; }
    finally { echo "finally2\n"; }
}
`
	want := "destruct3\nfinally1\ndestruct2\ncatch\nfinally2\ndestruct1\n"
	if got := mustRunTry(t, src); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// return с разрушением временного цикла: исключение деструктора ловится
// внешним try, а не try-ом вокруг return (Zend/tests/try/try_finally_022:
// ZEND_FREE_ON_RETURN относит исключение к концу foreach).
func TestTryAgentReturnDtorException(t *testing.T) {
	src := `<?php
class A { public $x = 1; function __destruct() { throw new Exception(); } }
try {
    foreach (new A() as $a) {
        try { return $a; } catch (Exception $e) { echo "exception1\n"; }
    }
} catch (Exception $e) { echo "exception2\n"; }
`
	if got := mustRunTry(t, src); got != "exception2\n" {
		t.Fatalf("got %q, want %q", got, "exception2\n")
	}
}

// Исключение из finally перекрывает исходное только если его не поймали;
// пойманное не мешает перевозбуждению исходного
// (Zend/tests/try/try_finally_023).
func TestTryAgentFinallyOverride(t *testing.T) {
	src := `<?php
class Dtor { public function __destruct() { throw new Exception(2); } }
function test() {
    try {
        throw new Exception(1);
    } finally {
        try {
            foreach ([new Dtor] as $v) {
                unset($v);
                return 42;
            }
        } catch (Exception $e) { }
    }
}
try { test(); } catch (Exception $e) { echo $e->getMessage(), "\n"; }
`
	if got := mustRunTry(t, src); got != "1\n" {
		t.Fatalf("got %q, want %q", got, "1\n")
	}
}

// catch без переменной: объект исключения освобождается на входе, деструктор
// может выбросить до тела catch (Zend/tests/try/catch_novar_2).
func TestTryAgentCatchNoVarRelease(t *testing.T) {
	src := `<?php
class ThrowsOnDestruct extends Exception {
    public function __destruct() { echo "Throwing\n"; throw new RuntimeException("dtor"); }
}
try { throw new ThrowsOnDestruct(); } catch (Exception) { echo "Unreachable catch\n"; }
echo "Unreachable fallthrough\n";
`
	got, err := runFile(src)
	if err == nil {
		t.Fatalf("want uncaught error, got nil")
	}
	if !strings.HasPrefix(got, "Throwing\n") {
		t.Fatalf("got %q, want Throwing first", got)
	}
	if strings.Contains(got, "Unreachable catch") || strings.Contains(got, "Unreachable fallthrough") {
		t.Fatalf("catch body must not run: %q", got)
	}
	if !strings.Contains(got, "Uncaught RuntimeException") {
		t.Fatalf("want uncaught RuntimeException, got %q", got)
	}
}

// Закрытие генератора, приостановленного внутри finally при раскрутке: наружу
// выходит отложенное исключение с previous (bug71604 (2/3)).
func TestTryAgentGeneratorCloseChain(t *testing.T) {
	src := `<?php
function gen() {
    try {
        throw new Exception(1);
    } finally {
        try {
            throw new Exception(2);
        } finally {
            try { yield; } finally { }
        }
    }
}
try { gen()->rewind(); } catch (Exception $e) { echo $e, "\n"; }
`
	got := mustRunTry(t, src)
	if !strings.HasPrefix(got, "Exception: 1 in ") {
		t.Fatalf("chain head must be printed first, got %q", got)
	}
	if !strings.Contains(got, "Next Exception: 2 in ") {
		t.Fatalf("want chained previous, got %q", got)
	}

	src3 := `<?php
function gen() {
    try {
        throw new Exception(1);
    } finally {
        try {
            yield;
        } finally {
            try { throw new Exception(2); } finally { }
        }
    }
}
try { gen()->rewind(); } catch (Exception $e) { echo $e, "\n"; }
`
	got3 := mustRunTry(t, src3)
	if !strings.HasPrefix(got3, "Exception: 1 in ") || !strings.Contains(got3, "Next Exception: 2 in ") {
		t.Fatalf("got %q", got3)
	}
	// Кадр трейса #0 — место resume (файл функции), а не "[internal function]".
	if !strings.Contains(got3, ": gen()\n#1 {main}") {
		t.Fatalf("resume frame must carry file/line: %q", got3)
	}
}

// finally выполняется на break/continue/goto, покидающих try, и не выполняется
// повторно на обратном переходе внутрь (Zend/tests/try/finally_goto_005).
func TestTryAgentFinallyOnce(t *testing.T) {
	src := `<?php
$i = 0;
while ($i++ < 3) {
    try {
        if ($i == 2) continue;
        if ($i == 3) break;
    } finally {
        echo "f", $i, "\n";
    }
    echo "body", $i, "\n";
}
echo "end\n";
`
	want := "f1\nbody1\nf2\nf3\nend\n"
	if got := mustRunTry(t, src); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
