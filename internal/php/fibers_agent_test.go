package php

import (
	"strings"
	"testing"
)

// runFibersAgent выполняет сниппет и возвращает вывод (непойманные ошибки так
// же печатаются в вывод — как в PHPT-прогоне).
func runFibersAgent(t *testing.T, src string) string {
	t.Helper()
	ResetResourceState()
	out, _ := runFile(src)
	return out
}

// TestFibersDtorNoSwitchContextAgent: __destruct, вызванный разрушением стека
// файбера, видит контекст файбера: Fiber::suspend() внутри него даёт
// "Cannot switch fibers in current execution context" (а не "suspend outside of
// a fiber"), кадр деструктора печатается как "[internal function]"
// (Zend/tests/fibers/no-switch-dtor-suspend).
func TestFibersDtorNoSwitchContextAgent(t *testing.T) {
	src := `<?php
$fiber = new Fiber(function () {
    $a = new class () {
        public function __destruct() {
            try { Fiber::suspend(); } catch (Throwable $e) {
                echo get_class($e), ": ", $e->getMessage(), "\n";
            }
        }
    };
});
$fiber->start();
echo "done\n";`
	got := runFibersAgent(t, src)
	if !strings.Contains(got, "FiberError: Cannot switch fibers in current execution context") {
		t.Fatalf("dtor context: %q", got)
	}
	if strings.Contains(got, "outside of a fiber") {
		t.Fatalf("dtor saw main context: %q", got)
	}
	if !strings.HasSuffix(got, "done\n") {
		t.Fatalf("tail: %q", got)
	}
}

// TestFibersGCCycleCallbackAgent: цикл «объект ↔ замыкание в файбере» не
// разрывается обычным проходом: деструктор выполняется только на
// gc_collect_cycles (Zend/tests/fibers/gc-cycle-callback).
func TestFibersGCCycleCallbackAgent(t *testing.T) {
	src := `<?php
$ref = new class () {
    public $fiber;
    public function __destruct() {
        var_dump('DTOR');
    }
};
$fiber = new Fiber(function () use ($ref) { die('UNREACHABLE'); });
$ref->fiber = $fiber;
$fiber = null;
$ref = null;
var_dump('COLLECT CYCLES');
gc_collect_cycles();
var_dump('DONE');`
	got := runFibersAgent(t, src)
	want := "string(14) \"COLLECT CYCLES\"\nstring(4) \"DTOR\"\nstring(4) \"DONE\"\n"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

// TestFibersForceCloseCatchAgent: FiberError из finally принудительно
// закрываемого файбера (объект потерял последнюю ссылку) поднимается в точке
// уничтожения и ловится обычным catch (Zend/tests/fibers/
// suspend-in-force-close-fiber-catching-exception).
func TestFibersForceCloseCatchAgent(t *testing.T) {
	src := `<?php
try {
    (function (): void {
        $fiber = new Fiber(function (): void {
            try {
                Fiber::suspend();
            } finally {
                Fiber::suspend();
            }
        });
        $fiber->start();
    })();
} catch (FiberError $exception) {
    echo $exception->getMessage(), "\n";
}
echo "done\n";`
	got := runFibersAgent(t, src)
	if got != "Cannot suspend in a force-closed fiber\ndone\n" {
		t.Fatalf("got %q", got)
	}
}

// TestFibersShutdownChainAgent: несколько незавершённых файберов, бросающих из
// finally при завершении запроса, печатаются одной цепочкой Uncaught/Next
// (Zend/tests/fibers/throw-in-multiple-destroyed-fibers-after-shutdown).
func TestFibersShutdownChainAgent(t *testing.T) {
	src := `<?php
$fiber = new Fiber(function (): void {
    $fiber1 = new Fiber(function (): void {
        try { Fiber::suspend(); } finally { throw new Exception('test1'); }
    });
    $fiber1->start();
    $fiber2 = new Fiber(function (): void {
        try { Fiber::suspend(); } finally { throw new Exception('test2'); }
    });
    $fiber2->start();
    Fiber::suspend();
});
$fiber->start();
echo "done\n";`
	got := runFibersAgent(t, src)
	if !strings.Contains(got, "done\n") {
		t.Fatalf("head: %q", got)
	}
	if !strings.Contains(got, "Fatal error: Uncaught Exception: test1") {
		t.Fatalf("no test1 head: %q", got)
	}
	if !strings.Contains(got, "\nNext Exception: test2") {
		t.Fatalf("no chained test2: %q", got)
	}
	if strings.Count(got, "Fatal error: Uncaught") != 1 {
		t.Fatalf("multiple fatal reports: %q", got)
	}
	if i, j := strings.Index(got, "test1"), strings.Index(got, "test2"); i < 0 || j < 0 || i > j {
		t.Fatalf("order: %q", got)
	}
}

// TestFibersFatalDiagNoFinallyAgent: после фатальной диагностики
// (E_USER_ERROR) finally незавершённых файберов не выполняется — как в Zend
// (bailout до деструкторов), Zend/tests/fibers/fatal-error-with-multiple-fibers.
func TestFibersFatalDiagNoFinallyAgent(t *testing.T) {
	src := `<?php
$fiber1 = new Fiber(function (): void {
    try { Fiber::suspend(1); } finally { echo "not executed"; }
});
$fiber2 = new Fiber(function (): void {
    Fiber::suspend(2);
    trigger_error("Fatal error in fiber", E_USER_ERROR);
});
var_dump($fiber1->start());
var_dump($fiber2->start());
$fiber2->resume();`
	got := runFibersAgent(t, src)
	if strings.Contains(got, "not executed") {
		t.Fatalf("finally после фатальной диагностики: %q", got)
	}
	if !strings.Contains(got, "Fatal error in fiber") {
		t.Fatalf("no fatal diag: %q", got)
	}
}

// TestFibersSilenceOperatorAgent: `@` не протекает между файбером и его
// возобновителем (Zend/tests/fibers/silence-operator-*).
func TestFibersSilenceOperatorAgent(t *testing.T) {
	// `@` вызывающего не подавляет диагностики внутри файбера.
	src := `<?php
$fiber = @new Fiber(function (): void {
    trigger_error("Warning A", E_USER_WARNING);
    Fiber::suspend();
    trigger_error("Warning C", E_USER_WARNING);
});
@$fiber->start();
trigger_error("Warning B", E_USER_WARNING);
@$fiber->resume();
trigger_error("Warning D", E_USER_WARNING);`
	got := runFibersAgent(t, src)
	for _, w := range []string{"Warning A", "Warning B", "Warning C", "Warning D"} {
		if !strings.Contains(got, w) {
			t.Fatalf("outside-fiber: нет %s: %q", w, got)
		}
	}

	// `@` внутри файбера не подавляет диагностики возобновителя.
	src2 := `<?php
function suspend_with_warnings(): void {
    trigger_error("Warning A", E_USER_WARNING);
    Fiber::suspend();
    trigger_error("Warning B", E_USER_WARNING);
}
$fiber = new Fiber(function (): void {
    @suspend_with_warnings();
});
$fiber->start();
trigger_error("Warning C", E_USER_WARNING);
$fiber->resume();
trigger_error("Warning D", E_USER_WARNING);`
	got2 := runFibersAgent(t, src2)
	if strings.Contains(got2, "Warning A") || strings.Contains(got2, "Warning B") {
		t.Fatalf("inside-fiber: подавление не сработало: %q", got2)
	}
	if !strings.Contains(got2, "Warning C") || !strings.Contains(got2, "Warning D") {
		t.Fatalf("inside-fiber: подавление протекло в main: %q", got2)
	}
}

// TestFibersTicksAgent: tick-функция выполняется внутри файбера и не может
// переключать файберы (Zend/tests/fibers/ticks); declare(ticks=1) действует и
// в телах замыканий, объявленных после него.
func TestFibersTicksAgent(t *testing.T) {
	src := `<?php
declare(ticks=1);
register_tick_function(function (): void {
    if (Fiber::getCurrent() !== null) {
        Fiber::suspend();
    }
});
$fiber = new Fiber(function (): void {
    echo "1\n";
    echo "2\n";
});
$fiber->start();`
	got := runFibersAgent(t, src)
	if !strings.HasPrefix(got, "1\n") {
		t.Fatalf("нет вывода до тика: %q", got)
	}
	if !strings.Contains(got, "Cannot switch fibers in current execution context") {
		t.Fatalf("tick не запретил переключение: %q", got)
	}
	if strings.Contains(got, "2\n") {
		t.Fatalf("файбер продолжил исполнение после тика: %q", got)
	}
}
