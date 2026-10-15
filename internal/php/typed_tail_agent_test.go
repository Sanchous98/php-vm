package php

// typed_tail_agent_test.go — регрессии агента w_typed (раунд 21):
//   - &__get() при взятии по ссылке unset-typed-свойства (typed_properties_073):
//     геттер по ссылке, приведение значения «на месте», свойство остаётся unset;
//   - by-ref foreach по ArrayIterator/ArrayObject вокруг объекта
//     (typed_properties_113/114/115): живые свойства с гвардами типов и
//     readonly-диагностикой; по собственному массиву — запись в хранилище
//     итератора; прочие Iterator по-прежнему запрещены;
//   - отложенная variance-проверка трейта с автозагрузкой класса из сигнатуры
//     (bug #79179, Zend/tests/type_declarations/variance/trait_success.phpt);
//   - трейсы вклеенных include/require: кадр require, файл/строка инструкций
//     подключаемого файла (weak_include_strict/explicit_weak_include_strict,
//     tests/basic/bug73969).
//
// Сравнение с живым php 8.3 — байт в байт (с нормализацией путей temp-каталога).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"phpvm/internal/vm"
)

// typedTailRunFiles выкладывает файлы в temp-каталог, исполняет нашей VM
// главный файл (main) и живым php, нормализует пути каталога и сравнивает
// вывод. Ожидаемый фатальный вывод нашего VM не считается ошибкой запуска.
func typedTailRunFiles(t *testing.T, files map[string]string, main string) {
	t.Helper()
	livePHP(t)
	dir := t.TempDir()
	for name, src := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mainPath := filepath.Join(dir, main)
	got, err := runFileAt(files[main], mainPath)
	if err != nil && throwableOf(err) == nil && !vm.IsExitSignal(err) {
		t.Fatalf("run error: %v", err)
	}
	out, _ := exec.Command(livePHP(t), "-d", "error_reporting=E_ALL", "-d", "display_errors=1",
		"-d", "log_errors=0", "-d", "opcache.enable=0", "-d", "zend.exception_ignore_args=0", mainPath).CombinedOutput()
	norm := func(s string) string {
		s = strings.ReplaceAll(s, dir+string(os.PathSeparator), "")
		s = strings.ReplaceAll(s, dir, "")
		return s
	}
	g, w := norm(got), norm(strings.ReplaceAll(string(out), "\r\n", "\n"))
	if g != w {
		t.Errorf("--- got ---\n%q\n--- want ---\n%q", g, w)
	}
}

func TestTypedTail073MagicGetRef(t *testing.T) {
	livePHP(t)
	r20Check(t, "typed_tail_073", `<?php

class Test {
    public $prop = "42";
    public int $val;

    public function &__get($name) {
        return $this->prop;
    }
}

$test = new Test;
unset($test->val);
var_dump($test);
var_dump($val = &$test->val);
var_dump($test);

$test->prop = "x";
var_dump($test, $val);
`)
}

func TestTypedTailMagicGetRefStrict(t *testing.T) {
	livePHP(t)
	r20Check(t, "typed_tail_magic_get_strict", `<?php declare(strict_types=1);

class Test {
    public $prop = "42";
    public int $val;
    public function &__get($name) { return $this->prop; }
}
$t = new Test;
unset($t->val);
try { $v = &$t->val; var_dump($v); }
catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
`)
}

func TestTypedTailMagicGetRefNeedsUnset(t *testing.T) {
	livePHP(t)
	// Без unset() свойство остаётся uninitialized: Zend по ссылке сразу
	// бросает Error и __get не вызывает.
	r20Check(t, "typed_tail_magic_get_nounset", `<?php
class Test {
    public $prop = "42";
    public int $val;
    public function &__get($name) { return $this->prop; }
}
$t = new Test;
try { $v = &$t->val; var_dump($v); }
catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }

// Геттер по значению (без &) ссылку не даёт — тоже Error.
class Test2 {
    public $prop = "42";
    public int $val;
    public function __get($name) { return $this->prop; }
}
$t2 = new Test2;
unset($t2->val);
try { $v = &$t2->val; var_dump($v); }
catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
`)
}

func TestTypedTailArrayIterByRef(t *testing.T) {
	livePHP(t)
	r20Check(t, "typed_tail_113", `<?php
class A implements IteratorAggregate {
    function __construct(public string $foo = 'bar') {}
    function getIterator(): Traversable { return new ArrayIterator($this); }
}
$obj = new A;
foreach ($obj as $k => &$v) { $v = 42; }
var_dump($obj);
`)
	r20Check(t, "typed_tail_114", `<?php
class A implements IteratorAggregate {
    function __construct(public string $foo = 'bar') {}
    function getIterator(): Traversable { return new ArrayIterator($this); }
}
$obj = new A;
foreach ($obj as $k => &$v) {
    try { $v = []; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
}
foreach ($obj as $k => &$v) {
    try { $v = []; } catch (Throwable $e) { echo $e->getMessage(), "\n"; }
}
var_dump($obj);
`)
	r20Check(t, "typed_tail_115", `<?php
class A implements IteratorAggregate {
    function __construct(public readonly string $foo = 'bar') {}
    function getIterator(): Traversable { return new ArrayIterator($this); }
}
$obj = new A;
try { foreach ($obj as $k => &$v) {} }
catch (Throwable $e) { echo $e->getMessage(), "\n"; }
var_dump($obj);
`)
	// Собственное хранилище-массив: ссылки живут в памяти итератора.
	r20Check(t, "typed_tail_ait_array", `<?php
$a = [1, 2, 3];
$it = new ArrayIterator($a);
foreach ($it as $k => &$v) { $v++; }
var_dump($a, $it->getArrayCopy());
`)
	// Пользовательский Iterator по ссылке запрещён (tests/lang/foreachLoopIterator.002).
	r20Check(t, "typed_tail_custom_iter", `<?php
class MyIterator implements Iterator {
    public function valid(): bool { return false; }
    public function next(): void {}
    public function rewind(): void {}
    public function current(): mixed { return null; }
    public function key(): mixed { return ""; }
}
try { foreach (new MyIterator as $k => &$v) {} }
catch (Throwable $e) { echo get_class($e), ": ", $e->getMessage(), "\n"; }
`)
}

func TestTypedTailTraitDelayedVariance(t *testing.T) {
	livePHP(t)
	r20Check(t, "typed_tail_bug79179", `<?php
// Bug #79179: InterfaceB появится только через автозагрузку в момент
// отложенной проверки трейта.
spl_autoload_register(function() {
    interface InterfaceB extends InterfaceA {}
});

interface InterfaceA {}

trait SomeTrait {
    abstract public function func(): ?InterfaceA;
}

class Foo {
    public function func(): ?InterfaceB {}
}

class Bar extends Foo {
    use SomeTrait;
}
echo "===DONE===", "\n";
`)
}

func TestTypedTailIncludeTrace(t *testing.T) {
	main := `<?php

// implicitly weak code
require 'tail_inc.inc';
`
	inc := `<?php

declare(strict_types=1);

function takes_int(int $x) {
    echo "Success!";
}

takes_int(1.5); // fails in strict mode
`
	typedTailRunFiles(t, map[string]string{
		"tail_main.php": main,
		"tail_inc.inc":  inc,
	}, "tail_main.php")
}

func TestTypedTailIncludeFrames(t *testing.T) {
	// Кадр require() в debug_print_backtrace + строка конструкции внутри метода.
	main := `<?php
trait c2 { public static function f1() {} }

class c1 {
    use c2 { c2::f1 as f2; }
    public static function go() {
        return require 'tail_inc2.inc';
    }
}
c1::go();
`
	inc := `<?php
debug_print_backtrace();
`
	typedTailRunFiles(t, map[string]string{
		"tail_main2.php": main,
		"tail_inc2.inc":  inc,
	}, "tail_main2.php")
}
