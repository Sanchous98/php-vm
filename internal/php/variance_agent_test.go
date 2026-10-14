package php

import (
	"strings"
	"testing"
)

// variance_agent_test — регрессионные тесты отложенных проверок совместимости
// сигнатур (Zend variance obligations): классы, объявляемые в рантайме, видят
// друг друга как «почти слинкованные», недостающие классы автозагружаются, а
// нерешённые проверки повторяются (Zend/tests/type_declarations/variance).

// TestVarianceAutoloadCycleOrdering: цикл A<-B<-C через автозагрузку; Zend
// догружает C при объявлении B, поэтому C печатается раньше B.
func TestVarianceAutoloadCycleOrdering(t *testing.T) {
	src := `<?php
spl_autoload_register(function($class) {
    if ($class === 'A') {
        class A { public function method() : B {} }
        var_dump(new A);
    } else if ($class == 'B') {
        class B extends A { public function method() : C {} }
        var_dump(new B);
    } else {
        class C extends B {}
        var_dump(new C);
    }
});
var_dump(new B);
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	want := "object(A)#2 (0) {\n}\nobject(C)#2 (0) {\n}\nobject(B)#2 (0) {\n}\nobject(B)#2 (0) {\n}\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestVarianceDeferredIncompatible: проверка, отложенная из-за недостающего C,
// после его автозагрузки доказывает несовместимость.
func TestVarianceDeferredIncompatible(t *testing.T) {
	src := `<?php
spl_autoload_register(function($class) {
    if ($class === 'A') {
        class A { public function method() : C {} }
    } else if ($class == 'B') {
        class B extends A { public function method() : B {} }
    } else {
        class C extends B {}
    }
});
$b = new B;
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "Declaration of B::method(): B must be compatible with A::method(): C") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestVarianceUnavailableAfterAutoload: недостающий класс не появился после
// автозагрузки — фатальная ошибка «Could not check compatibility».
func TestVarianceUnavailableAfterAutoload(t *testing.T) {
	src := `<?php
spl_autoload_register(function ($class) {
    if ($class == 'A') {
        class A { function m(): B {} }
    } elseif ($class == 'B') {
        class B extends A { function m(): X {} }
    } else {
        class C extends B {}
    }
});
new B;
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "Could not check compatibility between B::m(): X and A::m(): B, because class X is not available") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestVarianceTopLevelAutoload: класс верхнего уровня, чья проверка не
// завершилась при компиляции: недостающие классы автозагружаются на позиции
// объявления (Zend: без early binding).
func TestVarianceTopLevelAutoload(t *testing.T) {
	src := `<?php
spl_autoload_register(function($class) {
    var_dump($class);
    if ($class === 'X') {
        class X {}
    } else {
        class Y {}
    }
});

class A {
    public function method(X $param) : object {}
}

class B extends A {
    public function method(object $param) : Y {}
}
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	want := "string(1) \"X\"\nstring(1) \"Y\"\n"
	if out != want {
		t.Errorf("got %q want %q", out, want)
	}
}

// TestVarianceUnlinkedUseFatal: класс B уже использован отложенной проверкой
// (Y::test(): B) — при провале автозагрузки родителя Zend не может откатить
// объявление и фаталит «During inheritance of B with variance dependencies».
func TestVarianceUnlinkedUseFatal(t *testing.T) {
	src := `<?php
spl_autoload_register(function($class) {
    throw new Exception("Class $class does not exist");
});
interface I {}
spl_autoload_register(function($class) {
    class X { public function test(): I {} }
    class Y extends X { public function test(): B {} }
}, true, true);
try {
    class B extends A implements I {}
} catch (Exception $e) {
    echo $e->getMessage(), "\n";
}
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "During inheritance of B with variance dependencies: Uncaught Exception: Class A does not exist") {
		t.Errorf("unexpected output: %q", out)
	}
}

// TestVarianceUnlinkedInstanceof: проверка Y::test(): C видит незалинкованный
// C (объявление в процессе) и сразу сообщает несовместимость с I, а не
// «Could not check» (Zend unlinked_instanceof).
func TestVarianceUnlinkedInstanceof(t *testing.T) {
	src := `<?php
interface I {}
spl_autoload_register(function() {
    class X {
        function test(): I {}
    }
    class Y extends X {
        function test(): C {}
    }
});
class C extends Z implements C {}
`
	out, err := runFile(src)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if !strings.Contains(out, "Declaration of Y::test(): C must be compatible with X::test(): I") {
		t.Errorf("unexpected output: %q", out)
	}
}
